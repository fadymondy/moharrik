package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	circlexo "github.com/circlexo/circlexo-go"
	"github.com/circlexo/circlexo-go/circlexotest"
	"github.com/circlexo/circlexo-go/oidc"
	"github.com/circlexo/circlexo-go/webhooks"
	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	hub "github.com/fadymondy/moharrik/internal/circlexo"
	"github.com/fadymondy/moharrik/internal/marketing"
)

// The CircleXO integration against circlexotest, the SDK's fake hub. The secrets below are
// throwaway values made up for this file: they are not, and never were, a real credential.
const (
	testHubApp        = "app-moharrik-test"
	testWebhookSecret = "whsec_" + "bW9oYXJyaWstdGVzdC13ZWJob29rLXNlY3JldC0wMTIzNDU2"
	otherSecret       = "whsec_" + "c29tZS1vdGhlci1zZWNyZXQtbm90LXRoZS1yZWFsLW9uZQ=="
)

type hubStack struct {
	t   *testing.T
	hub *circlexotest.Hub
	a   *app.App
	url string
	svc *hub.Service
}

// bootHub starts the fake hub and a Moharrik stack configured for it. WEB_URL is a made-up host:
// the OIDC redirect only has to be consistent, and the tests that follow it rewrite the host.
func bootHub(t *testing.T) *hubStack {
	t.Helper()
	h := circlexotest.New(t, testHubApp)
	t.Setenv("CIRCLEXO_ISSUER", h.URL)
	t.Setenv("CIRCLEXO_APP_ID", testHubApp)
	t.Setenv("CIRCLEXO_CLIENT_ID", h.ClientID)
	t.Setenv("CIRCLEXO_CLIENT_SECRET", h.ClientSecret)
	t.Setenv("CIRCLEXO_APP_KEY", h.AppKey)
	t.Setenv("CIRCLEXO_WEBHOOK_SECRET", testWebhookSecret)
	t.Setenv("CIRCLEXO_SESSION_KEY", "test-session-key-not-a-secret")
	t.Setenv("WEB_URL", "http://moharrik.test")
	a, url := bootAudit(t)
	t.Cleanup(func() {
		hub.Install(nil) // a later test in this process boots without the hub (also clears the hooks)
	})
	svc := hub.Current()
	if svc == nil {
		t.Fatal("the integration did not start")
	}
	return &hubStack{t: t, hub: h, a: a, url: url, svc: svc}
}

// event POSTs a signed webhook and returns the status.
func (s *hubStack) event(typ, org string, data any, id string) int {
	s.t.Helper()
	body, _ := json.Marshal(map[string]any{"id": id, "type": typ, "version": 1, "occurred_at": time.Now().UTC(), "org_id": org, "data": data})
	return s.post(hub.EventsPath, body, id, testWebhookSecret)
}

