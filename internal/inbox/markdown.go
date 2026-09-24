package inbox

import (
	"bytes"
	"sync"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
)

/*
Markdown formatting for chat, WhatsApp and email replies (MH-556): a member
types Markdown in the reply editor (web/components/inbox/reply-editor.tsx),
and every channel gets a rendering of it —

  - chat/WhatsApp: the client renders it directly (both are the team's own
    plain-text wire format already; only the UI needs the HTML, so it renders
    client-side too, the same small renderer the preview tab uses).
  - email: RenderMarkdown here produces the HTML half of the multipart
    reply (marketing.Send already builds multipart/alternative when Message.HTML
    is set — see internal/inbox/send.go).

goldmark's own output is already escaped (raw HTML in the SOURCE Markdown is
not passed through, by default), but the bluemonday pass stays: a member's own
text should never be trusted more than a customer's just because it came from
inside the product, and it is the same one-call sanitiser already used for
inbound email (sanitize.go), not a second thing to keep in sync.
*/

var (
	mdPolicyOnce sync.Once
	mdPolicy     *bluemonday.Policy
)

func markdownPolicy() *bluemonday.Policy {
	mdPolicyOnce.Do(func() {
		p := bluemonday.UGCPolicy()
		p.AllowAttrs("class").OnElements("code", "pre", "span")
		mdPolicy = p
	})
	return mdPolicy
}

// RenderMarkdown turns a member's Markdown reply into sanitised HTML.
func RenderMarkdown(src string) string {
	if src == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(src), &buf); err != nil {
		return ""
	}
	return markdownPolicy().Sanitize(buf.String())
}
