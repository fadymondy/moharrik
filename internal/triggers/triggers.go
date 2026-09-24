/*
Package triggers is where Moharrik says that something happened, for the
automation rules (MH-427) to react to.

Most events are raised by database triggers where their rows are written
(internal/db/post/126_automation_rule_triggers.sql): a deal is opened from four
places, and a trigger hears all of them. This package is for the rest — events
with no row of their own to hang a trigger on (a form submission is an upsert
of rows that other things write too) and the schedule trigger.

It is a leaf on purpose. The services that raise events cannot import the
rules engine (it imports all of them to carry out its actions), so they import
this instead: one function, Fire, that writes the event to the rules' outbox
(automation_rule_events) and nudges the dispatcher.

Fire is cheap when nothing listens, which is almost always: it asks whether any
enabled rule in the workspace listens to that event type and writes nothing
when none does. It never fails the caller — the thing that happened already
happened, and a rule not hearing of it is logged, not raised.

# The loop guard's half

A rule's actions run with the rule on the context (WithRule). Anything they
cause — a deal the rule created raises deal.created — is written with the chain
of rules that led to it, so the dispatcher can refuse to run a rule on an event
its own actions caused, and stop any chain at MaxDepth. Without this, "when a
deal is created, create a deal" runs until something breaks.
*/
package triggers

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/tenant"
)

// Event types. The catalog of what each carries is in internal/rules.
const (
	LeadCreated             = "lead.created"
	FormSubmitted           = "form.submitted"
	ConversationCreated     = "conversation.created"
	MessageReceived         = "message.received"
	DealCreated             = "deal.created"
	DealStageChanged        = "deal.stage_changed"
	DealWon                 = "deal.won"
	DealLost                = "deal.lost"
	ContactLifecycleChanged = "contact.lifecycle_changed"
	ContactOptedIn          = "contact.opted_in"
	CampaignSent            = "campaign.sent"
	AutomationRunFailed     = "automation.run_failed"
	NotificationReceived    = "notification.received"
	TrendTopic              = "trend.topic"
	Schedule                = "schedule"
)

// MaxDepth is how many rules deep a chain may go: a rule's action may cause an
// event another rule reacts to, and so on, this many times.
const MaxDepth = 3

// Event is one thing that happened.
type Event struct {
	Type string
	// SubjectID is the record it is about: a lead, a deal, a conversation.
	SubjectID string
	// Data is flat: field name to a string, number or bool. It is what the
	// conditions compare and the templates fill in.
	Data map[string]any
}

// WithRule marks ctx as running rule id's actions: every event they cause
// carries the chain, whether it is raised here (Fire) or by a database trigger
// (which reads it from the app.audit setting, internal/tenant/audit.go).
func WithRule(ctx context.Context, chain []string, ruleID string) context.Context {
	return tenant.WithRuleChain(ctx, append(append([]string(nil), chain...), ruleID))
}

// Chain is the rules whose actions are running on ctx, oldest first.
func Chain(ctx context.Context) []string { return tenant.RuleChain(ctx) }

// wake nudges the dispatcher; buffered so Fire never blocks on it.
var wake = make(chan struct{}, 1)

// Wake is what the dispatcher waits on beside its ticker.
func Wake() <-chan struct{} { return wake }

// Log is where a failed write is reported. Set by the server at boot.
var Log = slog.Default()

// Fire records ev for workspaceID's rules, if any enabled rule listens to its
// type. db is the APP pool; the write runs under RLS in that workspace.
func Fire(ctx context.Context, db *sql.DB, workspaceID string, ev Event) {
	if db == nil || workspaceID == "" || ev.Type == "" {
		return
	}
	// Detached from the request's cancellation: the response may already be on
	// its way, and the event still happened.
	sctx, cancel := context.WithTimeout(tenant.WithWorkspace(context.WithoutCancel(ctx), workspaceID), 5*time.Second)
	defer cancel()
	var listening bool
	if err := db.QueryRowContext(sctx, `
		SELECT EXISTS (SELECT 1 FROM automation_rules WHERE workspace_id = $1 AND enabled AND trigger = $2)`,
		workspaceID, ev.Type).Scan(&listening); err != nil {
		Log.Warn("rules: could not check for listeners", "workspace", workspaceID, "event", ev.Type, "err", err)
		return
	}
	if !listening {
		return
	}
	if err := insert(sctx, db, workspaceID, ev, Chain(ctx), ""); err != nil {
		Log.Warn("rules: could not record an event", "workspace", workspaceID, "event", ev.Type, "err", err)
		return
	}
	Nudge()
}

// FireFor records a schedule event for one rule, whatever else listens.
func FireFor(ctx context.Context, db *sql.DB, workspaceID, ruleID string, ev Event) error {
	if err := insert(tenant.WithWorkspace(ctx, workspaceID), db, workspaceID, ev, nil, ruleID); err != nil {
		return err
	}
	Nudge()
	return nil
}

// Nudge wakes the dispatcher now rather than at its next tick.
func Nudge() {
	select {
	case wake <- struct{}{}:
	default:
	}
}

func insert(ctx context.Context, db *sql.DB, workspaceID string, ev Event, chain []string, ruleID string) error {
	data := ev.Data
	if data == nil {
		data = map[string]any{}
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var rule any
	if ruleID != "" {
		rule = ruleID
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO automation_rule_events (workspace_id, type, subject_id, data, chain, rule_id)
		VALUES ($1, $2, $3, $4::jsonb, string_to_array($5, ','), $6)`,
		workspaceID, ev.Type, ev.SubjectID, string(b), strings.Join(chain, ","), rule)
	return err
}

// Clip shortens free text an event carries (a message body, a note) so one
// essay does not become the whole event row.
func Clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
