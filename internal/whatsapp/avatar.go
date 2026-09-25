package whatsapp

/*
avatar.go is MH-602's provider-photo path: fetch a WhatsApp profile picture
through Evolution, download it into a data: URI (never hotlink Evolution's
CDN, whose links expire), and cache the result on whichever row asked for it —
a contact when the conversation is linked, the conversation itself when it is
not (so an unlinked customer's photo shows too).

Both success AND failure are cached (photo_fetch_status), and neither is
retried inside PhotoRefreshWindow — a customer with genuinely no photo, or a
workspace whose Evolution is down, must not be re-fetched on every inbound
message. Callers MUST run these in a goroutine: each one makes up to two
outbound network calls and this package has no business blocking a webhook
response on either of them.
*/

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// PhotoRefreshWindow is exported so internal/inbox can decide, from the
	// same list/detail query that already read photo_fetched_at, whether a
	// row is stale enough to queue for a background fetch (MH-602 defect 1) —
	// without duplicating the window in two packages.
	PhotoRefreshWindow = 7 * 24 * time.Hour
	photoMaxBytes      = 2 << 20 // 2MB, the same ceiling as an uploaded avatar
)

// downloadPhoto fetches url (Evolution's briefly-valid CDN link) and returns
// it re-encoded as a data: URI so nothing served later depends on that link
// still being alive.
func downloadPhoto(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	hc := http.Client{Timeout: 15 * time.Second}
	res, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("photo server answered %d", res.StatusCode)
	}
	ct := res.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		return "", errors.New("the photo url did not answer with an image")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, photoMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > photoMaxBytes {
		return "", errors.New("the photo is too large to cache")
	}
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

// fetchPhoto asks Evolution for number's current photo and downloads it. An
// empty string with a nil error means WhatsApp genuinely has none — not a
// failure, and still worth caching so it is not asked again for a week.
func fetchPhoto(ctx context.Context, cfg Config, link *Link, number string) (string, error) {
	if !cfg.Configured() || link == nil {
		return "", ErrNotConfigured
	}
	c := ClientFor(cfg, link)
	url, err := c.ProfilePicture(ctx, number)
	if err != nil || url == "" {
		return "", err
	}
	return downloadPhoto(ctx, url)
}

// RefreshContactPhoto fetches and caches a contact's WhatsApp photo, at most
// once every PhotoRefreshWindow. Safe to call on every inbound message: it
// is a no-op read until the window has passed.
func RefreshContactPhoto(ctx context.Context, db *sql.DB, cfg Config, link *Link, workspaceID, contactID, number string) {
	var fetchedAt sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT photo_fetched_at FROM contacts WHERE workspace_id = $1 AND id = $2`,
		workspaceID, contactID).Scan(&fetchedAt); err != nil {
		return
	}
	if fetchedAt.Valid && time.Since(fetchedAt.Time) < PhotoRefreshWindow {
		return
	}
	dataURL, err := fetchPhoto(ctx, cfg, link, number)
	status := "ok"
	if err != nil {
		status = "failed"
	}
	_, _ = db.ExecContext(ctx, `
		UPDATE contacts SET photo_url = $1, photo_source = 'whatsapp', photo_fetched_at = now(), photo_fetch_status = $2
		WHERE workspace_id = $3 AND id = $4`, dataURL, status, workspaceID, contactID)
}

// RefreshConversationPhoto is RefreshContactPhoto's twin for a WhatsApp
// conversation with no linked contact yet — same cache shape, on
// conversations instead, so an unlinked customer's photo shows too.
func RefreshConversationPhoto(ctx context.Context, db *sql.DB, cfg Config, link *Link, workspaceID, conversationID, number string) {
	var fetchedAt sql.NullTime
	if err := db.QueryRowContext(ctx, `
		SELECT photo_fetched_at FROM conversations WHERE workspace_id = $1 AND id = $2`,
		workspaceID, conversationID).Scan(&fetchedAt); err != nil {
		return
	}
	if fetchedAt.Valid && time.Since(fetchedAt.Time) < PhotoRefreshWindow {
		return
	}
	dataURL, err := fetchPhoto(ctx, cfg, link, number)
	status := "ok"
	if err != nil {
		status = "failed"
	}
	_, _ = db.ExecContext(ctx, `
		UPDATE conversations SET photo_url = $1, photo_source = 'whatsapp', photo_fetched_at = now(), photo_fetch_status = $2
		WHERE workspace_id = $3 AND id = $4`, dataURL, status, workspaceID, conversationID)
}
