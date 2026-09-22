package server

import (
	"net/http"
	"strings"

	"github.com/togo-framework/togo"
)

/*
CORS for the embeddable chat widget, and only for it.

/api/chat/{key}/messages is meant to be called by a <script> running on the
CUSTOMER's site — a different origin by definition — so without these headers
the browser refuses to hand the response to the widget, and a visitor's message
never arrives. Nothing else in the API is cross-origin: the app and its API are
one origin (next.config.mjs proxies /api), so this middleware is scoped to the
/api/chat/ prefix rather than applied globally.

The echoed Origin is NOT the authorization. internal/inbox checks the request's
Origin against the widget's own allow-list and answers 403 when it does not
match; this only lets the browser read that answer, so the widget can say "this
chat is not available on this site" instead of failing silently. No credentials
are allowed — the widget authenticates with the key in the path and its
visitor_token, never with cookies.

/api/subscribe/{key} (MH-318) is the same shape — a form on the customer's
site, the key in the path, the Origin checked against the form's own
allow-list — and needs the same headers, or no browser can ever submit it.
*/

// publicCrossOrigin is the set of prefixes a customer's page calls directly.
var publicCrossOrigin = []string{"/api/chat/", "/api/subscribe/"}

func isPublicCrossOrigin(path string) bool {
	for _, p := range publicCrossOrigin {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
func installChatCORS(k *togo.Kernel) {
	k.UseMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isPublicCrossOrigin(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Content-Type, Accept")
				h.Set("Access-Control-Max-Age", "600")
				// Any cache in front of this must key on the origin it answered for.
				h.Add("Vary", "Origin")
			}
			// The preflight never reaches a handler: answer it here.
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
}
