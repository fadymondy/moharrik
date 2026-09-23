package alerts

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SherClockHolmes/webpush-go"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/testdb"
)

/*
Web push end to end against a real database (MH-378): who is pushed to, what
each device can read, and what happens to a subscription the push service says
is gone. The "push service" is an httptest server; every body it receives is
DECRYPTED here with the device's private key (RFC 8291), so the assertions are
about what a member's lock screen would actually show — not about what the
sender meant to send.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/alerts/ -v
*/

// device is a browser: a P-256 key pair and an auth secret, as PushManager makes.
type device struct {
	name string
	priv *ecdh.PrivateKey
	auth []byte
}

func newDevice(t *testing.T, name string) *device {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return &device{name: name, priv: priv, auth: auth}
}

func (d *device) keys() (p256dh, auth string) {
	return base64.RawURLEncoding.EncodeToString(d.priv.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString(d.auth)
}

// decrypt opens an aes128gcm web push body (RFC 8291 §3.4, RFC 8188).
func (d *device) decrypt(t *testing.T, body []byte) Payload {
	t.Helper()
	if len(body) < 21 {
		t.Fatalf("body too short: %d", len(body))
	}
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	asPublic := body[21 : 21+idlen]
	ciphertext := body[21+idlen:]
	if int(rs) < len(ciphertext) {
		t.Fatalf("record size %d smaller than the ciphertext %d", rs, len(ciphertext))
	}
	peer, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := d.priv.ECDH(peer)
	if err != nil {
		t.Fatal(err)
	}
	prkKey, _ := hkdf.Extract(sha256.New, shared, d.auth)
	keyInfo := "WebPush: info\x00" + string(d.priv.PublicKey().Bytes()) + string(asPublic)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, keyInfo, 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("%s could not decrypt its push: %v", d.name, err)
	}
	plain = []byte(strings.TrimRight(string(plain), "\x00"))
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		t.Fatal("no last-record delimiter")
	}
	var p Payload
	if err := json.Unmarshal(plain[:len(plain)-1], &p); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	return p
}

// pushService records each delivery by the path of the endpoint it was sent to,
// and answers with the status configured for that path (201 by default).
type pushService struct {
	*httptest.Server
	mu     sync.Mutex
	got    map[string][][]byte
	status map[string]int
	auth   map[string]string
}

