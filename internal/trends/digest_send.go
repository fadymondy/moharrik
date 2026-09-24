package trends

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// DigestSettings is one member's trends digest (MH-389).
type DigestSettings struct {
	Frequency string `json:"frequency" enum:"off,daily,weekly"`
	Hour      int    `json:"hour" minimum:"0" maximum:"23" doc:"The local hour it goes out."`
	Weekday   int    `json:"weekday" minimum:"0" maximum:"6" doc:"Weekly only: 0 = Sunday … 6 = Saturday."`
	Timezone  string `json:"timezone" maxLength:"64" doc:"IANA zone; empty uses the workspace's."`
	Locale    string `json:"locale" enum:"en,ar"`
	Top       int    `json:"top" minimum:"3" maximum:"10" doc:"How many stories."`
}

// DigestState is the settings plus what the page shows around them.
type DigestState struct {
	DigestSettings
	Email string `json:"email" doc:"Where it goes: the member's own account email."`
	// Whether the workspace can send email at all; without an SMTP service the
	// digest cannot go, and the page says so instead of offering a dead switch.
	EmailReady        bool       `json:"email_ready"`
	WorkspaceTimezone string     `json:"workspace_timezone"`
	NextAt            *time.Time `json:"next_at" doc:"When the next digest is due, if it is on."`
	LastSentAt        *time.Time `json:"last_sent_at"`
	LastStatus        string     `json:"last_status" doc:"sent, empty (nothing to send) or failed."`
	LastError         string     `json:"last_error"`
}

func defaultDigest(locale string) DigestSettings {
	if locale != "ar" {
		locale = "en"
	}
	return DigestSettings{Frequency: "off", Hour: 8, Weekday: 0, Locale: locale, Top: 5}
}

// ErrDigestBadZone is a time zone Go does not know.
var ErrDigestBadZone = errors.New("that time zone is not one the server knows (use an IANA name such as Africa/Cairo)")

func location(tz, fallback string) *time.Location {
	for _, z := range []string{tz, fallback} {
		if z == "" {
			continue
		}
		if loc, err := time.LoadLocation(z); err == nil {
			return loc
		}
	}
	return time.UTC
}

// period is the digest period `local` falls in, and whether it is due: at or
// after the hour, and for weekly on the chosen day. The key is what makes a
// period go once.
func period(s DigestSettings, local time.Time) (key string, due bool) {
	if s.Frequency == "weekly" {
		if int(local.Weekday()) != s.Weekday || local.Hour() < s.Hour {
			return "", false
		}
		return "w:" + local.Format("2006-01-02"), true
	}
	if s.Frequency != "daily" || local.Hour() < s.Hour {
		return "", false
	}
	return "d:" + local.Format("2006-01-02"), true
}

// nextAt is when the next digest is due after now.
func nextAt(s DigestSettings, now time.Time, loc *time.Location) *time.Time {
	if s.Frequency == "off" {
		return nil
	}
	local := now.In(loc)
	for d := 0; d <= 8; d++ {
		day := time.Date(local.Year(), local.Month(), local.Day()+d, s.Hour, 0, 0, 0, loc)
		if !day.After(now) {
			continue
		}
		if s.Frequency == "weekly" && int(day.Weekday()) != s.Weekday {
			continue
		}
		return &day
	}
	return nil
}

func memberEmail(ctx context.Context, a *app.App, wsID, userID string) string {
	var email string
	// users is outside RLS and the app role's grants: the owner pool, with the
	// membership as the predicate.
	_ = a.SysDB.QueryRowContext(ctx, `SELECT u.email FROM users u JOIN workspace_members wm ON wm.user_id = u.id
		WHERE wm.workspace_id = $1 AND wm.user_id = $2 AND wm.status = 'active'`, wsID, userID).Scan(&email)
	return email
}

// GetDigest reads a member's digest settings (defaults when they never chose).
func GetDigest(ctx context.Context, a *app.App, wsID, userID, uiLocale string) (DigestState, error) {
	st := DigestState{DigestSettings: defaultDigest(uiLocale)}
	var last sql.NullTime
	err := a.SQLDB.QueryRowContext(ctx, `
		SELECT frequency, hour, weekday, timezone, locale, top, last_sent_at, last_status, last_error
		FROM member_trend_digest WHERE workspace_id = $1 AND user_id = $2`, wsID, userID).Scan(
		&st.Frequency, &st.Hour, &st.Weekday, &st.Timezone, &st.Locale, &st.Top, &last, &st.LastStatus, &st.LastError)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return st, err
	}
	if last.Valid {
		t := last.Time
		st.LastSentAt = &t
	}
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT timezone FROM workspaces WHERE id = $1`, wsID).Scan(&st.WorkspaceTimezone)
	st.Email = memberEmail(ctx, a, wsID, userID)
	st.EmailReady = marketing.HasSMTP(ctx, a.SQLDB, wsID)
	st.NextAt = nextAt(st.DigestSettings, DigestClock(), location(st.Timezone, st.WorkspaceTimezone))
	return st, nil
}

// SaveDigest stores a member's choice. Switching it on (or changing when it
// goes) never sends one straight away for a period already past: the current
// period is claimed as done unless it is still ahead.
func SaveDigest(ctx context.Context, a *app.App, wsID, userID string, s DigestSettings) error {
	s.Timezone = strings.TrimSpace(s.Timezone)
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return ErrDigestBadZone
		}
	}
	if s.Locale != "ar" {
		s.Locale = "en"
	}
	if s.Top == 0 {
		s.Top = 5
	}
	var wsZone string
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT timezone FROM workspaces WHERE id = $1`, wsID).Scan(&wsZone)
	key, _ := period(s, DigestClock().In(location(s.Timezone, wsZone)))
	_, err := a.SQLDB.ExecContext(ctx, `
		INSERT INTO member_trend_digest (workspace_id, user_id, frequency, hour, weekday, timezone, locale, top, last_period)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET frequency = EXCLUDED.frequency, hour = EXCLUDED.hour,
		  weekday = EXCLUDED.weekday, timezone = EXCLUDED.timezone, locale = EXCLUDED.locale, top = EXCLUDED.top,
		  last_period = CASE WHEN EXCLUDED.last_period <> '' THEN EXCLUDED.last_period ELSE member_trend_digest.last_period END,
		  updated_at = now()`,
		wsID, userID, s.Frequency, s.Hour, s.Weekday, s.Timezone, s.Locale, s.Top, key)
	return err
}

