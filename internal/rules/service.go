package rules

import (
	"context"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The rules' operations, once: the REST routes and the MCP tools both call these,
so an agent's rule is checked exactly like a member's.

Reading rules and their runs is automation:read (every member). Creating,
changing, switching on, testing and deleting one is automation:run (managers
and up) — a rule acts with nobody watching, which is the same line a schedule
draws. Each reads the caller from ctx; errors are huma status errors.
*/

func scope(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

func httpErr(err error, fallback string) error {
	var inv *ErrInvalid
	var se huma.StatusError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &inv):
		return huma.Error422UnprocessableEntity(inv.Msg)
	case errors.Is(err, ErrNotFound):
		return huma.Error404NotFound("rule not found")
	case errors.Is(err, ErrNameTaken):
		return huma.Error409Conflict(ErrNameTaken.Error())
	case errors.As(err, &se):
		return err
	}
	return huma.Error500InternalServerError(fallback)
}

// ListRules is every rule in the caller's workspace.
func ListRules(ctx context.Context, a *app.App) ([]Rule, error) {
	s, err := scope(ctx, authz.AutomationRead)
	if err != nil {
		return nil, err
	}
	list, err := List(ctx, a.SQLDB, s.WorkspaceID)
	return list, httpErr(err, "could not read the rules")
}

// GetRule is one rule.
func GetRule(ctx context.Context, a *app.App, id string) (Rule, error) {
	s, err := scope(ctx, authz.AutomationRead)
	if err != nil {
		return Rule{}, err
	}
	r, err := Get(ctx, a.SQLDB, s.WorkspaceID, id)
	return r, httpErr(err, "could not read the rule")
}

// CreateRule saves a new rule; it acts as the caller.
func CreateRule(ctx context.Context, a *app.App, in RuleInput) (Rule, error) {
	s, err := scope(ctx, authz.AutomationRun)
	if err != nil {
		return Rule{}, err
	}
	r, err := Create(ctx, a.SQLDB, s.WorkspaceID, s.UserID, in)
	return r, httpErr(err, "could not save the rule")
}

// ReplaceRule saves a rule's new definition; the caller becomes the member it
// acts as.
func ReplaceRule(ctx context.Context, a *app.App, id string, in RuleInput) (Rule, error) {
	s, err := scope(ctx, authz.AutomationRun)
	if err != nil {
		return Rule{}, err
	}
	r, err := Replace(ctx, a.SQLDB, s.WorkspaceID, s.UserID, id, in)
	return r, httpErr(err, "could not save the rule")
}

// EnableRule switches a rule on or off.
func EnableRule(ctx context.Context, a *app.App, id string, on bool) (Rule, error) {
	s, err := scope(ctx, authz.AutomationRun)
	if err != nil {
		return Rule{}, err
	}
	r, err := SetEnabled(ctx, a.SQLDB, s.WorkspaceID, s.UserID, id, on)
	return r, httpErr(err, "could not switch the rule")
}

// DeleteRule removes a rule and its run log.
func DeleteRule(ctx context.Context, a *app.App, id string) error {
	s, err := scope(ctx, authz.AutomationRun)
	if err != nil {
		return err
	}
	return httpErr(Delete(ctx, a.SQLDB, s.WorkspaceID, id), "could not delete the rule")
}

// RuleTestInput is a test: the saved rule (id) or a draft (Rule), against a sample
// event (Event laid over the trigger's own sample).
type RuleTestInput struct {
	Rule  *RuleInput     `json:"rule,omitempty" doc:"Test this definition instead of the saved one (unsaved changes, or a rule not saved yet)."`
	Event map[string]any `json:"event,omitempty" doc:"The sample event's fields; anything left out comes from the trigger's own sample."`
	// SubjectID is the record the sample is about, for "once per record".
	SubjectID string `json:"subject_id,omitempty" maxLength:"80"`
}

// TestRule evaluates a rule against a sample event and carries nothing out:
// every action says what it would do. A saved rule's test is kept in its log,
// marked as a test.
func TestRule(ctx context.Context, a *app.App, id string, in RuleTestInput) (RuleRun, error) {
	s, err := scope(ctx, authz.AutomationRun)
	if err != nil {
		return RuleRun{}, err
	}
	var r Rule
	if id != "" {
		if r, err = Get(ctx, a.SQLDB, s.WorkspaceID, id); err != nil {
			return RuleRun{}, httpErr(err, "could not read the rule")
		}
	}
	if in.Rule != nil {
		clean, err := Clean(*in.Rule)
		if err != nil {
			return RuleRun{}, httpErr(err, "")
		}
		r.Name, r.Trigger, r.Match, r.Conditions, r.Actions, r.OncePerSubject =
			clean.Name, clean.Trigger, clean.Match, clean.Conditions, clean.Actions, clean.OncePerSubject
		r.MaxRunsPerHour = clean.MaxRunsPerHour
	}
	if r.Trigger == "" {
		return RuleRun{}, huma.Error422UnprocessableEntity("give the rule to test")
	}
	// A draft acts as the person testing it; a saved rule as its member.
	if r.UpdatedBy == "" {
		r.UpdatedBy = s.UserID
	}
	ev := Event{Type: r.Trigger, Data: Sample(r.Trigger, in.Event), SubjectID: strings.TrimSpace(in.SubjectID)}
	run := Execute(ctx, a, s.WorkspaceID, r, ev, s.UserID)
	if r.ID != "" {
		runID, err := logRun(ctx, a, s.WorkspaceID, r, run)
		if err != nil {
			return RuleRun{}, huma.Error500InternalServerError("could not log the test")
		}
		run.ID = runID
	}
	return run, nil
}

// ListRuleRuns reads the run log.
func ListRuleRuns(ctx context.Context, a *app.App, f RunFilter) (RuleRunPage, error) {
	s, err := scope(ctx, authz.AutomationRead)
	if err != nil {
		return RuleRunPage{}, err
	}
	p, err := ListRuns(ctx, a.SQLDB, s.WorkspaceID, f)
	return p, httpErr(err, "could not read the run log")
}

// GetRuleRun reads one run.
func GetRuleRun(ctx context.Context, a *app.App, id string) (RuleRun, error) {
	s, err := scope(ctx, authz.AutomationRead)
	if err != nil {
		return RuleRun{}, err
	}
	r, err := GetRun(ctx, a.SQLDB, s.WorkspaceID, id)
	if errors.Is(err, ErrNotFound) {
		return r, huma.Error404NotFound("run not found")
	}
	return r, httpErr(err, "could not read the run")
}

// RuleSecret reveals (rotate=false) or replaces (rotate=true) a rule's webhook
// signing secret.
func RuleSecret(ctx context.Context, a *app.App, id string, rotate bool) (string, error) {
	s, err := scope(ctx, authz.AutomationRun)
	if err != nil {
		return "", err
	}
	var v string
	if rotate {
		v, err = RotateSecret(ctx, a.SQLDB, s.WorkspaceID, id)
	} else {
		v, err = RevealSecret(ctx, a.SQLDB, s.WorkspaceID, id)
	}
	return v, httpErr(err, "could not read the rule's secret")
}
