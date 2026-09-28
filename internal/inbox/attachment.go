package inbox

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
)

/*
Attachments (MH-556): stored in Postgres (internal/db/schema/inbox.sql,
message_attachments — the doc there explains why no object store), served
through a SIGNED, time-limited URL rather than an authenticated endpoint — the
same shape as a webhook source's id or a chat widget's public_key: the URL
itself is the authorization, which is what lets it be used directly as
<img src> inside the sandboxed email iframe (web/components/inbox/email-body.tsx),
which carries no cookies or headers of its own.

sig = hex(HMAC-SHA256(AUTH_SECRET, "attachment:" + workspaceID + ":" + attachmentID + ":" + exp))

exp is a Unix timestamp; a request past it is refused outright rather than
re-signed, so a link handed to a customer (a "view this file" share) has a
real expiry rather than one nobody enforces.
*/

// attachmentURLTTL is how long a signed download URL is valid. Long enough to
// cover an open thread staying open in a browser tab, short enough that a URL
// leaked into a log or a browser history is not a permanent hole.
const attachmentURLTTL = 6 * time.Hour

func signAttachment(workspaceID, attachmentID string, exp int64) (string, error) {
	secret := os.Getenv("AUTH_SECRET")
	if len(secret) < 16 {
		return "", errors.New("AUTH_SECRET is not set; cannot sign an attachment URL")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "attachment:%s:%s:%d", workspaceID, attachmentID, exp)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// AttachmentURL builds one attachment's signed download URL.
func AttachmentURL(workspaceID, attachmentID string) string {
	exp := time.Now().Add(attachmentURLTTL).Unix()
	sig, err := signAttachment(workspaceID, attachmentID, exp)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("/api/attachments/%s?ws=%s&exp=%d&sig=%s", attachmentID, workspaceID, exp, sig)
}

func verifyAttachmentURL(workspaceID, attachmentID, expStr, sig string) error {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return errors.New("bad expiry")
	}
	if time.Now().Unix() > exp {
		return errors.New("this link has expired")
	}
	want, err := signAttachment(workspaceID, attachmentID, exp)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(strings.ToLower(sig)), []byte(want)) {
		return errors.New("bad signature")
	}
	return nil
}

// attachmentsFor loads the attachment metadata (never the bytes) for a set of
// messages, keyed by message id — one query for a whole page, not one per
// message.
func attachmentsFor(ctx context.Context, db *sql.DB, workspaceID string, messageIDs []string) (map[string][]AttachmentOut, error) {
	out := map[string][]AttachmentOut{}
	if len(messageIDs) == 0 {
		return out, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, message_id, filename, content_type, size_bytes, is_inline, content_id, kind, status, meta
		FROM message_attachments WHERE workspace_id = $1 AND message_id = ANY($2)
		ORDER BY created_at`, workspaceID, messageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a AttachmentOut
		var msgID string
		var meta []byte
		if err := rows.Scan(&a.ID, &msgID, &a.Filename, &a.ContentType, &a.SizeBytes, &a.Inline, &a.ContentID, &a.Kind, &a.Status, &meta); err != nil {
			return nil, err
		}
		if len(meta) > 0 && string(meta) != "{}" {
			a.Meta = meta
		}
		// A location/contact row carries no bytes, and a failed download has
		// none either — the signed download URL would only 404/empty for them.
		if a.Status != "failed" && a.Kind != "location" {
			a.URL = AttachmentURL(workspaceID, a.ID)
		}
		out[msgID] = append(out[msgID], a)
	}
	return out, rows.Err()
}

// RegisterAttachments mounts the signed, unauthenticated download endpoint.
// It is deliberately NOT under /api/w/ (which requires a workspace session):
// the signature IS the authorization, the same design as POST /api/hooks/{id}.
func RegisterAttachments(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "attachment-download", Method: http.MethodGet, Path: "/api/attachments/{id}",
		Summary: "Download one email attachment via its signed URL (MH-556)",
		Tags:    []string{"Inbox"},
	}, func(ctx context.Context, in *struct {
		ID  string `path:"id"`
		WS  string `query:"ws"`
		Exp string `query:"exp"`
		Sig string `query:"sig"`
	}) (*huma.StreamResponse, error) {
		if err := verifyAttachmentURL(in.WS, in.ID, in.Exp, in.Sig); err != nil {
			return nil, huma.Error403Forbidden(err.Error())
		}
		var filename, contentType string
		var data []byte
		// No app-role/RLS pool here — the URL's own signature already proved
		// the caller was handed this exact (workspace, attachment) pair by the
		// server, which is what RLS exists to prove for a session-based caller.
		if err := a.SysDB.QueryRowContext(ctx, `
			SELECT filename, content_type, data FROM message_attachments WHERE workspace_id = $1 AND id = $2`,
			in.WS, in.ID).Scan(&filename, &contentType, &data); err != nil {
			if notFound(err) {
				return nil, huma.Error404NotFound("attachment not found")
			}
			return nil, huma.Error500InternalServerError("could not read the attachment")
		}
		return &huma.StreamResponse{Body: func(hctx huma.Context) {
			hctx.SetHeader("Content-Type", contentType)
			hctx.SetHeader("Content-Disposition", `inline; filename="`+strings.ReplaceAll(filename, `"`, "")+`"`)
			hctx.SetHeader("Cache-Control", "private, max-age=3600")
			// Byte ranges (MH-658): a browser reads an Ogg/Opus voice note's
			// length by seeking to its end, and without 206 support it reports
			// the duration as Infinity — the player showed 0:00.
			if w, ok := hctx.BodyWriter().(http.ResponseWriter); ok {
				r := &http.Request{Method: hctx.Method(), Header: http.Header{}}
				for _, h := range []string{"Range", "If-Range", "If-Modified-Since", "If-None-Match"} {
					if v := hctx.Header(h); v != "" {
						r.Header.Set(h, v)
					}
				}
				http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
				return
			}
			hctx.BodyWriter().Write(data) //nolint:errcheck
		}}, nil
	})
}
