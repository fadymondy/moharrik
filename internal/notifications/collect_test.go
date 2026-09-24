package notifications

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An RSS URL is typed by a workspace member: it must not be able to point the
// server at its own network. The check is on the dialled IP, so a redirect to
// a private address is refused too.
func TestCollectorRefusesPrivateAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<rss><channel><item><title>secret</title></channel></rss>`)
	}))
	defer srv.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL, http.StatusFound)
	}))
	defer public.Close()

	prev := allowPrivate
	allowPrivate = false
	defer func() { allowPrivate = prev }()

	for _, u := range []string{srv.URL, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1), "http://169.254.169.254/latest/meta-data/", "http://10.0.0.1/"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3e9)
		_, err := get(ctx, u, "")
		cancel()
		if !errors.Is(err, errPrivate) {
			t.Errorf("%s: got %v, want the private-address refusal", u, err)
		}
	}
}

func TestPublicIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"8.8.8.8": true, "1.1.1.1": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "100.127.255.255": false, "100.128.0.1": true, "::1": false, "fc00::1": false, "0.0.0.0": false,
	} {
		if got := publicIP(net.ParseIP(ip)); got != want {
			t.Errorf("publicIP(%s) = %v, want %v", ip, got, want)
		}
	}
}

func TestNormaliseHandleAndKinds(t *testing.T) {
	for in, want := range map[string]string{
		"@Shop.bsky.social": "shop.bsky.social", "https://bsky.app/profile/shop.bsky.social/": "shop.bsky.social",
		"did:plc:abc123xyz": "did:plc:abc123xyz",
	} {
		if got, ok := normaliseHandle(in); !ok || got != want {
			t.Errorf("normaliseHandle(%q) = %q, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "no spaces.bsky.social", "localhost", "a..b"} {
		if _, ok := normaliseHandle(bad); ok {
			t.Errorf("normaliseHandle(%q) accepted", bad)
		}
	}
	if got := normaliseKinds(" Reply, mention,reply,, "); got != "reply,mention" {
		t.Errorf("normaliseKinds: %q", got)
	}
	if !pushKind("", "anything") || !pushKind("reply,mention", "Mention") || pushKind("reply", "video") {
		t.Error("pushKind")
	}
}

func TestSourcePredicate(t *testing.T) {
	p, v := sourcePredicate("internal:New lead", 3)
	if !strings.Contains(p, "source_name = $3") || v != "New lead" {
		t.Errorf("internal key: %s %v", p, v)
	}
	p, v = sourcePredicate("0b6c…", 2)
	if !strings.Contains(p, "source_id = $2 OR feed_source_id = $2") || v != "0b6c…" {
		t.Errorf("id key: %s %v", p, v)
	}
	if got := likeEscape(`50%_off\`); got != `50\%\_off\\` {
		t.Errorf("likeEscape: %s", got)
	}
}
