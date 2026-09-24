/*
Package rules is Moharrik's automation rules (MH-427): when something happens,
do something.

	WHEN   an event (internal/triggers: raised by database triggers where the
	       rows are written, by Fire for the rest, and by the clock)
	IF     conditions on the event's fields hold (all of them, or any)
	THEN   actions run, in order, through the same services a member's request
	       goes through — so a rule meets exactly the validation, consent rules
	       and limits a person does, and cannot do anything the member it acts
	       as could not.

What keeps it safe to leave running:

  - it acts as the member who last saved it, with their role at the moment it
    runs: demote them and the rule loses what they lost; remove them and it
    stops;
  - every evaluation is logged (automation_rule_runs) with the event it was
    given, each condition's result and each action's outcome — including the
    ones that did not match, so "why did it not fire" has an answer;
  - the loop guard: an event carries the chain of rules that caused it, a rule
    never runs on an event its own actions caused, and no chain goes deeper
    than triggers.MaxDepth;
  - rate limits: each rule's own hourly brake, and a ceiling for the workspace;
  - once per record, for rules that should act on a lead or a story once;
  - a test against a sample event that renders every action and sends nothing;
  - a new rule is off until someone switches it on.

fadymondy.com has no rules engine to port (its kernel hook bus has two
hard-coded listeners); the event names follow its resource.verb style.
*/
package rules

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/secret"
	"github.com/fadymondy/moharrik/internal/triggers"
)

// Limits.
const (
	MaxConditions = 20
	MaxActions    = 10
	// WorkspaceHourlyCap is how many runs that did something a workspace's
	// rules may make in a rolling hour, all rules together.
	WorkspaceHourlyCap = 500
	DefaultHourly      = 30
)

// RuleCondition is one test on an event field.
type RuleCondition struct {
	Field string `json:"field" maxLength:"80"`
	Op    string `json:"op" maxLength:"20"`
	// For in / not_in: comma-separated. Ignored by is_empty / is_not_empty.
	Value string `json:"value,omitempty" maxLength:"500"`
}

// RuleAction is one step of a rule.
type RuleAction struct {
	Type   string            `json:"type" maxLength:"40"`
	Params map[string]string `json:"params,omitempty"`
}

// Rule is a stored rule.
type Rule struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Enabled        bool            `json:"enabled"`
	Trigger        string          `json:"trigger"`
	Match          string          `json:"match" enum:"all,any"`
	Conditions     []RuleCondition `json:"conditions"`
	Actions        []RuleAction    `json:"actions"`
	Cron           string          `json:"cron"`
	Timezone       string          `json:"timezone"`
	NextRunAt      *time.Time      `json:"next_run_at"`
	OncePerSubject bool            `json:"once_per_subject"`
	MaxRunsPerHour int             `json:"max_runs_per_hour"`
	RunCount       int64           `json:"run_count"`
	LastRunAt      *time.Time      `json:"last_run_at"`
	LastStatus     string          `json:"last_status"`
	LastError      string          `json:"last_error"`
	// Whether a signing secret exists for its webhook actions (never the secret).
	HasWebhookSecret bool      `json:"has_webhook_secret"`
	CreatedBy        string    `json:"created_by"`
	UpdatedBy        string    `json:"updated_by" doc:"The member the rule acts as."`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// RuleInput is a rule create or a full replace.
type RuleInput struct {
	Name           string          `json:"name" minLength:"1" maxLength:"80"`
	Description    string          `json:"description,omitempty" maxLength:"500"`
	Enabled        *bool           `json:"enabled,omitempty" doc:"Off unless given. Test a rule before switching it on."`
	Trigger        string          `json:"trigger" maxLength:"40" doc:"An event type from GET /api/w/rules/catalog."`
	Match          string          `json:"match,omitempty" enum:",all,any" doc:"all (default): every condition must hold; any: one is enough."`
	Conditions     []RuleCondition `json:"conditions,omitempty" maxItems:"20"`
	Actions        []RuleAction    `json:"actions" maxItems:"10"`
	Cron           string          `json:"cron,omitempty" maxLength:"100" doc:"The schedule trigger only: five-field cron or @daily etc."`
	Timezone       string          `json:"timezone,omitempty" maxLength:"60"`
	OncePerSubject bool            `json:"once_per_subject,omitempty" doc:"Run at most once for the same record (lead, deal, story…)."`
	MaxRunsPerHour int             `json:"max_runs_per_hour,omitempty" minimum:"0" maximum:"1000" doc:"The rule's own brake; default 30."`
}

