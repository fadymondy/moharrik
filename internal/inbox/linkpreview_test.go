package inbox

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// MH-656: a URL in a message gets its page's card.
func TestFetchPreviewReadsOpenGraph(t *testing.T) {
	previewAllowPrivate = true
	t.Cleanup(func() { previewAllowPrivate = false })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><html><head><title>Fallback</title>
<meta property="og:title" content="Fady Mondy — AI Engineering Leader">
<meta property="og:description" content="  Sign in   to your account ">
<meta property="og:image" content="/og.png">
<meta property="og:site_name" content="fadymondy.com">
</head><body><meta property="og:title" content="IGNORED"></body></html>`)
		case "/bare":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><head><title>Only a title</title></head></html>`)
		case "/img.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			fmt.Fprint(w, "\xff\xd8")
		case "/pdf":
			w.Header().Set("Content-Type", "application/pdf")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := fetchPreview(context.Background(), srv.URL+"/page")
	if p.Status != "ok" || p.Title != "Fady Mondy — AI Engineering Leader" || p.Description != "Sign in to your account" ||
		p.ImageURL != srv.URL+"/og.png" || p.SiteName != "fadymondy.com" {
		t.Fatalf("got %+v", p)
	}
	if p := fetchPreview(context.Background(), srv.URL+"/bare"); p.Status != "ok" || p.Title != "Only a title" {
		t.Errorf("a page with only <title>: %+v", p)
	}
	if p := fetchPreview(context.Background(), srv.URL+"/img.jpg"); p.Status != "ok" || p.ImageURL != srv.URL+"/img.jpg" {
		t.Errorf("a direct image link: %+v", p)
	}
	if p := fetchPreview(context.Background(), srv.URL+"/pdf"); p.Status != "none" {
		t.Errorf("a PDF: %+v", p)
	}
	if p := fetchPreview(context.Background(), srv.URL+"/missing"); p.Status != "error" {
		t.Errorf("a 404: %+v", p)
	}
}

// A customer can put any URL in a message; the server must not fetch its own
// network for them.
func TestFetchPreviewRefusesPrivateAddresses(t *testing.T) {
	previewAllowPrivate = false
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	for _, u := range []string{srv.URL + "/", "http://169.254.169.254/latest/meta-data/", "http://10.0.0.1/"} {
		if p := fetchPreview(context.Background(), u); p.Status != "error" {
			t.Errorf("%s was fetched: %+v", u, p)
		}
	}
	if hit {
		t.Error("the loopback server was reached")
	}
}

func TestNormalisePreviewURL(t *testing.T) {
	good := map[string]string{
		"https://Example.com/a?b=1#frag": "https://example.com/a?b=1",
		" http://x.io ":                  "http://x.io",
	}
	for in, want := range good {
		if got, ok := normalisePreviewURL(in); !ok || got != want {
			t.Errorf("%q → %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "javascript:alert(1)", "file:///etc/passwd", "ftp://x", "https://user:pw@x.com/", "/relative", "https://" + strings.Repeat("a", 2100)} {
		if _, ok := normalisePreviewURL(bad); ok {
			t.Errorf("%q was accepted", bad)
		}
	}
}
