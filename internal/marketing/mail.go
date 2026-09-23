// Package marketing is Moharrik's email-marketing module: lists, contacts with
// per-list consent (double opt-in), campaigns and a per-workspace SMTP service.
//
// Ported from fadymondy.com-v2 (internal/mail, internal/rest/subscribe_handler.go,
// internal/mail/campaign.go) and made multi-tenant: every tenant query runs on the
// app (RLS) pool under the workspace on the context, with an explicit
// workspace_id predicate as well.
package marketing

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"mime"
	"net"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// TLS modes an SMTP service can be configured with.
const (
	TLSNone     = "none"     // plaintext; only sane for a local relay
	TLSStartTLS = "starttls" // 587: connect plain, upgrade
	TLSImplicit = "tls"      // 465: TLS from the first byte
)

// SMTP is one workspace's resolved sending service (password unsealed).
type SMTP struct {
	Host, Username, Password, FromName, FromEmail, TLSMode string
	Port                                                   int64
	// Which service this is (MH-412): a workspace may have several.
	ServiceID, ServiceName string
}

// RecipientError is a rejection of one address, as opposed to a failure of the
// connection or the sender. A 5xx on RCPT means the mailbox is gone, so the
// subscription is marked bounced rather than retried forever.
type RecipientError struct {
	Address string
	Err     error
}

func (e *RecipientError) Error() string {
	return fmt.Sprintf("recipient %s rejected: %v", e.Address, e.Err)
}
func (e *RecipientError) Unwrap() error { return e.Err }

// IsHardBounce reports whether the address was permanently rejected.
func IsHardBounce(err error) bool {
	var rejected *RecipientError
	if !errors.As(err, &rejected) {
		return false
	}
	var reply *textproto.Error
	if !errors.As(rejected.Err, &reply) {
		return false
	}
	return reply.Code >= 500 && reply.Code < 600
}

// ErrNoService: the workspace has not configured SMTP.
var ErrNoService = errors.New("this workspace has no SMTP service configured")

// Message is one email.
type Message struct {
	To, Subject, Text, HTML string
	Headers                 map[string]string
}

