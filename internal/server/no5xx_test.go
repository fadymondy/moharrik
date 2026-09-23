package server

/*
TestNoServerErrorOnBadInput — MH-472/473/474, end to end through the real stack.

A 5xx says "the server is broken". A request for something that does not exist,
or one whose input is malformed, is the CALLER's mistake and must say so with a
4xx. Probes found three routes answering 500 to exactly that; this walks every
route so the next one is found here rather than on app.moharrik.com.

It boots the app, signs in as the owner of a workspace with Google connected
(against the fake Google, so the analytics, Search Console and YouTube routes
have a property to read), then:

 1. every route with a path parameter, EVERY method, is called with each
    parameter set to a nil UUID, a garbage word, invalid UTF-8, a NUL byte, a
    negative number, an overlong number and a quote;
 2. every GET route is called with each of its declared query parameters
    (from the OpenAPI document, so new ones are covered without editing this)
    set to the same values;
 3. the workspace header itself is sent malformed.

The sweep asserts what the cards ask for: no answer is a 5xx. The cards' own
routes are then checked answer by answer for the right 4xx and its message.

The first run found 96 5xx answers on about 45 routes, every one of them text
Postgres refuses (invalid UTF-8 or a NUL, SQLSTATE 22021) reaching a query;
the fix is the input guard in input.go.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run NoServerError -v
*/

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/google/googletest"
	"github.com/fadymondy/moharrik/internal/testdb"
)

// badValues are what a path or query parameter is set to. Each is already
// URL-escaped where it needs to be: %FF%FE is not UTF-8 once decoded, and %00
// is a NUL — both of which Postgres refuses outright in a text parameter.
var badValues = []string{
	"00000000-0000-0000-0000-000000000000",
	"garbage",
	"%FF%FE",
	"%00",
	"-1",
	"99999999999999999999999999",
	"x%27%22--",
}

// streams answer with an open connection rather than a body; a request to
// them never ends, so they are not swept.
var streams = map[string]bool{
	"/api/w/events":        true,
	"/api/mcp":             true,
	"/api/admin/mcp":       true,
	"/api/agents/ws":       true,
	"/graphql/play":        true,
	"/api/health":          true,
	"/api/w/alerts/stream": true,
}

func TestNoServerErrorOnBadInput(t *testing.T) {
	srv, alice, a, _ := bootWithGoogle(t)

	// The declared query parameters of every GET, from the OpenAPI document.
	queryParams := map[string][]string{}
	{
		res, err := http.Get(srv.URL + "/openapi.json")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Paths map[string]map[string]struct {
				Parameters []struct {
					Name string `json:"name"`
					In   string `json:"in"`
				} `json:"parameters"`
			} `json:"paths"`
		}
		err = json.NewDecoder(res.Body).Decode(&doc)
		res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		for path, ops := range doc.Paths {
			for _, p := range ops["get"].Parameters {
				if p.In == "query" {
					queryParams[path] = append(queryParams[path], p.Name)
				}
			}
		}
		if len(queryParams) < 20 {
			t.Fatalf("only %d GET routes declare query parameters — the OpenAPI read is broken", len(queryParams))
		}
	}

	type route struct{ method, pattern string }
	var routes []route
	_ = chi.Walk(a.Kernel.Router, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if (strings.HasPrefix(pattern, "/api/") || pattern == "/graphql") && !streams[pattern] {
			routes = append(routes, route{method, pattern})
		}
		return nil
	})
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].pattern+routes[i].method < routes[j].pattern+routes[j].method
	})

	param := regexp.MustCompile(`\{[^}]+\}`)
	var failures []string
	calls := 0
	call := func(method, path string) {
		calls++
		code, b := alice.do(method, path, map[string]any{})
		if code >= 500 {
			failures = append(failures, method+" "+path+" -> "+http.StatusText(code)+": "+strings.TrimSpace(string(b)))
		}
	}
	for _, rt := range routes {
		hasParams := param.MatchString(rt.pattern)
		// 1. every path parameter, every method.
		if hasParams {
			for _, v := range badValues {
				call(rt.method, param.ReplaceAllString(rt.pattern, v))
			}
		}
		// 2. every declared query parameter of a GET.
		if rt.method != http.MethodGet {
			continue
		}
		base := param.ReplaceAllString(rt.pattern, badValues[0])
		if !hasParams {
			call(rt.method, base)
		}
		for _, q := range queryParams[rt.pattern] {
			for _, v := range badValues {
				call(rt.method, base+"?"+q+"="+v)
			}
		}
	}
	if calls < 500 {
		t.Fatalf("made only %d calls — the route walk is broken", calls)
	}

	// 3. the workspace header itself, malformed. Every workspace route reads it
	// before anything else, so one route is enough to prove the middleware.
	// (A NUL or other control byte cannot be sent: Go's client refuses it, and
	// so does its server — it never reaches a handler.)
	for _, h := range []string{"\xff\xfe", "ws-\xc3", strings.Repeat("w", 5000)} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/w/definitely-not-a-route", nil)
		req.Header.Set("X-Workspace-ID", h)
		res, err := alice.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			failures = append(failures, "malformed X-Workspace-ID "+strings.ToValidUTF8(h[:min(len(h), 8)], "?")+" -> "+res.Status+": "+string(b))
		}
	}

	// And the cards' own routes, answer by answer: not merely "not 5xx" but the
	// right 4xx, as JSON, saying what was wrong.
	for _, c := range []struct {
		path string
		want int
	}{
		{"/api/w/definitely-not-a-route", http.StatusNotFound},                                  // MH-472
		{"/api/w/workflows/00000000-0000-0000-0000-000000000000/versions", http.StatusNotFound}, // MH-474
		{"/api/w/workflows/garbage/versions", http.StatusNotFound},
		{"/api/w/workflows/%00/versions", http.StatusBadRequest},
	} {
		code, b := alice.do("GET", c.path, nil)
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if code != c.want || !json.Valid(b) || (code >= 400 && e.Error == "") {
			t.Errorf("GET %s: %d %s, want %d as JSON with an error message", c.path, code, b, c.want)
		}
	}

	for _, f := range failures {
		t.Error(f)
	}
	t.Logf("%d calls over %d routes, %d answered 5xx", calls, len(routes), len(failures))
}

