package crm

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/photourl"
)

/*
Contact avatars (MH-602): "if the number connected to a contact and contact
have a name you must showup the name on the chat and for avatar alwase showup
avatar placeholder and try to get avatar from chats or provider if it's
exsits or add avatar upload on the contact" (Fady, verbatim).

Three sources, in priority order, resolved once here so both crm.ContactDetail
and inbox.ConversationOut agree on the same answer:

 1. An uploaded photo — bytes live in contact_avatars (internal/db/post/
    171_contact_avatars.sql), served through a signed URL exactly like
    internal/inbox/attachment.go serves an email attachment: the signature
    IS the authorization, which is what lets it be used as a bare <img src>.
 2. A provider photo fetched from WhatsApp via Evolution and downloaded into
    photo_url as a data: URI (internal/whatsapp/avatar.go) rather than
    hotlinking Evolution's CDN, which expires.
 3. Initials, rendered client-side from whatever name is available — needs no
    column and so is never this function's job to return.

No multipart anywhere else in this codebase (vCard import sends its file as
text in a JSON field) — the upload endpoint below follows that same shape:
base64 bytes in a JSON body, not a multipart form.
*/

const avatarMaxSize = 2 << 20 // 2MB, per the task brief

var avatarContentTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
}

// providerPhotoKinds names the two rows a WhatsApp-cached photo can live on
// (internal/whatsapp/avatar.go's RefreshContactPhoto/RefreshConversationPhoto):
// a linked contact, or an unlinked conversation. The map value is the table
// the signed provider-photo endpoint re-reads photo_url from.
var providerPhotoKinds = photourl.ProviderPhotoKinds

// AvatarURLLite, AvatarURL, ContactAvatarURL and ProviderPhotoURL are thin
// re-exports of internal/photourl (MH-588): that package holds the actual,
// dependency-free signing logic now, so internal/marketing (the contacts
// list) can build the same signed URLs without importing internal/crm, which
// would cycle back (internal/crm already imports internal/marketing). Every
// existing caller here (internal/inbox, internal/alerts) is untouched.

// AvatarURLLite is AvatarURL's list-safe twin (MH-602 defect 2): it takes a
// hasProviderPhoto flag instead of the actual photo_url, so a list query
// never has to select the data: URI (30-80KB apiece) just to decide whether
// to link to it — see internal/inbox/routes.go's conversationSelect, which
// selects a boolean instead. kind and id say which row (contact or
// conversation) the signed endpoint should re-read photo_url from.
func AvatarURLLite(workspaceID, kind, id, avatarID string, hasProviderPhoto bool, externalPhotoURL string) string {
	return photourl.AvatarURLLite(workspaceID, kind, id, avatarID, hasProviderPhoto, externalPhotoURL)
}

// AvatarURL is AvatarURLLite for a caller that already has photoURL in hand
// (getContact reads one row, not a list, so the cost AvatarURLLite avoids
// doesn't apply here) — it still never lets a data: URI itself out as JSON.
func AvatarURL(workspaceID, kind, id, avatarID, photoURL string) string {
	return photourl.AvatarURL(workspaceID, kind, id, avatarID, photoURL)
}

// ContactAvatarURL builds one uploaded avatar's signed download URL.
func ContactAvatarURL(workspaceID, avatarID string) string {
	return photourl.ContactAvatarURL(workspaceID, avatarID)
}

func verifyAvatarURL(workspaceID, avatarID, expStr, sig string) error {
	return photourl.VerifyAvatarURL(workspaceID, avatarID, expStr, sig)
}

// signAvatar/signProviderPhoto are test-only wrappers (avatar_db_test.go,
// provider_photo_db_test.go mint raw signatures at an arbitrary exp to check
// tamper/expiry/cross-domain rejection) — the real signing lives in
// internal/photourl now.
func signAvatar(workspaceID, avatarID string, exp int64) (string, error) {
	return photourl.SignAvatar(workspaceID, avatarID, exp)
}