// ErrInvalid wraps every validation failure: its message says what to change.
type ErrInvalid struct{ Msg string }

func (e *ErrInvalid) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ErrInvalid{Msg: fmt.Sprintf(format, a...)} }

var fieldName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Clean normalises and validates an input. It checks shape, not references:
// whether a list or workflow still exists is answered when the rule runs (and
// shown by a test), because either can be deleted after the rule is saved.
func Clean(in RuleInput) (RuleInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Trigger = strings.TrimSpace(in.Trigger)
	if in.Name == "" {
		return in, invalid("a rule needs a name")
	}
	def, ok := triggerDef(in.Trigger)
	if !ok {
		return in, invalid("%q is not an event a rule can listen to", in.Trigger)
	}
	if in.Match == "" {
		in.Match = "all"
	}
	if in.Match != "all" && in.Match != "any" {
		return in, invalid("match is all or any")
	}
	if in.MaxRunsPerHour == 0 {
		in.MaxRunsPerHour = DefaultHourly
	}
	if in.MaxRunsPerHour < 1 || in.MaxRunsPerHour > 1000 {
		return in, invalid("max_runs_per_hour is between 1 and 1000")
	}
	if len(in.Conditions) > MaxConditions {
		return in, invalid("a rule has at most %d conditions", MaxConditions)
	}
	known := map[string]bool{}
	for _, f := range def.Fields {
		known[f.Name] = true
	}
	for i, c := range in.Conditions {
		c.Field = strings.TrimSpace(c.Field)
		c.Op = strings.TrimSpace(c.Op)
		if !fieldName.MatchString(c.Field) {
			return in, invalid("condition %d: the field name is not valid", i+1)
		}
		if !known[c.Field] {
			return in, invalid("condition %d: %s does not carry a field %q", i+1, in.Trigger, c.Field)
		}
		if !validOp(c.Op) {
			return in, invalid("condition %d: %q is not an operator", i+1, c.Op)
		}
		if (c.Op == "gt" || c.Op == "gte" || c.Op == "lt" || c.Op == "lte") && !isNumber(c.Value) {
			return in, invalid("condition %d: %s compares with a number", i+1, c.Op)
		}
		in.Conditions[i] = c
	}
	if len(in.Actions) == 0 {
		return in, invalid("a rule needs at least one action")
	}
	if len(in.Actions) > MaxActions {
		return in, invalid("a rule has at most %d actions", MaxActions)
	}
	for i, a := range in.Actions {
		ad, ok := actionDef(a.Type)
		if !ok {
			return in, invalid("action %d: %q is not an action", i+1, a.Type)
		}
		params := map[string]string{}
		allowed := map[string]bool{}
		for _, p := range ad.Params {
			allowed[p.Name] = true
		}
		for k, v := range a.Params {
			if !allowed[k] {
				return in, invalid("action %d (%s) has no setting %q", i+1, a.Type, k)
			}
			if len(v) > 5000 {
				return in, invalid("action %d: %s is too long", i+1, k)
			}
			params[k] = v
		}
		for _, p := range ad.Params {
			if p.Required && strings.TrimSpace(params[p.Name]) == "" {
				return in, invalid("action %d (%s) needs %s", i+1, a.Type, p.Name)
			}
		}
		if a.Type == ActRunWorkflow && strings.TrimSpace(params["values"]) != "" {
			var m map[string]string
			if err := json.Unmarshal([]byte(params["values"]), &m); err != nil {
				return in, invalid("action %d (run_workflow): values is a JSON object of text, e.g. {\"page\":\"{{lead_id}}\"}", i+1)
			}
		}
		if a.Type == ActWebhook {
			u := strings.TrimSpace(params["url"])
			if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
				return in, invalid("action %d (webhook): the address starts with https://", i+1)
			}
		}
		in.Actions[i] = RuleAction{Type: a.Type, Params: params}
	}
	if in.Trigger == triggers.Schedule {
		in.Cron = strings.TrimSpace(in.Cron)
		in.Timezone = strings.TrimSpace(in.Timezone)
		if in.Timezone == "" {
			in.Timezone = "UTC"
		}
		if in.Cron == "" {
			return in, invalid("a schedule rule needs a cron, e.g. 0 9 * * 1-5")
		}
		if err := automation.ValidateSchedule(in.Cron, in.Timezone, automation.CatchUpSkip); err != nil {
			return in, invalid("%s", err.Error())
		}
	} else {
		in.Cron, in.Timezone = "", "UTC"
	}
	if in.Conditions == nil {
		in.Conditions = []RuleCondition{}
	}
	return in, nil
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil
}