// bootWithGoogle boots the real stack and signs in the owner of a fresh
// workspace with Google Analytics, Search Console and YouTube connected through
// the fake Google, every discovered property, site and channel enabled. The
// server and fake close when the test ends.
func bootWithGoogle(t *testing.T) (*httptest.Server, *client, *app.App, *googletest.Server) {
	t.Helper()
	owner, appURL := testdb.Setup(t, "server", "../..")
	t.Setenv("DATABASE_URL", owner)
	t.Setenv("APP_DATABASE_URL", appURL)
	t.Setenv("DB_DRIVER", "pgx")
	t.Setenv("AUTH_SECRET", "no-5xx-test-secret-no-5xx-test-secret-0123456")
	t.Setenv("STORAGE_DIR", t.TempDir())
	t.Setenv("TRENDS_COLLECTOR", "off")
	t.Setenv("MAIL_COLLECTOR", "off")
	t.Setenv("SCHEDULER", "off")
	t.Setenv("GOOGLE_COLLECTOR", "off")
	fakeGoogle := googletest.New()
	t.Cleanup(fakeGoogle.Close)
	t.Setenv("APP_URL", "http://127.0.0.1")
	t.Setenv("GOOGLE_API_BASE", fakeGoogle.URL)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_ID", fakeGoogle.ClientID)
	t.Setenv("GOOGLE_INTEGRATIONS_CLIENT_SECRET", fakeGoogle.ClientSecret)

	a := Boot()
	srv := httptest.NewServer(a.Kernel.Handler())
	t.Cleanup(func() { srv.Close(); a.Kernel.Close() })
	t.Setenv("APP_URL", srv.URL)

	alice := newClient(t, srv.URL)
	alice.http.Timeout = 20 * time.Second
	alice.must("POST", "/api/auth/register", map[string]string{"email": "no5xx-" + rnd() + "@example.test", "password": "correct-horse-1"}, 201)
	ws := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "No 5xx " + rnd()}, 201)["workspace"].(map[string]any)
	alice.ws = ws["id"].(string)

	// Google, all three products, so the data routes have something enabled to
	// read and their parameters reach the code behind the "nothing connected" answer.
	fakeGoogle.Set(func(s *googletest.Server) {
		s.Account = googletest.Account{Sub: "no5xx-" + rnd(), Email: "no5xx-" + rnd() + "@example.test"}
	})
	noFollow := &http.Client{Jar: alice.http.Jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	connection := ""
	for _, product := range []string{"analytics", "search_console", "youtube"} {
		q := "/api/w/google/connect?product=" + product
		if connection != "" {
			q += "&connection=" + connection
		}
		consent := alice.must("GET", q, nil, 200)["url"].(string)
		back, err := fakeGoogle.Consent(consent)
		if err != nil {
			t.Fatal(err)
		}
		res, err := noFollow.Get(back)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if loc := res.Header.Get("Location"); res.StatusCode != http.StatusFound || !strings.Contains(loc, "google=connected") {
			t.Fatalf("google connect %s: %d %s", product, res.StatusCode, loc)
		}
		google.WaitBackground()
		conns := alice.must("GET", "/api/w/google/connections", nil, 200)["connections"].([]any)
		connection = conns[0].(map[string]any)["id"].(string)
	}
	resources := alice.must("GET", "/api/w/google/resources", nil, 200)["resources"].([]any)
	if len(resources) < 3 {
		t.Fatalf("expected a GA4 property, a site and a channel, got %v", resources)
	}
	for _, r := range resources {
		alice.must("PATCH", "/api/w/google/resources/"+r.(map[string]any)["id"].(string), map[string]any{"enabled": true}, 200)
	}
	return srv, alice, a, fakeGoogle
}
