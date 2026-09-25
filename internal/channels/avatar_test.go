package channels_test

/*
MH-588: Telegram/Slack/Discord's profile-photo fetch (internal/channels/
avatar.go), against FAKE platform servers only — httptest, never a real Bot
API, Slack workspace or Discord app, per the batch rule. Mirrors internal/
whatsapp/avatar_test.go's shape (a fake server answering the exact endpoints a
real fetch touches) generalised to three more platforms.
*/

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/channels"
)

// avatarTinyPNG is the smallest possible valid PNG: a 1x1 transparent pixel.
var avatarTinyPNG, _ = base64.StdEncoding.DecodeString(
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")

// --- Telegram ---------------------------------------------------------

// fakeTelegramWithPhoto answers getUserProfilePhotos, getFile and the file
// download itself — the same server plays all three roles, the way the real
// Bot API's /bot<token>/ and /file/bot<token>/ paths both live on
// api.telegram.org.
func fakeTelegramWithPhoto(t *testing.T, hasPhoto bool) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "getUserProfilePhotos"):
			if !hasPhoto {
				w.Write([]byte(`{"ok":true,"result":{"total_count":0,"photos":[]}}`)) //nolint:errcheck
				return
			}
			w.Write([]byte(`{"ok":true,"result":{"total_count":1,"photos":[[{"file_id":"small"},{"file_id":"big"}]]}}`)) //nolint:errcheck
		case strings.Contains(r.URL.Path, "getFile"):
			w.Write([]byte(`{"ok":true,"result":{"file_path":"photos/big.jpg"}}`)) //nolint:errcheck
		case strings.HasSuffix(r.URL.Path, "/photos/big.jpg"):
			w.Header().Set("Content-Type", "image/png")
			w.Write(avatarTinyPNG) //nolint:errcheck
		default:
			// setWebhook (called when the connection is created) and anything
			// else this test doesn't care about — answer ok, the same generic
			// case startFakeTelegram (telegram_test.go) uses.
			w.Write([]byte(`{"ok":true,"result":true}`)) //nolint:errcheck
		}
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// telegramConn creates a real Telegram connection through the app's own API
// (RefreshChannelPhoto needs a real sealed bot token to open), against the
// fake server.
func telegramConn(t *testing.T, f *fixture) string {
	t.Helper()
	res := f.api.PostCtx(f.ctx, "/api/w/telegram-channels", map[string]any{"name": "Bot", "bot_token": "T:1"})
	var created struct {
		Channel struct{ ID string } `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	if created.Channel.ID == "" {
		t.Fatalf("could not create a Telegram connection: %s", res.Body)
	}
	return created.Channel.ID
}

// seedWorkspaceRow backs newFixture's synthetic workspace id with a real
// workspaces row: conversations carries conversations_workspace_fk (internal/
// db/post/18_inbox.sql), which the rest of this package's tests never hit
// because they only write to telegram_channels/slack_channels/
// discord_channels — this file is the first here to insert a conversation
// directly, so it needs the FK satisfied. SysDB bypasses RLS the same way
// internal/inbox's own newMailboxWorkspace test helper does.
func seedWorkspaceRow(t *testing.T, f *fixture) {
	t.Helper()
	userID := "u-" + f.ws
	if _, err := f.a.SysDB.ExecContext(context.Background(), `
		INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', now())
		ON CONFLICT (id) DO NOTHING`, userID, "owner-"+f.ws+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.SysDB.ExecContext(context.Background(), `
		INSERT INTO workspaces (id, slug, name, created_by) VALUES ($1, $1, 'Avatar Test', $2)
		ON CONFLICT (id) DO NOTHING`, f.ws, userID); err != nil {
		t.Fatal(err)
	}
}

func insertConversation(t *testing.T, f *fixture, channel, threadID string) string {
	t.Helper()
	seedWorkspaceRow(t, f)
	var id string
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		INSERT INTO conversations (workspace_id, channel, external_thread_id, customer_name, subject, last_message_at)
		VALUES ($1, $2, $3, 'Someone', 'hi', now()) RETURNING id`,
		f.ws, channel, threadID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func photoRow(t *testing.T, f *fixture, table, id string) (photoURL, source, status string, fetched bool) {
	t.Helper()
	var url, src, st string
	var fetchedAt *time.Time
	if err := f.a.SQLDB.QueryRowContext(f.ctx, `
		SELECT coalesce(photo_url,''), coalesce(photo_source,''), coalesce(photo_fetch_status,''), photo_fetched_at
		FROM `+table+` WHERE workspace_id = $1 AND id = $2`, f.ws, id).Scan(&url, &src, &st, &fetchedAt); err != nil {
		t.Fatal(err)
	}
	return url, src, st, fetchedAt != nil
}

func TestRefreshChannelPhotoTelegramCachesTheLargestPhotoAsADataURI(t *testing.T) {
	fake := fakeTelegramWithPhoto(t, true)
	defer channels.SetTelegramAPIBase(fake.URL)()
	f := newFixture(t)
	connID := telegramConn(t, f)
	convID := insertConversation(t, f, "telegram", "9001")

	channels.RefreshChannelPhoto(f.ctx, f.a, f.ws, "telegram", connID, "conversation", convID, "9001")

	url, source, status, fetched := photoRow(t, f, "conversations", convID)
	if !fetched {
		t.Fatal("photo_fetched_at was not set")
	}
	if status != "ok" {
		t.Fatalf("photo_fetch_status = %q, want ok", status)
	}
	if source != "telegram" {
		t.Fatalf("photo_source = %q, want telegram", source)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("photo_url = %q, want a data: URI (never a hotlinked Telegram URL)", url)
	}
}

func TestRefreshChannelPhotoTelegramNoPhotoIsCachedNotRetried(t *testing.T) {
	fake := fakeTelegramWithPhoto(t, false)
	defer channels.SetTelegramAPIBase(fake.URL)()
	f := newFixture(t)
	connID := telegramConn(t, f)
	convID := insertConversation(t, f, "telegram", "9002")

	channels.RefreshChannelPhoto(f.ctx, f.a, f.ws, "telegram", connID, "conversation", convID, "9002")

	url, _, status, fetched := photoRow(t, f, "conversations", convID)
	if !fetched {
		t.Fatal("photo_fetched_at was not set")
	}
	if status != "ok" {
		t.Fatalf("no photo is not a failure, got status %q", status)
	}
	if url != "" {
		t.Fatalf("expected no photo, got %q", url)
	}
}

func TestRefreshChannelPhotoTelegramGroupChatIsSkipped(t *testing.T) {
	fake := fakeTelegramWithPhoto(t, true)
	defer channels.SetTelegramAPIBase(fake.URL)()
	f := newFixture(t)
	connID := telegramConn(t, f)
	convID := insertConversation(t, f, "telegram", "-1001")

	// A Telegram group/channel chat id starts with "-"; it has no personal
	// profile photo, and must never even call the fake server.
	channels.RefreshChannelPhoto(f.ctx, f.a, f.ws, "telegram", connID, "conversation", convID, "-1001")

	_, _, _, fetched := photoRow(t, f, "conversations", convID)
	if fetched {
		t.Fatal("a group chat id must not trigger a photo fetch")
	}
}

// --- Slack --------------------------------------------------------------

func fakeSlackUsersInfo(t *testing.T, imageURL string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/users.info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"user":{"profile":{"image_192":"` + imageURL + `"}}}`)) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func slackConn(t *testing.T, f *fixture) string {
	t.Helper()
	res := f.api.PostCtx(f.ctx, "/api/w/slack-channels", map[string]any{"name": "Bot", "bot_token": "xoxb-1"})
	var created struct {
		Channel struct{ ID string } `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	if created.Channel.ID == "" {
		t.Fatalf("could not create a Slack connection: %s", res.Body)
	}
	return created.Channel.ID
}

func TestRefreshChannelPhotoSlackCachesTheImage192(t *testing.T) {
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/img/photo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(avatarTinyPNG) //nolint:errcheck
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	fake := fakeSlackUsersInfo(t, srv.URL+"/img/photo.png")
	defer channels.SetSlackAPIBase(fake.URL)()
	f := newFixture(t)
	connID := slackConn(t, f)
	convID := insertConversation(t, f, "slack", "C123")

	channels.RefreshChannelPhoto(f.ctx, f.a, f.ws, "slack", connID, "conversation", convID, "U456")

	url, source, status, fetched := photoRow(t, f, "conversations", convID)
	if !fetched || status != "ok" {
		t.Fatalf("fetched=%v status=%q", fetched, status)
	}
	if source != "slack" {
		t.Fatalf("photo_source = %q, want slack", source)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("photo_url = %q, want a data: URI", url)
	}
}

// --- Discord --------------------------------------------------------------

func fakeDiscordUser(t *testing.T, avatarHash string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"999999999999999999","avatar":"` + avatarHash + `"}`)) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func discordConn(t *testing.T, f *fixture) string {
	t.Helper()
	res := f.api.PostCtx(f.ctx, "/api/w/discord-channels", map[string]any{"name": "Bot", "bot_token": "T.1"})
	var created struct {
		Channel struct{ ID string } `json:"channel"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &created)
	if created.Channel.ID == "" {
		t.Fatalf("could not create a Discord connection: %s", res.Body)
	}
	return created.Channel.ID
}

