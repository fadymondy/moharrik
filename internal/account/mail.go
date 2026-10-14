package account

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/marketing"
)

// The system mail: Moharrik's own messages to a person about their account
// (verify your email, reset your password), sent through MAIL_SMTP_*. Not a
// workspace's SMTP service — those send a workspace's campaigns and replies.
//
// Unset = no account mail: sign-up still works (verification is advisory),
// and "Forgot password?" is hidden, because a link that never arrives is
// worse than no link.

// Message is one account email.
type Message struct {
	To, Subject, Text, HTML string
}

// Deliver sends account mail. A variable so tests can read what was sent.
var Deliver = func(ctx context.Context, m Message) error {
	return marketing.Send(ctx, systemSMTP(), marketing.Message{To: m.To, Subject: m.Subject, Text: m.Text, HTML: m.HTML})
}

// MailConfigured reports whether account mail can be sent.
var MailConfigured = func() bool { return systemSMTP() != nil }

func systemSMTP() *marketing.SMTP {
	host := strings.TrimSpace(os.Getenv("MAIL_SMTP_HOST"))
	from := strings.TrimSpace(os.Getenv("MAIL_FROM_EMAIL"))
	if host == "" || from == "" {
		return nil
	}
	port, _ := strconv.ParseInt(os.Getenv("MAIL_SMTP_PORT"), 10, 64)
	if port == 0 {
		port = 587
	}
	tls := strings.TrimSpace(os.Getenv("MAIL_SMTP_TLS"))
	switch tls {
	case marketing.TLSNone, marketing.TLSStartTLS, marketing.TLSImplicit:
	default:
		tls = marketing.TLSStartTLS
		if port == 465 {
			tls = marketing.TLSImplicit
		}
	}
	name := strings.TrimSpace(os.Getenv("MAIL_FROM_NAME"))
	if name == "" {
		name = "Moharrik"
	}
	return &marketing.SMTP{
		Host: host, Port: port, TLSMode: tls,
		Username: os.Getenv("MAIL_SMTP_USERNAME"), Password: os.Getenv("MAIL_SMTP_PASSWORD"),
		FromName: name, FromEmail: from, ServiceName: "system",
	}
}

// webURL is where the links in account mail open: the public web origin.
func webURL() string {
	for _, k := range []string{"WEB_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return "http://localhost:3120"
}

const (
	verifyTTL = 48 * time.Hour
	resetTTL  = time.Hour
)

// issueEmailToken makes a one-time link token for purpose, voiding the
// person's earlier unused ones for the same purpose.
func issueEmailToken(ctx context.Context, db *sql.DB, purpose, userID, email string, ttl time.Duration) (string, error) {
	tok := randHex(32)
	if _, err := db.ExecContext(ctx, `DELETE FROM auth_email_tokens WHERE user_id = $1 AND purpose = $2`, userID, purpose); err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO auth_email_tokens (token_hash, purpose, user_id, email, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		sha256Hex(tok), purpose, userID, email, time.Now().Add(ttl)); err != nil {
		return "", err
	}
	return tok, nil
}

// sendVerify mails the "confirm your email" link.
func sendVerify(ctx context.Context, db *sql.DB, userID, email string) error {
	if !MailConfigured() {
		return nil
	}
	tok, err := issueEmailToken(ctx, db, "verify", userID, email, verifyTTL)
	if err != nil {
		return err
	}
	link := webURL() + "/verify-email?token=" + url.QueryEscape(tok)
	return Deliver(ctx, compose(email,
		"Confirm your email for Moharrik — أكّد بريدك الإلكتروني في محرّك",
		"Confirm that this is your email address for Moharrik. The link works for 48 hours.",
		"أكّد أن هذا بريدك الإلكتروني في محرّك. يعمل الرابط لمدة 48 ساعة.",
		"Confirm email", "تأكيد البريد", link,
		"If you did not create a Moharrik account, ignore this email.",
		"إن لم تنشئ حسابًا في محرّك فتجاهل هذه الرسالة."))
}

// sendReset mails the "reset your password" link.
func sendReset(ctx context.Context, db *sql.DB, userID, email string) error {
	tok, err := issueEmailToken(ctx, db, "reset", userID, email, resetTTL)
	if err != nil {
		return err
	}
	link := webURL() + "/reset-password?token=" + url.QueryEscape(tok)
	return Deliver(ctx, compose(email,
		"Reset your Moharrik password — إعادة تعيين كلمة مرور محرّك",
		"Someone asked to reset the password of your Moharrik account. The link works for one hour, once.",
		"طلب أحدهم إعادة تعيين كلمة مرور حسابك في محرّك. يعمل الرابط مرة واحدة خلال ساعة.",
		"Choose a new password", "اختر كلمة مرور جديدة", link,
		"If it was not you, ignore this email: your password stays as it is.",
		"إن لم تكن أنت فتجاهل هذه الرسالة، وستبقى كلمة مرورك كما هي."))
}

// compose writes a short bilingual message: English, then Arabic.
func compose(to, subject, en, ar, btnEn, btnAr, link, footEn, footAr string) Message {
	text := fmt.Sprintf("%s\n\n%s: %s\n\n%s\n\n---\n\n%s\n\n%s: %s\n\n%s\n", en, btnEn, link, footEn, ar, btnAr, link, footAr)
	e := html.EscapeString
	button := func(label string) string {
		return `<p><a href="` + e(link) + `" style="display:inline-block;padding:10px 18px;border-radius:8px;background:#0f766e;color:#fff;text-decoration:none">` + e(label) + `</a></p>`
	}
	body := `<div style="font-family:system-ui,sans-serif;font-size:15px;line-height:1.5;max-width:520px">` +
		`<div dir="ltr"><p>` + e(en) + `</p>` + button(btnEn) + `<p style="color:#666;font-size:13px">` + e(footEn) + `</p></div>` +
		`<hr style="border:0;border-top:1px solid #ddd">` +
		`<div dir="rtl" lang="ar"><p>` + e(ar) + `</p>` + button(btnAr) + `<p style="color:#666;font-size:13px">` + e(footAr) + `</p></div>` +
		`<p style="color:#999;font-size:12px" dir="ltr">` + e(link) + `</p></div>`
	return Message{To: to, Subject: subject, Text: text, HTML: body}
}
