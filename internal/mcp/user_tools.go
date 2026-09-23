package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/workspaces"
)

/*
UserTools is the user MCP registry. Tools arrive with the phases that build
the tables they operate on — the inbox, automation, research and trends
tools are not registered yet, because those tables do not exist yet.

Every tool reads its workspace from the token (tenant.Scope on ctx), never
from an argument.
*/
func UserTools(researchEngine *research.Engine) []Tool {
	tools := []Tool{
		{
			Name:        "whoami",
			Description: "Who this token acts as: workspace, user, role, and the scopes in effect.",
			Scope:       "members:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				var b strings.Builder
				fmt.Fprintf(&b, "workspace: %s (%s) id=%s\n", s.WorkspaceName, s.WorkspaceSlug, s.WorkspaceID)
				fmt.Fprintf(&b, "user: %s id=%s\n", s.Email, s.UserID)
				fmt.Fprintf(&b, "role: %s\n", s.Role)
				fmt.Fprintf(&b, "scopes: %s\n", strings.Join(nonEmpty(s.Scopes), ", "))
				return b.String(), nil
			},
		},
		{
			Name:        "list_members",
			Description: "Members of the token's workspace with their roles.",
			Scope:       "members:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				s, ok := tenant.From(ctx)
				if !ok {
					return "", errNotFound
				}
				list, err := workspaces.Members(ctx, a.SysDB, s.WorkspaceID)
				if err != nil {
					return "", fmt.Errorf("could not list members: %w", err)
				}
				var b strings.Builder
				fmt.Fprintf(&b, "%d member(s) of %s:\n", len(list), s.WorkspaceSlug)
				for _, m := range list {
					fmt.Fprintf(&b, "  %-8s %s  (%s)\n", m.Role, m.Email, m.Status)
				}
				return b.String(), nil
			},
		},
	}
	tools = append(tools, CrmTools()...)
	tools = append(tools, LeadTools()...)
	tools = append(tools, MarketingTools()...)
	tools = append(tools, NotificationTools()...)
	tools = append(tools, AutomationTools()...)
	tools = append(tools, WorkflowTools()...)
	tools = append(tools, ResearchTools(researchEngine)...)
	return tools
}

var _ = time.Now
