package trends

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
Trends reach the rest of the product (MH-389).

Nothing outside this package read the trends center: a story that mattered
was seen only by whoever opened the page. Two ways out, both ported from
fadymondy.com and made per member:

  - the DIGEST (fadymondy.com: internal/radar/digest/trends.go): an email of
    the workspace's top open stories, each with the reason it ranks and its
    summary, daily or weekly at an hour in the member's own time zone and
    language, through the workspace's own SMTP service. fadymondy.com's is
    English-only and rides along with another digest; this one stands alone
    and is written in the member's language, reasons included.
  - the COMMAND (fadymondy.com: internal/whatsapp/actions.go, "/fm trends"):
    "trends" or "ترندات" sent from a member's VERIFIED WhatsApp number to the
    workspace's own is answered with the top stories, in the member's
    language, instead of being filed as a customer conversation. fadymondy.com
    answers only its owner's self-chat; here any member who verified their
    number (MH-411) and whose role reads trends.

"Top" is one query for both: stories not reviewed, dismissed or saved yet,
seen in the period (a day, or a week for the weekly digest and the command's
three days), best score first — the digest's selection, which fadymondy.com's
command did not share.
*/

// DigestClock is what "now" is for the digest. The tests move it.
var DigestClock = time.Now

// TopStory is one story in a digest or a WhatsApp answer.
type TopStory struct {
	ID      string
	Title   string
	Summary string
	Score   float64
	Reason  TrendReason
	Outlets []string
}

