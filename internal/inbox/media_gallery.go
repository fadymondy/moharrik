package inbox

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

/*
MH-662: the Contact info panel's "Media, links and docs" gallery. Media and
docs come straight from message_attachments — AttachmentOut.Kind (MH-658)
already classifies each one as image/video/audio/voice (media) or
document/sticker/location/contact (docs), so no new storage is needed. Links
have no storage of their own: they're the first URL in a message body,
extracted with a regex over the same messages the thread already shows.

This is one call for all three tabs rather than one per tab: the panel shows
all three counts on its tab bar the moment it opens, and a chat's whole
gallery is small enough (attachmentMaxTotal per message, and Moharrik's inbox
is support/sales correspondence, not bulk file sharing) that fetching the lot
up front is simpler than three separately-paged endpoints.
*/

var galleryLinkPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

var galleryMediaKinds = map[string]bool{"image": true, "video": true, "audio": true, "voice": true}

// GalleryCounts is the tab bar's badges.
type GalleryCounts struct {
	Media int `json:"media"`
	Links int `json:"links"`
	Docs  int `json:"docs"`
}

// GalleryItem is one row in a Media/Links/Docs tab.
type GalleryItem struct {
	MessageID  string          `json:"message_id"`
	CreatedAt  string          `json:"created_at"`
	Attachment *AttachmentOut  `json:"attachment,omitempty" doc:"Present in the media/docs tabs."`
	Link       string          `json:"link,omitempty" doc:"Present only in the links tab."`
}

// GalleryPage is the whole panel's gallery in one response.
type GalleryPage struct {
	Counts GalleryCounts `json:"counts"`
	Media  []GalleryItem `json:"media"`
	Links  []GalleryItem `json:"links"`
	Docs   []GalleryItem `json:"docs"`
}

func conversationGallery(ctx context.Context, a *app.App, convID string, limit int) (*GalleryPage, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	if _, err := readConversation(ctx, a.SQLDB, s.WorkspaceID, convID); notFound(err) {
		return nil, huma.Error404NotFound("conversation not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	if limit <= 0 || limit > 200 {
		limit = 60
	}

	page := &GalleryPage{Media: []GalleryItem{}, Links: []GalleryItem{}, Docs: []GalleryItem{}}

	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT ma.id, ma.filename, ma.content_type, ma.size_bytes, ma.is_inline, ma.content_id, ma.kind, ma.status, ma.meta,
		       m.id, m.created_at
		FROM message_attachments ma
		JOIN messages m ON m.workspace_id = ma.workspace_id AND m.id = ma.message_id
		WHERE ma.workspace_id = $1 AND m.conversation_id = $2 AND ma.status = 'ready'
		ORDER BY m.created_at DESC`, s.WorkspaceID, convID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the gallery")
	}
	for rows.Next() {
		var att AttachmentOut
		var meta []byte
		var msgID string
		var createdAt sql.NullTime
		if err := rows.Scan(&att.ID, &att.Filename, &att.ContentType, &att.SizeBytes, &att.Inline, &att.ContentID, &att.Kind, &att.Status, &meta,
			&msgID, &createdAt); err != nil {
			rows.Close()
			return nil, huma.Error500InternalServerError("could not read the gallery")
		}
		if len(meta) > 0 && string(meta) != "{}" {
			att.Meta = meta
		}
		if att.Kind != "location" {
			att.URL = AttachmentURL(s.WorkspaceID, att.ID)
		}
		item := GalleryItem{MessageID: msgID, Attachment: &att}
		if createdAt.Valid {
			item.CreatedAt = createdAt.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
		}
		if galleryMediaKinds[att.Kind] {
			page.Counts.Media++
			if len(page.Media) < limit {
				page.Media = append(page.Media, item)
			}
		} else {
			page.Counts.Docs++
			if len(page.Docs) < limit {
				page.Docs = append(page.Docs, item)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, huma.Error500InternalServerError("could not read the gallery")
	}
	rows.Close()

	linkRows, err := a.SQLDB.QueryContext(ctx, `
		SELECT id, body, created_at FROM messages
		WHERE workspace_id = $1 AND conversation_id = $2 AND body ~ 'https?://'
		ORDER BY created_at DESC`, s.WorkspaceID, convID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the gallery")
	}
	defer linkRows.Close()
	for linkRows.Next() {
		var msgID, body string
		var createdAt sql.NullTime
		if err := linkRows.Scan(&msgID, &body, &createdAt); err != nil {
			return nil, huma.Error500InternalServerError("could not read the gallery")
		}
		link := galleryLinkPattern.FindString(body)
		if link == "" {
			continue
		}
		page.Counts.Links++
		if len(page.Links) < limit {
			item := GalleryItem{MessageID: msgID, Link: link}
			if createdAt.Valid {
				item.CreatedAt = createdAt.Time.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")
			}
			page.Links = append(page.Links, item)
		}
	}
	if err := linkRows.Err(); err != nil {
		return nil, huma.Error500InternalServerError("could not read the gallery")
	}

	return page, nil
}

func registerMediaGallery(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-conversation-gallery", Method: http.MethodGet, Path: "/api/w/conversations/{id}/media",
		Summary: "Media, links and docs shared in this chat, with tab counts (MH-662)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID    string `path:"id"`
		Limit int    `query:"limit" minimum:"0" maximum:"200" doc:"Rows per tab; defaults to 60."`
	}) (*body[GalleryPage], error) {
		page, err := conversationGallery(ctx, a, in.ID, in.Limit)
		if err != nil {
			return nil, err
		}
		return out(*page), nil
	})
}
