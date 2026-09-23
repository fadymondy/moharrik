package alerts

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
)

/*
VAPID (RFC 8292) identifies this server to the browsers' push services. The key
pair comes from the environment and nowhere else:

	VAPID_PUBLIC_KEY   base64url, uncompressed P-256 point (the browser's applicationServerKey)
	VAPID_PRIVATE_KEY  base64url, the P-256 private scalar
	VAPID_SUBJECT      a contact for the push service operator: mailto:… or https://…

Generate a pair with `go run ./cmd/moharrik-admin vapid`. Rotating it invalidates
every existing subscription (browsers bind a subscription to the public key), so
it is set once per environment and kept.

With any of the three missing, push is simply off: the Delivery page says so,
the subscribe endpoint answers 503, and nothing else changes — the bell, popup,
chime and desktop notification do not depend on it.
*/

type vapid struct {
	Public, Private, Subject string
}

func vapidFromEnv() (vapid, bool) {
	v := vapid{
		Public:  strings.TrimSpace(os.Getenv("VAPID_PUBLIC_KEY")),
		Private: strings.TrimSpace(os.Getenv("VAPID_PRIVATE_KEY")),
		Subject: strings.TrimSpace(os.Getenv("VAPID_SUBJECT")),
	}
	return v, v.Public != "" && v.Private != "" && v.Subject != ""
}

// Configured reports whether web push can be sent from this process.
func Configured() bool {
	_, ok := vapidFromEnv()
	return ok
}

// subscriber is the JWT "sub" claim as the web-push library wants it: it adds
// "mailto:" itself to anything that is not an https URL, so an env value that
// already says mailto: would otherwise become mailto:mailto:.
func (v vapid) subscriber() string {
	return strings.TrimPrefix(v.Subject, "mailto:")
}

/*
checkEndpoint decides whether a browser-supplied endpoint may be POSTed to.

The server makes an outbound request to whatever URL a member's browser hands
it, so this is the server-side-request-forgery boundary: without it, any member
could point the push sender at an internal address. The endpoint must be https,
on the standard port, on a known push service. PUSH_ALLOWED_HOSTS replaces the
default list (comma-separated host suffixes) for a push service not listed here.

Loopback over plain http is allowed outside production only, for the tests.
*/
var defaultPushHosts = []string{
	"fcm.googleapis.com",              // Chrome, Edge on Android, Opera, Brave
	"updates.push.services.mozilla.com", // Firefox
	"push.services.mozilla.com",
	"notify.windows.com",              // Edge on Windows (WNS)
	"push.apple.com",                  // Safari (web.push.apple.com)
}

var errEndpoint = errors.New("that is not a push service this server will deliver to")

func checkEndpoint(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return errEndpoint
	}
	host := strings.ToLower(u.Hostname())
	if isLoopback(host) && os.Getenv("APP_ENV") != "production" {
		if u.Scheme == "http" || u.Scheme == "https" {
			return nil
		}
		return errEndpoint
	}
	if u.Scheme != "https" || (u.Port() != "" && u.Port() != "443") {
		return errEndpoint
	}
	if net.ParseIP(host) != nil {
		return errEndpoint // a push service is always a name
	}
	for _, h := range pushHosts() {
		if host == h || strings.HasSuffix(host, "."+h) {
			return nil
		}
	}
	return errEndpoint
}

func pushHosts() []string {
	raw := strings.TrimSpace(os.Getenv("PUSH_ALLOWED_HOSTS"))
	if raw == "" {
		return defaultPushHosts
	}
	var out []string
	for _, h := range strings.Split(raw, ",") {
		if h = strings.Trim(strings.ToLower(strings.TrimSpace(h)), "."); h != "" {
			out = append(out, h)
		}
	}
	return out
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
