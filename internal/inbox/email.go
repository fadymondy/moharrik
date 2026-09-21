package inbox

import (
	"fmt"
	"io"
	"strings"
	"time"

	_ "github.com/emersion/go-message/charset" // registers the charsets real mail actually uses
	"github.com/emersion/go-message/mail"
)

/*
The email channel's parsing and threading rules (MH-133).

This file is deliberately free of IMAP and of the database: it turns raw
message bytes into an Email, and answers "which conversation does this belong
to" from values a caller looked up. That is what makes the rules testable
without a mail server, and the rules are the part that goes subtly wrong.
*/

// Email is one inbound message, flattened.
type Email struct {
	MessageID  string
	InReplyTo  string
	References []string
	From       string // address only, lowercased
	FromName   string
	Subject    string
	Body       string // text/plain, or HTML stripped to text
	Date       time.Time
}

// maxBody caps what is stored from one email. A newsletter can be megabytes of
// HTML, and the inbox shows a conversation, not a document.
const maxBody = 16 << 10

// ParseEmail reads an RFC 5322 message.
//
// It is forgiving on purpose: a message with no Message-ID, no Date or no
// text/plain part is still filed, because refusing it would mean silently
// dropping a customer's email.
func ParseEmail(r io.Reader) (*Email, error) {
	mr, err := mail.CreateReader(r)
	if err != nil {
		return nil, fmt.Errorf("this does not parse as an email: %w", err)
	}
	h := mr.Header
	out := &Email{}

	out.MessageID = trimAngles(h.Get("Message-Id"))
	out.InReplyTo = trimAngles(h.Get("In-Reply-To"))
	out.References = splitReferences(h.Get("References"))
	out.Subject, _ = h.Subject() // decodes RFC 2047 for us
	if addrs, err := h.AddressList("From"); err == nil && len(addrs) > 0 {
		out.From = strings.ToLower(strings.TrimSpace(addrs[0].Address))
		out.FromName = strings.TrimSpace(addrs[0].Name)
	}
	// A missing Date header comes back as the zero time with NO error, so this
	// has to test the value rather than err. An undated email arrived now;
	// dating it to 1970 would bury it at the bottom of its own thread.
	if d, err := h.Date(); err == nil && !d.IsZero() {
		out.Date = d
	} else {
		out.Date = time.Now().UTC()
	}

	var plain, html string
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		} else if err != nil {
			break // a malformed part must not lose the parts already read
		}
		switch ph := part.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := ph.ContentType()
			b, _ := io.ReadAll(io.LimitReader(part.Body, maxBody*4))
			if strings.HasPrefix(ct, "text/html") {
				if html == "" {
					html = string(b)
				}
			} else if plain == "" {
				plain = string(b)
			}
		default:
			// An attachment. The body is what the inbox shows; attachments are
			// not stored in P0, and pretending otherwise would be worse.
		}
	}
	body := plain
	if strings.TrimSpace(body) == "" {
		body = stripHTML(html)
	}
	out.Body = trimTo(strings.TrimSpace(collapseBlankLines(body)), maxBody)
	return out, nil
}

// trimAngles turns "<abc@host>" into "abc@host". A Message-ID is compared
// exactly, so the brackets have to go before it is stored or matched.
func trimAngles(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "<")
	v = strings.TrimSuffix(v, ">")
	return strings.TrimSpace(v)
}

// splitReferences reads a References header, which is whitespace-separated and
// very often folded across several lines.
func splitReferences(v string) []string {
	var out []string
	for _, f := range strings.Fields(v) {
		if id := trimAngles(f); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// ThreadKeys returns the Message-IDs this email claims to answer, newest first.
// In-Reply-To is the direct parent, so it is tried before References, and
// References is walked backwards because its last entry is the nearest
// ancestor.
func (e *Email) ThreadKeys() []string {
	var keys []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			keys = append(keys, id)
		}
	}
	add(e.InReplyTo)
	for i := len(e.References) - 1; i >= 0; i-- {
		add(e.References[i])
	}
	return keys
}

// NormalisedSubject strips the reply and forward prefixes mail clients add, in
// the languages this product actually ships in, so "Re: Fwd: Invoice" and
// "Invoice" fall in the same thread.
//
// It is the FALLBACK, used only when no Message-ID matched: subjects collide
// ("Hello" from two customers), so it is always paired with the sender address.
func NormalisedSubject(s string) string {
	s = strings.TrimSpace(s)
	for {
		lower := strings.ToLower(s)
		cut := -1
		for _, p := range []string{"re:", "re :", "fw:", "fwd:", "رد:", "إعادة توجيه:"} {
			if strings.HasPrefix(lower, p) {
				cut = len(p)
				break
			}
		}
		if cut < 0 {
			break
		}
		s = strings.TrimSpace(s[cut:])
	}
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// ReplyHeaders are what an outbound reply must carry for the customer's mail
// client to thread it under their own message rather than starting a new one.
func ReplyHeaders(inReplyTo string, references []string) map[string]string {
	if inReplyTo == "" {
		return nil
	}
	refs := make([]string, 0, len(references)+1)
	for _, r := range references {
		refs = append(refs, "<"+r+">")
	}
	if !contains(references, inReplyTo) {
		refs = append(refs, "<"+inReplyTo+">")
	}
	return map[string]string{
		"In-Reply-To": "<" + inReplyTo + ">",
		"References":  strings.Join(refs, " "),
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// stripHTML is the same shape as the trends parser's clean(): enough to read a
// message as text, and not a renderer.
func stripHTML(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>':
			if depth > 0 {
				depth--
			}
			b.WriteByte(' ')
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return decodeEntities(b.String())
}

func decodeEntities(s string) string {
	for from, to := range map[string]string{
		"&nbsp;": " ", "&amp;": "&", "&lt;": "<", "&gt;": ">", "&quot;": `"`, "&#39;": "'",
	} {
		s = strings.ReplaceAll(s, from, to)
	}
	return s
}

// collapseBlankLines keeps paragraph breaks but drops the runs of empty lines
// that quoted replies and signatures leave behind.
func collapseBlankLines(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func trimTo(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// subjectOrDefault is what a conversation is called when the email had no
// subject at all.
func subjectOrDefault(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no subject)"
	}
	return trimTo(strings.TrimSpace(s), 200)
}
