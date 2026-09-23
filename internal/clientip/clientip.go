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

  - Behind proxies that rewrite X-Forwarded-For (Hosbah's Caddy does), keep
    CLIENT_IP_HEADER=X-Real-IP (NPM's peer, i.e. the Cloudflare edge) and add
    EDGE_PROXIES=cloudflare. When the address found above is a Cloudflare edge,
    the visitor is read from CF-Connecting-IP, which Cloudflare always
    overwrites. A request that reaches NPM without Cloudflare has a non-edge
    peer, so a forged CF-Connecting-IP from it is never believed (MH-486).

	EDGE_PROXIES        "cloudflare" (the published ranges below) and/or
	                    IPs/CIDRs of a CDN in front of the trusted proxies.
	EDGE_CLIENT_HEADER  the header that edge supplies the visitor in.
	                    Default CF-Connecting-IP.

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
	// edge is a CDN in front of the trusted proxies. When the address the
	// proxies report is inside it, the visitor is read from edgeHeader.
	edge       []netip.Prefix
	edgeHeader string
}

// cloudflareRanges is https://www.cloudflare.com/ips (fetched 2026-09-23).
var cloudflareRanges = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
}

// WithEdge returns r with a CDN edge in front of its trusted proxies. edges are
// IPs/CIDRs or "cloudflare"; header "" means CF-Connecting-IP.
func (r *Resolver) WithEdge(edges []string, header string) (*Resolver, error) {
	out := *r
	out.edge = nil
	out.edgeHeader = http.CanonicalHeaderKey(strings.TrimSpace(header))
	if out.edgeHeader == "" {
		out.edgeHeader = "Cf-Connecting-Ip"
	}
	for _, raw := range edges {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		list := []string{raw}
		if strings.EqualFold(raw, "cloudflare") {
			list = cloudflareRanges
		}
		for _, one := range list {
			p, err := parsePrefix(one)
			if err != nil {
				return nil, fmt.Errorf("EDGE_PROXIES: %q is not an IP, CIDR or \"cloudflare\"", one)
			}
			out.edge = append(out.edge, p)
		}
	}
	return &out, nil
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
	if err != nil {
		return nil, configured, err
	}
	if edges := strings.TrimSpace(os.Getenv("EDGE_PROXIES")); edges != "" {
		r, err = r.WithEdge(strings.Split(edges, ","), os.Getenv("EDGE_CLIENT_HEADER"))
	}
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
	hop := r.hop(req)
	if len(r.edge) == 0 {
		return hop
	}
	// The proxies handed us an edge address: the edge names the visitor.
	if a, ok := parseAddr(hop); ok && inAny(r.edge, a) {
		if v, ok := parseAddr(req.Header.Get(r.edgeHeader)); ok {
			return v.String()
		}
	}
	return hop
}

func inAny(ps []netip.Prefix, a netip.Addr) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// hop is the address the trusted proxies say they received the request from.
func (r *Resolver) hop(req *http.Request) string {
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
