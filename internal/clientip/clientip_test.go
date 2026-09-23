package clientip

import (
	"net/http/httptest"
	"os"
	"testing"
)

func TestClientIP(t *testing.T) {
	npm := []string{"10.10.10.100", "172.16.0.0/12"}
	cases := []struct {
		name    string
		proxies []string
		header  string
		remote  string
		hdrs    map[string][]string
		want    string
	}{
		{
			name:    "untrusted peer: every forwarding header is ignored",
			proxies: npm, remote: "203.0.113.9:5555",
			hdrs: map[string][]string{"X-Forwarded-For": {"1.2.3.4"}, "Cf-Connecting-Ip": {"5.6.7.8"}},
			want: "203.0.113.9",
		},
		{
			name:    "trusted peer: the rightmost untrusted hop is the client",
			proxies: npm, remote: "10.10.10.100:443",
			hdrs: map[string][]string{"X-Forwarded-For": {"198.51.100.7"}},
			want: "198.51.100.7",
		},
		{
			name:    "a forged leftmost entry is not believed",
			proxies: npm, remote: "10.10.10.100:443",
			hdrs: map[string][]string{"X-Forwarded-For": {"1.2.3.4, 198.51.100.7"}},
			want: "198.51.100.7",
		},
		{
			name:    "several trusted hops are walked over",
			proxies: npm, remote: "172.18.0.1:40000",
			hdrs: map[string][]string{"X-Forwarded-For": {"1.2.3.4, 198.51.100.7, 10.10.10.100"}},
			want: "198.51.100.7",
		},
		{
			name:    "two X-Forwarded-For lines are one chain",
			proxies: npm, remote: "10.10.10.100:443",
			hdrs: map[string][]string{"X-Forwarded-For": {"1.2.3.4", "198.51.100.7"}},
			want: "198.51.100.7",
		},
		{
			name:    "garbage stops the walk at the last readable hop",
			proxies: npm, remote: "10.10.10.100:443",
			hdrs: map[string][]string{"X-Forwarded-For": {"1.2.3.4, not-an-ip, 172.18.0.5"}},
			want: "172.18.0.5",
		},
		{
			name:    "an untrusted hop right of garbage is the client",
			proxies: npm, remote: "10.10.10.100:443",
			hdrs: map[string][]string{"X-Forwarded-For": {"1.2.3.4, not-an-ip, 198.51.100.7"}},
			want: "198.51.100.7",
		},
		{
			name:    "trusted peer with no header is itself the client",
			proxies: npm, remote: "10.10.10.100:443",
			want: "10.10.10.100",
		},
		{
			name:    "CF-Connecting-IP is taken from a trusted peer",
			proxies: npm, header: "CF-Connecting-IP", remote: "10.10.10.100:443",
			hdrs: map[string][]string{"Cf-Connecting-Ip": {"198.51.100.7"}, "X-Forwarded-For": {"1.2.3.4"}},
			want: "198.51.100.7",
		},
		{
			name:    "CF-Connecting-IP from an untrusted peer is ignored",
			proxies: npm, header: "CF-Connecting-IP", remote: "203.0.113.9:5555",
			hdrs: map[string][]string{"Cf-Connecting-Ip": {"198.51.100.7"}},
			want: "203.0.113.9",
		},
		{
			name:    "no trusted proxies at all: always the peer",
			proxies: nil, remote: "127.0.0.1:5555",
			hdrs: map[string][]string{"X-Forwarded-For": {"198.51.100.7"}},
			want: "127.0.0.1",
		},
		{
			name:    "IPv4-mapped IPv6 peer is normalised",
			proxies: npm, remote: "[::ffff:203.0.113.9]:5555",
			want: "203.0.113.9",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := New(c.proxies, c.header)
			if err != nil {
				t.Fatal(err)
			}
			q := httptest.NewRequest("POST", "/api/chat/k/messages", nil)
			q.RemoteAddr = c.remote
			for k, vs := range c.hdrs {
				for _, v := range vs {
					q.Header.Add(k, v)
				}
			}
			if got := r.IP(q); got != c.want {
				t.Fatalf("IP = %q, want %q", got, c.want)
			}
		})
	}
}

func TestFromEnvDefaultsToLoopbackOnly(t *testing.T) {
	t.Setenv("CLIENT_IP_HEADER", "")
	t.Setenv("TRUSTED_PROXIES", "") // registers the restore
	os.Unsetenv("TRUSTED_PROXIES")
	r, configured, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if configured {
		t.Fatal("an unset TRUSTED_PROXIES reported as configured")
	}
	q := httptest.NewRequest("GET", "/", nil)
	q.RemoteAddr = "10.10.10.100:443"
	q.Header.Set("X-Forwarded-For", "198.51.100.7")
	if got := r.IP(q); got != "10.10.10.100" {
		t.Fatalf("default trusted a non-loopback proxy: %q", got)
	}
	q.RemoteAddr = "127.0.0.1:443"
	if got := r.IP(q); got != "198.51.100.7" {
		t.Fatalf("default did not trust loopback: %q", got)
	}
}

func TestFromEnvRejectsGarbage(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, nope")
	if _, _, err := FromEnv(); err == nil {
		t.Fatal("a malformed TRUSTED_PROXIES was accepted")
	}
}

func TestBucketGroupsIPv6ByPrefix(t *testing.T) {
	if a, b := Bucket("2001:db8:1:2::1"), Bucket("2001:db8:1:2:ffff::9"); a != b {
		t.Fatalf("two addresses in one /64 got different buckets: %q vs %q", a, b)
	}
	if a, b := Bucket("2001:db8:1:2::1"), Bucket("2001:db8:1:3::1"); a == b {
		t.Fatal("two different /64s shared a bucket")
	}
	if got := Bucket("198.51.100.7"); got != "198.51.100.7" {
		t.Fatalf("IPv4 bucket = %q", got)
	}
}
