package marketing

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	"github.com/fadymondy/moharrik/internal/paging"
)

/*
Campaigns, every one of them, and send audiences with a stated limit (MH-467).

GET /api/w/campaigns ended in LIMIT 500 and MCP list_campaigns in LIMIT 200;
campaign 501 was not on the page and nothing said so. Both now page the way
MH-408's contacts list does: keyset on (created_at, id) newest first, an
accurate total, and an oversize page refused rather than clamped.

The two send paths read their recipients with LIMIT 50000, so a list of 60,000
was sent to its oldest 50,000 and the campaign reported "50,000 / 50,000 sent"
— complete, by its own account. The limit stays (one send is one in-memory
list, paced for hours), but it is now a stated rule: MaxSendAudience is in the
API, the send dialog says it before anything is pressed, and a send over it is
refused BEFORE the campaign is claimed, naming both numbers.
*/

const (
	DefaultCampaignPage = 50
	MaxCampaignPage     = 200

	// MaxSendAudience is the most recipients one send reaches, email or
	// WhatsApp. A larger audience is refused up front, never truncated.
	MaxSendAudience = 50000
)

// AudienceTooLargeError is a send refused because its audience is over MaxSendAudience.
type AudienceTooLargeError struct {
	Audience int64
	Max      int
}

func (e *AudienceTooLargeError) Error() string {
	return fmt.Sprintf("this send would reach %d recipients; one send reaches at most %d. Split the list and send each part",
		e.Audience, e.Max)
}

// CampaignPage is one page of the workspace's campaigns.
type CampaignPage struct {
	Campaigns  []CampaignOut `json:"campaigns"`
	Total      int64         `json:"total"`
	Limit      int           `json:"limit"`
	NextCursor string        `json:"next_cursor"`
}

// ListCampaignsPage is one page of campaigns, newest first.
func ListCampaignsPage(ctx context.Context, db *sql.DB, wsID, after string, limit int) (*CampaignPage, error) {
	cur, err := paging.Decode(after, 2)
	if err != nil {
		return nil, err
	}
	page := &CampaignPage{Campaigns: []CampaignOut{}, Limit: limit}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM campaigns WHERE workspace_id = $1`, wsID).Scan(&page.Total); err != nil {
		return nil, err
	}
	args := []any{wsID}
	where := ""
	if cur != nil {
		us, err := strconv.ParseInt(cur[0], 10, 64)
		if err != nil {
			return nil, paging.ErrBadCursor
		}
		args = append(args, time.UnixMicro(us).UTC(), cur[1])
		where = ` AND (c.created_at, c.id) < ($2, $3)`
	}
	args = append(args, limit+1)
	rows, err := db.QueryContext(ctx, `
		SELECT c.id, c.list_id, l.name, c.channel, c.subject, c.body, c.locale, c.status, c.recipient_count,
		       c.sent_count, c.failed_count, c.skipped_count, c.last_error, c.sent_at, c.created_at, `+campaignServiceCols+`, c.audience_locale
		FROM campaigns c JOIN mail_lists l ON l.id = c.list_id AND l.workspace_id = c.workspace_id
		WHERE c.workspace_id = $1`+where+` ORDER BY c.created_at DESC, c.id DESC LIMIT $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c CampaignOut
		if err := rows.Scan(&c.ID, &c.ListID, &c.ListName, &c.Channel, &c.Subject, &c.Body, &c.Locale, &c.Status, &c.RecipientCount,
			&c.SentCount, &c.FailedCount, &c.SkippedCount, &c.LastError, &c.SentAt, &c.CreatedAt, &c.SMTPServiceID, &c.SMTPServiceName, &c.AudienceLocale); err != nil {
			return nil, err
		}
		page.Campaigns = append(page.Campaigns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(page.Campaigns) > limit {
		page.Campaigns = page.Campaigns[:limit]
		last := page.Campaigns[limit-1]
		page.NextCursor = paging.Encode(strconv.FormatInt(last.CreatedAt.UnixMicro(), 10), last.ID)
	}
	return page, nil
}

// CountEmailAudience is how many addresses an email send to a list reaches now.
func CountEmailAudience(ctx context.Context, db *sql.DB, wsID, listID string) (int64, error) {
	return CountEmailAudienceFor(ctx, db, wsID, listID, "")
}

// AudienceLocale normalises a campaign's audience language (MH-413): en or ar
// narrows the send to contacts in that language; anything else is everyone.
func AudienceLocale(l string) string {
	if l == "en" || l == "ar" {
		return l
	}
	return ""
}

// emailAudienceFrom is the email audience rule, written once for the count
// and the send. MH-561: a contact can now be email-less (phone or handle
// only); it was always excludable from an email send by having no
// subscription, and now it is excluded explicitly too, so the rule reads the
// same regardless of how it got into list_subscriptions.
const emailAudienceFrom = `
		FROM list_subscriptions s
		JOIN contacts c ON c.id = s.contact_id AND c.workspace_id = s.workspace_id
		WHERE s.workspace_id = $1 AND s.list_id = $2 AND s.status = 'subscribed' AND c.email <> ''
		  AND ($3 = '' OR c.locale = $3)`

// CountEmailAudienceFor counts one language's share of it (MH-413).
func CountEmailAudienceFor(ctx context.Context, db *sql.DB, wsID, listID, locale string) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `SELECT count(*) `+emailAudienceFrom, wsID, listID, AudienceLocale(locale)).Scan(&n)
	return n, err
}

// CountSkippedEmailAudienceFor is a subscribed contact this send would reach
// except that it has no email (MH-561) — a phone/handle-only contact that
// ended up subscribed some other way (e.g. imported). Reported alongside the
// count so a workspace is told its send will skip them, not just how many it
// will reach.
func CountSkippedEmailAudienceFor(ctx context.Context, db *sql.DB, wsID, listID, locale string) (int64, error) {
	var n int64
	err := db.QueryRowContext(ctx, `
		SELECT count(*)
		FROM list_subscriptions s
		JOIN contacts c ON c.id = s.contact_id AND c.workspace_id = s.workspace_id
		WHERE s.workspace_id = $1 AND s.list_id = $2 AND s.status = 'subscribed' AND c.email = ''
		  AND ($3 = '' OR c.locale = $3)`, wsID, listID, AudienceLocale(locale)).Scan(&n)
	return n, err
}

// checkAudience refuses a campaign whose audience is over MaxSendAudience. It
// runs before the campaign is claimed, so a refused send leaves it as it was.
func checkAudience(ctx context.Context, db *sql.DB, wsID, campaignID string) error {
	var listID, channel, audience string
	if err := db.QueryRowContext(ctx, `SELECT list_id, channel, audience_locale FROM campaigns WHERE workspace_id = $1 AND id = $2`,
		wsID, campaignID).Scan(&listID, &channel, &audience); err != nil {
		return err
	}
	var n int64
	var err error
	if channel == "whatsapp" {
		n, err = CountWhatsAppAudienceFor(ctx, db, wsID, listID, audience)
	} else {
		n, err = CountEmailAudienceFor(ctx, db, wsID, listID, audience)
	}
	if err != nil {
		return err
	}
	if n > MaxSendAudience {
		return &AudienceTooLargeError{Audience: n, Max: MaxSendAudience}
	}
	return nil
}
