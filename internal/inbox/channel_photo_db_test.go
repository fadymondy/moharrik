package inbox

/*
MH-588: the Telegram/Slack/Discord half of "contact avatars from every
channel" — FileChannelMessage (channel.go) must queue a background photo
fetch (queueChannelPhotoRefresh, avatar_channels.go) exactly the way
FileWhatsApp already queues refreshWhatsAppPhoto, landing on internal/
channels.RefreshChannelPhoto against a FAKE platform server, never a real
Bot API/Slack/Discord.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/inbox/ -run ChannelPhoto -v
*/

import (
	"crypto/rand"
	"encoding/base64"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/channels"
)

var channelPhotoTinyPNG, _ = base64.StdEncoding.DecodeString(tinyPNGB64)

// numericIDSuffix is idSuffix's counterpart for Telegram chat/user ids, which
// are always numeric — idSuffix's hex alphabet (a-f) would make
// fetchTelegramPhotoURL correctly, but here unintentionally, treat the id as
// non-numeric and skip the fetch, which is exactly the flake this replaced.
func numericIDSuffix() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(900000000))
	return big.NewInt(0).Add(n, big.NewInt(100000000)).String()
}

// fakeTelegramForFiling answers exactly the calls a real inbound message
// triggers here: setWebhook (connection creation), getUserProfilePhotos,
// getFile and the file download itself.
func fakeTelegramForFiling(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "getUserProfilePhotos"):
			w.Write([]byte(`{"ok":true,"result":{"total_count":1,"photos":[[{"file_id":"f1"}]]}}`)) //nolint:errcheck
		case strings.Contains(r.URL.Path, "getFile"):
			w.Write([]byte(`{"ok":true,"result":{"file_path":"photos/f1.jpg"}}`)) //nolint:errcheck
		case strings.HasSuffix(r.URL.Path, "/photos/f1.jpg"):
			w.Header().Set("Content-Type", "image/png")
			w.Write(channelPhotoTinyPNG) //nolint:errcheck
		default:
			w.Write([]byte(`{"ok":true,"result":true}`)) //nolint:errcheck
		}
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFileChannelMessageQueuesAPhotoFetchForTheLinkedContact(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	fake := fakeTelegramForFiling(t)
	defer channels.SetTelegramAPIBase(fake.URL)()

	created, err := channels.CreateTelegram(f.ctx, a, f.wsID, f.userID, channels.TelegramInput{Name: "Bot", BotToken: "T:1"})
	if err != nil {
		t.Fatal(err)
	}

	chatID := "700" + numericIDSuffix()
	if err := FileChannelMessage(f.ctx, a, f.wsID, "telegram", created.ID, chatID, "@dana", "Dana", "hello", "tg:"+idSuffix()); err != nil {
		t.Fatal(err)
	}

	var convID, contactID string
	if err := a.SQLDB.QueryRowContext(f.ctx, `
		SELECT id, coalesce(contact_id, '') FROM conversations
		WHERE workspace_id = $1 AND channel = 'telegram' AND external_thread_id = $2`,
		f.wsID, chatID).Scan(&convID, &contactID); err != nil {
		t.Fatal(err)
	}
	if contactID == "" {
		t.Fatal("the inbound message did not link a contact")
	}

	waitUntil(t, 5*time.Second, func() (bool, error) {
		var fetched bool
		err := a.SQLDB.QueryRowContext(f.ctx, `SELECT photo_fetched_at IS NOT NULL FROM contacts WHERE workspace_id = $1 AND id = $2`,
			f.wsID, contactID).Scan(&fetched)
		return fetched, err
	})

	var url, source, status string
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT coalesce(photo_url,''), coalesce(photo_source,''), coalesce(photo_fetch_status,'') FROM contacts WHERE workspace_id = $1 AND id = $2`,
		f.wsID, contactID).Scan(&url, &source, &status); err != nil {
		t.Fatal(err)
	}
	if source != "telegram" {
		t.Fatalf("photo_source = %q (status=%q), want telegram", source, status)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("photo_url = %q (status=%q), want a cached data: URI on the LINKED CONTACT (never the conversation once a contact exists)", url, status)
	}
}

// TestFileChannelMessageWithNoConnectionNeverBlocksFiling proves the "a
// failure never blocks filing" contract: sourceChannelID pointing at nothing
// real (the ordinary case in channel_contact_db_test.go's own tests, which
// pass "") must still file the message and simply skip the photo fetch.
func TestFileChannelMessageWithNoConnectionNeverBlocksFiling(t *testing.T) {
	a := testApp(t)
	f := newMailboxWorkspace(t, a)

	chatID := "701" + numericIDSuffix()
	if err := FileChannelMessage(f.ctx, a, f.wsID, "telegram", "", chatID, "@dana", "Dana", "hello", "tg:"+idSuffix()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.SQLDB.QueryRowContext(f.ctx, `SELECT count(*) FROM conversations WHERE workspace_id = $1 AND channel = 'telegram' AND external_thread_id = $2`,
		f.wsID, chatID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("the message must still be filed with no connection id, got %d conversations", count)
	}
}