// ---- storage -----------------------------------------------------------------------

const ruleCols = `id, name, description, enabled, trigger, match, conditions::text, actions::text, cron, timezone, next_run_at,
	once_per_subject, max_runs_per_hour, run_count, last_run_at, last_status, last_error, webhook_secret <> '',
	created_by, updated_by, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanRule(r scanner) (Rule, error) {
	var x Rule
	var conds, acts string
	var next, last sql.NullTime
	err := r.Scan(&x.ID, &x.Name, &x.Description, &x.Enabled, &x.Trigger, &x.Match, &conds, &acts, &x.Cron, &x.Timezone, &next,
		&x.OncePerSubject, &x.MaxRunsPerHour, &x.RunCount, &last, &x.LastStatus, &x.LastError, &x.HasWebhookSecret,
		&x.CreatedBy, &x.UpdatedBy, &x.CreatedAt, &x.UpdatedAt)
	if err != nil {
		return x, err
	}
	_ = json.Unmarshal([]byte(conds), &x.Conditions)
	_ = json.Unmarshal([]byte(acts), &x.Actions)
	if x.Conditions == nil {
		x.Conditions = []RuleCondition{}
	}
	if x.Actions == nil {
		x.Actions = []RuleAction{}
	}
	if next.Valid {
		t := next.Time
		x.NextRunAt = &t
	}
	if last.Valid {
		t := last.Time
		x.LastRunAt = &t
	}
	return x, nil
}

// ErrNotFound is a rule this workspace does not have.
var ErrNotFound = errors.New("rule not found")

// ErrNameTaken is a second rule with the same name.
var ErrNameTaken = errors.New("a rule with that name already exists")

// List returns the workspace's rules, newest first.
func List(ctx context.Context, db *sql.DB, wsID string) ([]Rule, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+ruleCols+` FROM automation_rules WHERE workspace_id = $1 ORDER BY created_at DESC, id DESC`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns one rule.
func Get(ctx context.Context, db *sql.DB, wsID, id string) (Rule, error) {
	r, err := scanRule(db.QueryRowContext(ctx, `SELECT `+ruleCols+` FROM automation_rules WHERE workspace_id = $1 AND id = $2`, wsID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func nextRun(in RuleInput) (any, error) {
	if in.Trigger != triggers.Schedule {
		return nil, nil
	}
	t, err := automation.NextAfter(in.Cron, in.Timezone, time.Now().UTC())
	if err != nil {
		return nil, invalid("%s", err.Error())
	}
	return t, nil
}

func pgUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}

// Create stores a new rule, acting as userID.
func Create(ctx context.Context, db *sql.DB, wsID, userID string, in RuleInput) (Rule, error) {
	in, err := Clean(in)
	if err != nil {
		return Rule{}, err
	}
	next, err := nextRun(in)
	if err != nil {
		return Rule{}, err
	}
	conds, _ := json.Marshal(in.Conditions)
	acts, _ := json.Marshal(in.Actions)
	enabled := in.Enabled != nil && *in.Enabled
	var id string
	err = db.QueryRowContext(ctx, `
		INSERT INTO automation_rules (workspace_id, name, description, enabled, trigger, match, conditions, actions, cron, timezone,
		                              next_run_at, once_per_subject, max_runs_per_hour, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10, $11, $12, $13, $14, $14) RETURNING id`,
		wsID, in.Name, in.Description, enabled, in.Trigger, in.Match, string(conds), string(acts), in.Cron, in.Timezone,
		next, in.OncePerSubject, in.MaxRunsPerHour, userID).Scan(&id)
	if pgUnique(err) {
		return Rule{}, ErrNameTaken
	}
	if err != nil {
		return Rule{}, err
	}
	return Get(ctx, db, wsID, id)
}

// Replace overwrites a rule's definition. Enabled is kept unless given. The
// member saving it becomes the member it acts as.
func Replace(ctx context.Context, db *sql.DB, wsID, userID, id string, in RuleInput) (Rule, error) {
	cur, err := Get(ctx, db, wsID, id)
	if err != nil {
		return Rule{}, err
	}
	in, err = Clean(in)
	if err != nil {
		return Rule{}, err
	}
	next, err := nextRun(in)
	if err != nil {
		return Rule{}, err
	}
	enabled := cur.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	conds, _ := json.Marshal(in.Conditions)
	acts, _ := json.Marshal(in.Actions)
	_, err = db.ExecContext(ctx, `
		UPDATE automation_rules SET name = $3, description = $4, enabled = $5, trigger = $6, match = $7, conditions = $8::jsonb,
		  actions = $9::jsonb, cron = $10, timezone = $11, next_run_at = $12, once_per_subject = $13, max_runs_per_hour = $14,
		  updated_by = $15, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`,
		wsID, id, in.Name, in.Description, enabled, in.Trigger, in.Match, string(conds), string(acts), in.Cron, in.Timezone,
		next, in.OncePerSubject, in.MaxRunsPerHour, userID)
	if pgUnique(err) {
		return Rule{}, ErrNameTaken
	}
	if err != nil {
		return Rule{}, err
	}
	return Get(ctx, db, wsID, id)
}

// SetEnabled switches a rule on or off. Switching one on makes the member
// who did it the member it acts as: they are the one who decided it runs.
func SetEnabled(ctx context.Context, db *sql.DB, wsID, userID, id string, on bool) (Rule, error) {
	cur, err := Get(ctx, db, wsID, id)
	if err != nil {
		return Rule{}, err
	}
	var next any
	if cur.Trigger == triggers.Schedule && on {
		t, err := automation.NextAfter(cur.Cron, cur.Timezone, time.Now().UTC())
		if err != nil {
			return Rule{}, invalid("%s", err.Error())
		}
		next = t
	}
	by := cur.UpdatedBy
	if on && !cur.Enabled {
		by = userID
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE automation_rules SET enabled = $3, updated_by = $4, updated_at = now(),
		  next_run_at = CASE WHEN trigger = 'schedule' THEN $5::timestamptz ELSE next_run_at END
		WHERE workspace_id = $1 AND id = $2`, wsID, id, on, by, next); err != nil {
		return Rule{}, err
	}
	return Get(ctx, db, wsID, id)
}

