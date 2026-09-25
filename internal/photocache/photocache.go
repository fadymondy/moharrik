// Package photocache is MH-588's shared half of every provider-photo
// fetcher: download a URL, resize it to a small square-ish cap, encode it as
// a data: URI (internal/crm/avatar.go's signed /api/provider-photos/{kind}/
// {id} endpoint decodes it back out — nothing here ever hands a raw provider
// URL to the client, the same "never hotlink" rule internal/whatsapp/
// avatar.go established first), and cache the result on a contacts or
// conversations row.
//
// internal/whatsapp/avatar.go predates this package and is left as it was
// built (MH-602): it does not resize and keeps its own 7-day window, which is
// still well inside "at most daily". Every NEW fetcher this card adds
// (internal/channels/avatar.go for Telegram/Slack/Discord, internal/inbox's
// Gravatar path for email) goes through here instead, so they share one
// resize implementation, one size cap and one rate-limit shape rather than
// four hand-rolled copies.
package photocache

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"
)

const (
	// MaxBytes caps a downloaded photo before it is even decoded — the same
	// 2MB ceiling as an uploaded avatar (internal/crm/avatar.go).
	MaxBytes = 2 << 20
	// MaxDim is the longest side a cached photo is resized down to, per the
	// task brief ("resize to 256px").
	MaxDim = 256
	// RefreshWindow bounds how often any of these NEW fetchers re-asks a
	// provider for the same row's photo — "refresh at most daily".
	RefreshWindow = 24 * time.Hour
)

// Download fetches url and returns its raw bytes and content type, capped at
// MaxBytes. headers lets a caller add auth (none of the current providers
// need it — Telegram's file link, Slack's image_192 and Discord's CDN URL are
// all bare, unauthenticated links once resolved).
func Download(ctx context.Context, url string, headers map[string]string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	hc := http.Client{Timeout: 15 * time.Second}
	res, err := hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, "", fmt.Errorf("photo server answered %d", res.StatusCode)
	}
	ct := res.Header.Get("Content-Type")
	raw, err := io.ReadAll(io.LimitReader(res.Body, MaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(raw) > MaxBytes {
		return nil, "", errors.New("the photo is too large to cache")
	}
	return raw, ct, nil
}

// EncodeDataURI resizes raw to at most MaxDim on its longest side and returns
// it as a data: URI. Only PNG and JPEG (what every provider this card wires
// up actually sends) are decoded and resized; anything else is stored as-is,
// still capped at MaxBytes by Download above, rather than dropping a photo
// this package simply does not know how to shrink.
func EncodeDataURI(raw []byte, contentType string) (string, error) {
	if len(raw) == 0 {
		return "", errors.New("no photo bytes to cache")
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	img, format, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
	}

	resized := resizeToMax(img, MaxDim)
	var buf bytes.Buffer
	outCT := "image/jpeg"
	if format == "png" {
		outCT = "image/png"
		if err := png.Encode(&buf, resized); err != nil {
			return "", err
		}
	} else if err := jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 85}); err != nil {
		return "", err
	}
	return "data:" + outCT + ";base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func resizeToMax(img image.Image, maxDim int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxDim && h <= maxDim {
		return img
	}
	var nw, nh int
	if w >= h {
		nw = maxDim
		nh = int(float64(h) * float64(maxDim) / float64(w))
	} else {
		nh = maxDim
		nw = int(float64(w) * float64(maxDim) / float64(h))
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Over, nil)
	return dst
}

// IsStale answers whether a row's cached photo is old enough (or was never
// fetched) to ask a provider again.
func IsStale(fetchedAt sql.NullTime, window time.Duration) bool {
	return !fetchedAt.Valid || time.Since(fetchedAt.Time) >= window
}

var photoTables = map[string]bool{"contacts": true, "conversations": true}

// UpdatePhoto caches one fetch's result onto contacts or conversations —
// generalised from internal/whatsapp/avatar.go's RefreshContactPhoto/
// RefreshConversationPhoto, which established this exact shape: both success
// AND failure are cached (photo_fetch_status), so a row with genuinely no
// photo, or a provider that is down, is not re-asked inside RefreshWindow.
// dataURI == "" with fetchErr == nil means "the provider confirmed there is
// no photo" — still worth caching.
func UpdatePhoto(ctx context.Context, db *sql.DB, table, workspaceID, id, source, dataURI string, fetchErr error) error {
	if !photoTables[table] {
		return fmt.Errorf("photocache: unknown table %q", table)
	}
	status := "ok"
	if fetchErr != nil {
		status = "failed"
		dataURI = ""
	}
	_, err := db.ExecContext(ctx, `UPDATE `+table+` SET photo_url = $1, photo_source = $2, photo_fetched_at = now(), photo_fetch_status = $3, updated_at = now()
		WHERE workspace_id = $4 AND id = $5`, dataURI, source, status, workspaceID, id)
	return err
}

// Limiter is a bare per-workspace rate cap: Allow reports whether enough time
// has passed since the last allowed call for this workspace. Every NEW
// background fetcher this card adds shares one of these per provider, so a
// burst of inbound messages (a busy Slack channel, a Telegram group) cannot
// hammer that provider even though each row's own RefreshWindow only bounds
// how often the SAME row is asked again.
//
// Allow never blocks or queues a retry: a call it refuses is simply skipped
// for this trigger and picked up by the next one (the next inbound message,
// or — for channels that get one — the next list/open), the same
// best-effort contract photoRefreshQueued/photoRefreshSem already use for
// WhatsApp's own concurrency cap.
type Limiter struct {
	mu    sync.Mutex
	last  map[string]time.Time
	every time.Duration
}

// NewLimiter builds a Limiter that allows at most one call per workspace
// every `every`.
func NewLimiter(every time.Duration) *Limiter {
	return &Limiter{last: map[string]time.Time{}, every: every}
}

func (l *Limiter) Allow(workspaceID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if t, ok := l.last[workspaceID]; ok && now.Sub(t) < l.every {
		return false
	}
	l.last[workspaceID] = now
	return true
}