func newPushService(t *testing.T) *pushService {
	ps := &pushService{got: map[string][][]byte{}, status: map[string]int{}, auth: map[string]string{}}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ps.mu.Lock()
		ps.got[r.URL.Path] = append(ps.got[r.URL.Path], b)
		ps.auth[r.URL.Path] = r.Header.Get("Authorization")
		code := ps.status[r.URL.Path]
		ps.mu.Unlock()
		if code == 0 {
			code = http.StatusCreated
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func (ps *pushService) take(path string) [][]byte {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := ps.got[path]
	delete(ps.got, path)
	return out
}

type fixture struct {
	a     *app.App
	api   humatest.TestAPI
	ws    string
	users map[string]string // name -> user id
	ctx   map[string]context.Context
	ps    *pushService
	dev   map[string]*device
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	owner, appURL := testdb.Setup(t, "alerts", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "alerts-test-secret-alerts-test-secret-0123")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("APP_ENV", "test")
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("VAPID_PUBLIC_KEY", pub)
	t.Setenv("VAPID_PRIVATE_KEY", priv)
	t.Setenv("VAPID_SUBJECT", "mailto:alerts@example.test")

	// Synchronous, so a test can look at the push service right after Emit.
	prev := dispatch
	dispatch = func(f func()) { f() }
	t.Cleanup(func() { dispatch = prev })

	ctx := context.Background()
	k := togo.New()
	a := app.New(ctx, k)
	t.Cleanup(func() { k.Close() })

	f := &fixture{a: a, users: map[string]string{}, ctx: map[string]context.Context{}, ps: newPushService(t), dev: map[string]*device{}}
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	roles := map[string]string{"owner": "owner", "member": "member", "client": "client", "suspended": "member"}
	for name := range roles {
		id := "u-" + name + "-" + suffix
		f.users[name] = id
		if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', now()::text)`, id, id+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.SysDB.QueryRowContext(ctx, `INSERT INTO workspaces (name, slug, created_by) VALUES ('Alerts Test', $1, $2) RETURNING id`,
		"alerts-"+suffix, f.users["owner"]).Scan(&f.ws); err != nil {
		t.Fatal(err)
	}
	for name, role := range roles {
		status := "active"
		if name == "suspended" {
			status = "suspended"
		}
		if _, err := a.SysDB.ExecContext(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role, status) VALUES ($1, $2, $3, $4)`,
			f.ws, f.users[name], role, status); err != nil {
			t.Fatal(err)
		}
		f.ctx[name] = tenant.With(ctx, &tenant.Scope{WorkspaceID: f.ws, UserID: f.users[name], Role: role, Via: "session"})
	}
	_, f.api = humatest.New(t)
	Register(f.api, a)
	return f
}

// subscribe enables push for a member through the real endpoint.
func (f *fixture) subscribe(t *testing.T, who, path string) string {
	t.Helper()
	d := newDevice(t, who)
	f.dev[path] = d
	p, au := d.keys()
	res := f.api.PostCtx(f.ctx[who], "/api/w/push/subscriptions", map[string]any{
		"endpoint": f.ps.URL + path, "keys": map[string]string{"p256dh": p, "auth": au}, "locale": "en",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("subscribe %s: %d %s", who, res.Code, res.Body)
	}
	var out struct {
		Device Device `json:"device"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	return out.Device.ID
}

// subscribeDirect writes a device row the way an earlier role would have: a
// client cannot enable push, but a member who enabled it and was later made a
// client keeps the row until they remove it — and must still hear nothing.
func (f *fixture) subscribeDirect(t *testing.T, who, path string) {
	t.Helper()
	d := newDevice(t, who)
	f.dev[path] = d
	p, au := d.keys()
	keys, _ := json.Marshal(sealedKeys{P256dh: p, Auth: au})
	sealed, err := seal(string(keys))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.SysDB.Exec(`INSERT INTO push_subscriptions (workspace_id, user_id, endpoint, keys_sealed) VALUES ($1, $2, $3, $4)`,
		f.ws, f.users[who], f.ps.URL+path, sealed); err != nil {
		t.Fatal(err)
	}
}

// received decrypts what the device at path got since the last look.
func (f *fixture) received(t *testing.T, path string) []Payload {
	t.Helper()
	var out []Payload
	for _, b := range f.ps.take(path) {
		out = append(out, f.dev[path].decrypt(t, b))
	}
	return out
}

func inboundEvent(conv string) realtime.Event {
	return realtime.Event{Kind: "message", Direction: "in", ConversationID: conv, Channel: "chat",
		Sender: "Rana", Snippet: "Is the blue one in stock?", Permission: authz.InboxRead, Alerting: true}
}

func TestPushAudience(t *testing.T) {
	f := newFixture(t)
	f.subscribe(t, "owner", "/owner")
	f.subscribe(t, "member", "/member")
	// Both enabled push in an earlier role; the rows are theirs, but a client
	// and a suspended member are nobody's audience for these events.
	f.subscribeDirect(t, "client", "/client")
	f.subscribeDirect(t, "suspended", "/suspended")

	t.Run("an unassigned customer message reaches every inbox reader, with its words", func(t *testing.T) {
		Emit(context.Background(), f.a, f.ws, inboundEvent("conv-1"))
		for _, who := range []string{"owner", "member"} {
			got := f.received(t, "/"+who)
			if len(got) != 1 {
				t.Fatalf("%s got %d pushes, want 1", who, len(got))
			}
			p := got[0]
			if p.Title != "New message from Rana" || p.Body != "Is the blue one in stock?" || p.ConversationID != "conv-1" ||
				!strings.HasSuffix(p.URL, "/inbox?c=conv-1") || !strings.HasPrefix(p.URL, "/en/w/alerts-") {
				t.Errorf("%s's push: %+v", who, p)
			}
		}
		if got := f.received(t, "/client"); len(got) != 0 {
			t.Errorf("a client (no inbox:read) was pushed a customer's message: %+v", got)
		}
		if got := f.received(t, "/suspended"); len(got) != 0 {
			t.Errorf("a suspended member was pushed: %+v", got)
		}
	})

	t.Run("an assigned conversation pushes only to its assignee", func(t *testing.T) {
		ev := inboundEvent("conv-2")
		ev.AlertOnly = f.users["member"]
		Emit(context.Background(), f.a, f.ws, ev)
		if got := f.received(t, "/member"); len(got) != 1 {
			t.Errorf("the assignee got %d pushes, want 1", len(got))
		}
		if got := f.received(t, "/owner"); len(got) != 0 {
			t.Errorf("the owner was pushed a conversation assigned to someone else: %+v", got)
		}
	})

	t.Run("a reply pushes to nobody", func(t *testing.T) {
		Emit(context.Background(), f.a, f.ws, realtime.Event{Kind: "message", Direction: "out", ConversationID: "conv-1",
			AuthorUserID: f.users["owner"], Permission: authz.InboxRead})
		for _, who := range []string{"owner", "member", "client"} {
			if got := f.received(t, "/"+who); len(got) != 0 {
				t.Errorf("%s was pushed a reply: %+v", who, got)
			}
		}
	})

	t.Run("a notification item reaches the feed readers only", func(t *testing.T) {
		Emit(context.Background(), f.a, f.ws, realtime.Event{Kind: "notification", ItemID: "item-1", Title: "Deploy failed", Source: "CI",
			Severity: "error", Permission: authz.NotificationsRead, Alerting: true})
		for _, who := range []string{"owner", "member"} {
			got := f.received(t, "/"+who)
			if len(got) != 1 || got[0].Title != "Deploy failed" || got[0].Body != "CI" || !strings.HasSuffix(got[0].URL, "/notifications") {
				t.Errorf("%s's notification push: %+v", who, got)
			}
		}
		if got := f.received(t, "/client"); len(got) != 0 {
			t.Errorf("a client was pushed a notification item: %+v", got)
		}
	})

	// The body guarantee does not rest on the audience filter alone: even an
	// event published under a permission a client DOES hold carries no
	// customer words to a member without inbox:read.
	t.Run("a member without inbox:read never receives a message body", func(t *testing.T) {
		ev := inboundEvent("conv-3")
		ev.Permission = authz.WorkspaceRead // mislabelled on purpose
		Emit(context.Background(), f.a, f.ws, ev)
		got := f.received(t, "/client")
		if len(got) != 1 {
			t.Fatalf("expected the mislabelled event to reach the client, got %d", len(got))
		}
		if got[0].Body != "" || strings.Contains(got[0].Title, "Rana") || strings.Contains(got[0].Title+got[0].Body, "blue") {
			t.Errorf("a member without inbox:read received conversation content: %+v", got[0])
		}
		f.received(t, "/owner")
		f.received(t, "/member")
	})

	t.Run("pushes are signed with this server's VAPID key", func(t *testing.T) {
		Emit(context.Background(), f.a, f.ws, inboundEvent("conv-4"))
		f.ps.mu.Lock()
		auth := f.ps.auth["/owner"]
		f.ps.mu.Unlock()
		if !strings.HasPrefix(auth, "vapid t=") || !strings.Contains(auth, ", k="+strings.TrimSpace(os.Getenv("VAPID_PUBLIC_KEY"))) {
			t.Errorf("Authorization = %q, want a VAPID header naming our public key", auth)
		}
		f.received(t, "/owner")
		f.received(t, "/member")
	})
}

func TestGoneSubscriptionsAreRemoved(t *testing.T) {
	f := newFixture(t)
	gone := f.subscribe(t, "owner", "/gone")
	missing := f.subscribe(t, "owner", "/missing")
	broken := f.subscribe(t, "owner", "/broken")
	ok := f.subscribe(t, "owner", "/ok")
	f.ps.status["/gone"] = http.StatusGone
	f.ps.status["/missing"] = http.StatusNotFound
	f.ps.status["/broken"] = http.StatusInternalServerError

	res, n, err := PushTest(context.Background(), f.a, f.ws, f.users["owner"])
	if err != nil || n != 4 {
		t.Fatalf("PushTest: n=%d err=%v", n, err)
	}
	if res.Sent != 1 || res.Removed != 2 || res.Failed != 1 {
		t.Errorf("result = %+v, want sent 1, removed 2, failed 1", res)
	}
	exists := func(id string) bool {
		var c int
		_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM push_subscriptions WHERE id = $1`, id).Scan(&c)
		return c == 1
	}
	if exists(gone) || exists(missing) {
		t.Error("a subscription answered 410/404 must be deleted")
	}
	if !exists(broken) || !exists(ok) {
		t.Error("a subscription that merely failed, or succeeded, must stay")
	}
	var lastErr string
	var lastOK *time.Time
	_ = f.a.SysDB.QueryRow(`SELECT last_error FROM push_subscriptions WHERE id = $1`, broken).Scan(&lastErr)
	_ = f.a.SysDB.QueryRow(`SELECT last_ok_at FROM push_subscriptions WHERE id = $1`, ok).Scan(&lastOK)
	if !strings.Contains(lastErr, "500") {
		t.Errorf("last_error = %q, want the refusal recorded", lastErr)
	}
	if lastOK == nil {
		t.Error("an accepted push must set last_ok_at")
	}
	if got := f.received(t, "/ok"); len(got) != 1 || got[0].Kind != "test" || got[0].Title != "Moharrik test notification" {
		t.Errorf("the test push: %+v", got)
	}
}

func TestSubscriptionRoutes(t *testing.T) {
	f := newFixture(t)
	d := newDevice(t, "x")
	p, au := d.keys()
	sub := func(who, endpoint string) int {
		return f.api.PostCtx(f.ctx[who], "/api/w/push/subscriptions", map[string]any{
			"endpoint": endpoint, "keys": map[string]string{"p256dh": p, "auth": au},
		}).Code
	}

	// The SSRF boundary: only push services (and loopback outside production).
	for _, bad := range []string{"http://example.com/push", "https://169.254.169.254/latest/meta-data", "https://internal.corp/push",
		"https://fcm.googleapis.com.evil.test/x", "ftp://127.0.0.1/x", "https://fcm.googleapis.com:8443/x"} {
		if code := sub("owner", bad); code != http.StatusUnprocessableEntity {
			t.Errorf("subscribing %s: got %d, want 422", bad, code)
		}
	}

	// A client has nothing to be pushed about, and no Delivery settings.
	if code := sub("client", f.ps.URL+"/c"); code != http.StatusForbidden {
		t.Errorf("a client subscribing: got %d, want 403", code)
	}

	ownerDev := f.subscribe(t, "owner", "/owner-laptop")
	f.subscribe(t, "member", "/member-phone")

	// Each member lists only their own devices.
	var st PushStatus
	res := f.api.GetCtx(f.ctx["owner"], "/api/w/push")
	_ = json.Unmarshal(res.Body.Bytes(), &st)
	if !st.Configured || st.PublicKey == "" || len(st.Devices) != 1 || st.Devices[0].ID != ownerDev {
		t.Errorf("the owner's status: %+v", st)
	}

	// Nobody removes another member's device — not even the owner.
	if code := f.api.DeleteCtx(f.ctx["member"], "/api/w/push/subscriptions/"+ownerDev).Code; code != http.StatusNotFound {
		t.Errorf("a member deleting the owner's device: got %d, want 404", code)
	}

	// The same browser again moves the row, never duplicates it.
	f.subscribe(t, "owner", "/owner-laptop")
	var n int
	_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM push_subscriptions WHERE workspace_id = $1 AND endpoint = $2`, f.ws, f.ps.URL+"/owner-laptop").Scan(&n)
	if n != 1 {
		t.Errorf("re-subscribing the same browser made %d rows", n)
	}

	// The test goes to the caller's own devices only.
	res = f.api.PostCtx(f.ctx["member"], "/api/w/push/test", map[string]any{})
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"sent":1`) {
		t.Errorf("the member's test: %d %s", res.Code, res.Body)
	}
	if got := f.received(t, "/member-phone"); len(got) != 1 {
		t.Errorf("the member's device got %d test pushes", len(got))
	}
	if got := f.received(t, "/owner-laptop"); len(got) != 0 {
		t.Errorf("the member's test reached the owner's laptop: %+v", got)
	}

	// Keys are sealed at rest, never stored as sent.
	var sealed string
	_ = f.a.SysDB.QueryRow(`SELECT keys_sealed FROM push_subscriptions WHERE id = $1`, ownerDev).Scan(&sealed)
	if strings.Contains(sealed, p) || strings.Contains(sealed, au) {
		t.Error("push keys are stored in the clear")
	}

	if code := f.api.DeleteCtx(f.ctx["owner"], "/api/w/push/subscriptions/"+ownerDev).Code; code != http.StatusNoContent {
		t.Errorf("the owner removing their own device: %d", code)
	}

	// Removing a member removes their devices with them.
	if _, err := f.a.SysDB.Exec(`DELETE FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, f.ws, f.users["member"]); err != nil {
		t.Fatal(err)
	}
	_ = f.a.SysDB.QueryRow(`SELECT count(*) FROM push_subscriptions WHERE workspace_id = $1 AND user_id = $2`, f.ws, f.users["member"]).Scan(&n)
	if n != 0 {
		t.Errorf("a removed member still has %d push subscriptions", n)
	}
}
