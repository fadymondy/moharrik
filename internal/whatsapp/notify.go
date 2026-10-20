package whatsapp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pglock"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
WhatsApp notifications for members (MH-411).

Ported from fadymondy.com's internal/whatsapp (feed.go, batch.go, store.go) and
made per member: there the owner is the only recipient; here every member may
opt in with their own number, verified by a code the workspace's connection
sends, and chooses what reaches them.

	SOURCES   rows other packages already write, read on a ticker since a
	          watermark per workspace (the first scan sets it to now, so
	          switching this on never sends the backlog):
	            automation_failed  automation_runs that failed    automation:read
	            lead               a new lead (MH-390)             leads:read
	            notification       any other notification item     notifications:read
	FAN-OUT   an event is queued for each verified, enabled member whose ROLE
	          may read it and who chose that kind. Never wider than the page.
	FLUSH     per member: nothing in quiet hours (it waits until they end),
	          nothing past the daily cap (it waits for the next day), and the
	          events are gathered for batch_minutes after the first one, so a
	          burst becomes ONE message. A failed send keeps them pending.

Every message goes through SendAs, so it is paced in the workspace's lane with
replies and broadcasts, and logged in whatsapp_sends.

Not ported: fadymondy.com's inbound /fm command language and its pause switch.
They are the owner's personal control channel; the card leaves them out.
*/

// Event kinds a member can choose.
const (
	NotifyAutomationFailed = "automation_failed"
	NotifyLead             = "lead"
	NotifyNotification     = "notification"

	KindNotify = "notify"
	KindVerify = "verify"
)

// NotifyKinds are the kinds in the order the settings page lists them, with
// the permission a member must hold to receive each.
var NotifyKinds = []struct{ Kind, Permission string }{
	{NotifyAutomationFailed, authz.AutomationRead},
	{NotifyLead, authz.LeadsRead},
	{NotifyNotification, authz.NotificationsRead},
}

func kindPermission(kind string) string {
	for _, k := range NotifyKinds {
		if k.Kind == kind {
			return k.Permission
		}
	}
	return ""
}

// Clock is what "now" is. The tests move it.
var Clock = time.Now

// Verification limits.
const (
	codeTTL      = 10 * time.Minute
	codeInterval = time.Minute
	codeTries    = 5
	maxLines     = 8
)

var (
	ErrNotifyNotStarted = errors.New("ask for a code first")
	ErrNotifyBadCode    = errors.New("that code is not right, or it has expired; ask for a new one")
	ErrNotifyTooSoon    = errors.New("a code was sent less than a minute ago; wait for it, or try again in a minute")
	ErrNotifyNoNumber   = errors.New("the number must be 8 to 15 digits with its country code, e.g. 201012345678")
	ErrNotifyNotLinked  = errors.New("this workspace has no connected WhatsApp number to send from")
	ErrNotifyUnverified = errors.New("verify your number first")
)

type memberRow struct {
	Phone         string
	VerifiedAt    *time.Time
	CodeHash      string
	CodeExpires   *time.Time
	CodeAttempts  int
	CodeSentAt    *time.Time
	Enabled       bool
	Kinds         pgtext.Array
	QuietStart    int
	QuietEnd      int
	Timezone      string
	DailyCap      int
	BatchMinutes  int
	Locale        string
	LastAttemptAt *time.Time
}

