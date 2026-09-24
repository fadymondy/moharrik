package inbox

import (
	"sync"

	"github.com/microcosm-cc/bluemonday"
)

/*
Email HTML is untrusted content from the open internet, rendered inside a
product used by a signed-in team (MH-556). It goes through TWO independent
sanitisers before a browser ever parses it as markup:

 1. HERE, on ingestion (bluemonday, server-side): what is stored in
    messages.body_html is already inert. Nothing downstream — the REST API,
    an MCP client, a future export — can see the raw HTML.
 2. AGAIN, client-side (DOMPurify) immediately before it is written into the
    sandboxed <iframe>'s srcdoc, because a stored value read back is still
    "HTML from a variable" and the rule for that is always sanitise-then-render,
    never trust-then-render.

A single global policy (not rebuilt per call): bluemonday's policy compiles
its allow-list into regexes at construction, so building one per email would
be wasted work on every poll.
*/

var (
	htmlPolicyOnce sync.Once
	htmlPolicy     *bluemonday.Policy
)

func emailPolicy() *bluemonday.Policy {
	htmlPolicyOnce.Do(func() {
		p := bluemonday.UGCPolicy()
		// Email HTML leans on tables and inline styles for layout far more than
		// user comments do (bluemonday's UGC baseline). Structure and cosmetic
		// styling are allowed; nothing that runs code or reaches off-page is
		// (no <script>, no on* handlers, no javascript:/data: hrefs — UGCPolicy
		// already refuses all of those, and this only adds to its allow-list).
		p.AllowStyling()
		p.AllowStyles("background", "background-color", "color", "font-size", "font-weight",
			"font-family", "text-align", "padding", "margin", "border", "border-collapse",
			"width", "max-width", "height", "vertical-align", "line-height").Globally()
		p.AllowElements("table", "thead", "tbody", "tfoot", "tr", "td", "th", "center", "font", "hr")
		p.AllowAttrs("width", "height", "align", "valign", "border", "cellpadding", "cellspacing", "bgcolor").
			OnElements("table", "td", "th", "tr")
		p.AllowAttrs("color", "face", "size").OnElements("font")
		// <img src> is kept (including cid: references to this message's own
		// attachments) — the CLIENT decides whether to load it, behind "Show
		// images" (web/components/inbox/email-body.tsx). Blocking it here too
		// would mean an inline attachment could never be shown either.
		p.AllowImages()
		p.AllowAttrs("cite").OnElements("blockquote", "q")
		p.RequireNoFollowOnLinks(true)
		p.AddTargetBlankToFullyQualifiedLinks(true)
		htmlPolicy = p
	})
	return htmlPolicy
}

// SanitizeEmailHTML makes raw email HTML safe to store and, eventually, to
// render — see the package doc above for why this is only HALF the defence.
func SanitizeEmailHTML(raw string) string {
	if raw == "" {
		return ""
	}
	return emailPolicy().Sanitize(raw)
}