// Send delivers one message. Errors are returned verbatim: the mail server's own
// refusal is the part that says what to change.
func Send(ctx context.Context, svc *SMTP, msg Message) error {
	if svc == nil {
		return ErrNoService
	}
	if msg.To == "" {
		return errors.New("no recipient")
	}
	addr := net.JoinHostPort(svc.Host, fmt.Sprint(svc.Port))
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	var conn net.Conn
	var err error
	if svc.TLSMode == TLSImplicit {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: svc.Host})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	client, err := smtp.NewClient(conn, svc.Host)
	if err != nil {
		return fmt.Errorf("SMTP handshake: %w", err)
	}
	defer client.Quit() //nolint:errcheck

	if svc.TLSMode == TLSStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("%s does not offer STARTTLS", addr)
		}
		if err := client.StartTLS(&tls.Config{ServerName: svc.Host}); err != nil {
			return fmt.Errorf("STARTTLS: %w", err)
		}
	}
	if svc.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", svc.Username, svc.Password, svc.Host)); err != nil {
			return fmt.Errorf("authenticate as %s: %w", svc.Username, err)
		}
	}
	from := svc.FromEmail
	if from == "" {
		from = svc.Username
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("sender %s rejected: %w", from, err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return &RecipientError{Address: msg.To, Err: err}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("start message body: %w", err)
	}
	if _, err := w.Write([]byte(build(svc, msg))); err != nil {
		return fmt.Errorf("write message body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("deliver: %w", err)
	}
	return nil
}

// build renders the RFC 5322 message (text + optional HTML alternative).
func build(svc *SMTP, msg Message) string {
	var b strings.Builder
	from := svc.FromEmail
	if from == "" {
		from = svc.Username
	}
	if svc.FromName != "" {
		fmt.Fprintf(&b, "From: %s <%s>\r\n", mime.QEncoding.Encode("utf-8", svc.FromName), from)
	} else {
		fmt.Fprintf(&b, "From: %s\r\n", from)
	}
	fmt.Fprintf(&b, "To: %s\r\n", msg.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	for k, v := range msg.Headers {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	if msg.HTML == "" {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
		b.WriteString(msg.Text)
		return b.String()
	}
	boundary := fmt.Sprintf("mhr-alt-%d", time.Now().UnixNano())
	fmt.Fprintf(&b, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	// Plain text first: clients pick the LAST part they can render.
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, msg.Text)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n", boundary, msg.HTML)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.String()
}

// UnsubscribeHeaders let a mail client show its own one-click unsubscribe.
func UnsubscribeHeaders(unsubURL string) map[string]string {
	return map[string]string{
		"List-Unsubscribe":      "<" + unsubURL + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}

// ---- templates (inline-styled tables; email clients support little else) ----

const (
	colourInk    = "#0E1A3C"
	colourPaper  = "#F7F7F8"
	colourMuted  = "#4A4A55"
	colourAccent = "#4B00B6"
	colourLine   = "#E2E2E8"
)

// Shell wraps body content in the frame, RTL for Arabic.
func Shell(locale, heading, bodyHTML, footerHTML string) string {
	dir, align, font := "ltr", "left", "'Segoe UI', Helvetica, Arial, sans-serif"
	if locale == "ar" {
		dir, align, font = "rtl", "right", "'IBM Plex Sans Arabic', 'Segoe UI', Tahoma, sans-serif"
	}
	return fmt.Sprintf(`<!doctype html>
<html dir="%s" lang="%s">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width"></head>
<body style="margin:0;padding:0;background:%s;">
  <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" dir="%s" style="background:%s;padding:32px 16px;">
    <tr><td align="center">
      <table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;background:#ffffff;border:1px solid %s;">
        <tr><td style="padding:28px 28px 8px 28px;text-align:%s;">
          <h1 style="margin:0;font-family:%s;font-size:20px;font-weight:500;color:%s;">%s</h1>
        </td></tr>
        <tr><td style="padding:8px 28px 24px 28px;text-align:%s;font-family:%s;font-size:15px;line-height:1.6;color:%s;">%s</td></tr>
        <tr><td style="padding:16px 28px;border-top:1px solid %s;text-align:%s;font-family:ui-monospace,Menlo,monospace;font-size:12px;color:%s;">%s</td></tr>
      </table>
    </td></tr>
  </table>
</body></html>`,
		dir, locale, colourPaper, dir, colourPaper, colourLine,
		align, font, colourInk, html.EscapeString(heading),
		align, font, colourMuted, bodyHTML,
		colourLine, align, colourMuted, footerHTML)
}

// Button is a call to action that survives clients without CSS support.
func Button(label, url string) string {
	return fmt.Sprintf(`<table role="presentation" cellpadding="0" cellspacing="0" border="0" style="margin:20px 0;"><tr><td style="background:%s;">`+
		`<a href="%s" style="display:inline-block;padding:11px 20px;font-size:15px;color:#ffffff;text-decoration:none;">%s</a></td></tr></table>`,
		colourAccent, html.EscapeString(url), html.EscapeString(label))
}

// ConfirmEmail is the double opt-in message for one list of one workspace.
func ConfirmEmail(locale, confirmURL, workspace, list string) (subject, text, htmlBody string) {
	name := workspace + " — " + list
	if locale == "ar" {
		subject = "أكّد اشتراكك في " + name
		text = "أكّد اشتراكك في " + name + " عبر الرابط التالي:\n\n" + confirmURL + "\n\nإذا لم تطلب هذا، تجاهل الرسالة ولن يحدث شيء."
		htmlBody = Shell(locale, "أكّد اشتراكك",
			`<p style="margin:0 0 8px 0;">اضغط الزر لتأكيد اشتراكك في `+html.EscapeString(name)+`.</p>`+
				Button("تأكيد الاشتراك", confirmURL)+
				`<p style="margin:8px 0 0 0;">إذا لم تطلب هذا، تجاهل الرسالة ولن يحدث شيء.</p>`,
			html.EscapeString(workspace))
		return
	}
	subject = "Confirm your subscription to " + name
	text = "Confirm your subscription to " + name + " with this link:\n\n" + confirmURL + "\n\nIf you did not ask for this, ignore this email and nothing happens."
	htmlBody = Shell(locale, "Confirm your subscription",
		`<p style="margin:0 0 8px 0;">Click the button to confirm your subscription to `+html.EscapeString(name)+`.</p>`+
			Button("Confirm subscription", confirmURL)+
			`<p style="margin:8px 0 0 0;">If you did not ask for this, ignore this email and nothing happens.</p>`,
		html.EscapeString(workspace))
	return
}

// RenderCampaign turns the markdown body into the email, with its unsubscribe footer.
func RenderCampaign(locale, subject, body, unsubURL string) (text, htmlBody string) {
	label := "Unsubscribe"
	if locale == "ar" {
		label = "إلغاء الاشتراك"
	}
	footer := fmt.Sprintf(`<a href="%s" style="color:%s;text-decoration:underline;">%s</a>`, html.EscapeString(unsubURL), colourMuted, label)
	htmlBody = Shell(locale, subject, markdownToHTML(body), footer)
	text = body + "\n\n—\n" + label + ": " + unsubURL
	return
}

// markdownToHTML renders the subset of markdown that survives an email client:
// headings, bold, italic, links, lists, paragraphs. Everything is escaped first.
func markdownToHTML(md string) string {
	var out strings.Builder
	inList := false
	flush := func() {
		if inList {
			out.WriteString("</ul>")
			inList = false
		}
	}
	for _, raw := range strings.Split(md, "\n") {
		t := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		switch {
		case t == "":
			flush()
		case strings.HasPrefix(t, "### "):
			flush()
			fmt.Fprintf(&out, `<h3 style="margin:18px 0 6px 0;font-size:16px;font-weight:500;color:%s;">%s</h3>`, colourInk, inline(t[4:]))
		case strings.HasPrefix(t, "## "):
			flush()
			fmt.Fprintf(&out, `<h2 style="margin:22px 0 8px 0;font-size:18px;font-weight:500;color:%s;">%s</h2>`, colourInk, inline(t[3:]))
		case strings.HasPrefix(t, "# "):
			flush()
			fmt.Fprintf(&out, `<h1 style="margin:24px 0 10px 0;font-size:20px;font-weight:500;color:%s;">%s</h1>`, colourInk, inline(t[2:]))
		case strings.HasPrefix(t, "- "), strings.HasPrefix(t, "* "):
			if !inList {
				out.WriteString(`<ul style="margin:8px 0;padding-inline-start:20px;">`)
				inList = true
			}
			fmt.Fprintf(&out, `<li style="margin:4px 0;">%s</li>`, inline(t[2:]))
		default:
			flush()
			fmt.Fprintf(&out, `<p style="margin:10px 0;">%s</p>`, inline(t))
		}
	}
	flush()
	return out.String()
}

func inline(s string) string {
	s = html.EscapeString(s)
	s = replacePairs(s, "**", `<strong style="font-weight:600;">`, "</strong>")
	s = replacePairs(s, "*", "<em>", "</em>")
	for {
		open := strings.Index(s, "[")
		if open == -1 {
			break
		}
		mid := strings.Index(s[open:], "](")
		if mid == -1 {
			break
		}
		closeIdx := strings.Index(s[open+mid:], ")")
		if closeIdx == -1 {
			break
		}
		label := s[open+1 : open+mid]
		url := s[open+mid+2 : open+mid+closeIdx]
		// Only web and mail links: a javascript: href must not reach an inbox.
		if !(strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "mailto:")) {
			s = s[:open] + label + s[open+mid+closeIdx+1:]
			continue
		}
		anchor := fmt.Sprintf(`<a href="%s" style="color:%s;">%s</a>`, url, colourAccent, label)
		s = s[:open] + anchor + s[open+mid+closeIdx+1:]
	}
	return s
}

func replacePairs(s, marker, openTag, closeTag string) string {
	var b strings.Builder
	rest := s
	odd := false
	for {
		i := strings.Index(rest, marker)
		if i == -1 {
			break
		}
		if !odd && !strings.Contains(rest[i+len(marker):], marker) {
			break
		}
		b.WriteString(rest[:i])
		if odd {
			b.WriteString(closeTag)
		} else {
			b.WriteString(openTag)
		}
		odd = !odd
		rest = rest[i+len(marker):]
	}
	b.WriteString(rest)
	return b.String()
}
