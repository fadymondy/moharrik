package inbox

import "testing"

// MH-687: "https://*.fadymondy.com" was stored as typed and matched nothing,
// so the widget answered 403 on the live site. A leading "*." now covers the
// subdomains, and entries that could never match are refused when saved.
func TestOriginAllowed(t *testing.T) {
	list := "https://*.fadymondy.com, https://shop.test/"
	for origin, want := range map[string]bool{
		"https://www.fadymondy.com":       true,
		"https://a.b.fadymondy.com":       true,
		"HTTPS://WWW.FADYMONDY.COM":       true,
		"https://shop.test":               true,
		"https://fadymondy.com":           false, // the apex is not a subdomain
		"http://www.fadymondy.com":        false, // the scheme counts
		"https://www.fadymondy.com:8443":  false, // so does the port
		"https://evilfadymondy.com":       false,
		"https://fadymondy.com.evil.test": false,
		"https://x@www.fadymondy.com":     false,
		"":                                false,
	} {
		if got := originAllowed(list, origin); got != want {
			t.Errorf("originAllowed(%q) = %v, want %v", origin, got, want)
		}
	}
	if originAllowed("", "https://shop.test") {
		t.Error("an empty list must allow nothing")
	}
}

func TestBadOrigin(t *testing.T) {
	for _, ok := range [][]string{
		nil,
		{"https://example.com", "http://localhost:3000", "https://*.example.com/"},
		{"https://a.test, https://b.test"},
	} {
		if bad := badOrigin(ok); bad != "" {
			t.Errorf("badOrigin(%q) refused %q", ok, bad)
		}
	}
	for list, want := range map[string]string{
		"fadymondy.com":                     "fadymondy.com",
		"https://site.test/contact":         "https://site.test/contact",
		"https://ok.test, ftp://files.test": "ftp://files.test",
		"https://*":                         "https://*",
		"https://a.*.test":                  "https://a.*.test",
		"https://site.test?x=1":             "https://site.test?x=1",
	} {
		if got := badOrigin([]string{list}); got != want {
			t.Errorf("badOrigin(%q) = %q, want %q", list, got, want)
		}
	}
}