func loadMember(ctx context.Context, db *sql.DB, wsID, userID string) (*memberRow, error) {
	var m memberRow
	err := db.QueryRowContext(ctx, `
		SELECT phone, verified_at, code_hash, code_expires_at, code_attempts, code_sent_at, enabled, kinds,
		       quiet_start, quiet_end, timezone, daily_cap, batch_minutes, locale, last_attempt_at
		FROM member_whatsapp WHERE workspace_id = $1 AND user_id = $2`, wsID, userID,
	).Scan(&m.Phone, &m.VerifiedAt, &m.CodeHash, &m.CodeExpires, &m.CodeAttempts, &m.CodeSentAt, &m.Enabled, &m.Kinds,
		&m.QuietStart, &m.QuietEnd, &m.Timezone, &m.DailyCap, &m.BatchMinutes, &m.Locale, &m.LastAttemptAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &m, err
}

func notifyCodeHash(wsID, userID, code string) string {
	sum := sha256.Sum256([]byte("member-whatsapp:" + wsID + ":" + userID + ":" + code))
	return hex.EncodeToString(sum[:])
}

// sendable picks the number a member message goes from and reports whether
// one can send: the default number, unless it is the member's own number (a
// message to yourself never notifies), in which case another linked number
// sends it. With no other number the default still does, as it always has.
func sendable(ctx context.Context, a *app.App, wsID, to string) (context.Context, bool) {
	if !LoadConfig().Configured() {
		return ctx, false
	}
	links, err := List(ctx, a, wsID)
	if err != nil {
		return ctx, false
	}
	var pick *Link
	for _, l := range links {
		if l.State != "open" {
			continue
		}
		if pick == nil {
			pick = l
		}
		if l.Phone == "" || !SamePhone(l.Phone, Number(to)) {
			pick = l
			break
		}
	}
	if pick == nil {
		return ctx, false
	}
	return WithLink(ctx, pick.ID), true
}

// StartVerify sends a one-time code to the member's number from the
// workspace's connection. The number is the member's own; typing it and then
// the code back is their consent to be messaged on it.
func StartVerify(ctx context.Context, a *app.App, wsID, userID, phone, locale string) error {
	digits := Number(phone)
	if len(digits) < 8 || len(digits) > 15 {
		return ErrNotifyNoNumber
	}
	ctx, ok := sendable(ctx, a, wsID, digits)
	if !ok {
		return ErrNotifyNotLinked
	}
	if locale != "ar" {
		locale = "en"
	}
	now := Clock()
	cur, err := loadMember(ctx, a.SQLDB, wsID, userID)
	if err != nil {
		return err
	}
	if cur != nil && cur.CodeSentAt != nil && now.Sub(*cur.CodeSentAt) < codeInterval {
		return ErrNotifyTooSoon
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return err
	}
	code := fmt.Sprintf("%06d", n.Int64())
	// A new number (or a re-verification) is unverified until the code comes
	// back: the old number stops being messaged the moment another is typed.
	if _, err := a.SQLDB.ExecContext(ctx, `
		INSERT INTO member_whatsapp (workspace_id, user_id, phone, code_hash, code_expires_at, code_attempts, code_sent_at, locale)
		VALUES ($1, $2, $3, $4, $5, 0, $6, $7)
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET phone = EXCLUDED.phone,
		  verified_at = CASE WHEN member_whatsapp.phone = EXCLUDED.phone THEN member_whatsapp.verified_at END,
		  code_hash = EXCLUDED.code_hash, code_expires_at = EXCLUDED.code_expires_at, code_attempts = 0,
		  code_sent_at = EXCLUDED.code_sent_at, locale = EXCLUDED.locale, updated_at = now()`,
		wsID, userID, digits, notifyCodeHash(wsID, userID, code), now.Add(codeTTL), now, locale); err != nil {
		return err
	}
	var wsName string
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT name FROM workspaces WHERE id = $1`, wsID).Scan(&wsName)
	text := fmt.Sprintf("Your Moharrik code to get %s notifications on WhatsApp: %s\nIt expires in 10 minutes. If you did not ask for it, ignore this message.", wsName, code)
	if locale == "ar" {
		text = fmt.Sprintf("رمزك في محرّك لتلقي إشعارات %s على واتساب: %s\nينتهي خلال 10 دقائق. إن لم تطلبه فتجاهل هذه الرسالة.", wsName, code)
	}
	if _, err := SendAs(ctx, a, wsID, digits, text, KindVerify); err != nil {
		// Nothing arrived, so no code is outstanding: they may ask again now.
		_, _ = a.SQLDB.ExecContext(ctx, `UPDATE member_whatsapp SET code_hash = '', code_expires_at = NULL, code_sent_at = NULL
			WHERE workspace_id = $1 AND user_id = $2`, wsID, userID)
		return err
	}
	return nil
}

// ConfirmVerify checks the code the member typed.
func ConfirmVerify(ctx context.Context, a *app.App, wsID, userID, code string) error {
	m, err := loadMember(ctx, a.SQLDB, wsID, userID)
	if err != nil {
		return err
	}
	if m == nil || m.CodeHash == "" {
		return ErrNotifyNotStarted
	}
	code = strings.TrimSpace(code)
	if m.CodeExpires == nil || Clock().After(*m.CodeExpires) || m.CodeAttempts >= codeTries {
		return ErrNotifyBadCode
	}
	if subtle.ConstantTimeCompare([]byte(notifyCodeHash(wsID, userID, code)), []byte(m.CodeHash)) != 1 {
		_, _ = a.SQLDB.ExecContext(ctx, `UPDATE member_whatsapp SET code_attempts = code_attempts + 1 WHERE workspace_id = $1 AND user_id = $2`, wsID, userID)
		return ErrNotifyBadCode
	}
	_, err = a.SQLDB.ExecContext(ctx, `UPDATE member_whatsapp SET verified_at = $3, code_hash = '', code_expires_at = NULL,
		code_attempts = 0, enabled = true, updated_at = now() WHERE workspace_id = $1 AND user_id = $2`, wsID, userID, Clock())
	return err
}

// ---- the feed ----------------------------------------------------------------

type feedEvent struct {
	Kind, DedupeKey string
	// Title/Body per locale; the member's language picks one.
	Title, Body map[string]string
	Link        func(locale string) string
}

// NotifyTick collects and flushes every workspace with at least one member
// signed up. One pass; StartNotifier runs it on a ticker.
func NotifyTick(ctx context.Context, a *app.App) {
	// Which workspaces: the one owner-pool read, like every collector's.
	rows, err := a.SysDB.QueryContext(ctx, `SELECT DISTINCT workspace_id FROM member_whatsapp WHERE verified_at IS NOT NULL AND enabled`)
	if err != nil {
		a.Log.Warn("whatsapp notify: could not list workspaces", "err", err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, ws := range ids {
		sctx := tenant.WithWorkspace(ctx, ws)
		if err := Collect(sctx, a, ws); err != nil {
			a.Log.Warn("whatsapp notify: collect failed", "workspace", ws, "err", err)
		}
		if _, err := FlushWorkspace(sctx, a, ws); err != nil {
			a.Log.Warn("whatsapp notify: flush failed", "workspace", ws, "err", err)
		}
	}
}

// StartNotifier runs NotifyTick every tick for the life of the process.
// WHATSAPP_NOTIFY=off turns it off.
//
// getMark/setMark race across replicas (read-then-conditional-write, not an
// atomic claim), so two replicas ticking at once could both read the same old
// watermark and queue the same message twice. The cluster-wide lock
// (internal/pglock) makes the tick run on exactly one replica at a time.
func StartNotifier(a *app.App, tick time.Duration) {
	if os.Getenv("WHATSAPP_NOTIFY") == "off" {
		return
	}
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for range t.C {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			_, err := pglock.WithLock(ctx, a.SysDB, "moharrik:whatsapp:notify", func() {
				NotifyTick(ctx, a)
			})
			if err != nil {
				a.Log.Error("whatsapp notify: could not acquire the cluster lock", "err", err)
			}
			cancel()
		}
	}()
}

// getMark reads a source's watermark. The first read sets it to the
// database's now (the sources' timestamps are the database's too), so turning
// notifications on never sends what happened before.
func getMark(ctx context.Context, db *sql.DB, wsID, source string) (time.Time, bool, error) {
	var mark time.Time
	err := db.QueryRowContext(ctx, `SELECT mark FROM member_whatsapp_marks WHERE workspace_id = $1 AND source = $2`, wsID, source).Scan(&mark)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = db.ExecContext(ctx, `INSERT INTO member_whatsapp_marks (workspace_id, source, mark) VALUES ($1, $2, now())
			ON CONFLICT DO NOTHING`, wsID, source)
		return mark, false, err
	}
	return mark, true, err
}

func setMark(ctx context.Context, db *sql.DB, wsID, source string, mark time.Time) error {
	_, err := db.ExecContext(ctx, `UPDATE member_whatsapp_marks SET mark = $3 WHERE workspace_id = $1 AND source = $2 AND mark < $3`, wsID, source, mark)
	return err
}

func workspaceSlug(ctx context.Context, db *sql.DB, wsID string) (slug, name string) {
	_ = db.QueryRowContext(ctx, `SELECT slug, name FROM workspaces WHERE id = $1`, wsID).Scan(&slug, &name)
	return
}

// Collect reads new source rows for one workspace and queues them for the
// members they are for.
func Collect(ctx context.Context, a *app.App, wsID string) error {
	db := a.SQLDB
	slug, _ := workspaceSlug(ctx, db, wsID)
	path := func(locale, rest string) string {
		return "/" + locale + "/w/" + url.PathEscape(slug) + rest
	}
	var evs []feedEvent

	// Failed automation runs.
	if mark, ok, err := getMark(ctx, db, wsID, "automation_runs"); err != nil {
		return err
	} else if ok {
		rows, err := db.QueryContext(ctx, `
			SELECT r.id, r.error, r.finished_at, coalesce(b.name, ''), coalesce(w.name, '')
			FROM automation_runs r
			LEFT JOIN browser_agents b ON b.workspace_id = r.workspace_id AND b.id = r.agent_id
			LEFT JOIN automation_workflows w ON w.workspace_id = r.workspace_id AND w.id = r.workflow_id
			WHERE r.workspace_id = $1 AND r.status = 'failed' AND r.finished_at > $2
			ORDER BY r.finished_at LIMIT 200`, wsID, mark)
		if err != nil {
			return err
		}
		last := mark
		for rows.Next() {
			var id, failure, agent, workflow string
			var finished time.Time
			if err := rows.Scan(&id, &failure, &finished, &agent, &workflow); err != nil {
				rows.Close()
				return err
			}
			last = finished
			what := firstNonEmpty(workflow, agent)
			runID := id
			evs = append(evs, feedEvent{
				Kind: NotifyAutomationFailed, DedupeKey: "run:" + id,
				Title: map[string]string{"en": "Automation failed: " + firstNonEmpty(what, "a browser run"), "ar": "فشل تشغيل أتمتة: " + firstNonEmpty(what, "تشغيل متصفح")},
				Body:  map[string]string{"en": failure, "ar": failure},
				Link:  func(l string) string { return path(l, "/automation/runs/"+url.PathEscape(runID)) },
			})
		}
		rows.Close()
		if err := setMark(ctx, db, wsID, "automation_runs", last); err != nil {
			return err
		}
	}

	// Notification items: new leads (MH-390) and everything else.
	if mark, ok, err := getMark(ctx, db, wsID, "notification_items"); err != nil {
		return err
	} else if ok {
		rows, err := db.QueryContext(ctx, `
			SELECT id, kind, title, body, url, source_name, created_at FROM notification_items
			WHERE workspace_id = $1 AND created_at > $2 ORDER BY created_at LIMIT 200`, wsID, mark)
		if err != nil {
			return err
		}
		last := mark
		for rows.Next() {
			var id, kind, title, body, link, source string
			var created time.Time
			if err := rows.Scan(&id, &kind, &title, &body, &link, &source, &created); err != nil {
				rows.Close()
				return err
			}
			last = created
			itemID, itemLink := id, link
			if kind == "lead" {
				evs = append(evs, feedEvent{
					Kind: NotifyLead, DedupeKey: "item:" + id,
					Title: map[string]string{"en": title, "ar": title}, Body: map[string]string{"en": body, "ar": body},
					Link: func(l string) string {
						// The lead's own page, which the item carries.
						if u, err := url.Parse(itemLink); err == nil && strings.Contains(u.Path, "/leads/") {
							parts := strings.SplitN(u.Path, "/leads/", 2)
							return path(l, "/leads/"+parts[1])
						}
						return path(l, "/leads")
					},
				})
				continue
			}
			label := strings.TrimSpace(source)
			t := title
			if label != "" {
				t = label + ": " + title
			}
			evs = append(evs, feedEvent{
				Kind: NotifyNotification, DedupeKey: "item:" + itemID,
				Title: map[string]string{"en": t, "ar": t}, Body: map[string]string{"en": body, "ar": body},
				Link: func(l string) string { return path(l, "/notifications") },
			})
		}
		rows.Close()
		if err := setMark(ctx, db, wsID, "notification_items", last); err != nil {
			return err
		}
	}
	if len(evs) == 0 {
		return nil
	}
	return queue(ctx, db, wsID, evs)
}

// queue fans the events out to the members they are for.
func queue(ctx context.Context, db *sql.DB, wsID string, evs []feedEvent) error {
	rows, err := db.QueryContext(ctx, `
		SELECT m.user_id, wm.role, m.kinds, m.locale FROM member_whatsapp m
		JOIN workspace_members wm ON wm.workspace_id = m.workspace_id AND wm.user_id = m.user_id AND wm.status = 'active'
		WHERE m.workspace_id = $1 AND m.verified_at IS NOT NULL AND m.enabled`, wsID)
	if err != nil {
		return err
	}
	type member struct {
		user, role, locale string
		kinds              map[string]bool
	}
	var members []member
	for rows.Next() {
		var m member
		var kinds pgtext.Array
		if err := rows.Scan(&m.user, &m.role, &kinds, &m.locale); err != nil {
			rows.Close()
			return err
		}
		m.kinds = map[string]bool{}
		for _, k := range kinds {
			m.kinds[k] = true
		}
		members = append(members, m)
	}
	rows.Close()
	for _, ev := range evs {
		perm := kindPermission(ev.Kind)
		for _, m := range members {
			if !m.kinds[ev.Kind] || perm == "" || !authz.Can(m.role, perm) {
				continue
			}
			if _, err := db.ExecContext(ctx, `
				INSERT INTO member_whatsapp_events (workspace_id, user_id, kind, title, body, link, dedupe_key, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (workspace_id, user_id, dedupe_key) DO NOTHING`,
				wsID, m.user, ev.Kind, clip(pick(ev.Title, m.locale), 200), clip(pick(ev.Body, m.locale), 300),
				ev.Link(m.locale), ev.DedupeKey, Clock()); err != nil {
				return err
			}
		}
	}
	return nil
}

func pick(m map[string]string, locale string) string {
	if v, ok := m[locale]; ok {
		return v
	}
	return m["en"]
}

// ---- the flush ----------------------------------------------------------------

// FlushResult says what one member's flush did and why.
type FlushResult struct {
	UserID string `json:"user_id"`
	Sent   bool   `json:"sent"`
	Events int    `json:"events"`
	// Why nothing went: quiet_hours | daily_cap | batching | not_linked | retry_wait | nothing
	Held string `json:"held,omitempty"`
	Err  string `json:"error,omitempty"`
}

// FlushWorkspace flushes every signed-up member of one workspace.
func FlushWorkspace(ctx context.Context, a *app.App, wsID string) ([]FlushResult, error) {
	rows, err := a.SQLDB.QueryContext(ctx, `SELECT user_id FROM member_whatsapp WHERE workspace_id = $1 AND verified_at IS NOT NULL AND enabled`, wsID)
	if err != nil {
		return nil, err
	}
	var users []string
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil {
			users = append(users, u)
		}
	}
	rows.Close()
	var out []FlushResult
	for _, u := range users {
		r, err := FlushMember(ctx, a, wsID, u)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

// InQuiet reports whether a local time falls in [start, end) minutes after
// midnight. The window may wrap midnight; start == end means no quiet hours.
func InQuiet(local time.Time, start, end int) bool {
	if start == end {
		return false
	}
	m := local.Hour()*60 + local.Minute()
	if start < end {
		return m >= start && m < end
	}
	return m >= start || m < end
}

func memberLocation(ctx context.Context, db *sql.DB, wsID, tz string) *time.Location {
	if tz == "" {
		_ = db.QueryRowContext(ctx, `SELECT timezone FROM workspaces WHERE id = $1`, wsID).Scan(&tz)
	}
	if loc, err := time.LoadLocation(tz); err == nil && tz != "" {
		return loc
	}
	return time.UTC
}

// FlushMember sends one member's pending events as one message, if the rules
// allow one now.
func FlushMember(ctx context.Context, a *app.App, wsID, userID string) (FlushResult, error) {
	res := FlushResult{UserID: userID}
	db := a.SQLDB
	m, err := loadMember(ctx, db, wsID, userID)
	if err != nil || m == nil || m.VerifiedAt == nil || !m.Enabled {
		res.Held = "nothing"
		return res, err
	}
	type pending struct {
		id, kind, title, body, link string
		at                          time.Time
	}
	rows, err := db.QueryContext(ctx, `
		SELECT id, kind, title, body, link, created_at FROM member_whatsapp_events
		WHERE workspace_id = $1 AND user_id = $2 AND sent_at IS NULL ORDER BY created_at, id LIMIT 200`, wsID, userID)
	if err != nil {
		return res, err
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.kind, &p.title, &p.body, &p.link, &p.at); err != nil {
			rows.Close()
			return res, err
		}
		list = append(list, p)
	}
	rows.Close()
	res.Events = len(list)
	if len(list) == 0 {
		res.Held = "nothing"
		return res, nil
	}

	now := Clock()
	loc := memberLocation(ctx, db, wsID, m.Timezone)
	local := now.In(loc)
	window := time.Duration(m.BatchMinutes) * time.Minute
	switch {
	case InQuiet(local, m.QuietStart, m.QuietEnd):
		res.Held = "quiet_hours"
		return res, nil
	case now.Sub(list[0].at) < window:
		// Gathering: the first event waits out the window so the ones behind
		// it travel in the same message.
		res.Held = "batching"
		return res, nil
	case m.LastAttemptAt != nil && now.Sub(*m.LastAttemptAt) < max(window, time.Minute):
		res.Held = "retry_wait"
		return res, nil
	}
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	var today int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM member_whatsapp_messages
		WHERE workspace_id = $1 AND user_id = $2 AND kind = 'batch' AND ok AND created_at >= $3`, wsID, userID, dayStart).Scan(&today); err != nil {
		return res, err
	}
	if today >= m.DailyCap {
		res.Held = "daily_cap"
		return res, nil
	}
	ctx, ok := sendable(ctx, a, wsID, m.Phone)
	if !ok {
		res.Held = "not_linked"
		return res, nil
	}

	slug, wsName := workspaceSlug(ctx, db, wsID)
	base := appBase()
	var b strings.Builder
	if m.Locale == "ar" {
		if len(list) == 1 {
			fmt.Fprintf(&b, "محرّك · %s", wsName)
		} else {
			fmt.Fprintf(&b, "محرّك · %s: %d تحديثات", wsName, len(list))
		}
	} else {
		if len(list) == 1 {
			fmt.Fprintf(&b, "Moharrik · %s", wsName)
		} else {
			fmt.Fprintf(&b, "Moharrik · %s: %d updates", wsName, len(list))
		}
	}
	for i, p := range list {
		if i == maxLines {
			more := "+%d more: %s"
			if m.Locale == "ar" {
				more = "و%d غيرها: %s"
			}
			fmt.Fprintf(&b, "\n"+more, len(list)-maxLines, base+"/"+m.Locale+"/w/"+url.PathEscape(slug)+"/notifications")
			break
		}
		line := "\n• " + clip(p.title, 120)
		if p.body != "" && (p.kind == NotifyAutomationFailed || len(list) <= 3) {
			line += " — " + clip(p.body, 140)
		}
		if p.link != "" {
			line += "\n  " + base + p.link
		}
		b.WriteString(line)
	}

	_, sendErr := SendAs(ctx, a, wsID, m.Phone, b.String(), KindNotify)
	errText := ""
	if sendErr != nil {
		errText = clip(sendErr.Error(), 300)
	}
	var msgID string
	if err := db.QueryRowContext(ctx, `INSERT INTO member_whatsapp_messages (workspace_id, user_id, kind, events, ok, error, created_at)
		VALUES ($1, $2, 'batch', $3, $4, $5, $6) RETURNING id`, wsID, userID, len(list), sendErr == nil, errText, now).Scan(&msgID); err != nil {
		return res, err
	}
	_, _ = db.ExecContext(ctx, `UPDATE member_whatsapp SET last_attempt_at = $3 WHERE workspace_id = $1 AND user_id = $2`, wsID, userID, now)
	if sendErr != nil {
		res.Err = errText
		return res, nil
	}
	ids := make([]string, len(list))
	for i, p := range list {
		ids[i] = p.id
	}
	if _, err := db.ExecContext(ctx, `UPDATE member_whatsapp_events SET sent_at = $3, message_id = $4
		WHERE workspace_id = $1 AND user_id = $2 AND id = ANY(string_to_array($5, ','))`, wsID, userID, now, msgID, strings.Join(ids, ",")); err != nil {
		return res, err
	}
	res.Sent = true
	return res, nil
}

// SendNotifyTest sends one test message to a verified member now: no quiet
// hours (they just asked), not counted against the daily cap.
func SendNotifyTest(ctx context.Context, a *app.App, wsID, userID string) error {
	m, err := loadMember(ctx, a.SQLDB, wsID, userID)
	if err != nil {
		return err
	}
	if m == nil || m.VerifiedAt == nil {
		return ErrNotifyUnverified
	}
	ctx, ok := sendable(ctx, a, wsID, m.Phone)
	if !ok {
		return ErrNotifyNotLinked
	}
	var last *time.Time
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT max(created_at) FROM member_whatsapp_messages WHERE workspace_id = $1 AND user_id = $2 AND kind = 'test'`, wsID, userID).Scan(&last)
	if last != nil && Clock().Sub(*last) < 30*time.Second {
		return ErrNotifyTooSoon
	}
	_, wsName := workspaceSlug(ctx, a.SQLDB, wsID)
	text := "Moharrik · " + wsName + "\nThis is a test. WhatsApp notifications reach you on this number."
	if m.Locale == "ar" {
		text = "محرّك · " + wsName + "\nهذه رسالة تجريبية. تصلك إشعارات واتساب على هذا الرقم."
	}
	_, sendErr := SendAs(ctx, a, wsID, m.Phone, text, KindNotify)
	errText := ""
	if sendErr != nil {
		errText = clip(sendErr.Error(), 300)
	}
	_, _ = a.SQLDB.ExecContext(ctx, `INSERT INTO member_whatsapp_messages (workspace_id, user_id, kind, events, ok, error, created_at)
		VALUES ($1, $2, 'test', 0, $3, $4, $5)`, wsID, userID, sendErr == nil, errText, Clock())
	return sendErr
}

// appBase is where links in a message point: the public web origin.
func appBase() string {
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return ""
}
