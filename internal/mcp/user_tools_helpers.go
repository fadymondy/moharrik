package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/workspaces"
)

/*
Helpers for the tools added with the MCP surface (MH-380, MH-386, MH-393,
MH-409).

Those tools answer in JSON rather than prose: every record carries its id, so
what one tool returns is what the next one targets (get_conversation → reply,
search_contacts → update_contact, list_webhook_sources → rotate_webhook_source).
*/

// jsonResult renders a tool's answer.
func jsonResult(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("could not encode the answer: %w", err)
	}
	return string(b), nil
}

// bind decodes a tool's arguments into a typed struct. Absent arguments stay
// at their zero value; pointer fields tell "not given" from "given empty".
func bind(raw json.RawMessage, into any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("bad arguments: %w", err)
	}
	return nil
}

// need returns the caller, or errNotFound when ctx carries none.
func need(ctx context.Context) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, errNotFound
	}
	return s, nil
}

// required trims a required string argument and names it when it is missing.
func required(name, v string) (string, error) {
	if v = strings.TrimSpace(v); v == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return v, nil
}

// memberID resolves an assignee or owner given as a user id or as a member's
// email to the member's user id. "" stays "" (unassign). Anyone who is not a
// member of the token's workspace is refused by name, not silently ignored.
func memberID(ctx context.Context, a *app.App, who string) (string, error) {
	who = strings.TrimSpace(who)
	if who == "" {
		return "", nil
	}
	s, err := need(ctx)
	if err != nil {
		return "", err
	}
	list, err := workspaces.Members(ctx, a.SysDB, s.WorkspaceID)
	if err != nil {
		return "", fmt.Errorf("could not read the members: %w", err)
	}
	for _, m := range list {
		if m.Status == "active" && (m.UserID == who || strings.EqualFold(m.Email, who)) {
			return m.UserID, nil
		}
	}
	return "", fmt.Errorf("%q is not an active member of this workspace — call list_members for their ids", who)
}

// minor converts a value in major units (1500.50) to minor units (150050).
func minor(v float64) int64 {
	if v < 0 {
		return int64(v*100 - 0.5)
	}
	return int64(v*100 + 0.5)
}

// noMatch answers a tool call that found nothing to act on — an ambiguous or
// absent reference the caller can resolve (unlike errNotFound, which is for
// something wholly outside the caller's reach). reason carries whatever a
// person would need to try again: what was paired, what matched, or why not.
func noMatch(reason string) (string, error) {
	return jsonResult(map[string]any{"ok": false, "reason": reason})
}

// clipText bounds a long text in a list answer.
func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
