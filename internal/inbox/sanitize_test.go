package inbox

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

/*
The XSS corpus (MH-556): classic and less-classic vectors, run through
SanitizeEmailHTML (what an inbound email's HTML part goes through before it is
stored — sanitize.go) and asserted INERT: whatever comes out must not be able
to run script in the browser that eventually renders it.

This is the server's OWN pass, the first of two (the second is DOMPurify,
client-side, immediately before the sandboxed iframe's srcdoc is set —
web/lib/inbox-html.ts). Testing it alone is deliberate: the server pass is
what is actually stored and handed to every consumer (REST, MCP, a future
export), so it has to be inert on its own and not rely on the client to catch
what it missed.
*/

var xssCorpus = []struct {
	name    string
	payload string
}{
	{"script tag", `<script>alert(1)</script><p>hi</p>`},
	{"script tag, mixed case", `<ScRiPt>alert(1)</sCrIpT>`},
	{"img onerror", `<img src=x onerror=alert(1)>`},
	{"svg onload", `<svg onload=alert(1)></svg>`},
	{"svg with embedded script", `<svg><script>alert(1)</script></svg>`},
	{"body onload", `<body onload=alert(1)>hi</body>`},
	{"anchor javascript URL", `<a href="javascript:alert(1)">click</a>`},
	{"anchor javascript URL, encoded", `<a href="jav&#x61;script:alert(1)">click</a>`},
	{"iframe javascript URL", `<iframe src="javascript:alert(1)"></iframe>`},
	{"style expression", `<div style="width:expression(alert(1))">hi</div>`},
	{"style behavior", `<div style="behavior:url(evil.htc)">hi</div>`},
	{"meta refresh", `<meta http-equiv="refresh" content="0;url=javascript:alert(1)">`},
	{"object data", `<object data="javascript:alert(1)"></object>`},
	{"embed", `<embed src="javascript:alert(1)">`},
	{"form action", `<form action="javascript:alert(1)"><input type=submit></form>`},
	{"foreignObject with script", `<svg><foreignObject><script xmlns="http://www.w3.org/1999/xhtml">alert(1)</script></foreignObject></svg>`},
	{"data URI iframe", `<iframe src="data:text/html,<script>alert(1)</script>"></iframe>`},
	{"link import", `<link rel="import" href="evil.html">`},
	{"base tag hijack", `<base href="https://evil.example/">`},
	{"event handler on any element", `<p onclick="alert(1)" onmouseover="alert(1)">click me</p>`},
	{"CDATA-wrapped script", `<script><![CDATA[</script><script>alert(1)</script>]]></script>`},
	{"null-byte obfuscated tag", "<scr\x00ipt>alert(1)</scr\x00ipt>"},
	{"double-encoded angle brackets", `&lt;script&gt;alert(1)&lt;/script&gt;`},
	{"malformed unclosed tag", `<img src=x onerror=alert(1)//`},
	{"nested quotes attribute break-out", `<img src="x" alt="" onerror="alert(String.fromCharCode(88,83,83))">`},
	{"css url() javascript", `<div style="background:url(javascript:alert(1))">hi</div>`},
	{"marquee onstart", `<marquee onstart=alert(1)>hi</marquee>`},
	{"video poster + onerror", `<video poster=javascript:alert(1)//></video>`},
	{"details ontoggle", `<details open ontoggle=alert(1)>x</details>`},
}

var dangerousTags = map[atom.Atom]bool{
	atom.Script: true, atom.Iframe: true, atom.Object: true, atom.Embed: true,
	atom.Form: true, atom.Meta: true, atom.Link: true, atom.Base: true, atom.Marquee: true,
}

// findDanger parses out AS HTML (an attacker-controlled string is only
// dangerous once a browser parses it as markup, which is exactly what the
// consumer of SanitizeEmailHTML's output eventually does) and reports the
// first real, live threat: a dangerous element, an on* event handler
// attribute, or a URL-bearing attribute with a javascript:/vbscript: scheme —
// as opposed to grepping the raw string, which flags escaped, inert TEXT
// (e.g. "&lt;img onerror=..." renders as the literal characters "<img
// onerror=...", not a real attribute) as a false positive.
func findDanger(out string) string {
	doc, err := html.ParseFragment(strings.NewReader(out), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return ""
	}
	var walk func(*html.Node) string
	walk = func(n *html.Node) string {
		if n.Type == html.ElementNode {
			if dangerousTags[n.DataAtom] {
				return "dangerous element <" + n.Data + ">"
			}
			for _, a := range n.Attr {
				if strings.HasPrefix(strings.ToLower(a.Key), "on") {
					return "event handler attribute " + a.Key
				}
				v := strings.ToLower(strings.TrimSpace(a.Val))
				if (a.Key == "href" || a.Key == "src" || a.Key == "action" || a.Key == "data") &&
					(strings.HasPrefix(v, "javascript:") || strings.HasPrefix(v, "vbscript:")) {
					return a.Key + " with a script URL: " + a.Val
				}
				if a.Key == "style" && (strings.Contains(v, "expression(") || strings.Contains(v, "behavior:") || strings.Contains(v, "javascript:")) {
					return "dangerous style: " + a.Val
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if d := walk(c); d != "" {
				return d
			}
		}
		return ""
	}
	for _, n := range doc {
		if d := walk(n); d != "" {
			return d
		}
	}
	return ""
}

func TestSanitizeEmailHTML_XSSCorpusIsInert(t *testing.T) {
	for _, c := range xssCorpus {
		t.Run(c.name, func(t *testing.T) {
			out := SanitizeEmailHTML(c.payload)
			if d := findDanger(out); d != "" {
				t.Errorf("sanitised output still executable (%s):\n  in:  %q\n  out: %q", d, c.payload, out)
			}
		})
	}
}

// The same corpus through a Markdown reply (RenderMarkdown, markdown.go):
// goldmark does not interpret raw HTML in the source as markup by default,
// and bluemonday runs on the result regardless — this checks both hold.
func TestRenderMarkdown_XSSCorpusIsInert(t *testing.T) {
	for _, c := range xssCorpus {
		t.Run(c.name, func(t *testing.T) {
			out := RenderMarkdown(c.payload)
			if d := findDanger(out); d != "" {
				t.Errorf("markdown-rendered output still executable (%s):\n  in:  %q\n  out: %q", d, c.payload, out)
			}
		})
	}
}

// A legitimate email is NOT gutted by the sanitiser — the corpus test above
// would pass trivially if SanitizeEmailHTML just returned "".
func TestSanitizeEmailHTML_KeepsOrdinaryContent(t *testing.T) {
	in := `<p>Hi <b>Dana</b>,</p><table><tr><td>Invoice</td><td>#402</td></tr></table>
	       <p>See <a href="https://example.test/invoice/402">the invoice</a>.</p>`
	out := SanitizeEmailHTML(in)
	for _, want := range []string{"Dana", "Invoice", "#402", "https://example.test/invoice/402", "<table", "<b>"} {
		if !strings.Contains(out, want) {
			t.Errorf("sanitising dropped ordinary content %q from: %s", want, out)
		}
	}
}