// TopStories is the workspace's best open stories seen since `since`.
func TopStories(ctx context.Context, db *sql.DB, wsID string, since time.Time, limit int) ([]TopStory, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, title, summary, score, reasons::text, outlets FROM trend_clusters
		WHERE workspace_id = $1 AND reviewed_at IS NULL AND dismissed_at IS NULL AND saved_at IS NULL
		  AND last_seen_at >= $2 AND score > 0
		ORDER BY score DESC, last_seen_at DESC, id DESC LIMIT $3`, wsID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TopStory
	for rows.Next() {
		var s TopStory
		var reasons string
		var outlets pgtext.Array
		if err := rows.Scan(&s.ID, &s.Title, &s.Summary, &s.Score, &reasons, &outlets); err != nil {
			return nil, err
		}
		var rs []TrendReason
		_ = json.Unmarshal([]byte(reasons), &rs)
		if len(rs) > 0 {
			s.Reason = rs[0]
		}
		s.Outlets = outlets
		out = append(out, s)
	}
	return out, rows.Err()
}

// ReasonText says why a story ranks, in the reader's language — the phrasing
// the trends page uses (web/lang trends.reason.*).
func ReasonText(r TrendReason, locale string) string {
	ar := locale == "ar"
	switch r.Kind {
	case "sources":
		if r.Count <= 1 {
			if ar {
				return "مصدر واحد"
			}
			return "1 source"
		}
		if ar {
			return fmt.Sprintf("%d مصادر", r.Count)
		}
		return fmt.Sprintf("%d sources", r.Count)
	case "velocity":
		switch r.Signal {
		case "hn":
			if ar {
				return fmt.Sprintf("صاعد: Hacker News رقم %d، %d نقطة", r.Position, r.Metric)
			}
			return fmt.Sprintf("Rising: Hacker News #%d, %d points", r.Position, r.Metric)
		case "devto":
			if ar {
				return fmt.Sprintf("صاعد: %d تفاعلاً على DEV", r.Metric)
			}
			return fmt.Sprintf("Rising: %d reactions on DEV", r.Metric)
		}
		if ar {
			return fmt.Sprintf("صاعد: %d تقارير خلال 6 ساعات", r.Count)
		}
		return fmt.Sprintf("Rising: %d reports within 6 hours", r.Count)
	case "relevance":
		topics := strings.Join(r.Detail, "، ")
		if ar {
			return "يهمك: يطابق " + topics
		}
		return "Relevant to you: matches " + strings.Join(r.Detail, ", ")
	}
	return r.Label
}

func digestBase() string {
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return ""
}

func storyURL(locale, slug, id string) string {
	return digestBase() + "/" + locale + "/w/" + url.PathEscape(slug) + "/trends/" + url.PathEscape(id)
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n])) + "…"
	}
	return s
}

// ---- the email -------------------------------------------------------------------

// isolate wraps user text (a workspace name, a story title) in first-strong
// isolates, so an English title in an Arabic line keeps its punctuation at its
// own end — what <bdi> does on the page, in plain text too.
func isolate(s string) string { return string(rune(0x2068)) + s + string(rune(0x2069)) }

// DigestEmail renders a digest: subject, text and HTML, in locale.
func DigestEmail(locale, workspace, slug, frequency string, day time.Time, stories []TopStory) (subject, text, htmlBody string) {
	ar := locale == "ar"
	date := day.Format("2006-01-02")
	workspace = isolate(workspace)
	var heading, intro, settings, review string
	switch {
	case ar && frequency == "weekly":
		heading, intro = "ترندات الأسبوع في "+workspace, "أبرز القصص في مركز الترندات هذا الأسبوع، الأعلى تقييمًا أولًا:"
	case ar:
		heading, intro = "ترندات اليوم في "+workspace, "أبرز القصص في مركز الترندات منذ أمس، الأعلى تقييمًا أولًا:"
	case frequency == "weekly":
		heading, intro = "This week's trends for "+workspace, "The top stories in your trends center this week, best first:"
	default:
		heading, intro = "Today's trends for "+workspace, "The top stories in your trends center since yesterday, best first:"
	}
	if ar {
		settings, review = "وصلتك هذه الرسالة لأنك اخترت ملخص الترندات في إعدادات التوصيل. غيّره أو أوقفه من هناك:", "راجعها في مركز الترندات"
	} else {
		settings, review = "You get this because you chose the trends digest in your Delivery settings. Change or stop it there:", "Review them in the trends center"
	}
	subject = heading + " — " + date
	base := digestBase() + "/" + locale + "/w/" + url.PathEscape(slug)

	var t strings.Builder
	fmt.Fprintf(&t, "%s\n\n%s\n\n", heading, intro)
	var h strings.Builder
	fmt.Fprintf(&h, `<p style="margin:0 0 16px 0;">%s</p>`, html.EscapeString(intro))
	for i, s := range stories {
		reason := ReasonText(s.Reason, locale)
		link := storyURL(locale, slug, s.ID)
		if reason != "" {
			fmt.Fprintf(&t, "%d. %s  |  %s\n", i+1, isolate(s.Title), reason)
		} else {
			fmt.Fprintf(&t, "%d. %s\n", i+1, isolate(s.Title))
		}
		if s.Summary != "" {
			fmt.Fprintf(&t, "   %s\n", clipText(s.Summary, 240))
		}
		fmt.Fprintf(&t, "   %s\n\n", link)
		fmt.Fprintf(&h, `<div style="margin:0 0 18px 0;"><a href="%s" dir="auto" style="color:#0E1A3C;font-weight:600;text-decoration:none;">%s</a><br><span style="color:#6B6B76;font-size:13px;">%s</span>`,
			html.EscapeString(link), html.EscapeString(s.Title), html.EscapeString(reason))
		if s.Summary != "" {
			fmt.Fprintf(&h, `<br><span dir="auto">%s</span>`, html.EscapeString(clipText(s.Summary, 240)))
		}
		h.WriteString(`</div>`)
	}
	fmt.Fprintf(&t, "%s: %s/trends\n\n%s %s/settings/delivery\n", review, base, settings, base)
	h.WriteString(marketing.Button(review, base+"/trends"))
	footer := html.EscapeString(settings) + ` <a href="` + html.EscapeString(base+"/settings/delivery") + `">` + html.EscapeString(base+"/settings/delivery") + `</a>`
	return subject, t.String(), marketing.Shell(locale, heading, h.String(), footer)
}

// ---- the WhatsApp answer ----------------------------------------------------------

// commandWords are what a member sends to ask for trends: whole message,
// either language, with or without a slash.
var commandWords = map[string]bool{"trends": true, "trend": true, "ترندات": true, "الترندات": true, "ترند": true}

// IsTrendsCommand reports whether an inbound WhatsApp message asks for trends.
func IsTrendsCommand(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	t = strings.TrimPrefix(t, "/")
	t = strings.Trim(t, ".!؟?، ")
	return commandWords[t]
}

// WhatsAppAnswer is the reply to "trends", in locale.
func WhatsAppAnswer(locale, workspace, slug string, stories []TopStory) string {
	ar := locale == "ar"
	workspace = isolate(workspace)
	link := digestBase() + "/" + locale + "/w/" + url.PathEscape(slug) + "/trends"
	var b strings.Builder
	if len(stories) == 0 {
		if ar {
			return "محرّك · " + workspace + "\nلا توجد ترندات جديدة لمراجعتها في آخر 3 أيام.\n" + link
		}
		return "Moharrik · " + workspace + "\nNo new trends to review in the last 3 days.\n" + link
	}
	if ar {
		fmt.Fprintf(&b, "محرّك · %s\nأبرز الترندات للمراجعة:", workspace)
	} else {
		fmt.Fprintf(&b, "Moharrik · %s\nTop trends to review:", workspace)
	}
	for _, s := range stories {
		fmt.Fprintf(&b, "\n• %s (%.0f)", isolate(clipText(s.Title, 90)), s.Score)
		if r := ReasonText(s.Reason, locale); r != "" {
			b.WriteString(" — " + r)
		}
	}
	b.WriteString("\n" + link)
	return b.String()
}

// ErrDigestNoEmail is a member whose account has no email to send to.
var ErrDigestNoEmail = errors.New("your account has no email address to send the digest to")

// HandleWhatsAppCommand answers "trends" from a member's verified number. It
// reports whether the message was a command it answered (and so must not be
// filed as a customer conversation). ctx must not carry a workspace yet.
func HandleWhatsAppCommand(ctx context.Context, a *app.App, wsID, phone, text string) (bool, error) {
	if !IsTrendsCommand(text) {
		return false, nil
	}
	sctx := tenant.WithWorkspace(ctx, wsID)
	m, err := whatsapp.MemberByPhone(sctx, a, wsID, phone)
	if err != nil || m == nil {
		// Not a member's verified number: a customer who wrote "trends" is a
		// customer, and their message is filed like any other.
		return false, err
	}
	locale := m.Locale
	if locale != "ar" {
		locale = "en"
	}
	if !authz.Can(m.Role, authz.TrendsRead) {
		msg := "Your role in this workspace does not include the trends center."
		if locale == "ar" {
			msg = "دورك في مساحة العمل هذه لا يشمل مركز الترندات."
		}
		_, err := whatsapp.SendAs(sctx, a, wsID, phone, msg, whatsapp.KindNotify)
		return true, err
	}
	// One answer every 30 seconds per member: a stuck phone resending the
	// same message must not become a burst from the workspace's number.
	var recent int
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT count(*) FROM trend_digest_sends WHERE workspace_id = $1 AND user_id = $2
		AND kind = 'whatsapp' AND created_at > $3`, wsID, m.UserID, DigestClock().Add(-30*time.Second)).Scan(&recent)
	if recent > 0 {
		return true, nil
	}
	var slug, name string
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT slug, name FROM workspaces WHERE id = $1`, wsID).Scan(&slug, &name)
	stories, err := TopStories(sctx, a.SQLDB, wsID, DigestClock().Add(-72*time.Hour), 5)
	if err != nil {
		return true, err
	}
	_, sendErr := whatsapp.SendAs(sctx, a, wsID, phone, WhatsAppAnswer(locale, name, slug, stories), whatsapp.KindNotify)
	status, errText := "sent", ""
	if sendErr != nil {
		status, errText = "failed", clipText(sendErr.Error(), 300)
	}
	_, _ = a.SQLDB.ExecContext(sctx, `INSERT INTO trend_digest_sends (workspace_id, user_id, kind, status, topics, error, created_at)
		VALUES ($1, $2, 'whatsapp', $3, $4, $5, $6)`, wsID, m.UserID, status, len(stories), errText, DigestClock())
	return true, sendErr
}
