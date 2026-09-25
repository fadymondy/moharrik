package inbox

// MH-588: Gravatar is email's photo source — the lowest-priority one in the
// task spec ("only when nothing else exists"), so this only ever writes a
// photo onto a row whose photo_source is still empty; a WhatsApp/Telegram/
// Slack/Discord photo, or an uploaded one, is never displaced by it.

import (
	"context"
	"crypto/md5" //nolint:gosec // Gravatar's own hashing scheme, not a security use
	"database/sql"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/photocache"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var gravatarBaseOverride string

func gravatarBase() string {
	if gravatarBaseOverride != "" {
		return gravatarBaseOverride
	}
	return "https://www.gravatar.com"
}

// SetGravatarBase points Gravatar lookups at a fake server for tests.
func SetGravatarBase(base string) func() {
	prev := gravatarBaseOverride
	gravatarBaseOverride = strings.TrimRight(base, "/")
	return func() { gravatarBaseOverride = prev }
}

// gravatarURL is Gravatar's documented address scheme: an md5 hash of the
// lowercased, trimmed email. d=404 makes "no gravatar for this address"
// answer with a plain 404 instead of a generated default image, so it can be
// told apart from a real fetch failure.
func gravatarURL(email string) string {
	h := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(email)))) //nolint:gosec
	return gravatarBase() + "/avatar/" + hex.EncodeToString(h[:]) + "?s=256&d=404"
}

// gravatarLimiter is Gravatar's own per-workspace rate cap, separate from
// channelPhotoLimiter (internal/channels/avatar.go) — a workspace with a
// heavy morning email batch must not starve its Telegram/Slack/Discord photo
// fetches, or the other way around.
var gravatarLimiter = photocache.NewLimiter(1500 * time.Millisecond)

// queueGravatarRefresh fires a background Gravatar lookup for kind
// ("contact" or "conversation") id, sharing WhatsApp's own dedupe map and
// concurrency cap (photoRefreshQueued/photoRefreshSem in whatsapp.go) so an
// inbox with busy email AND WhatsApp traffic can't open unbounded goroutines
// between the two. Never blocks File(): a Gravatar miss or timeout must not
// hold up filing the email that triggered it.
func queueGravatarRefresh(a *app.App, workspaceID, kind, id, email string) {
	email = strings.TrimSpace(email)
	if id == "" || email == "" || !strings.Contains(email, "@") {
		return
	}
	key := workspaceID + "|" + kind + "|" + id
	now := time.Now()
	if v, ok := photoRefreshQueued.Load(key); ok {
		if now.Sub(v.(time.Time)) < photoRefreshDedupeWindow {
			return
		}
	}
	photoRefreshQueued.Store(key, now)
	go func() {
		photoRefreshSem <- struct{}{}
		defer func() { <-photoRefreshSem }()
		bctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		bctx = tenant.WithWorkspace(bctx, workspaceID)
		refreshGravatarPhoto(bctx, a, workspaceID, kind, id, email)
	}()
}

func refreshGravatarPhoto(ctx context.Context, a *app.App, workspaceID, kind, id, email string) {
	table := "contacts"
	if kind == "conversation" {
		table = "conversations"
	}

	// Only when nothing else exists: a row that already has ANY photo_source
	// (whatsapp, telegram, slack, discord, or an uploaded photo recorded the
	// same way) keeps it — Gravatar never overwrites a better source.
	var fetchedAt sql.NullTime
	var source string
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT photo_fetched_at, coalesce(photo_source, '') FROM `+table+` WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id).Scan(&fetchedAt, &source); err != nil {
		return
	}
	if source != "" && source != "gravatar" {
		return
	}
	if !photocache.IsStale(fetchedAt, photocache.RefreshWindow) {
		return
	}
	if !gravatarLimiter.Allow(workspaceID) {
		return
	}

	raw, ct, err := photocache.Download(ctx, gravatarURL(email), nil)
	var dataURI string
	if err != nil {
		// A plain 404 (d=404) means Gravatar confirmed there is no photo for
		// this address — worth caching as "ok, none", not as a failure that
		// keeps retrying inside the refresh window.
		if isGravatarNotFound(err) {
			err = nil
		}
	} else {
		dataURI, err = photocache.EncodeDataURI(raw, ct)
	}
	_ = photocache.UpdatePhoto(ctx, a.SQLDB, table, workspaceID, id, "gravatar", dataURI, err)
}

func isGravatarNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "answered "+strconv.Itoa(http.StatusNotFound))
}
