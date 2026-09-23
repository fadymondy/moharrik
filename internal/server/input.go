package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/togo-framework/togo"
)

/*
installInputGuard refuses input Postgres cannot store, before any handler
passes it to a query (MH-472, MH-473, MH-474).

Postgres text holds valid UTF-8 without NUL bytes, and nothing else: a
parameter carrying %FF or %00 fails the query with SQLSTATE 22021, which every
handler reported as its own "could not read the …" 500. The probes that found
/api/w/definitely-not-a-route, /api/w/analytics/report and a workflow's
versions answering 500 were hitting exactly that — the route never mattered,
because the workspace header or the parameter failed first. Checking it here,
once, covers every route including the ones not written yet; the sweep in
no5xx_test.go proves it for all of them.

  - path and query: not valid UTF-8, or holding a NUL, is 400 with a message
    saying so. Nothing legitimate is refused: a browser always percent-encodes
    text as UTF-8.
  - a JSON body holding \u0000 in a string is 400 the same way. (Invalid UTF-8
    in a JSON body is harmless: encoding/json turns it into U+FFFD.)
  - headers are repaired rather than refused: HTTP allows Latin-1 bytes there,
    and a visitor's odd User-Agent must not cost them their chat message. The
    bytes that are not UTF-8 become U+FFFD, so a malformed X-Workspace-ID is
    simply a workspace nobody is a member of (404).

Only the API's own trees are checked; pages and assets pass untouched.
*/
func installInputGuard(k *togo.Kernel) {
	k.UseMiddleware(inputGuard)
}

// maxCheckedBody is how much of a JSON body is inspected for \u0000. Huma's own
// limit is 1 MiB, so a body past this is refused by Huma anyway.
const maxCheckedBody = 1 << 20

func inputGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !apiOwned(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !storable(r.URL.Path) {
			writeError(w, http.StatusBadRequest, "the path holds bytes that are not text (invalid UTF-8 or a NUL)")
			return
		}
		for key, vals := range r.URL.Query() {
			if !storable(key) {
				writeError(w, http.StatusBadRequest, "a query parameter's name holds bytes that are not text (invalid UTF-8 or a NUL)")
				return
			}
			for _, v := range vals {
				if !storable(v) {
					writeError(w, http.StatusBadRequest, "the query parameter "+key+" holds bytes that are not text (invalid UTF-8 or a NUL)")
					return
				}
			}
		}
		for key, vals := range r.Header {
			for i, v := range vals {
				if !utf8.ValidString(v) {
					vals[i] = strings.ToValidUTF8(v, "�")
				}
			}
			r.Header[key] = vals
		}
		if r.Body != nil && isJSON(r.Header.Get("Content-Type")) {
			body, err := io.ReadAll(io.LimitReader(r.Body, maxCheckedBody+1))
			if err != nil {
				writeError(w, http.StatusBadRequest, "could not read the request body")
				return
			}
			if len(body) <= maxCheckedBody && jsonHoldsNUL(body) {
				writeError(w, http.StatusBadRequest, "the body holds a NUL character (\\u0000), which cannot be stored")
				return
			}
			// Hand the handler the whole body: what was read, then the rest.
			r.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(body), r.Body), r.Body}
		}
		next.ServeHTTP(w, r)
	})
}

// storable reports whether s can be a Postgres text value.
func storable(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func isJSON(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && (mt == "application/json" || strings.HasSuffix(mt, "+json"))
}

// jsonHoldsNUL reports whether any string (value or key) in the JSON document
// decodes to one holding a NUL. The byte scan is only the fast path: "\u0000"
// can also be the text of an escaped backslash ("\\u0000"), which is fine, so a
// hit is confirmed by decoding. A body that is not JSON at all is left for the
// handler to refuse in its own words.
func jsonHoldsNUL(body []byte) bool {
	if !bytes.Contains(body, []byte(`\u0000`)) {
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if s, ok := tok.(string); ok && strings.ContainsRune(s, 0) {
			return true
		}
	}
}
