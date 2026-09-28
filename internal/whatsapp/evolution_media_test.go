package whatsapp

// MH-658: GetMediaBase64 against a FAKE Evolution server (httptest) — never a
// real one, and nothing here sends anything for real. Covers: happy path
// decoding base64 (with and without a data: URL prefix), the 25MB size cap,
// and a non-2xx answer surfacing as *Error.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeEvolutionMedia(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/chat/getBase64FromMediaMessage/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "want POST", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("apikey") != "test-key" {
			http.Error(w, "missing apikey", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body)) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func clientFor(t *testing.T, url string) *Client {
	t.Helper()
	return &Client{Cfg: Config{URL: url, Key: "test-key"}, Instance: "ws-test"}
}

func TestGetMediaBase64DecodesTheHappyPath(t *testing.T) {
	raw, _ := json.Marshal(base64.StdEncoding.EncodeToString([]byte("hello world")))
	srv := fakeEvolutionMedia(t, `{"base64":`+string(raw)+`,"mimetype":"image/jpeg","fileName":"pic.jpg"}`, http.StatusOK)
	c := clientFor(t, srv.URL)

	mf, err := c.GetMediaBase64(context.Background(), json.RawMessage(`{"key":{}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(mf.Data) != "hello world" {
		t.Errorf("got data %q", mf.Data)
	}
	if mf.MimeType != "image/jpeg" || mf.FileName != "pic.jpg" {
		t.Errorf("got mimeType=%q fileName=%q", mf.MimeType, mf.FileName)
	}
}

func TestGetMediaBase64StripsADataURLPrefix(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte("abc123"))
	raw, _ := json.Marshal("data:application/pdf;base64," + enc)
	srv := fakeEvolutionMedia(t, `{"base64":`+string(raw)+`}`, http.StatusOK)
	c := clientFor(t, srv.URL)

	mf, err := c.GetMediaBase64(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(mf.Data) != "abc123" {
		t.Errorf("got data %q", mf.Data)
	}
}

func TestGetMediaBase64EnforcesTheSizeCap(t *testing.T) {
	// One byte over mediaMaxBytes, base64-encoded — GetMediaBase64 must refuse
	// this rather than buffer or return an oversized attachment.
	big := bytes.Repeat([]byte("x"), mediaMaxBytes+1)
	raw, _ := json.Marshal(base64.StdEncoding.EncodeToString(big))
	srv := fakeEvolutionMedia(t, `{"base64":`+string(raw)+`}`, http.StatusOK)
	c := clientFor(t, srv.URL)

	_, err := c.GetMediaBase64(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("want an error for oversized media, got nil")
	}
}

func TestGetMediaBase64SurfacesANon2xxAsError(t *testing.T) {
	srv := fakeEvolutionMedia(t, `{"message":"not found"}`, http.StatusNotFound)
	c := clientFor(t, srv.URL)

	_, err := c.GetMediaBase64(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if e.Status != http.StatusNotFound {
		t.Errorf("got status %d", e.Status)
	}
	if !strings.Contains(e.Message, "not found") {
		t.Errorf("got message %q", e.Message)
	}
}
