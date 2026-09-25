package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/rules"
)

/*
RuleTools: the automation rules (MH-427) for an agent — list, read, create,
change, switch on or off, test, and read the run log.

They call the same internal/rules operations as the Rules page, so an agent's
rule is validated like a member's, starts switched off unless asked, acts as
the member whose token saved it, and is subject to the same loop guard, rate
limits and consent rules when it runs. test_rule sends nothing: it is how an
agent shows a person what a rule would do before switching it on.
*/

var ruleConditionSchema = map[string]any{
	"type": "object", "required": []string{"field", "op"},
	"properties": map[string]any{
		"field": map[string]any{"type": "string", "description": "A field the trigger carries (rule_catalog)."},
		"op":    map[string]any{"type": "string", "enum": rules.Operators},
		"value": map[string]any{"type": "string", "description": "Text or a number; for in / not_in a comma-separated list."},
	},
}

var ruleActionSchema = map[string]any{
	"type": "object", "required": []string{"type"},
	"properties": map[string]any{
		"type":   map[string]any{"type": "string", "description": "An action type from rule_catalog."},
		"params": map[string]any{"type": "object", "description": `Its settings, all text; {{field}} fills in an event field, e.g. {"title":"New lead: {{name}}"}.`},
	},
}

func ruleProps() map[string]any {
	return map[string]any{
		"name":              map[string]any{"type": "string"},
		"description":       map[string]any{"type": "string"},
		"trigger":           map[string]any{"type": "string", "description": "An event type from rule_catalog, e.g. lead.created."},
		"match":             map[string]any{"type": "string", "enum": []string{"all", "any"}},
		"conditions":        map[string]any{"type": "array", "items": ruleConditionSchema},
		"actions":           map[string]any{"type": "array", "items": ruleActionSchema},
		"enabled":           map[string]any{"type": "boolean", "description": "Default false: test it first."},
		"once_per_subject":  map[string]any{"type": "boolean", "description": "Run at most once per record (lead, deal, story…)."},
		"max_runs_per_hour": map[string]any{"type": "integer", "description": "The rule's own brake; default 30."},
		"cron":              map[string]any{"type": "string", "description": "schedule trigger only, e.g. 0 9 * * 1-5"},
		"timezone":          map[string]any{"type": "string", "description": "schedule trigger only, an IANA name"},
	}
}

