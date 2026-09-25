package crm

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
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
	"github.com/fadymondy/moharrik/internal/authz"
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

const (
	avatarURLTTL  = 6 * time.Hour
	avatarMaxSize = 2 << 20 // 2MB, per the task brief
)

var avatarContentTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
}

// AvatarURL is the shared priority: an uploaded avatar wins over a cached
// provider photo, which wins over nothing (the caller falls back to initials).
func AvatarURL(workspaceID, avatarID, photoURL string) string {
	if avatarID != "" {
		return ContactAvatarURL(workspaceID, avatarID)
	}
	return photoURL
}

func signAvatar(workspaceID, avatarID string, exp int64) (string, error) {
	secret := os.Getenv("AUTH_SECRET")
	if len(secret) < 16 {
		return "", errors.New("AUTH_SECRET is not set; cannot sign an avatar URL")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "contact-avatar:%s:%s:%d", workspaceID, avatarID, exp)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// ContactAvatarURL builds one uploaded avatar's signed download URL.
func ContactAvatarURL(workspaceID, avatarID string) string {
	exp := time.Now().Add(avatarURLTTL).Unix()
	sig, err := signAvatar(workspaceID, avatarID, exp)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("/api/contact-avatars/%s?ws=%s&exp=%d&sig=%s", avatarID, workspaceID, exp, sig)
}

func verifyAvatarURL(workspaceID, avatarID, expStr, sig string) error {
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return errors.New("bad expiry")
	}
	if time.Now().Unix() > exp {
		return errors.New("this link has expired")
	}
	want, err := signAvatar(workspaceID, avatarID, exp)
	if err != nil {
		return err
	}
	if !hmac.Equal([]byte(strings.ToLower(sig)), []byte(want)) {
		return errors.New("bad signature")
	}
	return nil
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
