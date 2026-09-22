package inbox

import (
	"net/url"
	"strings"
	"unicode"
)

// visitorContext is what a chat conversation records about where it started
// (MH-377): the page, the browser, the language. The same three fields
// fadymondy.com's chat.Open keeps — "which page, which browser" is the first
// thing worth knowing about a question typed into a chat box.
type visitorContext struct {
	PageURL   string
	UserAgent string
	Locale    string
}

const (
	maxPageURL   = 1000
	maxUserAgent = 500
	maxLocale    = 35 // the longest BCP 47 tag anyone sends in practice
)

// visitorContextOf cleans what the request says about the visitor. Every input
// here is written by the sender, so each is bounded, and the page URL is kept
// only when it is on the request's own origin — which already passed the
// widget's allow-list. Without that, anyone could plant an arbitrary link in
// the thread header an operator reads and clicks.
func visitorContextOf(origin, pageURL, referer, userAgent, locale, acceptLanguage string) visitorContext {
	return visitorContext{
		PageURL:   sameOriginURL(origin, pageURL, referer),
		UserAgent: printable(userAgent, maxUserAgent),
		Locale:    cleanLocale(locale, acceptLanguage),
	}
}

// sameOriginURL returns the first candidate that is an http(s) URL on origin.
// The Referer is the fallback: a browser sends only the ORIGIN of the page on a
// cross-origin fetch by default (strict-origin-when-cross-origin), which is
// still worth having when one widget is allowed on several sites.
func sameOriginURL(origin string, candidates ...string) string {
	want := strings.TrimRight(strings.ToLower(strings.TrimSpace(origin)), "/")
	if want == "" {
		return ""
	}
	for _, raw := range candidates {
		raw = strings.TrimSpace(raw)
		if raw == "" || len(raw) > 4*maxPageURL {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		if strings.ToLower(u.Scheme+"://"+u.Host) != want {
			continue
		}
		u.User = nil // credentials in a URL are never the operator's business
		u.Fragment = ""
		s := u.String()
		if len(s) > maxPageURL {
			s = s[:maxPageURL]
		}
		return s
	}
	return ""
}

// cleanLocale keeps a language tag: the widget's own if it sent one, else the
// browser's first Accept-Language choice. Letters, digits and hyphens only.
func cleanLocale(given, acceptLanguage string) string {
	for _, raw := range []string{given, firstLanguage(acceptLanguage)} {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == "*" || len(raw) > maxLocale {
			continue
		}
		ok := true
		for _, r := range raw {
			if !(r == '-' || r == '_' || (r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)))) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		raw = strings.ReplaceAll(raw, "_", "-")
		// Primary subtag lower-case ("AR-eg" -> "ar-eg"); the rest as sent.
		if i := strings.IndexByte(raw, '-'); i > 0 {
			return strings.ToLower(raw[:i]) + raw[i:]
		}
		return strings.ToLower(raw)
	}
	return ""
}

func firstLanguage(h string) string {
	first, _, _ := strings.Cut(h, ",")
	tag, _, _ := strings.Cut(first, ";")
	return strings.TrimSpace(tag)
}

// printable drops control characters and bounds the length, in runes.
func printable(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
