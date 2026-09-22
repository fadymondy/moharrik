package server

import (
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

Without WEB_ORIGIN nothing changes: unknown paths are 404 as before, and Next
stays the front (the old compose layout).
*/
func installWebProxy(k *togo.Kernel) {
	origin := strings.TrimSpace(os.Getenv("WEB_ORIGIN"))
	if origin == "" {
		return
	}
	target, err := url.Parse(origin)
	if err != nil || target.Host == "" {
		panic("moharrik: WEB_ORIGIN must be a URL such as http://127.0.0.1:3001")
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		// Next sees the public host, as it did when it was the front.
		r.Host = target.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		k.Log.Warn("web proxy: Next did not answer", "path", r.URL.Path, "err", err)
		http.Error(w, "the web is starting", http.StatusBadGateway)
	}
	k.Router.NotFound(proxy.ServeHTTP)
	k.Log.Info("web proxy: unknown paths go to Next", "origin", target.String())
}
