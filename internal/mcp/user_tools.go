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
the tables they operate on.

Every tool reads its workspace from the token (tenant.Scope on ctx), never
from an argument, declares a scope from scopes.go and a description
(scopes_test.go fails the build otherwise), and — since the MCP surface cards
(MH-380, MH-386, MH-393, MH-409) — calls the same package function the REST
route for the same thing calls, and answers with the ids other tools take.
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
			Description: "Members of the token's workspace: user id, email, role and status. The user id (or the email) is what assign_conversation and the owner fields take.",
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
				return jsonResult(map[string]any{"workspace": s.WorkspaceSlug, "count": len(list), "members": list})
			},
		},
	}
	tools = append(tools, CrmTools()...)
	tools = append(tools, LeadTools()...)
	tools = append(tools, ReportTools()...) // crm_report (MH-394)
	tools = append(tools, MarketingTools()...)
	tools = append(tools, NotificationTools()...)
	tools = append(tools, InboxTools()...)
	tools = append(tools, WhatsAppTools()...)
	tools = append(tools, TrendsTools()...)
	tools = append(tools, AutomationTools()...)
	tools = append(tools, WorkflowTools()...)
	tools = append(tools, ResearchTools(researchEngine)...)
	tools = append(tools, GoogleTools()...) // GA4, Search Console, YouTube (MH-447)
	tools = append(tools, AuditTools()...)  // the audit log (MH-392)
	return tools
}

var _ = time.Now