func (s *hubStack) post(path string, body []byte, id, secret string) int {
	s.t.Helper()
	ts := time.Now()
	req, _ := http.NewRequest(http.MethodPost, s.url+path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(ts.Unix(), 10))
	if secret != "" {
		sig, err := webhooks.Sign(secret, id, ts, body)
		if err != nil {
			s.t.Fatal(err)
		}
		req.Header.Set("webhook-signature", sig)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode
}

func (s *hubStack) provision(org string) (int, map[string]string) {
	s.t.Helper()
	body, _ := json.Marshal(map[string]string{"org_id": org})
	req, _ := http.NewRequest(http.MethodPost, s.url+hub.ProvisionPath, strings.NewReader(string(body)))
	ts := time.Now()
	id := "prov-" + rnd()
	sig, _ := webhooks.Sign(testWebhookSecret, id, ts, body)
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(ts.Unix(), 10))
	req.Header.Set("webhook-signature", sig)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	out := map[string]string{}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

// install registers an org at the fake hub with these members (hub id, role; the e-mail is derived).
func (s *hubStack) install(org string, members ...[2]string) []circlexo.Member {
	s.hub.Install(org)
	var ms []circlexo.Member
	for _, m := range members {
		ms = append(ms, circlexo.Member{UserID: m[0], Email: m[0] + "@example.test", DisplayName: m[0], Role: m[1]})
	}
	s.hub.Lock()
	s.hub.Members[org] = ms
	s.hub.Unlock()
	return ms
}

// installed is install + provision, and the workspace the org became.
func (s *hubStack) installed(members ...[2]string) (org, ws, slug string, ms []circlexo.Member) {
	s.t.Helper()
	org = hubOrg()
	ms = s.install(org, members...)
	if code, _ := s.provision(org); code != 200 {
		s.t.Fatal("provision")
	}
	ws, slug, _ = s.workspaceOf(org)
	return
}

func (s *hubStack) roleOf(ws, email string) (role, status string) {
	s.t.Helper()
	err := s.a.SysDB.QueryRow(`SELECT m.role, m.status FROM workspace_members m JOIN users u ON u.id = m.user_id
		WHERE m.workspace_id = $1 AND lower(u.email) = lower($2)`, ws, email).Scan(&role, &status)
	if err == sql.ErrNoRows {
		return "", ""
	}
	if err != nil {
		s.t.Fatal(err)
	}
	return role, status
}

func (s *hubStack) workspaceOf(org string) (id, slug string, active bool) {
	s.t.Helper()
	err := s.a.SysDB.QueryRow(`SELECT l.workspace_id, w.slug, l.status = 'active' FROM circlexo_org_links l JOIN workspaces w ON w.id = l.workspace_id WHERE l.org_id = $1`, org).Scan(&id, &slug, &active)
	if err != nil {
		s.t.Fatalf("no workspace linked to org %s: %v", org, err)
	}
	return
}

func hubOrg() string { return "org-" + rnd() }

// login is a completed hub sign-in as claims say, through the same code the callback runs, in a
// browser that holds no Moharrik session.
func (s *hubStack) login(sub, email string, verified bool, org, role, sid string) (*http.Response, string, *hub.LoginError) {
	return s.loginAs("", sub, email, verified, org, role, sid)
}

// loginAs is login in a browser that already holds a valid Moharrik session of user `current`.
func (s *hubStack) loginAs(current, sub, email string, verified bool, org, role, sid string) (*http.Response, string, *hub.LoginError) {
	s.t.Helper()
	id := &circlexo.Claims{Subject: sub, SessionID: sid, Raw: map[string]any{"email": email, "email_verified": verified}}
	at := &circlexo.Claims{Subject: sub, OrgID: org, OrgRole: role, SessionID: sid}
	w := httptest.NewRecorder()
	to, err := s.svc.FinishLoginAs(context.Background(), w, &oidc.Login{IDClaims: id, AccessClaims: at}, current)
	return w.Result(), to, err
}

func sessionCookie(res *http.Response) *http.Cookie {
	for _, c := range res.Cookies() {
		if c.Name == "togo_session" && c.Value != "" {
			return c
		}
	}
	return nil
}

func (s *hubStack) me(c *http.Cookie) int {
	s.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, s.url+"/api/me/workspaces", nil)
	req.AddCookie(c)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func (s *hubStack) count(q string, args ...any) int {
	s.t.Helper()
	var n int
	if err := s.a.SysDB.QueryRow(q, args...).Scan(&n); err != nil {
		s.t.Fatal(err)
	}
	return n
}

func (s *hubStack) get(path string) (int, []byte) {
	s.t.Helper()
	res, err := http.Get(s.url + path)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

// cxoSignup registers a password account and returns its client, id and address.
func (s *hubStack) signup(prefix string) (*client, string, string) {
	s.t.Helper()
	c := newClient(s.t, s.url)
	email := prefix + "-" + rnd() + "@example.test"
	c.must("POST", "/api/auth/register", map[string]string{"email": email, "password": "correct-horse-9"}, 201)
	var uid string
	if err := s.a.SysDB.QueryRow(`SELECT id FROM users WHERE email = $1`, email).Scan(&uid); err != nil {
		s.t.Fatal(err)
	}
	return c, uid, email
}

// asCookie is a client that holds this session cookie, acting in workspace ws.
func (s *hubStack) asCookie(ck *http.Cookie, ws string) *client {
	s.t.Helper()
	c := newClient(s.t, s.url)
	u, _ := url.Parse(s.url)
	c.http.Jar.SetCookies(u, []*http.Cookie{ck})
	c.ws = ws
	return c
}

// roundTrip is the whole browser flow for one hub user: /auth/circlexo/login -> the hub's authorize
// endpoint (which signs the fake user in) -> /auth/circlexo/callback. It returns where the callback
// sent the browser and the browser the flow ran in.
func (s *hubStack) roundTrip(u circlexotest.User, launch string) (string, *http.Client) {
	s.t.Helper()
	s.hub.Lock()
	s.hub.User = u
	s.hub.Unlock()
	noFollow := func() *http.Client {
		return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	browser := noFollow()
	browser.Jar = newClient(s.t, s.url).http.Jar
	step := func(c *http.Client, u string) *http.Response {
		res, err := c.Get(u)
		if err != nil {
			s.t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	start := hub.LoginPath
	if launch != "" {
		start = hub.LaunchPath + "?org=" + url.QueryEscape(launch)
	}
	res := step(browser, s.url+start)
	if launch != "" { // the launch URL bounces to the login route
		res = step(browser, s.url+res.Header.Get("Location"))
	}
	to := res.Header.Get("Location")
	if res.StatusCode != http.StatusFound || !strings.HasPrefix(to, s.hub.URL) {
		s.t.Fatalf("login should redirect to the hub, got %d %q", res.StatusCode, to)
	}
	res = step(noFollow(), to) // the hub, signed in as u
	cb, err := url.Parse(res.Header.Get("Location"))
	if err != nil || cb.Path != hub.CallbackPath {
		s.t.Fatalf("the hub sent us to %q", res.Header.Get("Location"))
	}
	res = step(browser, s.url+cb.Path+"?"+cb.RawQuery)
	if res.StatusCode != http.StatusFound {
		s.t.Fatalf("callback: %d", res.StatusCode)
	}
	return res.Header.Get("Location"), browser
}

// signedIn reports whether the browser's session works.
func (s *hubStack) signedIn(b *http.Client) bool {
	s.t.Helper()
	res, err := b.Get(s.url + "/api/me/workspaces")
	if err != nil {
		s.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode == 200
}

// The whole browser round trip against the test hub, which vouches for the address
// (email_verified) unless the user says otherwise.
func TestCircleXOLoginRoundTrip(t *testing.T) {
	s := bootHub(t)
	if code, b := s.get("/api/circlexo/config"); code != 200 || !strings.Contains(string(b), `"enabled":true`) || !strings.Contains(string(b), `"login_url":"/auth/circlexo/login"`) {
		t.Fatalf("config: %d %s", code, b)
	}

	t.Run("new user is created, linked and signed in", func(t *testing.T) {
		email, sub := "roundtrip-"+rnd()+"@example.test", "sub-"+rnd()
		to, browser := s.roundTrip(circlexotest.User{ID: sub, Email: email, Name: "Round Trip"}, "")
		if to != "/" || !s.signedIn(browser) {
			t.Fatalf("landing %q, signed in %v", to, s.signedIn(browser))
		}
		if n := s.count(`SELECT count(*) FROM circlexo_user_links l JOIN users u ON u.id = l.user_id WHERE l.subject = $1 AND lower(u.email) = $2 AND l.linked_via = 'created'`, sub, email); n != 1 {
			t.Fatalf("expected one created link, got %d", n)
		}
		// Signing in again is the same user, not a second account.
		before := s.count(`SELECT count(*) FROM users`)
		if _, b2 := s.roundTrip(circlexotest.User{ID: sub, Email: email}, ""); !s.signedIn(b2) || s.count(`SELECT count(*) FROM users`) != before {
			t.Fatal("a second sign-in did not reuse the account")
		}
	})

	t.Run("a password account is not taken over by a hub user with its address", func(t *testing.T) {
		_, _, email := s.signup("rtpw")
		sub := "sub-" + rnd()
		to, browser := s.roundTrip(circlexotest.User{ID: sub, Email: email}, "")
		if to != "/login?circlexo_error=account_conflict" || s.signedIn(browser) {
			t.Fatalf("got %q (signed in %v)", to, s.signedIn(browser))
		}
		if s.count(`SELECT count(*) FROM circlexo_user_links WHERE subject = $1`, sub) != 0 {
			t.Fatal("a refused sign-in left a link")
		}
	})

	t.Run("an unverified address is refused and creates nothing", func(t *testing.T) {
		email := "unverified-" + rnd() + "@example.test"
		to, browser := s.roundTrip(circlexotest.User{ID: "sub-" + rnd(), Email: email, EmailUnverified: true}, "")
		if to != "/login?circlexo_error=email_unverified" || s.signedIn(browser) {
			t.Fatalf("got %q (signed in %v)", to, s.signedIn(browser))
		}
		if n := s.count(`SELECT count(*) FROM users WHERE lower(email) = $1`, email); n != 0 {
			t.Fatalf("a refused sign-in created %d account(s)", n)
		}
	})

	t.Run("launch URL lands in the org's workspace", func(t *testing.T) {
		org, _, slug, _ := s.installed([2]string{"own-" + rnd(), "owner"})
		s.hub.Lock()
		delete(s.hub.Entitlements, org)
		s.hub.Unlock()
		to, browser := s.roundTrip(circlexotest.User{ID: "sub-" + rnd(), Email: "launch-" + rnd() + "@example.test", OrgID: org, OrgRole: "member"}, slug)
		if to != "/en/w/"+slug || !s.signedIn(browser) {
			t.Fatalf("launch landed on %q", to)
		}
	})
}

// Linking: the rules of sso.go, one by one.
func TestCircleXOLinking(t *testing.T) {
	s := bootHub(t)

	t.Run("a hub user with a new address gets a passwordless linked account", func(t *testing.T) {
		email, sub := "new-"+rnd()+"@example.test", "sub-"+rnd()
		res, to, err := s.login(sub, email, true, "", "", "sid-"+rnd())
		if err != nil || to != "/" {
			t.Fatalf("%q %v", to, err)
		}
		if ck := sessionCookie(res); ck == nil || s.me(ck) != 200 {
			t.Fatal("the session cookie does not sign in")
		}
		var hash string
		if e := s.a.SysDB.QueryRow(`SELECT password_hash FROM users WHERE lower(email) = $1`, email).Scan(&hash); e != nil || hash != "!sso" {
			t.Fatalf("the account must have no usable password: %q %v", hash, e)
		}
		// The same hub user again is the same Moharrik user, even with another address claim.
		before := s.count(`SELECT count(*) FROM users`)
		if _, _, err := s.login(sub, "other-"+rnd()+"@example.test", true, "", "", "sid-"+rnd()); err != nil {
			t.Fatal(err)
		}
		if s.count(`SELECT count(*) FROM users`) != before {
			t.Fatal("a second sign-in created another account")
		}
	})

	t.Run("a passwordless account is linked by its verified address", func(t *testing.T) {
		// A member the hub synced: created by an install, no password.
		_, _, _, ms := s.installed([2]string{"own-" + rnd(), "owner"})
		sub := "sub-" + rnd()
		before := s.count(`SELECT count(*) FROM users`)
		res, _, err := s.login(sub, strings.ToUpper(ms[0].Email), true, "", "", "sid-"+rnd())
		if err != nil || sessionCookie(res) == nil {
			t.Fatalf("%v", err)
		}
		if s.count(`SELECT count(*) FROM users`) != before {
			t.Fatal("a second account was made")
		}
		var via string
		if e := s.a.SysDB.QueryRow(`SELECT linked_via FROM circlexo_user_links WHERE subject = $1`, sub).Scan(&via); e != nil || via != "email" {
			t.Fatalf("expected an email link: %q %v", via, e)
		}
		// Another hub user with the same address cannot take the account over.
		if _, _, err := s.login("sub-"+rnd(), ms[0].Email, true, "", "", "sid-"+rnd()); err == nil || err.Code() != "account_conflict" {
			t.Fatalf("a second hub subject must not link to a linked account, got %v", err)
		}
	})

	t.Run("a password account is refused unless its owner is signed in", func(t *testing.T) {
		c, uid, email := s.signup("link")
		if _, _, err := s.login("sub-"+rnd(), email, true, "", "", "sid-"+rnd()); err == nil || err.Code() != "account_conflict" {
			t.Fatalf("a password account must not be linked from a signed-out browser, got %v", err)
		}
		// Someone else's session is no proof either.
		_, other, _ := s.signup("otherlink")
		if _, _, err := s.loginAs(other, "sub-"+rnd(), email, true, "", "", "sid-"+rnd()); err == nil || err.Code() != "account_conflict" {
			t.Fatalf("another user's session must not link, got %v", err)
		}
		// The owner, signed in here, links it.
		sub := "sub-" + rnd()
		res, _, err := s.loginAs(uid, sub, email, true, "", "", "sid-"+rnd())
		if err != nil || sessionCookie(res) == nil {
			t.Fatalf("%v", err)
		}
		var linked string
		if e := s.a.SysDB.QueryRow(`SELECT user_id FROM circlexo_user_links WHERE subject = $1`, sub).Scan(&linked); e != nil || linked != uid {
			t.Fatalf("expected a link to %s: %s %v", uid, linked, e)
		}
		// The password still works: dual login.
		c.must("POST", "/api/auth/login", map[string]string{"email": email, "password": "correct-horse-9"}, 200)
	})

	t.Run("an unverified hub address is refused", func(t *testing.T) {
		email := "unv-" + rnd() + "@example.test"
		if _, _, err := s.login("sub-"+rnd(), email, false, "", "", "sid-"+rnd()); err == nil || err.Code() != "email_unverified" {
			t.Fatalf("got %v", err)
		}
		if n := s.count(`SELECT count(*) FROM users WHERE lower(email) = $1`, email); n != 0 {
			t.Fatal("an account was created")
		}
	})

	t.Run("no address is refused", func(t *testing.T) {
		if _, _, err := s.login("sub-"+rnd(), "", true, "", "", "sid-"+rnd()); err == nil || err.Code() != "no_email" {
			t.Fatalf("got %v", err)
		}
	})
}

// Provision is idempotent, confirms the tenant to the hub, maps roles, and refuses bad signatures.
func TestCircleXOProvision(t *testing.T) {
	s := bootHub(t)
	org := hubOrg()
	ms := s.install(org, [2]string{"own-" + rnd(), "owner"}, [2]string{"adm-" + rnd(), "admin"}, [2]string{"mem-" + rnd(), "member"}, [2]string{"bil-" + rnd(), "billing"})

	if code := s.post(hub.ProvisionPath, []byte(`{"org_id":"`+org+`"}`), "x", ""); code != http.StatusUnauthorized {
		t.Fatalf("an unsigned provision call must be 401, got %d", code)
	}
	if code := s.post(hub.ProvisionPath, []byte(`{"org_id":"`+org+`"}`), "x", otherSecret); code != http.StatusUnauthorized {
		t.Fatalf("a wrongly signed provision call must be 401, got %d", code)
	}
	if n := s.count(`SELECT count(*) FROM circlexo_org_links WHERE org_id = $1`, org); n != 0 {
		t.Fatal("a refused call provisioned")
	}

	code, first := s.provision(org)
	if code != 200 || first["product_tenant_id"] == "" || first["slug"] == "" {
		t.Fatalf("provision: %d %v", code, first)
	}
	code, second := s.provision(org)
	if code != 200 || second["product_tenant_id"] != first["product_tenant_id"] {
		t.Fatalf("provision must be idempotent: %v then %v", first, second)
	}
	if n := s.count(`SELECT count(*) FROM circlexo_org_links WHERE org_id = $1`, org); n != 1 {
		t.Fatalf("%d links", n)
	}
	ws, slug, active := s.workspaceOf(org)
	if !active || ws != first["product_tenant_id"] || slug != first["slug"] {
		t.Fatalf("link does not match the answer: %s %s %v", ws, slug, active)
	}
	s.hub.Lock()
	tn := s.hub.Tenants[org]
	s.hub.Unlock()
	if tn.ProductTenantID != ws || tn.ProductTenantSlug != slug || tn.Status != "active" {
		t.Fatalf("the hub was not told about the tenant: %+v", tn)
	}

	// Members and the role mapping: owner/admin keep their rank, member and billing are members.
	want := map[string]string{ms[0].Email: "owner", ms[1].Email: "admin", ms[2].Email: "member", ms[3].Email: "member"}
	for email, role := range want {
		if got, st := s.roleOf(ws, email); got != role || st != "active" {
			t.Fatalf("%s: got %q/%q want %q", email, got, st, role)
		}
	}
	if n := s.count(`SELECT count(*) FROM workspace_members WHERE workspace_id = $1`, ws); n != 4 {
		t.Fatalf("%d members", n)
	}

	// The same install by webhook is the same workspace, and a redelivery is dropped by id.
	evt := "evt-" + rnd()
	if code := s.event(webhooks.AppInstalled, org, map[string]any{"slug": "org", "name": "Org"}, evt); code != http.StatusNoContent {
		t.Fatalf("install webhook: %d", code)
	}
	if again, _, _ := s.workspaceOf(org); again != ws {
		t.Fatal("the webhook made a second workspace")
	}
	// Replay: the same id again is acknowledged and does nothing (a demotion it would have made
	// must not happen twice).
	mem := strings.TrimSuffix(ms[2].Email, "@example.test")
	person := map[string]any{"id": mem, "email": ms[2].Email, "display_name": mem}
	id := "rp-" + rnd()
	s.event(webhooks.MemberRoleChanged, org, map[string]any{"user": person, "role": "admin"}, id)
	if r, _ := s.roleOf(ws, ms[2].Email); r != "admin" {
		t.Fatalf("promotion: %s", r)
	}
	s.a.SysDB.Exec(`UPDATE workspace_members m SET role = 'member' FROM users u WHERE u.id = m.user_id AND m.workspace_id = $1 AND u.email = $2`, ws, ms[2].Email)
	if code := s.event(webhooks.MemberRoleChanged, org, map[string]any{"user": person, "role": "admin"}, id); code != http.StatusNoContent {
		t.Fatalf("replay: %d", code)
	}
	if r, _ := s.roleOf(ws, ms[2].Email); r != "member" {
		t.Fatalf("a replayed event ran again: %s", r)
	}

	// A webhook with a bad signature never runs.
	other := hubOrg()
	s.install(other, [2]string{"o-" + rnd(), "owner"})
	body, _ := json.Marshal(map[string]any{"id": "evt-bad", "type": webhooks.AppInstalled, "org_id": other, "data": map[string]any{}})
	if code := s.post(hub.EventsPath, body, "evt-bad", otherSecret); code != http.StatusUnauthorized {
		t.Fatalf("bad signature: %d", code)
	}
	if n := s.count(`SELECT count(*) FROM circlexo_org_links WHERE org_id = $1`, other); n != 0 {
		t.Fatal("a badly signed install provisioned")
	}
	if code := s.post(hub.EventsPath, body, "evt-none", ""); code != http.StatusUnauthorized {
		t.Fatalf("unsigned webhook: %d", code)
	}
	// A stale timestamp is refused too.
	old := time.Now().Add(-time.Hour)
	sig, _ := webhooks.Sign(testWebhookSecret, "evt-old", old, body)
	req, _ := http.NewRequest(http.MethodPost, s.url+hub.EventsPath, strings.NewReader(string(body)))
	req.Header.Set("webhook-id", "evt-old")
	req.Header.Set("webhook-timestamp", strconv.FormatInt(old.Unix(), 10))
	req.Header.Set("webhook-signature", sig)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a stale webhook: %d", res.StatusCode)
	}
}

// member.* webhooks, and deprovision (which keeps everything).
func TestCircleXOMemberSync(t *testing.T) {
	s := bootHub(t)
	owner, mem := "own-"+rnd(), "mem-"+rnd()
	org, ws, _, ms := s.installed([2]string{owner, "owner"}, [2]string{mem, "member"})
	person := func(id string) map[string]any {
		return map[string]any{"id": id, "email": id + "@example.test", "display_name": id}
	}

	// Promoted in the hub.
	s.event(webhooks.MemberRoleChanged, org, map[string]any{"user": person(mem), "role": "admin", "previous_role": "member"}, "e1-"+rnd())
	if r, st := s.roleOf(ws, ms[1].Email); r != "admin" || st != "active" {
		t.Fatalf("after promotion: %s %s", r, st)
	}
	// A Moharrik-only role survives a hub "member" (the hub cannot express manager).
	s.a.SysDB.Exec(`UPDATE workspace_members m SET role = 'manager' FROM users u WHERE u.id = m.user_id AND m.workspace_id = $1 AND u.email = $2`, ws, ms[1].Email)
	s.event(webhooks.MemberRoleChanged, org, map[string]any{"user": person(mem), "role": "member"}, "e2-"+rnd())
	if r, _ := s.roleOf(ws, ms[1].Email); r != "manager" {
		t.Fatalf("a manager should stay a manager on hub role member, got %s", r)
	}
	// A new member.
	newbie := "new-" + rnd()
	s.event(webhooks.MemberAdded, org, map[string]any{"user": person(newbie), "role": "member"}, "e3-"+rnd())
	if r, st := s.roleOf(ws, newbie+"@example.test"); r != "member" || st != "active" {
		t.Fatalf("added member: %s %s", r, st)
	}
	// Removed: suspended, not deleted.
	s.event(webhooks.MemberRemoved, org, map[string]any{"user": person(newbie)}, "e4-"+rnd())
	if r, st := s.roleOf(ws, newbie+"@example.test"); r != "member" || st != "suspended" {
		t.Fatalf("removed member: %s %s", r, st)
	}
	// Re-added: active again.
	s.event(webhooks.MemberAdded, org, map[string]any{"user": person(newbie), "role": "member"}, "e5-"+rnd())
	if _, st := s.roleOf(ws, newbie+"@example.test"); st != "active" {
		t.Fatalf("re-added member: %s", st)
	}
	// The last owner is never removed or demoted by the hub.
	s.event(webhooks.MemberRemoved, org, map[string]any{"user": person(owner)}, "e6-"+rnd())
	s.event(webhooks.MemberRoleChanged, org, map[string]any{"user": person(owner), "role": "member"}, "e7-"+rnd())
	if r, st := s.roleOf(ws, ms[0].Email); r != "owner" || st != "active" {
		t.Fatalf("the last owner changed: %s %s", r, st)
	}
	// An event for an org that never installed the app is acknowledged and does nothing.
	if code := s.event(webhooks.MemberAdded, hubOrg(), map[string]any{"user": person("x-" + rnd()), "role": "owner"}, "e8-"+rnd()); code != http.StatusNoContent {
		t.Fatalf("unknown org: %d", code)
	}

	// Deprovision marks the link inactive and deletes nothing.
	members := s.count(`SELECT count(*) FROM workspace_members WHERE workspace_id = $1`, ws)
	if code := s.event(webhooks.AppRemoved, org, map[string]any{}, "e9-"+rnd()); code != http.StatusNoContent {
		t.Fatalf("app_removed: %d", code)
	}
	if _, _, active := s.workspaceOf(org); active {
		t.Fatal("the link is still active")
	}
	if s.count(`SELECT count(*) FROM workspaces WHERE id = $1`, ws) != 1 || s.count(`SELECT count(*) FROM workspace_members WHERE workspace_id = $1`, ws) != members {
		t.Fatal("deprovision deleted data")
	}
	// Installing again reactivates the same workspace.
	if code, out := s.provision(org); code != 200 || out["product_tenant_id"] != ws {
		t.Fatalf("re-provision: %d %v", code, out)
	}
	if _, _, active := s.workspaceOf(org); !active {
		t.Fatal("not reactivated")
	}
}

// After sign-in the user lands in the workspace of the token's org, with the hub role.
func TestCircleXOLandingWorkspace(t *testing.T) {
	s := bootHub(t)
	org := hubOrg()
	sub := "sub-" + rnd()
	s.install(org, [2]string{"own-" + rnd(), "owner"})
	email := "land-" + rnd() + "@example.test"

	// Not provisioned yet: the sign-in provisions it (the hub confirms the org has the app).
	res, to, err := s.login(sub, email, true, org, "admin", "sid-"+rnd())
	if err != nil {
		t.Fatal(err)
	}
	ws, slug, _ := s.workspaceOf(org)
	if to != "/en/w/"+slug {
		t.Fatalf("landing: %q want /en/w/%s", to, slug)
	}
	if r, st := s.roleOf(ws, email); r != "admin" || st != "active" {
		t.Fatalf("hub admin should be a workspace admin: %s %s", r, st)
	}
	if sessionCookie(res) == nil {
		t.Fatal("no session")
	}
	// An org the hub does not know lands on the home page, and nothing is created.
	other := hubOrg()
	if _, to, err := s.login(sub, email, true, other, "owner", "sid-"+rnd()); err != nil || to != "/" {
		t.Fatalf("unknown org: %q %v", to, err)
	}
	if s.count(`SELECT count(*) FROM circlexo_org_links WHERE org_id = $1`, other) != 0 {
		t.Fatal("an unknown org was provisioned")
	}
}

func hubEnts(org string, version int64, active bool, feats ...circlexo.Entitlement) circlexo.Entitlements {
	return circlexo.Entitlements{OrgID: org, AppID: testHubApp, Active: active, Plan: "pro", Status: "active", Version: version, Features: feats}
}

func lim(n int64) *int64 { return &n }

// Entitlements: exposed at /api/w/circlexo, enforced on invitations and campaign sends for a linked
// workspace, never touching an unlinked one; a hub outage is read-only inside the grace window.
func TestCircleXOEntitlements(t *testing.T) {
	s := bootHub(t)
	ownerID := "own-" + rnd()
	org, ws, _, ms := s.installed([2]string{ownerID, "owner"})
	res, _, err := s.login("sub-"+rnd(), ms[0].Email, true, org, "owner", "sid-"+rnd())
	if err != nil {
		t.Fatal(err)
	}
	owner := s.asCookie(sessionCookie(res), ws)
	ctx := context.Background()

	set := func(version int64, active bool, feats ...circlexo.Entitlement) {
		s.hub.Lock()
		s.hub.Entitlements[org] = hubEnts(org, version, active, feats...)
		s.hub.Unlock()
		s.svc.Ents.Invalidate(org, 0)
	}
	invite := func(c *client) int {
		code, _ := c.do("POST", "/api/w/invitations", map[string]string{"email": "inv-" + rnd() + "@example.test", "role": "member"})
		return code
	}
	campaignHook := func() error { return marketing.BeforeCampaignSend(ctx, ws) }
	status := func(err error) int {
		if err == nil {
			return 200
		}
		if se, ok := err.(huma.StatusError); ok {
			return se.GetStatus()
		}
		return -1
	}

	// Exposed.
	set(2, true,
		circlexo.Entitlement{Key: "members", Kind: "limit", Enabled: true, Limit: lim(1)},
		circlexo.Entitlement{Key: "campaigns", Kind: "flag", Enabled: true},
		circlexo.Entitlement{Key: "emails_sent", Kind: "meter", Enabled: true, Limit: lim(100), UsedInPeriod: 40},
	)
	code, body := owner.do("GET", "/api/w/circlexo", nil)
	var out hub.CircleXOOut
	if code != 200 || json.Unmarshal(body, &out) != nil || !out.Enabled || !out.Linked || !out.Active || out.Plan != "pro" || len(out.Features) != 3 {
		t.Fatalf("GET /api/w/circlexo: %d %s", code, body)
	}
	for _, f := range out.Features {
		if f.Key == "members" && (!f.Enforced || f.Limit == nil || *f.Limit != 1) {
			t.Fatalf("the members limit should be shown and enforced: %+v", f)
		}
	}

	// members limit: the owner is the one active member, so a limit of 1 refuses an invitation.
	if c := invite(owner); c != http.StatusForbidden {
		t.Fatalf("an invitation past the members limit must be refused, got %d", c)
	}
	set(3, true, circlexo.Entitlement{Key: "members", Kind: "limit", Enabled: true, Limit: lim(5)}, circlexo.Entitlement{Key: "campaigns", Kind: "flag", Enabled: true})
	if c := invite(owner); c != 201 {
		t.Fatalf("an invitation within the limit: %d", c)
	}

	// campaigns: the flag and the meter's allowance.
	if e := campaignHook(); e != nil {
		t.Fatalf("a send with the feature on and no meter cap: %v", e)
	}
	set(4, true, circlexo.Entitlement{Key: "members", Kind: "limit", Enabled: true}) // no campaigns
	if st := status(campaignHook()); st != http.StatusForbidden {
		t.Fatalf("a plan without campaigns must refuse a send, got %d", st)
	}
	set(5, true, circlexo.Entitlement{Key: "campaigns", Kind: "flag", Enabled: true},
		circlexo.Entitlement{Key: "emails_sent", Kind: "meter", Enabled: true, Limit: lim(100), UsedInPeriod: 100})
	if st := status(campaignHook()); st != http.StatusForbidden {
		t.Fatalf("an used-up allowance must refuse a send, got %d", st)
	}
	// ... and StartSend itself refuses before touching the campaign or the SMTP service.
	if err := marketing.StartSend(ctx, s.a, ws, "does-not-matter", nil); status(err) != http.StatusForbidden {
		t.Fatalf("StartSend must ask the hub first: %v", err)
	}
	set(6, true, circlexo.Entitlement{Key: "campaigns", Kind: "flag", Enabled: true},
		circlexo.Entitlement{Key: "emails_sent", Kind: "meter", Enabled: true, Limit: lim(100), UsedInPeriod: 100})
	s.hub.Lock()
	e := s.hub.Entitlements[org]
	e.PaygEnabled = true
	s.hub.Entitlements[org] = e
	s.hub.Unlock()
	s.svc.Ents.Invalidate(org, 0)
	if st := status(campaignHook()); st != 200 {
		t.Fatalf("pay-as-you-go lifts the allowance, got %d", st)
	}

	// entitlement.changed and subscription.* invalidate: the hub's new answer is seen at once.
	set(7, true, circlexo.Entitlement{Key: "campaigns", Kind: "flag", Enabled: true})
	if st := status(campaignHook()); st != 200 {
		t.Fatalf("baseline: %d", st)
	}
	s.hub.Lock()
	s.hub.Entitlements[org] = hubEnts(org, 8, true) // campaigns gone at the hub
	s.hub.Unlock()
	if st := status(campaignHook()); st != 200 {
		t.Fatal("expected the cached (stale) answer before the event")
	}
	s.event(webhooks.EntitlementChanged, org, map[string]any{"version": 8, "reason": "plan_changed"}, "en-"+rnd())
	if st := status(campaignHook()); st != http.StatusForbidden {
		t.Fatal("entitlement.changed did not invalidate")
	}
	s.hub.Lock()
	s.hub.Entitlements[org] = hubEnts(org, 9, true, circlexo.Entitlement{Key: "campaigns", Kind: "flag", Enabled: true})
	s.hub.Unlock()
	s.event("subscription.updated", org, map[string]any{}, "sub-"+rnd())
	if st := status(campaignHook()); st != 200 {
		t.Fatalf("subscription.* did not invalidate: %d", st)
	}

	// An inactive subscription refuses.
	set(10, false, circlexo.Entitlement{Key: "campaigns", Kind: "flag", Enabled: true})
	if st := status(campaignHook()); st != http.StatusForbidden {
		t.Fatalf("an inactive subscription must refuse, got %d", st)
	}

	// Hub down, inside the grace window: the last answer stands, read-only (gated actions refused).
	set(11, true, circlexo.Entitlement{Key: "campaigns", Kind: "flag", Enabled: true})
	if _, err := s.svc.Ents.Get(ctx, org, 0); err != nil {
		t.Fatal(err)
	}
	s.svc.Ents.Invalidate(org, 0)
	s.hub.Lock()
	s.hub.Fail = http.StatusInternalServerError
	s.hub.Unlock()
	if st, _ := s.svc.StateOf(ctx, ws); st.Set != nil && !st.Set.ReadOnly {
		t.Fatalf("expected a read-only or unknown state while the hub is down: %+v", st.Set)
	}
	if st := status(campaignHook()); st != http.StatusForbidden {
		t.Fatalf("a hub outage must fail closed for gated actions, got %d", st)
	}
	if c, _ := owner.do("GET", "/api/w/circlexo", nil); c != 200 {
		t.Fatalf("reading keeps working: %d", c)
	}
	s.hub.Lock()
	s.hub.Fail = 0
	s.hub.Unlock()

	// An org the hub has no entitlements for fails closed.
	s.hub.Lock()
	delete(s.hub.Entitlements, org)
	s.hub.Unlock()
	s.svc.Ents.Invalidate(org, 0)
	if st := status(campaignHook()); st != http.StatusForbidden {
		t.Fatalf("no entitlements must fail closed, got %d", st)
	}
	if c := invite(owner); c != http.StatusForbidden {
		t.Fatalf("no entitlements must refuse invitations, got %d", c)
	}

	// An unlinked workspace is untouched: same code path, no hub involved.
	plain, _, _ := s.signup("plain")
	w := plain.must("POST", "/api/me/workspaces", map[string]string{"name": "Plain " + rnd()}, 201)["workspace"].(map[string]any)
	plain.ws = w["id"].(string)
	if c := invite(plain); c != 201 {
		t.Fatalf("an unlinked workspace must not be limited: %d", c)
	}
	if err := marketing.BeforeCampaignSend(ctx, plain.ws); err != nil {
		t.Fatalf("an unlinked workspace must not be gated: %v", err)
	}
	var pout hub.CircleXOOut
	if code, body := plain.do("GET", "/api/w/circlexo", nil); code != 200 || json.Unmarshal(body, &pout) != nil || !pout.Enabled || pout.Linked || len(pout.Features) != 0 {
		t.Fatalf("an unlinked workspace shows no link: %d %s", code, body)
	}

	// Deprovisioned: back to being a plain workspace.
	s.event(webhooks.AppRemoved, org, map[string]any{}, "rm-"+rnd())
	if err := marketing.BeforeCampaignSend(ctx, ws); err != nil {
		t.Fatalf("after deprovision the workspace is no longer gated: %v", err)
	}
}

// Usage: a contact created in a linked workspace is reported to the hub once; an unlinked
// workspace reports nothing.
func TestCircleXOUsage(t *testing.T) {
	s := bootHub(t)
	org, ws, _, ms := s.installed([2]string{"own-" + rnd(), "owner"})
	s.hub.Lock()
	s.hub.Entitlements[org] = hubEnts(org, 1, true, circlexo.Entitlement{Key: "contacts", Kind: "meter", Enabled: true})
	s.hub.Unlock()
	res, _, err := s.login("sub-"+rnd(), ms[0].Email, true, org, "owner", "sid-"+rnd())
	if err != nil {
		t.Fatal(err)
	}
	owner := s.asCookie(sessionCookie(res), ws)
	email := "usage-" + rnd() + "@example.test"
	ct := owner.must("POST", "/api/w/contacts", map[string]any{"email": email, "name": "Usage"}, 201)["contact"].(map[string]any)["id"].(string)
	// The same contact again is a match, not a creation: nothing more is reported.
	owner.must("POST", "/api/w/contacts", map[string]any{"email": email, "name": "Usage"}, 201)

	key := "contact:" + ct
	var got circlexo.UsageReport
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.hub.Lock()
		got = s.hub.Usage[key]
		n := len(s.hub.Usage)
		s.hub.Unlock()
		if got.IdempotencyKey != "" || time.Now().After(deadline) {
			if n != 1 {
				t.Fatalf("expected exactly one usage report, the hub has %d", n)
			}
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got.OrgID != org || got.Feature != "contacts" || got.Qty != 1 {
		t.Fatalf("usage report: %+v", got)
	}

	// An unlinked workspace's contacts are not reported.
	plain, _, _ := s.signup("usageplain")
	w := plain.must("POST", "/api/me/workspaces", map[string]string{"name": "UP " + rnd()}, 201)["workspace"].(map[string]any)
	plain.ws = w["id"].(string)
	plain.must("POST", "/api/w/contacts", map[string]any{"email": "up-" + rnd() + "@example.test", "name": "P"}, 201)
	time.Sleep(300 * time.Millisecond)
	s.hub.Lock()
	n := len(s.hub.Usage)
	s.hub.Unlock()
	if n != 1 {
		t.Fatalf("an unlinked workspace reported usage: %d reports", n)
	}
}

// session.revoked ends the Moharrik session that came from that hub session, and only that one.
func TestCircleXOSessionRevoked(t *testing.T) {
	s := bootHub(t)
	_, _, _, ms := s.installed([2]string{"own-" + rnd(), "owner"})
	sub := "sub-" + rnd()
	res1, _, err := s.login(sub, ms[0].Email, true, "", "", "sid-a-"+rnd())
	if err != nil {
		t.Fatal(err)
	}
	// Distinct iat: the marker is (user, iat) at one-second resolution.
	time.Sleep(1100 * time.Millisecond)
	sidB := "sid-b-" + rnd()
	res2, _, err := s.login(sub, ms[0].Email, true, "", "", sidB)
	if err != nil {
		t.Fatal(err)
	}
	a, b := sessionCookie(res1), sessionCookie(res2)
	if s.me(a) != 200 || s.me(b) != 200 {
		t.Fatal("both sessions should work")
	}
	if code := s.event(webhooks.SessionRevoked, "", map[string]any{"user": map[string]any{"id": sub}, "sid": sidB}, "sr-"+rnd()); code != http.StatusNoContent {
		t.Fatalf("session.revoked: %d", code)
	}
	if s.me(b) != http.StatusUnauthorized {
		t.Fatal("the revoked session still works")
	}
	if s.me(a) != 200 {
		t.Fatal("another hub session was revoked too")
	}
}

// Federated MCP: a hub-issued token reaches /api/mcp as a Moharrik principal.
func TestCircleXOMCP(t *testing.T) {
	s := bootHub(t)
	org, ws, _, ms := s.installed([2]string{"own-" + rnd(), "owner"}, [2]string{"mem-" + rnd(), "member"})
	s.hub.Lock()
	s.hub.Entitlements[org] = hubEnts(org, 1, true)
	s.hub.Unlock()
	sub, subMember := "sub-"+rnd(), "sub-"+rnd()
	if _, _, err := s.login(sub, ms[0].Email, true, org, "owner", "sid-"+rnd()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.login(subMember, ms[1].Email, true, org, "member", "sid-"+rnd()); err != nil {
		t.Fatal(err)
	}
	call := func(token, tool string, args map[string]any) (int, string, bool) {
		c := newClient(t, s.url)
		c.bearer = token
		return mcpCall(c, "/api/mcp", tool, args)
	}

	// A token naming a tool scope gets exactly that scope.
	readTok := s.hub.AccessToken(map[string]any{"sub": sub, "org_id": org, "scope": "openid contacts:read members:read"})
	if code, text, isErr := call(readTok, "list_members", map[string]any{}); code != 200 || isErr || !strings.Contains(text, ms[0].Email) {
		t.Fatalf("an allowed tool: %d %v %s", code, isErr, text)
	}
	if code, text, isErr := call(readTok, "create_contact", map[string]any{"email": "x-" + rnd() + "@example.test"}); code != 200 || !isErr || !strings.Contains(text, "contacts:write") {
		t.Fatalf("a tool outside the token's scopes must be refused in-band, naming the scope: %d %v %q", code, isErr, text)
	}
	// A hub token without tool scopes is read-only.
	plainTok := s.hub.AccessToken(map[string]any{"sub": sub, "org_id": org, "scope": "openid profile"})
	if code, _, isErr := call(plainTok, "list_contacts", map[string]any{}); code != 200 || isErr {
		t.Fatalf("read for a plain hub token: %d %v", code, isErr)
	}
	if code, text, isErr := call(plainTok, "create_contact", map[string]any{"email": "x-" + rnd() + "@example.test"}); code != 200 || !isErr {
		t.Fatalf("write for a plain hub token must be refused: %d %v %q", code, isErr, text)
	}

	// A token from the hub gateway (it carries an act chain) gets the working set, but never the
	// customer-facing sends.
	agentTok := s.hub.AccessToken(map[string]any{"sub": sub, "org_id": org, "act": map[string]any{"sub": "gateway-1", "agent_id": "agent-7"}})
	email := "hub-" + rnd() + "@example.test"
	if code, text, isErr := call(agentTok, "create_contact", map[string]any{"email": email}); code != 200 || isErr {
		t.Fatalf("agent create_contact: %d %v %s", code, isErr, text)
	}
	if n := s.count(`SELECT count(*) FROM contacts WHERE workspace_id = $1 AND lower(email) = $2`, ws, email); n != 1 {
		t.Fatalf("the contact was not created in the org's workspace: %d", n)
	}
	if code, text, isErr := call(agentTok, "send_campaign", map[string]any{"id": "x"}); code != 200 || !isErr || !strings.Contains(text, "campaigns:write") {
		t.Fatalf("an agent must not send campaigns: %d %v %q", code, isErr, text)
	}

	// The workspace role is re-read on every call: a member's token cannot use an admin scope.
	memberTok := s.hub.AccessToken(map[string]any{"sub": subMember, "org_id": org, "scope": "members:manage members:read"})
	if code, text, isErr := call(memberTok, "invite_member", map[string]any{"email": "i-" + rnd() + "@example.test", "role": "member"}); code != 200 || !isErr {
		t.Fatalf("a member must not manage members through a hub token: %d %v %q", code, isErr, text)
	}

	// Unknown org, unlinked user, wrong audience, bad signature: all refused.
	deny := func(what, token string, wantHTTP int) {
		t.Helper()
		if code, text, _ := call(token, "list_members", map[string]any{}); code != wantHTTP {
			t.Fatalf("%s: got %d %q want %d", what, code, text, wantHTTP)
		}
	}
	deny("unknown org", s.hub.AccessToken(map[string]any{"sub": sub, "org_id": "org-unknown-" + rnd(), "scope": "members:read"}), http.StatusForbidden)
	deny("unlinked user", s.hub.AccessToken(map[string]any{"sub": "nobody-" + rnd(), "org_id": org, "scope": "members:read"}), http.StatusForbidden)
	deny("no org", s.hub.AccessToken(map[string]any{"sub": sub, "org_id": nil, "scope": "members:read"}), http.StatusForbidden)
	deny("wrong audience", s.hub.AccessToken(map[string]any{"sub": sub, "org_id": org, "aud": "some-other-app"}), http.StatusUnauthorized)
	deny("garbage", "not-a-token", http.StatusUnauthorized)
	deny("service token", s.hub.AccessToken(map[string]any{"sub": s.hub.ClientID, "org_id": org}), http.StatusUnauthorized)

	// A user who left the workspace is refused, and so is a deinstalled org.
	s.a.SysDB.Exec(`UPDATE workspace_members SET status = 'suspended' WHERE workspace_id = $1 AND user_id = (SELECT user_id FROM circlexo_user_links WHERE subject = $2)`, ws, subMember)
	deny("removed member", memberTok, http.StatusForbidden)
	s.event(webhooks.AppRemoved, org, map[string]any{}, "mcp-rm-"+rnd())
	deny("deprovisioned org", readTok, http.StatusForbidden)

	// The protected-resource document lists the hub as an authorization server.
	code2, body := s.get("/.well-known/oauth-protected-resource")
	if code2 != 200 || !strings.Contains(string(body), s.hub.URL) {
		t.Fatalf("protected resource metadata: %d %s", code2, body)
	}
}

// The guard: the new public routes are public and nothing else under /api/circlexo is.
func TestCircleXOGuardTiers(t *testing.T) {
	for _, p := range []string{hub.ConfigPath, hub.EventsPath, hub.ProvisionPath, hub.DeprovisionPath, hub.LoginPath, hub.CallbackPath, hub.LogoutPath, hub.LaunchPath} {
		if got := classify(httptest.NewRequest(http.MethodGet, p, nil)); got != tierPublic {
			t.Errorf("%s should be public, is tier %v", p, got)
		}
	}
	// The workspace overview is on the workspace tier: a session, a membership.
	if got := classify(httptest.NewRequest(http.MethodGet, "/api/w/circlexo", nil)); got == tierPublic {
		t.Errorf("/api/w/circlexo must not be public")
	}
	// An unknown path under /api/circlexo/ is not made public by the prefix.
	if got := classify(httptest.NewRequest(http.MethodGet, "/api/circlexo/other", nil)); got == tierPublic {
		t.Errorf("/api/circlexo/other must not be public")
	}
}

// With no CIRCLEXO_ISSUER the integration is off: the config says so and the routes are absent.
func TestCircleXODisabled(t *testing.T) {
	t.Setenv("CIRCLEXO_ISSUER", "")
	_, base := bootAudit(t)
	res, err := http.Get(base + hub.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || strings.TrimSpace(string(b)) != `{"enabled":false}` {
		t.Fatalf("config: %d %s", res.StatusCode, b)
	}
	for _, p := range []string{hub.LoginPath, hub.CallbackPath, hub.LaunchPath} {
		r, _ := http.Get(base + p)
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Fatalf("%s should not exist with the integration off: %d", p, r.StatusCode)
		}
	}
	for _, p := range []string{hub.EventsPath, hub.ProvisionPath, hub.DeprovisionPath} {
		r, _ := http.Post(base+p, "application/json", strings.NewReader("{}"))
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound && r.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s should not exist with the integration off: %d", p, r.StatusCode)
		}
	}
	if hub.Current() != nil {
		t.Fatal("Current must be nil when off")
	}
	// The existing login is untouched.
	c := newClient(t, base)
	c.must("POST", "/api/auth/register", map[string]string{"email": "off-" + rnd() + "@example.test", "password": "correct-horse-9"}, 201)
	c.must("GET", "/api/auth/me", nil, 200)
}
