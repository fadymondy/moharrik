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
	tools = append(tools, ICPTools()...)    // the ideal-customer profile and lifecycle (MH-395)
	tools = append(tools, MarketingTools()...)
	tools = append(tools, NotificationTools()...)
	tools = append(tools, InboxTools()...)
	tools = append(tools, EmailTemplateTools()...) // MH-661
	tools = append(tools, OrganiseTools()...)      // MH-657
	tools = append(tools, MailboxTools()...)       // several IMAP mailboxes (MH-555)
	tools = append(tools, WhatsAppTools()...)
	tools = append(tools, ChannelTools()...)        // Telegram, Slack, Discord and workflow channels (MH-563, MH-564)
	tools = append(tools, ImportTools()...)         // import a connection's chats into contacts + inbox (MH-562)
	tools = append(tools, VcardTools()...)          // import/export contacts as vCard 2.1/3.0/4.0 (MH-584)
	tools = append(tools, ContactContextTools()...) // WhatsApp export import, ask/report, free-text context (MH-586)
	tools = append(tools, BriefTools()...)          // the AI context brief on contacts, companies and leads (MH-1169)
	tools = append(tools, GoogleContactsTools()...) // google_contacts_sync (MH-585)
	tools = append(tools, TrendsTools()...)
	tools = append(tools, AutomationTools()...)
	tools = append(tools, WorkflowTools()...)
	tools = append(tools, WorkflowRunTools()...) // workflow overview + run again (MH-1067)
	tools = append(tools, BrowserTools()...)
	tools = append(tools, RuleTools()...) // automation rules (MH-427)
	tools = append(tools, BrowserRunTools()...)
	tools = append(tools, ResearchTools(researchEngine)...)
	tools = append(tools, ProspectTools(researchEngine)...) // prospect discovery (MH-397)
	tools = append(tools, GoogleTools()...)                 // GA4, Search Console, YouTube (MH-447)
	tools = append(tools, AuditTools()...)                  // the audit log (MH-392)
	tools = append(tools, SettingsTools()...)               // workspace, members, pipeline, mail, Google, audit settings (MH-680)
	return tools
}

var _ = time.Now
