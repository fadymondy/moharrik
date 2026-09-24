package rules

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

var tags = []string{"Automation rules"}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

type idPath struct {
	wsHeader
	ID string `path:"id" maxLength:"80"`
}

// RuleOut wraps one rule.
type RuleOut struct {
	Rule Rule `json:"rule"`
}

// RuleRunOut wraps one run.
type RuleRunOut struct {
	Run RuleRun `json:"run"`
}

// RuleSecretOut is a rule's webhook signing secret.
type RuleSecretOut struct {
	Secret string `json:"secret" doc:"HMAC-SHA256 key: X-Moharrik-Signature = sha256=hex(HMAC(secret, X-Moharrik-Timestamp + \".\" + body))."`
}

// Register mounts the rule endpoints. a may be nil (the OpenAPI export).
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-rules-catalog", Method: http.MethodGet, Path: "/api/w/rules/catalog",
		Summary: "What a rule can listen to, test and do", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[RuleCatalog], error) {
		if _, err := scope(ctx, authz.AutomationRead); err != nil {
			return nil, err
		}
		return out(GetCatalog()), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-rules", Method: http.MethodGet, Path: "/api/w/rules",
		Summary: "The workspace's automation rules", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[struct {
		Rules []Rule `json:"rules"`
	}], error) {
		list, err := ListRules(ctx, a)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Rules []Rule `json:"rules"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-rule", Method: http.MethodPost, Path: "/api/w/rules", DefaultStatus: http.StatusCreated,
		Summary: "Add a rule: when an event happens, if conditions hold, then run actions",
		Description: "A new rule is off unless enabled is true. Test it (POST /api/w/rules/{id}/test) before switching it on. " +
			"It acts as the member who saved it, with their role at the time it runs.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body RuleInput
	}) (*body[RuleOut], error) {
		r, err := CreateRule(ctx, a, in.Body)
		if err != nil {
			return nil, err
		}
		return out(RuleOut{r}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-rule", Method: http.MethodGet, Path: "/api/w/rules/{id}",
		Summary: "One rule", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[RuleOut], error) {
		r, err := GetRule(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return out(RuleOut{r}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-replace-rule", Method: http.MethodPut, Path: "/api/w/rules/{id}",
		Summary: "Save a rule's definition", Description: "The member saving it becomes the member it acts as. enabled is kept unless given.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id" maxLength:"80"`
		Body RuleInput
	}) (*body[RuleOut], error) {
		r, err := ReplaceRule(ctx, a, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return out(RuleOut{r}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-enable-rule", Method: http.MethodPatch, Path: "/api/w/rules/{id}",
		Summary: "Switch a rule on or off", Description: "Switching it on makes the caller the member it acts as.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id" maxLength:"80"`
		Body struct {
			Enabled bool `json:"enabled"`
		}
	}) (*body[RuleOut], error) {
		r, err := EnableRule(ctx, a, in.ID, in.Body.Enabled)
		if err != nil {
			return nil, err
		}
		return out(RuleOut{r}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-rule", Method: http.MethodDelete, Path: "/api/w/rules/{id}", DefaultStatus: http.StatusNoContent,
		Summary: "Delete a rule and its run log", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		return nil, DeleteRule(ctx, a, in.ID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-test-rule", Method: http.MethodPost, Path: "/api/w/rules/{id}/test",
		Summary: "Test a rule against a sample event",
		Description: "Nothing is sent or written: each action says what it would do, with its templates filled in. " +
			"The test is kept in the rule's log, marked as a test.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id" maxLength:"80"`
		Body RuleTestInput
	}) (*body[RuleRunOut], error) {
		r, err := TestRule(ctx, a, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return out(RuleRunOut{r}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-test-draft-rule", Method: http.MethodPost, Path: "/api/w/rules/test",
		Summary: "Test a rule that is not saved yet", Description: "rule is required. Nothing is sent, written or logged.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body RuleTestInput
	}) (*body[RuleRunOut], error) {
		if in.Body.Rule == nil {
			return nil, huma.Error422UnprocessableEntity("give the rule to test")
		}
		r, err := TestRule(ctx, a, "", in.Body)
		if err != nil {
			return nil, err
		}
		return out(RuleRunOut{r}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-rule-secret", Method: http.MethodGet, Path: "/api/w/rules/{id}/secret",
		Summary: "A rule's webhook signing secret", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[RuleSecretOut], error) {
		v, err := RuleSecret(ctx, a, in.ID, false)
		if err != nil {
			return nil, err
		}
		return out(RuleSecretOut{v}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-rotate-rule-secret", Method: http.MethodPost, Path: "/api/w/rules/{id}/secret",
		Summary: "Replace a rule's webhook signing secret", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[RuleSecretOut], error) {
		v, err := RuleSecret(ctx, a, in.ID, true)
		if err != nil {
			return nil, err
		}
		return out(RuleSecretOut{v}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-rule-runs", Method: http.MethodGet, Path: "/api/w/rule-runs",
		Summary: "The rules' run log, newest first", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		RuleID string `query:"rule_id" maxLength:"80"`
		Status string `query:"status" enum:",ok,partial,failed,skipped,rate_limited,loop_blocked"`
		Real   bool   `query:"real" doc:"Leave out tests."`
		Before string `query:"before" maxLength:"80" doc:"The next page: the last run id of the page before."`
		Limit  int    `query:"limit" minimum:"0" maximum:"100"`
	}) (*body[RuleRunPage], error) {
		p, err := ListRuleRuns(ctx, a, RunFilter{RuleID: in.RuleID, Status: in.Status, RealOnly: in.Real, Before: in.Before, Limit: in.Limit})
		if err != nil {
			return nil, err
		}
		return out(p), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-rule-run", Method: http.MethodGet, Path: "/api/w/rule-runs/{id}",
		Summary: "One run of a rule: its event, conditions and actions", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[RuleRunOut], error) {
		r, err := GetRuleRun(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return out(RuleRunOut{r}), nil
	})
}
