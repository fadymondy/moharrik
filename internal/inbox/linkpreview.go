package inbox

import (
	"context"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

/*
Link previews (MH-656): the card WhatsApp shows under a message with a URL in
it — the page's image, title, description and site. The page is fetched by
this server, not the member's browser, so the fetch goes through the same
private-network guard a rule's webhook does (internal/rules/webhook.go): a
customer can put any URL in a message, and without the guard one pointing at
169.254.169.254 or the database's address would have this server fetch it.

Only the page's <head> metadata is kept. The image itself is not proxied: the
thread loads it with no referrer, the way the email iframe loads remote
images.
*/

// LinkPreview is one URL's card. Status is "ok" when there is something to
// show, "none" when the page has no title or image worth a card, and "error"
// when it could not be fetched — the client shows no card for either.
type LinkPreview struct {
	URL         string `json:"url"`
	Status      string `json:"status" enum:"ok,none,error"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	SiteName    string `json:"site_name,omitempty"`
}

const (
	previewMaxBytes = 1 << 20 // the <head> is in the first megabyte of any page worth previewing
	previewFreshOK  = 7 * 24 * time.Hour
	previewFreshBad = 6 * time.Hour
)

// previewAllowPrivate lets tests (httptest) and local development reach
// loopback, mirroring RULES_WEBHOOK_ALLOW_PRIVATE.
var previewAllowPrivate = os.Getenv("LINK_PREVIEW_ALLOW_PRIVATE") == "1"

var errPreviewPrivate = errors.New("private address")

var previewClient = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 4 * time.Second,
			// Checked on the RESOLVED address at dial time, so neither DNS
			// rebinding nor a redirect to a private host gets past it.
			Control: func(_, address string, _ syscall.RawConn) error {
				if previewAllowPrivate {
					return nil
				}
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				ip := net.ParseIP(host)
				if ip == nil || !publicAddr(ip) {
					return errPreviewPrivate
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout:   4 * time.Second,
		ResponseHeaderTimeout: 6 * time.Second,
		MaxIdleConns:          10,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("redirect to a non-web URL")
		}
		return nil
	},
}

func publicAddr(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() ||
		(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&0xC0 == 64))
}

// normalisePreviewURL accepts only an absolute http(s) URL, without its
// fragment (which the server never sees anyway), so one page is one cache row.
func normalisePreviewURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", false
	}
	u.Fragment = ""
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	return u.String(), true
}

// GetLinkPreview answers a URL's card, from the cache when it is fresh.
func GetLinkPreview(ctx context.Context, a *app.App, rawURL string) (*LinkPreview, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	u, ok := normalisePreviewURL(rawURL)
	if !ok {
		return nil, huma.Error422UnprocessableEntity("url must be an http or https address")
	}

	p := &LinkPreview{URL: u}
	var fetchedAt time.Time
	err = a.SQLDB.QueryRowContext(ctx, `
		SELECT status, title, description, image_url, site_name, fetched_at
		FROM link_previews WHERE workspace_id = $1 AND url = $2`, s.WorkspaceID, u).
		Scan(&p.Status, &p.Title, &p.Description, &p.ImageURL, &p.SiteName, &fetchedAt)
	if err == nil {
		fresh := previewFreshOK
		if p.Status != "ok" {
			fresh = previewFreshBad
		}
		if time.Since(fetchedAt) < fresh {
			return p, nil
		}
	} else if !notFound(err) {
		return nil, huma.Error500InternalServerError("could not read the link preview")
	}

	fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	*p = fetchPreview(fctx, u)
	_, _ = a.SQLDB.ExecContext(ctx, `
		INSERT INTO link_previews (workspace_id, url, status, title, description, image_url, site_name, fetched_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (workspace_id, url) DO UPDATE SET status = EXCLUDED.status, title = EXCLUDED.title,
		  description = EXCLUDED.description, image_url = EXCLUDED.image_url, site_name = EXCLUDED.site_name,
		  fetched_at = now()`,
		s.WorkspaceID, u, p.Status, p.Title, p.Description, p.ImageURL, p.SiteName)
	return p, nil
}

// fetchPreview reads one page's preview metadata. It never fails: a page that
// cannot be read is a preview with status "error".
func fetchPreview(ctx context.Context, u string) LinkPreview {
	p := LinkPreview{URL: u, Status: "error"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return p
	}
	// Several sites (X, Instagram) only serve their Open Graph tags to
	// crawlers they recognise; this names itself as one.
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; MoharrikLinkPreview/1.0; +https://moharrik.com) facebookexternalhit/1.1")
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.1")
	req.Header.Set("Accept-Language", "en,ar;q=0.8")
	res, err := previewClient.Do(req)
	if err != nil {
		return p
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return p
	}
	final := res.Request.URL
	ct, params, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	switch {
	case strings.HasPrefix(ct, "image/"):
		// A direct image link previews as itself.
		p.Status, p.ImageURL, p.SiteName = "ok", final.String(), final.Hostname()
		return p
	case ct != "text/html" && ct != "application/xhtml+xml":
		p.Status = "none"
		return p
	}
	body := io.LimitReader(res.Body, previewMaxBytes)
	r, err := charset.NewReader(body, "text/html; charset="+params["charset"])
	if err != nil {
		r = body
	}
	meta := parsePreviewHead(r)
	p.Title = clean(firstNonEmpty(meta["og:title"], meta["twitter:title"], meta["title"]), 300)
	p.Description = clean(firstNonEmpty(meta["og:description"], meta["twitter:description"], meta["description"]), 500)
	p.SiteName = clean(firstNonEmpty(meta["og:site_name"], strings.TrimPrefix(final.Hostname(), "www.")), 120)
	if img := firstNonEmpty(meta["og:image:secure_url"], meta["og:image"], meta["og:image:url"], meta["twitter:image"], meta["twitter:image:src"]); img != "" {
		if ref, err := url.Parse(strings.TrimSpace(img)); err == nil {
			abs := final.ResolveReference(ref)
			if abs.Scheme == "https" || abs.Scheme == "http" {
				p.ImageURL = abs.String()
			}
		}
	}
	if p.Title == "" && p.ImageURL == "" {
		p.Status = "none"
		return p
	}
	p.Status = "ok"
	return p
}

// parsePreviewHead collects <title> and the <meta> tags a preview reads,
// stopping at <body> — everything it needs is in the head.
func parsePreviewHead(r io.Reader) map[string]string {
	out := map[string]string{}
	z := html.NewTokenizer(r)
	inTitle := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch string(name) {
			case "body":
				return out
			case "title":
				inTitle = true
			case "meta":
				var key, content string
				for hasAttr {
					var k, v []byte
					k, v, hasAttr = z.TagAttr()
					switch strings.ToLower(string(k)) {
					case "property", "name":
						if key == "" {
							key = strings.ToLower(strings.TrimSpace(string(v)))
						}
					case "content":
						content = string(v)
					}
				}
				if key != "" && content != "" {
					if _, seen := out[key]; !seen {
						out[key] = content
					}
				}
			}
		case html.TextToken:
			if inTitle {
				if _, seen := out["title"]; !seen {
					out["title"] = string(z.Text())
				}
			}
		case html.EndTagToken:
			if name, _ := z.TagName(); string(name) == "title" {
				inTitle = false
			} else if string(name) == "head" {
				return out
			}
		}
	}
}

// clean collapses whitespace and cuts s to at most n runes.
func clean(s string, n int) string {
	s = strings.Join(strings.Fields(html.UnescapeString(s)), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func registerLinkPreview(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-link-preview", Method: http.MethodGet, Path: "/api/w/link_preview",
		Summary:     "A URL's preview card (title, description, image, site) for the inbox thread",
		Description: "Fetched by the server with a private-network guard and cached per workspace for a week (six hours when the page could not be read).",
		Tags:        []string{"Inbox"},
	}, func(ctx context.Context, in *struct {
		wsHeader
		URL string `query:"url" maxLength:"2048" doc:"An absolute http(s) URL from a message."`
	}) (*body[LinkPreview], error) {
		p, err := GetLinkPreview(ctx, a, in.URL)
		if err != nil {
			return nil, err
		}
		return out(*p), nil
	})
}