// Delete removes a rule and its run log.
func Delete(ctx context.Context, db *sql.DB, wsID, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM automation_rules WHERE workspace_id = $1 AND id = $2`, wsID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- the run log ---------------------------------------------------------------------

// RuleConditionResult is one condition's outcome in a run.
type RuleConditionResult struct {
	Field  string `json:"field"`
	Op     string `json:"op"`
	Value  string `json:"value,omitempty"`
	Actual string `json:"actual"`
	Passed bool   `json:"passed"`
}

// RuleActionResult is one action's outcome in a run.
type RuleActionResult struct {
	Type string `json:"type"`
	// ok | failed | skipped | would (a test: nothing was done)
	Status  string `json:"status"`
	Summary string `json:"summary"`
	Error   string `json:"error,omitempty"`
	// What it made, when it made something (a deal id, a run id…).
	Ref string `json:"ref,omitempty"`
}

// RuleRun is one evaluation of a rule.
type RuleRun struct {
	ID         string                `json:"id"`
	RuleID     string                `json:"rule_id"`
	RuleName   string                `json:"rule_name"`
	EventID    string                `json:"event_id"`
	Trigger    string                `json:"trigger"`
	SubjectID  string                `json:"subject_id"`
	Status     string                `json:"status" doc:"ok, partial, failed, skipped, rate_limited or loop_blocked."`
	DryRun     bool                  `json:"dry_run"`
	Reason     string                `json:"reason"`
	Event      map[string]any        `json:"event"`
	Conditions []RuleConditionResult `json:"conditions"`
	Actions    []RuleActionResult    `json:"actions"`
	Depth      int                   `json:"depth"`
	TestedBy   string                `json:"tested_by"`
	StartedAt  time.Time             `json:"started_at"`
	FinishedAt *time.Time            `json:"finished_at"`
}

const runCols = `id, rule_id, rule_name, event_id, trigger, subject_id, status, dry_run, reason, event::text, conditions::text, actions::text,
	depth, tested_by, started_at, finished_at`

func scanRun(r scanner) (RuleRun, error) {
	var x RuleRun
	var ev, conds, acts string
	var fin sql.NullTime
	err := r.Scan(&x.ID, &x.RuleID, &x.RuleName, &x.EventID, &x.Trigger, &x.SubjectID, &x.Status, &x.DryRun, &x.Reason,
		&ev, &conds, &acts, &x.Depth, &x.TestedBy, &x.StartedAt, &fin)
	if err != nil {
		return x, err
	}
	_ = json.Unmarshal([]byte(ev), &x.Event)
	_ = json.Unmarshal([]byte(conds), &x.Conditions)
	_ = json.Unmarshal([]byte(acts), &x.Actions)
	if x.Event == nil {
		x.Event = map[string]any{}
	}
	if x.Conditions == nil {
		x.Conditions = []RuleConditionResult{}
	}
	if x.Actions == nil {
		x.Actions = []RuleActionResult{}
	}
	if fin.Valid {
		t := fin.Time
		x.FinishedAt = &t
	}
	return x, nil
}

// RunFilter narrows the run log.
type RunFilter struct {
	RuleID string
	Status string
	// RealOnly leaves out test runs; by default the log shows both.
	RealOnly bool
	Before   string // a run id: the page after it
	Limit    int
}

// RuleRunPage is one page of the log, newest first.
type RuleRunPage struct {
	Runs []RuleRun `json:"runs"`
	Next string    `json:"next,omitempty" doc:"Pass as before= for the next page; empty on the last."`
}

// ListRuns reads the run log.
func ListRuns(ctx context.Context, db *sql.DB, wsID string, f RunFilter) (RuleRunPage, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	where := []string{"workspace_id = $1"}
	args := []any{wsID}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.ReplaceAll(cond, "?", "$"+strconv.Itoa(len(args))))
	}
	if f.RuleID != "" {
		add("rule_id = ?", f.RuleID)
	}
	if f.Status != "" {
		add("status = ?", f.Status)
	}
	if f.RealOnly {
		where = append(where, "NOT dry_run")
	}
	if f.Before != "" {
		var at time.Time
		if err := db.QueryRowContext(ctx, `SELECT started_at FROM automation_rule_runs WHERE workspace_id = $1 AND id = $2`, wsID, f.Before).Scan(&at); err == nil {
			args = append(args, at, f.Before)
			n := len(args)
			where = append(where, fmt.Sprintf("(started_at, id) < ($%d, $%d)", n-1, n))
		}
	}
	args = append(args, f.Limit+1)
	rows, err := db.QueryContext(ctx, `SELECT `+runCols+` FROM automation_rule_runs WHERE `+strings.Join(where, " AND ")+
		fmt.Sprintf(` ORDER BY started_at DESC, id DESC LIMIT $%d`, len(args)), args...)
	if err != nil {
		return RuleRunPage{}, err
	}
	defer rows.Close()
	page := RuleRunPage{Runs: []RuleRun{}}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return RuleRunPage{}, err
		}
		page.Runs = append(page.Runs, r)
	}
	if len(page.Runs) > f.Limit {
		page.Runs = page.Runs[:f.Limit]
		page.Next = page.Runs[f.Limit-1].ID
	}
	return page, rows.Err()
}

// GetRun reads one run.
func GetRun(ctx context.Context, db *sql.DB, wsID, id string) (RuleRun, error) {
	r, err := scanRun(db.QueryRowContext(ctx, `SELECT `+runCols+` FROM automation_rule_runs WHERE workspace_id = $1 AND id = $2`, wsID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}

func nextAfter(cron, tz string, after time.Time) (time.Time, error) {
	return automation.NextAfter(cron, tz, after)
}

// RotateSecret makes a new signing secret for a rule's webhooks and returns
// it; the old one stops verifying at once.
func RotateSecret(ctx context.Context, db *sql.DB, wsID, id string) (string, error) {
	plain, err := secret.RandomHex(32)
	if err != nil {
		return "", err
	}
	sealed, err := webhookBox.Seal(plain)
	if err != nil {
		return "", err
	}
	res, err := db.ExecContext(ctx, `UPDATE automation_rules SET webhook_secret = $3 WHERE workspace_id = $1 AND id = $2`, wsID, id, sealed)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return plain, nil
}

// RevealSecret returns a rule's signing secret, making one if it has none.
func RevealSecret(ctx context.Context, db *sql.DB, wsID, id string) (string, error) {
	var sealed string
	err := db.QueryRowContext(ctx, `SELECT webhook_secret FROM automation_rules WHERE workspace_id = $1 AND id = $2`, wsID, id).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if sealed == "" {
		return RotateSecret(ctx, db, wsID, id)
	}
	return webhookBox.Open(sealed)
}