func TestRefreshChannelPhotoDiscordCachesTheCDNAvatar(t *testing.T) {
	fakeAPI := fakeDiscordUser(t, "abc123")
	defer channels.SetDiscordAPIBase(fakeAPI.URL)()

	mux := http.NewServeMux()
	mux.HandleFunc("/avatars/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(avatarTinyPNG) //nolint:errcheck
	})
	cdn := httptest.NewServer(mux)
	t.Cleanup(cdn.Close)
	defer channels.SetDiscordCDNBase(cdn.URL)()

	f := newFixture(t)
	connID := discordConn(t, f)
	convID := insertConversation(t, f, "discord", "555555555555555555")

	channels.RefreshChannelPhoto(f.ctx, f.a, f.ws, "discord", connID, "conversation", convID, "999999999999999999")

	url, source, status, fetched := photoRow(t, f, "conversations", convID)
	if !fetched || status != "ok" {
		t.Fatalf("fetched=%v status=%q", fetched, status)
	}
	if source != "discord" {
		t.Fatalf("photo_source = %q, want discord", source)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("photo_url = %q, want a data: URI", url)
	}
}

func TestRefreshChannelPhotoDiscordRejectsANonSnowflakeID(t *testing.T) {
	fakeAPI := fakeDiscordUser(t, "abc123")
	defer channels.SetDiscordAPIBase(fakeAPI.URL)()
	f := newFixture(t)
	connID := discordConn(t, f)
	convID := insertConversation(t, f, "discord", "chan-1")

	// Not a numeric snowflake — must be refused before any network call, the
	// same guard isSnowflake already gives inbound message handling.
	channels.RefreshChannelPhoto(f.ctx, f.a, f.ws, "discord", connID, "conversation", convID, "not-a-snowflake")

	_, _, _, fetched := photoRow(t, f, "conversations", convID)
	if fetched {
		t.Fatal("a non-snowflake id must not trigger a photo fetch")
	}
}

// --- failure caching --------------------------------------------------

func TestRefreshChannelPhotoCachesAFailureWithoutPanicking(t *testing.T) {
	// A Telegram server that answers every call with a hard failure: the fetch
	// must cache photo_fetch_status = 'failed' and leave photo_url empty,
	// never panic or propagate the error to the caller (RefreshChannelPhoto
	// returns nothing — best effort, exactly like RefreshContactPhoto).
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	defer channels.SetTelegramAPIBase(srv.URL)()

	f := newFixture(t)
	connID := telegramConn(t, f)
	convID := insertConversation(t, f, "telegram", "9003")

	channels.RefreshChannelPhoto(f.ctx, f.a, f.ws, "telegram", connID, "conversation", convID, "9003")

	url, _, status, fetched := photoRow(t, f, "conversations", convID)
	if !fetched {
		t.Fatal("a failure must still cache photo_fetched_at, so it is not retried immediately")
	}
	if status != "failed" {
		t.Fatalf("photo_fetch_status = %q, want failed", status)
	}
	if url != "" {
		t.Fatalf("a failed fetch must not leave a stale/partial photo_url, got %q", url)
	}
}