func signProviderPhoto(kind, workspaceID, id string, exp int64) (string, error) {
	return photourl.SignProviderPhoto(kind, workspaceID, id, exp)
}

// ProviderPhotoURL builds a WhatsApp-cached photo's signed download URL —
// AvatarURLLite's answer whenever a row's photo_url is a data: URI, so the
// bytes never round-trip through a JSON response again (MH-602 defect 2).
func ProviderPhotoURL(workspaceID, kind, id string) string {
	return photourl.ProviderPhotoURL(workspaceID, kind, id)
}

func verifyProviderPhotoURL(kind, workspaceID, id, expStr, sig string) error {
	return photourl.VerifyProviderPhotoURL(kind, workspaceID, id, expStr, sig)
}

// parseDataURI splits a data: URI (internal/whatsapp/avatar.go's cache
// format, always base64) into its content type and decoded bytes.
func parseDataURI(s string) (string, []byte, error) {
	return photourl.ParseDataURI(s)
}

// UploadContactAvatar validates and stores an uploaded photo, replacing any
// prior upload for this contact (one row per contact — a re-upload IS what
// "replace" means in the upload control).
func UploadContactAvatar(ctx context.Context, db *sql.DB, contactID, dataB64, contentType, filename string) (*ContactDetail, error) {
	s, err := scope(ctx, authz.ContactsWrite)
	if err != nil {
		return nil, err
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if !avatarContentTypes[contentType] {
		return nil, huma.Error422UnprocessableEntity("only PNG, JPEG or WEBP images are allowed")
	}
	raw, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("the image data is not valid base64")
	}
	if len(raw) == 0 {
		return nil, huma.Error422UnprocessableEntity("the image is empty")
	}
	if len(raw) > avatarMaxSize {
		return nil, huma.Error422UnprocessableEntity("images are limited to 2MB")
	}
	_ = filename // not stored; content-type + bytes is all a photo needs

	var avatarID string
	err = db.QueryRowContext(ctx, `
		INSERT INTO contact_avatars (workspace_id, contact_id, content_type, size_bytes, data)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (workspace_id, contact_id)
		DO UPDATE SET content_type = EXCLUDED.content_type, size_bytes = EXCLUDED.size_bytes,
		              data = EXCLUDED.data, updated_at = now()
		RETURNING id`, s.WorkspaceID, contactID, contentType, len(raw), raw).Scan(&avatarID)
	if err != nil {
		return nil, linkErr(err, "could not save the avatar")
	}
	res, err := db.ExecContext(ctx, `UPDATE contacts SET photo_attachment_id = $1, updated_at = now() WHERE workspace_id = $2 AND id = $3`,
		avatarID, s.WorkspaceID, contactID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, huma.Error404NotFound("contact not found")
	}
	return getContact(ctx, db, s.WorkspaceID, contactID)
}

// RemoveContactAvatar deletes the uploaded photo, if any, and falls back to
// whatever the provider-photo/initials chain resolves to next.
func RemoveContactAvatar(ctx context.Context, db *sql.DB, contactID string) (*ContactDetail, error) {
	s, err := scope(ctx, authz.ContactsWrite)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `UPDATE contacts SET photo_attachment_id = NULL, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		s.WorkspaceID, contactID); err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM contact_avatars WHERE workspace_id = $1 AND contact_id = $2`,
		s.WorkspaceID, contactID); err != nil {
		return nil, err
	}
	return getContact(ctx, db, s.WorkspaceID, contactID)
}