func RuleTools() []Tool {
	return []Tool{
		{
			Name:        "rule_catalog",
			Description: "What an automation rule can listen to (each trigger with the fields it carries and a sample), the operators a condition can use, and the actions with their settings.",
			Scope:       "automation:read",
			InputSchema: schema(nil, nil),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				if _, err := need(ctx); err != nil {
					return "", err
				}
				return jsonResult(rules.GetCatalog())
			},
		},
		{
			Name:        "list_rules",
			Description: "The workspace's automation rules: id, name, trigger, whether each is on, and how its last run went.",
			Scope:       "automation:read",
			InputSchema: schema(nil, nil),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				list, err := rules.ListRules(ctx, a)
				if err != nil {
					return "", err
				}
				type row struct {
					ID         string `json:"id"`
					Name       string `json:"name"`
					Trigger    string `json:"trigger"`
					Enabled    bool   `json:"enabled"`
					Conditions int    `json:"conditions"`
					Actions    int    `json:"actions"`
					RunCount   int64  `json:"run_count"`
					LastStatus string `json:"last_status"`
					LastError  string `json:"last_error,omitempty"`
				}
				out := []row{}
				for _, r := range list {
					out = append(out, row{r.ID, r.Name, r.Trigger, r.Enabled, len(r.Conditions), len(r.Actions), r.RunCount, r.LastStatus, r.LastError})
				}
				return jsonResult(map[string]any{"rules": out})
			},
		},
		{
			Name:        "get_rule",
			Description: "One automation rule in full, with its ten latest runs.",
			Scope:       "automation:read",
			InputSchema: schema([]string{"id"}, map[string]any{"id": map[string]any{"type": "string"}}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				var in struct {
					ID string `json:"id"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				r, err := rules.GetRule(ctx, a, in.ID)
				if err != nil {
					return "", err
				}
				runs, err := rules.ListRuleRuns(ctx, a, rules.RunFilter{RuleID: r.ID, Limit: 10})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"rule": r, "runs": runs.Runs})
			},
		},
		{
			Name: "create_rule",
			Description: "Add an automation rule: when trigger happens, if the conditions hold, run the actions in order. " +
				"It is saved switched off unless enabled is true — call test_rule first. It acts as you, with your role when it runs.",
			Scope:       "automation:run",
			InputSchema: schema([]string{"name", "trigger", "actions"}, ruleProps()),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				var in rules.RuleInput
				if err := bind(args, &in); err != nil {
					return "", err
				}
				r, err := rules.CreateRule(ctx, a, in)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"rule": r})
			},
		},
		{
			Name: "update_rule",
			Description: "Change an automation rule: switch it on or off (enabled), or change any of its definition — only what is given changes. " +
				"Saving it (or switching it on) makes you the member it acts as.",
			Scope: "automation:run",
			InputSchema: schema([]string{"id"}, func() map[string]any {
				p := ruleProps()
				p["id"] = map[string]any{"type": "string"}
				return p
			}()),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				var in struct {
					ID             string                 `json:"id"`
					Name           *string                `json:"name"`
					Description    *string                `json:"description"`
					Trigger        *string                `json:"trigger"`
					Match          *string                `json:"match"`
					Conditions     *[]rules.RuleCondition `json:"conditions"`
					Actions        *[]rules.RuleAction    `json:"actions"`
					Enabled        *bool                  `json:"enabled"`
					OncePerSubject *bool                  `json:"once_per_subject"`
					MaxRunsPerHour *int                   `json:"max_runs_per_hour"`
					Cron           *string                `json:"cron"`
					Timezone       *string                `json:"timezone"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				cur, err := rules.GetRule(ctx, a, in.ID)
				if err != nil {
					return "", err
				}
				definition := in.Name != nil || in.Description != nil || in.Trigger != nil || in.Match != nil || in.Conditions != nil ||
					in.Actions != nil || in.OncePerSubject != nil || in.MaxRunsPerHour != nil || in.Cron != nil || in.Timezone != nil
				if !definition {
					if in.Enabled == nil {
						return "", fmt.Errorf("nothing to change: give enabled, or part of the definition")
					}
					r, err := rules.EnableRule(ctx, a, in.ID, *in.Enabled)
					if err != nil {
						return "", err
					}
					return jsonResult(map[string]any{"rule": r})
				}
				next := rules.RuleInput{Name: cur.Name, Description: cur.Description, Trigger: cur.Trigger, Match: cur.Match,
					Conditions: cur.Conditions, Actions: cur.Actions, Cron: cur.Cron, Timezone: cur.Timezone,
					OncePerSubject: cur.OncePerSubject, MaxRunsPerHour: cur.MaxRunsPerHour, Enabled: in.Enabled}
				set := func(dst *string, v *string) {
					if v != nil {
						*dst = *v
					}
				}
				set(&next.Name, in.Name)
				set(&next.Description, in.Description)
				set(&next.Trigger, in.Trigger)
				set(&next.Match, in.Match)
				set(&next.Cron, in.Cron)
				set(&next.Timezone, in.Timezone)
				if in.Conditions != nil {
					next.Conditions = *in.Conditions
				}
				if in.Actions != nil {
					next.Actions = *in.Actions
				}
				if in.OncePerSubject != nil {
					next.OncePerSubject = *in.OncePerSubject
				}
				if in.MaxRunsPerHour != nil {
					next.MaxRunsPerHour = *in.MaxRunsPerHour
				}
				r, err := rules.ReplaceRule(ctx, a, in.ID, next)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"rule": r})
			},
		},
		{
			Name: "test_rule",
			Description: "Test a saved automation rule against a sample event. Nothing is sent or written: the answer says whether the " +
				"conditions held and what each action would do, with its {{fields}} filled in. Fields left out come from the trigger's sample.",
			Scope: "automation:run",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":         map[string]any{"type": "string"},
				"event":      map[string]any{"type": "object", "description": `The sample's fields, e.g. {"severity":"error","source_kind":"sentry"}.`},
				"subject_id": map[string]any{"type": "string", "description": "The record the sample is about, for once-per-record rules."},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				var in struct {
					ID        string         `json:"id"`
					Event     map[string]any `json:"event"`
					SubjectID string         `json:"subject_id"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				run, err := rules.TestRule(ctx, a, in.ID, rules.RuleTestInput{Event: in.Event, SubjectID: in.SubjectID})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"run": run})
			},
		},
		{
			Name:        "list_rule_runs",
			Description: "The automation rules' run log, newest first: each evaluation with its event, which conditions held and what each action did.",
			Scope:       "automation:read",
			InputSchema: schema(nil, map[string]any{
				"rule_id": map[string]any{"type": "string"},
				"status":  map[string]any{"type": "string", "enum": []string{"ok", "partial", "failed", "skipped", "rate_limited", "loop_blocked"}},
				"real":    map[string]any{"type": "boolean", "description": "Leave out tests."},
				"before":  map[string]any{"type": "string", "description": "next (or next_cursor) from the previous page"},
				"cursor":  map[string]any{"type": "string", "description": "Same as before — next_cursor from the previous page"},
				"limit":   map[string]any{"type": "integer", "description": "1–100, default 50"},
			}),
			Run: func(ctx context.Context, a *app.App, args json.RawMessage) (string, error) {
				var in struct {
					RuleID string `json:"rule_id"`
					Status string `json:"status"`
					Real   bool   `json:"real"`
					Before string `json:"before"`
					Cursor string `json:"cursor"`
					Limit  int    `json:"limit"`
				}
				if err := bind(args, &in); err != nil {
					return "", err
				}
				before := in.Before
				if before == "" {
					before = in.Cursor
				}
				p, err := rules.ListRuleRuns(ctx, a, rules.RunFilter{RuleID: in.RuleID, Status: in.Status, RealOnly: in.Real, Before: before, Limit: in.Limit})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"runs": p.Runs, "next": p.Next, "next_cursor": p.Next})
			},
		},
	}
}