// DigestResult is what one send did.
type DigestResult struct {
	Status string `json:"status" doc:"sent, empty (no open stories in the period: nothing was sent) or failed."`
	Topics int    `json:"topics"`
	To     string `json:"to"`
	Error  string `json:"error,omitempty"`
}

// SendDigest builds and sends one member's digest now. kind is digest (the
// scheduled one, for periodKey) or test (the member asked from the page).
func SendDigest(ctx context.Context, a *app.App, wsID, userID string, s DigestSettings, kind, periodKey string) (DigestResult, error) {
	res := DigestResult{To: memberEmail(ctx, a, wsID, userID)}
	record := func() {
		_, _ = a.SQLDB.ExecContext(ctx, `INSERT INTO trend_digest_sends (workspace_id, user_id, kind, period, status, topics, error, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, wsID, userID, kind, periodKey, res.Status, res.Topics, res.Error, DigestClock())
		_, _ = a.SQLDB.ExecContext(ctx, `UPDATE member_trend_digest SET last_sent_at = CASE WHEN $3 = 'sent' THEN $5::timestamptz ELSE last_sent_at END,
			last_status = $3, last_error = $4 WHERE workspace_id = $1 AND user_id = $2`, wsID, userID, res.Status, res.Error, DigestClock())
	}
	if res.To == "" {
		res.Status, res.Error = "failed", ErrDigestNoEmail.Error()
		record()
		return res, ErrDigestNoEmail
	}
	svc, err := marketing.LoadSMTP(ctx, a.SQLDB, wsID)
	if err != nil || svc == nil {
		res.Status, res.Error = "failed", marketing.ErrNoService.Error()
		record()
		return res, marketing.ErrNoService
	}
	window := 24 * time.Hour
	if s.Frequency == "weekly" {
		window = 7 * 24 * time.Hour
	}
	stories, err := TopStories(ctx, a.SQLDB, wsID, DigestClock().Add(-window), max(s.Top, 3))
	if err != nil {
		return res, err
	}
	res.Topics = len(stories)
	if len(stories) == 0 {
		// Nothing new is not worth an email (fadymondy.com skips it too).
		res.Status = "empty"
		record()
		return res, nil
	}
	var slug, name, wsZone string
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT slug, name, timezone FROM workspaces WHERE id = $1`, wsID).Scan(&slug, &name, &wsZone)
	subject, text, htmlBody := DigestEmail(s.Locale, name, slug, s.Frequency, DigestClock().In(location(s.Timezone, wsZone)), stories)
	if err := marketing.Send(ctx, svc, marketing.Message{To: res.To, Subject: subject, Text: text, HTML: htmlBody}); err != nil {
		res.Status, res.Error = "failed", clipText(err.Error(), 300)
		record()
		return res, err
	}
	res.Status = "sent"
	record()
	return res, nil
}

// DigestTick sends every digest that is due. One pass; StartDigest runs it.
func DigestTick(ctx context.Context, a *app.App) {
	// Who has a digest on: the owner pool, like every collector's queue.
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT d.workspace_id, d.user_id, d.frequency, d.hour, d.weekday, d.timezone, d.locale, d.top, d.last_period, w.timezone
		FROM member_trend_digest d JOIN workspaces w ON w.id = d.workspace_id WHERE d.frequency <> 'off'`)
	if err != nil {
		a.Log.Warn("trends digest: could not list members", "err", err)
		return
	}
	type due struct {
		ws, user, last, wsZone string
		s                      DigestSettings
	}
	var list []due
	for rows.Next() {
		var d due
		if rows.Scan(&d.ws, &d.user, &d.s.Frequency, &d.s.Hour, &d.s.Weekday, &d.s.Timezone, &d.s.Locale, &d.s.Top, &d.last, &d.wsZone) == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	now := DigestClock()
	for _, d := range list {
		key, ok := period(d.s, now.In(location(d.s.Timezone, d.wsZone)))
		if !ok || key == d.last {
			continue
		}
		sctx := tenant.WithWorkspace(ctx, d.ws)
		// Claim the period first: whatever happens below, it goes once.
		res, err := a.SQLDB.ExecContext(sctx, `UPDATE member_trend_digest SET last_period = $3
			WHERE workspace_id = $1 AND user_id = $2 AND last_period <> $3`, d.ws, d.user, key)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		if _, err := SendDigest(sctx, a, d.ws, d.user, d.s, "digest", key); err != nil {
			a.Log.Warn("trends digest: not sent", "workspace", d.ws, "err", err)
		}
	}
}

// StartDigest runs DigestTick every five minutes. DIGEST=off (or
// SCHEDULER=off, which the tests and the OpenAPI export set) turns it off.
func StartDigest(a *app.App) {
	if os.Getenv("DIGEST") == "off" || os.Getenv("SCHEDULER") == "off" || a == nil || a.SysDB == nil {
		return
	}
	go func() {
		t := time.NewTicker(5 * time.Minute)
		defer t.Stop()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			DigestTick(ctx, a)
			cancel()
			<-t.C
		}
	}()
}