// RegisterAvatars mounts the signed, unauthenticated download endpoint for
// uploaded avatars — the same tier as internal/inbox/attachment.go's
// /api/attachments/{id}, and for the same reason: a plain <img src> carries
// no cookies or headers, so the signature has to be the whole authorization.
func RegisterAvatars(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "contact-avatar-download", Method: http.MethodGet, Path: "/api/contact-avatars/{id}",
		Summary: "Download one contact's uploaded avatar via its signed URL (MH-602)",
		Tags:    []string{"CRM"},
	}, func(ctx context.Context, in *struct {
		ID  string `path:"id"`
		WS  string `query:"ws"`
		Exp string `query:"exp"`
		Sig string `query:"sig"`
	}) (*huma.StreamResponse, error) {
		if err := verifyAvatarURL(in.WS, in.ID, in.Exp, in.Sig); err != nil {
			return nil, huma.Error403Forbidden(err.Error())
		}
		var contentType string
		var data []byte
		// Owner pool, no RLS — same reasoning as attachment-download: the
		// signature already proved the caller was handed this exact pair.
		if err := a.SysDB.QueryRowContext(ctx, `
			SELECT content_type, data FROM contact_avatars WHERE workspace_id = $1 AND id = $2`,
			in.WS, in.ID).Scan(&contentType, &data); err != nil {
			if notFound(err) {
				return nil, huma.Error404NotFound("avatar not found")
			}
			return nil, huma.Error500InternalServerError("could not read the avatar")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			hctx.SetHeader("Content-Type", contentType)
			hctx.SetHeader("Cache-Control", "private, max-age=3600")
			hctx.BodyWriter().Write(data) //nolint:errcheck
		}}, nil
	})
}

// RegisterProviderPhotos mounts the signed, unauthenticated download endpoint
// for WhatsApp-cached photos (MH-602 defect 2) — contact-avatar-download's
// sibling for the OTHER photo source: photo_url holds a data: URI
// (internal/whatsapp/avatar.go), and that string must never be inlined into
// a JSON response again, list or detail. kind picks the table (contact or
// conversation) the same way AvatarURLLite chose it when it built the URL;
// verifyProviderPhotoURL's signature domain ("provider-photo:...") is
// distinct from signAvatar's ("contact-avatar:...") so one can't be replayed
// as the other.
func RegisterProviderPhotos(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "provider-photo-download", Method: http.MethodGet, Path: "/api/provider-photos/{kind}/{id}",
		Summary: "Download one WhatsApp-cached photo via its signed URL (MH-602)",
		Tags:    []string{"CRM"},
	}, func(ctx context.Context, in *struct {
		Kind string `path:"kind" enum:"contact,conversation"`
		ID   string `path:"id"`
		WS   string `query:"ws"`
		Exp  string `query:"exp"`
		Sig  string `query:"sig"`
	}) (*huma.StreamResponse, error) {
		if err := verifyProviderPhotoURL(in.Kind, in.WS, in.ID, in.Exp, in.Sig); err != nil {
			return nil, huma.Error403Forbidden(err.Error())
		}
		table := providerPhotoKinds[in.Kind] // validated above; never user input directly
		var raw sql.NullString
		// Owner pool, no RLS — same reasoning as contact-avatar-download: the
		// signature already proved the caller was handed this exact pair.
		if err := a.SysDB.QueryRowContext(ctx, `SELECT photo_url FROM `+table+` WHERE workspace_id = $1 AND id = $2`,
			in.WS, in.ID).Scan(&raw); err != nil {
			if notFound(err) {
				return nil, huma.Error404NotFound("photo not found")
			}
			return nil, huma.Error500InternalServerError("could not read the photo")
		}
		if !raw.Valid || raw.String == "" {
			return nil, huma.Error404NotFound("photo not found")
		}
		contentType, data, err := parseDataURI(raw.String)
		if err != nil || !strings.HasPrefix(contentType, "image/") {
			return nil, huma.Error404NotFound("photo not found")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			hctx.SetHeader("Content-Type", contentType)
			hctx.SetHeader("Cache-Control", "private, max-age=3600")
			hctx.BodyWriter().Write(data) //nolint:errcheck
		}}, nil
	})
}
