package conversions

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// ---- the Measurement Protocol ------------------------------------------------------

// HTTPClient is what events are POSTed with. A bounded timeout: it is Google's server.
var HTTPClient = &http.Client{Timeout: 10 * time.Second}

// payload is one Measurement Protocol request.
type payload struct {
	ClientID        string            `json:"client_id"`
	TimestampMicros int64             `json:"timestamp_micros,omitempty"`
	Consent         map[string]string `json:"consent,omitempty"`
	Events          []mpEvent         `json:"events"`
}

type mpEvent struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
}

// GA4ValidationMessage is one finding of GA4's validation server.
type GA4ValidationMessage struct {
	FieldPath      string `json:"fieldPath"`
	Description    string `json:"description"`
	ValidationCode string `json:"validationCode"`
}

// sendError is a failed send. Retry says whether trying again later may work.
type sendError struct {
	msg   string
	retry bool
}

func (e *sendError) Error() string { return e.msg }

// post sends one payload. debug=true asks the validation server instead, which
// records nothing and answers what is wrong with the payload.
//
// The API secret is in the query string, where Google wants it, so no error
// from here may quote the URL: a transport error is reduced to its cause.
func post(ctx context.Context, mid, apiSecret string, p payload, debug bool) ([]GA4ValidationMessage, error) {
	path := "/mp/collect"
	if debug {
		path = "/debug/mp/collect"
	}
	u := google.API().MeasurementProtocol + path + "?measurement_id=" + url.QueryEscape(mid) + "&api_secret=" + url.QueryEscape(apiSecret)
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, &sendError{msg: "could not build the request to Google Analytics"}
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := HTTPClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL carries the secret; the cause does not
		}
		return nil, &sendError{msg: "could not reach Google Analytics: " + scrub(err.Error(), apiSecret), retry: true}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	switch {
	case res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500:
		return nil, &sendError{msg: fmt.Sprintf("Google Analytics answered HTTP %d; will try again", res.StatusCode), retry: true}
	case res.StatusCode >= 300:
		return nil, &sendError{msg: fmt.Sprintf("Google Analytics refused the event (HTTP %d)", res.StatusCode)}
	}
	if !debug {
		return nil, nil
	}
	var v struct {
		ValidationMessages []GA4ValidationMessage `json:"validationMessages"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.ValidationMessages, nil
}

// scrub removes the secret from a message, should anything ever quote it.
func scrub(msg, apiSecret string) string {
	if apiSecret == "" {
		return msg
	}
	return strings.ReplaceAll(strings.ReplaceAll(msg, apiSecret, "[redacted]"), url.QueryEscape(apiSecret), "[redacted]")
}

// consentOf is the Measurement Protocol's consent block: what the visitor's
// page said, and DENIED for anything it did not say.
func consentOf(adUserData, adPersonalization string) map[string]string {
	up := func(v string) string {
		if ConsentValue(v) == "granted" {
			return "GRANTED"
		}
		return "DENIED"
	}
	return map[string]string{"ad_user_data": up(adUserData), "ad_personalization": up(adPersonalization)}
}

// ---- the outbox ----------------------------------------------------------------------

// ConversionEvent is one outbox row as the settings page lists it.
type ConversionEvent struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	SubjectKind string     `json:"subject_kind" enum:"lead,deal"`
	SubjectID   string     `json:"subject_id"`
	Status      string     `json:"status" enum:"pending,sent,failed,skipped"`
	Attempts    int        `json:"attempts"`
	Error       string     `json:"error"`
	Attributed  *bool      `json:"attributed" doc:"true: sent with the visitor's GA client id; false: under a pseudonymous one; null: not sent yet."`
	Debug       bool       `json:"debug"`
	OccurredAt  time.Time  `json:"occurred_at"`
	SentAt      *time.Time `json:"sent_at"`
}

// Recent lists the workspace's latest events, newest first (app pool).
func Recent(ctx context.Context, db *sql.DB, workspaceID string, limit int) ([]ConversionEvent, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, event_name, subject_kind, subject_id, status, attempts, error, attributed, debug, occurred_at, sent_at
		FROM ga4_conversion_events WHERE workspace_id = $1 ORDER BY occurred_at DESC, id LIMIT $2`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConversionEvent{}
	for rows.Next() {
		var e ConversionEvent
		if err := rows.Scan(&e.ID, &e.Name, &e.SubjectKind, &e.SubjectID, &e.Status, &e.Attempts, &e.Error,
			&e.Attributed, &e.Debug, &e.OccurredAt, &e.SentAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MaxAttempts is how often a send that failed for a passing reason is tried.
const MaxAttempts = 5

// lease is how long a claimed row is left alone before another tick may take
// it again (a process that died mid-send).
const lease = 2 * time.Minute

type due struct {
	id, ws, kind, subject, name string
	attempts                    int
	debug                       bool
	occurred                    time.Time
}

// Drain sends up to limit due events, across workspaces, and reports how many
// it settled (sent, failed or skipped). It runs on the owner pool: the
// outbox is read across workspaces, and each event's own row is then read with
// its workspace set.
func Drain(ctx context.Context, a *app.App, limit int) int {
	if a == nil || a.SysDB == nil {
		return 0
	}
	rows, err := a.SysDB.QueryContext(ctx, `
		UPDATE ga4_conversion_events e SET next_attempt_at = now() + make_interval(secs => $2), attempts = attempts + 1
		WHERE e.id IN (
			SELECT id FROM ga4_conversion_events
			WHERE status = 'pending' AND next_attempt_at <= now()
			ORDER BY next_attempt_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		RETURNING e.id, e.workspace_id, e.subject_kind, e.subject_id, e.event_name, e.attempts, e.debug, e.occurred_at`,
		limit, lease.Seconds())
	if err != nil {
		if a.Log != nil && !errors.Is(err, context.Canceled) {
			a.Log.Warn("ga4 conversions: could not read the outbox", "err", err)
		}
		return 0
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.ws, &d.kind, &d.subject, &d.name, &d.attempts, &d.debug, &d.occurred); err == nil {
			list = append(list, d)
		}
	}
	rows.Close()

	settled := 0
	for _, d := range list {
		if settle(ctx, a, d) {
			settled++
		}
	}
	return settled
}

// settle sends one event and records the outcome. It reports whether the
// event is finished with (anything but "try again later").
func settle(ctx context.Context, a *app.App, d due) bool {
	wctx := tenant.WithWorkspace(ctx, d.ws)
	mark := func(status, msg string, attributed *bool) {
		_, _ = a.SysDB.ExecContext(ctx, `
			UPDATE ga4_conversion_events SET status = $2, error = $3, attributed = COALESCE($4, attributed),
				sent_at = CASE WHEN $2 = 'sent' THEN now() ELSE sent_at END
			WHERE id = $1`, d.id, status, msg, attributed)
	}

	s, err := load(wctx, a.SQLDB, d.ws)
	if err != nil {
		return false // the database hiccuped; the lease brings it back
	}
	if !s.EventsEnabled || s.MeasurementID == "" || s.sealed == "" {
		mark("skipped", "sending was switched off before this event went out", nil)
		return true
	}
	apiSecret, err := box.Open(s.sealed)
	if err != nil {
		mark("failed", err.Error(), nil)
		return true
	}

	p, attributed, err := build(wctx, a.SQLDB, d)
	if errors.Is(err, sql.ErrNoRows) {
		mark("skipped", "the "+d.kind+" was deleted before this event went out", nil)
		return true
	}
	if err != nil {
		return false
	}
	if _, err := post(ctx, s.MeasurementID, apiSecret, p, false); err != nil {
		var se *sendError
		msg := scrub(err.Error(), apiSecret)
		if errors.As(err, &se) && se.retry && d.attempts < MaxAttempts {
			// Back off: 1, 2, 4, 8 minutes.
			wait := time.Duration(math.Pow(2, float64(d.attempts-1))) * time.Minute
			_, _ = a.SysDB.ExecContext(ctx, `UPDATE ga4_conversion_events SET error = $2, next_attempt_at = now() + make_interval(secs => $3) WHERE id = $1`,
				d.id, msg, wait.Seconds())
			return false
		}
		mark("failed", msg, &attributed)
		if a.Log != nil {
			a.Log.Warn("ga4 conversion not sent", "workspace", d.ws, "event", d.name, "err", msg)
		}
		return true
	}
	mark("sent", "", &attributed)
	return true
}

// build reads the lead or deal behind an event and makes its payload. It sends
// nothing personal: the client id, consent, the event name and, for a deal,
// its value and currency.
func build(ctx context.Context, db *sql.DB, d due) (payload, bool, error) {
	var clientID, adUserData, adPersonalization, source, leadID, currency string
	var valueMinor sql.NullInt64
	params := map[string]any{"engagement_time_msec": 1}
	switch d.kind {
	case "lead":
		var dealID sql.NullString
		if err := db.QueryRowContext(ctx, `
			SELECT id, ga_client_id, ga_ad_user_data, ga_ad_personalization, utm_source, deal_id FROM leads WHERE workspace_id = $1 AND id = $2`,
			d.ws, d.subject).Scan(&leadID, &clientID, &adUserData, &adPersonalization, &source, &dealID); err != nil {
			return payload{}, false, err
		}
		if d.name == EventConvertLead && dealID.Valid {
			_ = db.QueryRowContext(ctx, `SELECT value_minor, currency FROM deals WHERE workspace_id = $1 AND id = $2`,
				d.ws, dealID.String).Scan(&valueMinor, &currency)
		}
	case "deal":
		if err := db.QueryRowContext(ctx, `SELECT value_minor, currency FROM deals WHERE workspace_id = $1 AND id = $2`,
			d.ws, d.subject).Scan(&valueMinor, &currency); err != nil {
			return payload{}, false, err
		}
		// A deal a lead converted into carries the lead's visitor: the win joins their journey.
		_ = db.QueryRowContext(ctx, `
			SELECT id, ga_client_id, ga_ad_user_data, ga_ad_personalization, utm_source FROM leads
			WHERE workspace_id = $1 AND deal_id = $2 ORDER BY converted_at DESC NULLS LAST LIMIT 1`,
			d.ws, d.subject).Scan(&leadID, &clientID, &adUserData, &adPersonalization, &source)
	}
	if d.kind == "lead" || leadID != "" {
		params["lead_source"] = firstNonEmpty(strings.TrimSpace(source), "website")
	}
	if valueMinor.Valid && valueMinor.Int64 > 0 && len(currency) == 3 {
		params["value"] = float64(valueMinor.Int64) / math.Pow10(minorDigits(currency))
		params["currency"] = currency
	}
	if d.debug {
		params["debug_mode"] = true
	}

	attributed := ValidClientID(clientID)
	if !attributed {
		subject := d.kind + ":" + d.subject
		if leadID != "" {
			subject = "lead:" + leadID // one visitor across the lead's and its deal's events
		}
		clientID = pseudonymousClientID(subject)
	}
	p := payload{
		ClientID: clientID,
		Consent:  consentOf(adUserData, adPersonalization),
		Events:   []mpEvent{{Name: d.name, Params: params}},
	}
	// Google takes an event up to 72 hours late with its own time; later than
	// that, it is sent as now rather than refused.
	// The database's clock may run a little ahead of this process's: never a time in the future.
	at := d.occurred
	if now := time.Now(); at.After(now) {
		at = now
	}
	if time.Since(at) < 71*time.Hour {
		p.TimestampMicros = at.UnixMicro()
	}
	return p, attributed, nil
}

// ConversionTestResult is what "Send a test event" found.
type ConversionTestResult struct {
	Valid    bool                   `json:"valid" doc:"GA4's validation server accepted the payload."`
	Messages []GA4ValidationMessage `json:"messages" doc:"What the validation server objected to."`
	Sent     bool                   `json:"sent" doc:"The test event was sent to the property, marked for DebugView."`
	Error    string                 `json:"error,omitempty"`
}

// SendTest validates a test event with GA4's validation server and, when it
// passes, sends it marked for DebugView, so the owner can watch it arrive.
func SendTest(ctx context.Context, db *sql.DB, workspaceID string) (ConversionTestResult, error) {
	s, err := load(ctx, db, workspaceID)
	if err != nil {
		return ConversionTestResult{}, err
	}
	if s.MeasurementID == "" || s.sealed == "" {
		return ConversionTestResult{}, ErrNeedsBoth
	}
	apiSecret, err := box.Open(s.sealed)
	if err != nil {
		return ConversionTestResult{Error: err.Error()}, nil
	}
	p := payload{
		ClientID: pseudonymousClientID("test:" + workspaceID),
		Consent:  consentOf("", ""),
		Events:   []mpEvent{{Name: EventTest, Params: map[string]any{"engagement_time_msec": 1, "debug_mode": true}}},
	}
	msgs, err := post(ctx, s.MeasurementID, apiSecret, p, true)
	if err != nil {
		return ConversionTestResult{Messages: []GA4ValidationMessage{}, Error: scrub(err.Error(), apiSecret)}, nil
	}
	if msgs == nil {
		msgs = []GA4ValidationMessage{}
	}
	out := ConversionTestResult{Valid: len(msgs) == 0, Messages: msgs}
	if !out.Valid {
		return out, nil
	}
	if _, err := post(ctx, s.MeasurementID, apiSecret, p, false); err != nil {
		out.Error = scrub(err.Error(), apiSecret)
		return out, nil
	}
	out.Sent = true
	return out, nil
}

// StartSender drains the outbox every interval until the process stops.
// GA4_SENDER=off turns it off (tests drive Drain themselves).
func StartSender(a *app.App, interval time.Duration) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if os.Getenv("GA4_SENDER") == "off" {
		return cancel
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				for Drain(ctx, a, 50) == 50 {
				}
			}
		}
	}()
	return cancel
}

// minorDigits is the number of minor-unit digits of a currency (ISO 4217),
// the same table internal/leads uses for budgets.
func minorDigits(currency string) int {
	switch strings.ToUpper(currency) {
	case "BHD", "IQD", "JOD", "KWD", "LYD", "OMR", "TND":
		return 3
	case "JPY", "KRW", "CLP", "ISK", "VND", "UGX", "XAF", "XOF":
		return 0
	}
	return 2
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
