package server

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/togo-framework/togo"
)

/*
The API in front of the web (WEB_ORIGIN).

Under Hosbah one container serves the whole app on one port. Next cannot be that
front: its rewrite of /api cannot carry a protocol upgrade, so the browser socket
(/api/agents/ws) needs a separate published port and a hand-written route. The
Go API can: with WEB_ORIGIN set it listens on the public port, answers /api,
/graphql and /docs itself, and hands every other path — pages, assets, the
service worker — to Next over the loopback. Go's reverse proxy passes upgrades
through untouched, so the socket needs nothing special.

"Answers /api itself" includes the paths it does not know (MH-424). Handing an
unknown /api path to Next sent it round Next's own /api rewrite, whose
destination is fixed when the image is built — so a typo'd API path came back
as a 500 from a proxy with nothing behind it, and could never be a 404. The API
owns those three trees outright: an unknown path under them is a JSON 404 here
and never reaches Next.

Without WEB_ORIGIN nothing changes: unknown paths are 404 as before, and Next
stays the front (the old compose layout).
*/
func installWebProxy(k *togo.Kernel) {
	origin := strings.TrimSpace(os.Getenv("WEB_ORIGIN"))
	if origin == "" {
		return
	}
	proxy, err := newWebProxy(origin, k.Log)
	if err != nil {
		panic(err.Error())
	}
	k.Router.NotFound(webFallback(proxy).ServeHTTP)
	k.Log.Info("web proxy: unknown non-API paths go to Next", "origin", origin)
}

// apiOwnedPrefixes are the trees the API answers alone; nothing under them is Next's.
var apiOwnedPrefixes = []string{"/api", "/graphql", "/docs"}

// apiOwned reports whether path is one of the API's trees — the prefix itself or anything
// under it, but not a sibling such as /apiary or /docsets.
func apiOwned(path string) bool {
	for _, p := range apiOwnedPrefixes {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// webFallback is the router's NotFound when the API is the public process: an unknown API path
// is a 404 in the API's own error shape, and every other path goes to Next.
func webFallback(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiOwned(r.URL.Path) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newWebProxy(origin string, log *slog.Logger) (http.Handler, error) {
	target, err := url.Parse(origin)
	if err != nil || target.Host == "" {
		return nil, errWebOrigin
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		// Next sees the public host, as it did when it was the front.
		r.Host = target.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if log != nil {
			log.Warn("web proxy: Next did not answer", "path", r.URL.Path, "err", err)
		}
		http.Error(w, "the web is starting", http.StatusBadGateway)
	}
	return proxy, nil
}

var errWebOrigin = errors.New("moharrik: WEB_ORIGIN must be a URL such as http://127.0.0.1:3001")
