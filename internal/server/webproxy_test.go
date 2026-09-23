package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
)

// MH-424: with the API as the public process, an unknown /api, /graphql or /docs path is the
// API's own JSON 404 and never reaches Next; every other unknown path still goes to Next.
func TestWebFallbackKeepsAPITreesOffNext(t *testing.T) {
	var (
		mu  sync.Mutex
		hit []string
	)
	// A stand-in for Next that behaves like production did: its /api rewrite has nothing
	// behind it, so anything under /api that reaches it is a 500.
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hit = append(hit, r.URL.Path)
		mu.Unlock()
		if apiOwned(r.URL.Path) {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, "next page "+r.URL.Path)
	}))
	defer next.Close()

	proxy, err := newWebProxy(next.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Get("/api/health", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"ok":true}`) })
	r.NotFound(webFallback(proxy).ServeHTTP)
	front := httptest.NewServer(r)
	defer front.Close()

	get := func(path string) (int, string, string) {
		t.Helper()
		res, err := http.Get(front.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header.Get("Content-Type"), string(b)
	}

	for _, p := range []string{
		"/api/definitely-not-a-route",
		"/api/w/nope/deeper",
		"/api",
		"/graphql/nope",
		"/docs/nope",
	} {
		code, ct, body := get(p)
		if code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404 (body %q)", p, code, body)
			continue
		}
		var e struct {
			Error string `json:"error"`
		}
		if ct != "application/json" || json.Unmarshal([]byte(body), &e) != nil || e.Error == "" {
			t.Errorf("%s: want the API's JSON error, got %s %q", p, ct, body)
		}
	}

	// A known API route is still answered by the API.
	if code, _, body := get("/api/health"); code != http.StatusOK || body != `{"ok":true}` {
		t.Errorf("/api/health: %d %q", code, body)
	}
	// Pages, and look-alike paths that are not the API's trees, go to Next.
	for _, p := range []string{"/en/login", "/apiary", "/docsets/x", "/feedback"} {
		if code, _, body := get(p); code != http.StatusOK || body != "next page "+p {
			t.Errorf("%s: %d %q, want it proxied to Next", p, code, body)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for _, p := range hit {
		if apiOwned(p) {
			t.Errorf("Next was asked for %s; the API must answer its own trees", p)
		}
	}
	if len(hit) != 4 {
		t.Errorf("Next saw %v, want exactly the four page paths", hit)
	}
}

func TestNewWebProxyRejectsABadOrigin(t *testing.T) {
	if _, err := newWebProxy("not a url", nil); err == nil {
		t.Fatal("want an error for an origin with no host")
	}
}
