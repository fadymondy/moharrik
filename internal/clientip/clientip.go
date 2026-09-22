/*
Package clientip works out which address a request really came from.

# Why this is not just "read X-Forwarded-For"

Every forwarding header is a string the CLIENT can send. A visitor who writes
`X-Forwarded-For: 1.2.3.4` on their own request has that header arrive intact
unless something in front overwrites it — and a limiter keyed on a value the
attacker chooses is a limiter the attacker switches off by sending a new value
each time. (togo's ratelimit.ClientIP takes the leftmost X-Forwarded-For entry
as-is, which is exactly that; the chat limiter does not use it.)

So a header is believed only when the TCP peer that handed us the request is a
proxy we were TOLD to trust, and even then X-Forwarded-For is read from the
right: each trusted proxy appends the address it received from, so the first
entry from the right that is not one of our proxies is the last hop nobody
could forge. Everything to its left was written by the client.

# Configuration (never hard-coded)

	TRUSTED_PROXIES   comma-separated IPs/CIDRs whose forwarding headers are
	                  believed. Default: loopback only (127.0.0.0/8, ::1) — a
	                  process on the same host, such as a local reverse proxy.
	                  Set to "none" to believe no header at all.
	CLIENT_IP_HEADER  which header those proxies supply the client in.
	                  Default X-Forwarded-For (walked right to left, above).
	                  A single-value header such as CF-Connecting-IP or
	                  X-Real-IP is taken whole — only safe when every path to
	                  this process goes through a proxy that OVERWRITES it.

Production is Cloudflare -> NPM -> container. Two correct configurations:

  - NPM reachable only through Cloudflare (tunnel or firewalled to Cloudflare's
    ranges): TRUSTED_PROXIES=<NPM address>,<docker bridge range>,
    CLIENT_IP_HEADER=CF-Connecting-IP. Cloudflare always overwrites that header.
  - NPM reachable directly: keep X-Forwarded-For and add Cloudflare's published
    ranges (https://www.cloudflare.com/ips/) to TRUSTED_PROXIES as well, so the
    right-to-left walk steps over the Cloudflare edge to the visitor.

Left unset in production every visitor appears to come from NPM, and per-IP
limits collapse into one shared bucket. That fails CLOSED (visitors are limited
harder, never less) and Boot logs a warning about it.
*/
package clientip

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
)

// Resolver derives a client address from a request under one trust config.
type Resolver struct {
	trusted []netip.Prefix
	header  string // canonical header name
}

// defaultTrusted is loopback: a proxy on this host. Nothing that arrives over
// the network from elsewhere can claim to be it.
var defaultTrusted = []string{"127.0.0.0/8", "::1/128"}

// New builds a resolver. proxies are IPs or CIDRs; header "" means X-Forwarded-For.
func New(proxies []string, header string) (*Resolver, error) {
	r := &Resolver{header: http.CanonicalHeaderKey(strings.TrimSpace(header))}
	if r.header == "" {
		r.header = "X-Forwarded-For"
	}
	for _, raw := range proxies {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		p, err := parsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES: %q is not an IP or CIDR", raw)
		}
		r.trusted = append(r.trusted, p)
	}
	return r, nil
}

// FromEnv builds the resolver from TRUSTED_PROXIES and CLIENT_IP_HEADER.
// configured reports whether TRUSTED_PROXIES was set at all, so a caller can
// warn when production is running on the loopback-only default.
func FromEnv() (r *Resolver, configured bool, err error) {
	raw, configured := os.LookupEnv("TRUSTED_PROXIES")
	raw = strings.TrimSpace(raw)
	var proxies []string
	switch {
	case !configured || raw == "":
		proxies, configured = defaultTrusted, false
	case strings.EqualFold(raw, "none"):
		proxies = nil
	default:
		proxies = strings.Split(raw, ",")
	}
	r, err = New(proxies, os.Getenv("CLIENT_IP_HEADER"))
	return r, configured, err
}

func parsePrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

func (r *Resolver) isTrusted(a netip.Addr) bool {
	for _, p := range r.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func parseAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	// Some proxies write "ip:port" or "[v6]:port" into the header.
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(strings.Trim(s, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// IP returns the client address for req. Never empty for a request that came
// over a socket: it falls back to the peer when no header can be believed.
func (r *Resolver) IP(req *http.Request) string {
	peer, ok := parseAddr(req.RemoteAddr)
	if !ok {
		return req.RemoteAddr
	}
	// An untrusted peer IS the client, whatever headers it sent.
	if !r.isTrusted(peer) {
		return peer.String()
	}

	if r.header != "X-Forwarded-For" {
		if a, ok := parseAddr(req.Header.Get(r.header)); ok {
			return a.String()
		}
		return peer.String()
	}

	// Every X-Forwarded-For line, in order, joined: a proxy may add a second
	// header rather than append to the first.
	var hops []string
	for _, line := range req.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(line, ",")...)
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parseAddr(hops[i])
		if !ok {
			// Garbage in the chain: stop at the last hop we could read rather
			// than skip over it to something further left (and less trusted).
			break
		}
		client = a
		if !r.isTrusted(a) {
			break
		}
	}
	return client.String()
}

type ctxKey struct{}

// Middleware stores the derived client address on the request context, so a
// handler that never sees the *http.Request (a huma operation) can read it.
func Middleware(r *Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ip := r.IP(req)
			next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), ctxKey{}, ip)))
		})
	}
}

// From returns the address Middleware stored, or "" outside it.
func From(ctx context.Context) string {
	ip, _ := ctx.Value(ctxKey{}).(string)
	return ip
}

// WithIP stores an address directly — for tests, and for callers that derived
// it some other way.
func WithIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKey{}, ip)
}

// Bucket is the address as a rate-limit key. IPv6 is cut to its /64: one
// subscriber is routinely handed a whole /64, so keying on the full address
// would give a single visitor 2^64 fresh buckets to rotate through.
func Bucket(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	if a.Is6() && !a.Is4In6() {
		p, _ := a.Prefix(64)
		return p.String()
	}
	return a.Unmap().String()
}
