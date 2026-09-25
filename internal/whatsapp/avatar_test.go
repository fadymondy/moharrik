package whatsapp

// MH-602: the profile-picture fetch, against a FAKE Evolution server
// (httptest) — never a real one. Covers the shape a real fetch goes through:
// ask Evolution for the number's photo url, download it, and re-encode it as
// a data: URI so nothing served later hotlinks Evolution's (expiring) CDN.

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tinyPNG is the smallest possible valid PNG: a 1x1 transparent pixel.
var tinyPNG, _ = base64.StdEncoding.DecodeString(
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")

// fakeEvolutionWithPhoto serves both endpoints a real fetch touches:
// /chat/fetchProfilePictureUrl/{instance} (Evolution's own answer) and the
// image URL it points at — the SAME fake server plays both roles, since the
// real Evolution's CDN link is on its own domain too. photoURL == "" means
// "this number has no photo", the real shape of Evolution's answer for that.
func fakeEvolutionWithPhoto(t *testing.T, hasPhoto bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/chat/fetchProfilePictureUrl/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "want POST", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if !hasPhoto {
			w.Write([]byte(`{}`)) //nolint:errcheck
			return
		}
		w.Write([]byte(`{"profilePictureUrl":"` + srv.URL + `/img/photo.png"}`)) //nolint:errcheck
	})
	mux.HandleFunc("/img/photo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(tinyPNG) //nolint:errcheck
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchPhotoDownloadsAndEncodesAsDataURI(t *testing.T) {
	srv := fakeEvolutionWithPhoto(t, true)
	cfg := Config{URL: srv.URL, Key: "test-key"}
	link := &Link{WorkspaceID: "ws1", Instance: "moh-ws1"}

	dataURL, err := fetchPhoto(context.Background(), cfg, link, "201234567890")
	if err != nil {
		t.Fatalf("fetchPhoto: %v", err)
	}
	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Errorf("expected a data: URI, got %q", dataURL)
	}
}

func TestFetchPhotoNoPhotoIsNotAnError(t *testing.T) {
	srv := fakeEvolutionWithPhoto(t, false)
	cfg := Config{URL: srv.URL, Key: "test-key"}
	link := &Link{WorkspaceID: "ws1", Instance: "moh-ws1"}

	url, err := fetchPhoto(context.Background(), cfg, link, "201234567890")
	if err != nil {
		t.Fatalf("no photo must not be an error, got %v", err)
	}
	if url != "" {
		t.Errorf("expected no photo, got %q", url)
	}
}

func TestFetchPhotoNotConfiguredFails(t *testing.T) {
	if _, err := fetchPhoto(context.Background(), Config{}, nil, "201234567890"); err == nil {
		t.Error("an unconfigured Evolution / nil link must fail, not silently succeed")
	}
}
