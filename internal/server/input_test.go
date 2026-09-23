package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestInputGuard: text Postgres cannot store is refused (path, query, JSON
// body) or repaired (headers) before a handler sees it, and everything else —
// including a JSON body that merely LOOKS like it holds \u0000 — reaches the
// handler byte for byte (MH-472/473/474).
func TestInputGuard(t *testing.T) {
	var gotBody, gotHeader string
	h := inputGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotHeader = string(b), r.Header.Get("X-Workspace-ID")
		w.WriteHeader(http.StatusNoContent)
	}))

	cases := []struct {
		name, method, target, body, contentType, header string
		want                                            int
		wantBody, wantHeader                            string
	}{
		{name: "plain get", method: "GET", target: "/api/w/workflows/00000000-0000-0000-0000-000000000000/versions", want: 204},
		{name: "arabic query", method: "GET", target: "/api/w/contacts?q=%D9%85%D8%AD%D9%85%D8%AF", want: 204},
		{name: "invalid utf-8 in the path", method: "GET", target: "/api/w/workflows/%FF%FE/versions", want: 400},
		{name: "nul in the path", method: "GET", target: "/api/w/workflows/%00/versions", want: 400},
		{name: "invalid utf-8 in a query value", method: "GET", target: "/api/w/analytics/report?resource=%FF", want: 400},
		{name: "nul in a query value", method: "GET", target: "/api/w/analytics/report?resource=a%00b", want: 400},
		{name: "nul in a query name", method: "GET", target: "/api/w/analytics/report?a%00=1", want: 400},
		{name: "pages are not the API's", method: "GET", target: "/w/demo/%FF", want: 204},
		{name: "json with a nul", method: "POST", target: "/api/w/companies", body: `{"name":"a\u0000b"}`, contentType: "application/json", want: 400},
		{name: "json with a nul in a key", method: "POST", target: "/api/w/companies", body: `{"na\u0000me":"x"}`, contentType: "application/json; charset=utf-8", want: 400},
		{name: "json with an escaped backslash", method: "POST", target: "/api/w/companies", body: `{"name":"a\\u0000b"}`, contentType: "application/json", want: 204, wantBody: `{"name":"a\\u0000b"}`},
		{name: "not json is the handler's business", method: "POST", target: "/api/w/companies", body: `{"name":"a\u0000b"}`, contentType: "text/plain", want: 204, wantBody: `{"name":"a\u0000b"}`},
		{name: "big body reaches the handler whole", method: "POST", target: "/api/w/companies", body: `{"name":"` + strings.Repeat("x", 3<<20) + `"}`, contentType: "application/json", want: 204, wantBody: `{"name":"` + strings.Repeat("x", 3<<20) + `"}`},
		{name: "a malformed header is repaired, not refused", method: "GET", target: "/api/w/current", header: "ws-\xff", want: 204, wantHeader: "ws-�"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotBody, gotHeader = "", ""
			req := httptest.NewRequest(c.method, "http://x"+c.target, strings.NewReader(c.body))
			if c.contentType != "" {
				req.Header.Set("Content-Type", c.contentType)
			}
			if c.header != "" {
				req.Header.Set("X-Workspace-ID", c.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("got %d want %d: %s", rec.Code, c.want, rec.Body)
			}
			if c.want == 400 && !strings.Contains(rec.Body.String(), `"error"`) {
				t.Errorf("a refusal must say why in the API's error shape: %s", rec.Body)
			}
			if c.wantBody != "" && gotBody != c.wantBody {
				t.Errorf("the handler got a different body (%d bytes, want %d)", len(gotBody), len(c.wantBody))
			}
			if c.wantHeader != "" && gotHeader != c.wantHeader {
				t.Errorf("header: got %q want %q", gotHeader, c.wantHeader)
			}
		})
	}
}
