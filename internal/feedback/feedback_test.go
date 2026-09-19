package feedback

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestScriptTagEscapes(t *testing.T) {
	c := New(Config{BaseURL: "https://x.test/", Key: `pfk_a"b`, Locale: "ar"})
	got := string(c.ScriptTag())
	want := `<script src="https://x.test/embed/v1.js" data-key="pfk_a&#34;b" data-locale="ar" defer></script>`
	if got != want {
		t.Fatalf("got %s", got)
	}
	if New(Config{Key: "k", Disabled: true}).ScriptTag() != "" {
		t.Fatal("disabled should render nothing")
	}
}

func TestReport(t *testing.T) {
	var fields map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/feedback/embed" || r.Header.Get("Origin") != "" {
			t.Errorf("bad request %s", r.URL.Path)
		}
		_ = r.ParseMultipartForm(1 << 20)
		fields = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			fields[k] = v[0]
		}
		if fields["public_key"] == "pfk_bad" {
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `{"error":"origin not allowed"}`)
			return
		}
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"key":"MG-12"}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Key: "pfk_ok", AppURL: "https://app.test"})
	key, err := c.Report(context.Background(), Report{Title: "boom", Type: "bug", Extra: map[string]string{"route": "/x"}})
	if err != nil || key != "MG-12" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	if fields["page_url"] != "https://app.test" || fields["route"] != "/x" || fields["issue_type"] != "bug" {
		t.Fatalf("fields %v", fields)
	}
	_, err = New(Config{BaseURL: srv.URL, Key: "pfk_bad"}).Report(context.Background(), Report{Title: "x"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 403 || ae.Message != "origin not allowed" {
		t.Fatalf("err %v", err)
	}
}

func TestMiddlewareInjects(t *testing.T) {
	c := New(Config{BaseURL: "https://m.test", Key: "pfk_1"})
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"a":"</body>"}`)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><body>hi</body></html>")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), `hi<script src="https://m.test/embed/v1.js"`) {
		t.Fatalf("not injected: %s", rec.Body)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/json", nil))
	if strings.Contains(rec.Body.String(), "script") {
		t.Fatal("json modified")
	}
}

func TestRecoverReports(t *testing.T) {
	hit := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		hit <- r.FormValue("title")
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"key":"MG-1"}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Key: "pfk_1"})
	rec := httptest.NewRecorder()
	c.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("kaboom") })).
		ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
	if rec.Code != 500 || <-hit != "panic: kaboom" {
		t.Fatalf("code %d", rec.Code)
	}
}
