// Package authz is the single authorization file: a fixed role × action
// matrix, plus the per-table policy for generated CRUD. Everything that asks
// "may this caller do X" asks Can — browser sessions and MCP tokens alike, so
// an agent can never exceed the human it acts for.
//
// togo-framework/authz was evaluated for P0 and not adopted: it is a
// permission-string checker over Identity.Permissions (the global users.roles /
// users.permissions CSV columns), with no notion of a per-workspace role, which
// is the only role Moharrik has.
package authz

import "net/http"

// Role is a workspace role. Order matters: each role holds everything below it.
type Role string

const (
	Owner   Role = "owner"
	Admin   Role = "admin"
	Manager Role = "manager"
	Member  Role = "member"
	Client  Role = "client"
	// None marks an action or table operation no role may perform.
	None Role = ""
)

var rank = map[Role]int{Client: 1, Member: 2, Manager: 3, Admin: 4, Owner: 5}

// Valid reports whether r is a real workspace role.
func Valid(r string) bool { _, ok := rank[Role(r)]; return ok }

// AtLeast reports whether r holds min or higher. Nothing is at least None.
func (r Role) AtLeast(min Role) bool {
	if min == None {
		return false
	}
	return rank[r] >= rank[min] && rank[r] > 0
}

// Action names. Adding one means adding it to Matrix below.
const (
	WorkspaceRead     = "workspace:read"
	WorkspaceUpdate   = "workspace:update"
	MembersRead       = "members:read"
	MembersInvite     = "members:invite"
	InvitationsManage = "invitations:manage"
	TokensManageOwn   = "tokens:manage"
	TokensRevokeAny   = "tokens:revoke_any"
	GraphQL           = "graphql"
	// Email marketing.
	ContactsRead   = "contacts:read"
	ContactsWrite  = "contacts:write"
	// ContactsExport is downloading a list as a file (MH-408). See Matrix for
	// why it is not simply contacts:read.
	ContactsExport = "contacts:export"
	CampaignsRead  = "campaigns:read"
	CampaignsWrite = "campaigns:write"
	CampaignsSend  = "campaigns:send"
	MailSettings   = "mail:settings"
	// CRM. Companies, deals and activities are read and written under
	// contacts:read / contacts:write — the MCP scopes of the same names
	// (internal/mcp/scopes.go). Leads have their own pair, below.
	PipelineManage = "pipeline:manage"
	// Leads (MH-390): inquiries from a customer's website. Reading them and
	// working them — changing the status, converting one into a contact,
	// company and deal — is a member right, the same line contacts:* draws:
	// answering new business is the job. Creating the FORM that lets a
	// website file leads is campaigns:write, like every public form.
	LeadsRead  = "leads:read"
	LeadsWrite = "leads:write"
	// Notifications: reading the feed, marking read and saving is a member
	// right, analogous to contacts:read/write. Creating, renaming, disabling,
	// rotating or deleting a webhook source is a manager right, analogous to
	// pipeline:manage — it changes what a whole workspace collects.
	NotificationsRead   = "notifications:read"
	NotificationsWrite  = "notifications:write"
	NotificationsManage = "notifications:manage"
	// Inbox: reading and answering conversations is a member right (answering
	// customers is the job). Creating a chat widget mints a public key that
	// decides which origins may write into the workspace, so that is a
	// manager's call — the same line notifications:manage draws.
	InboxRead   = "inbox:read"
	InboxWrite  = "inbox:write"
	InboxManage = "inbox:manage"
	// Trends: everyone reads the timeline; adding or removing a feed or a topic
	// changes what the whole workspace collects, so it is a manager's call —
	// the same line notifications:manage and inbox:manage draw.
	TrendsRead   = "trends:read"
	TrendsManage = "trends:manage"
	// Automation. The line is drawn harder here than anywhere else in this file,
	// because these browsers are already signed into someone's mail, bank and
	// work tools: seeing what ran is a member right, but STARTING a run is a
	// manager right, and pairing a browser, changing where it may be driven, or
	// lifting the kill switch is an admin right. A wrong guess elsewhere in this
	// file leaks a list; a wrong guess here drives somebody's signed-in browser.
	// Research: reading a past run is a member right, but STARTING one reaches
	// out to the public web and spends a model call, so it is a manager's
	// call — the same line campaigns:send draws for the same reason.
	ResearchRead     = "research:read"
	ResearchRun      = "research:run"
	AutomationRead   = "automation:read"
	AutomationRun    = "automation:run"
	AutomationManage = "automation:manage"
	// Integrations: connecting a Google account hands this workspace a
	// standing grant to that account's Analytics, Search Console or YouTube
	// data, and disconnecting revokes it at Google. That is the owner's
	// business relationship with Google, so it is an admin right — the same
	// line mail:settings draws for the sending credentials (MH-439).
	IntegrationsManage = "integrations:manage"
)

// Matrix is the minimum role per action.
var Matrix = map[string]Role{
	WorkspaceRead:     Client,
	WorkspaceUpdate:   Admin,
	MembersRead:       Member,
	MembersInvite:     Admin,
	InvitationsManage: Admin,
	TokensManageOwn:   Client,
	TokensRevokeAny:   Admin,
	// The generic GraphQL endpoint exposes generated resolvers, which carry no
	// per-field authorization of their own; RLS isolates tenants, and this keeps
	// it to people who could make the same changes in the UI.
	GraphQL: Admin,
	// Email marketing: members keep the audience; managers compose and send;
	// admins own the sending credentials.
	ContactsRead:   Member,
	ContactsWrite:  Member,
	// Export is a manager's call although a member can page through the same
	// rows on screen. Reading contacts one page at a time is doing the job;
	// one click that puts the whole audience — names, addresses, phones, in a
	// file that leaves the workspace and cannot be recalled — is a bulk
	// disclosure of personal data. It is the classic way a list walks out with
	// someone who is leaving, and under GDPR/PDPL it is the kind of processing
	// a business should be able to say who is allowed to do. Managers already
	// own the list as a whole (they compose and send to all of it), so this
	// draws the line where campaigns:write does.
	ContactsExport: Manager,
	CampaignsRead:  Member,
	CampaignsWrite: Manager,
	CampaignsSend:  Manager,
	MailSettings:   Admin,
	// CRM: every member works companies, deals and the timeline (contacts:*);
	// the stages shape everyone's board, so renaming or reordering them is a
	// manager's call.
	PipelineManage: Manager,
	// Leads: every member reads and works them (converting also needs
	// contacts:write, which every member holds).
	LeadsRead:  Member,
	LeadsWrite: Member,
	// Notifications: every member reads the feed and marks/saves items; only a
	// manager or above creates, renames, disables, rotates or deletes a
	// webhook source (it decides what the whole workspace collects, and a
	// secret is shown once at that moment).
	NotificationsRead:   Member,
	NotificationsWrite:  Member,
	NotificationsManage: Manager,
	// Inbox: every member reads and answers; only a manager or above creates or
	// revokes a chat widget, because its public key is what lets an outside site
	// write into this workspace at all.
	InboxRead:        Member,
	InboxWrite:       Member,
	InboxManage:      Manager,
	TrendsRead:       Member,
	TrendsManage:     Manager,
	ResearchRead:     Member,
	ResearchRun:      Manager,
	AutomationRead:   Member,
	AutomationRun:    Manager,
	AutomationManage: Admin,
	// Integrations: admins and owners connect and disconnect Google accounts.
	IntegrationsManage: Admin,
}

// Can reports whether role may perform action. Unknown actions are denied.
func Can(role string, action string) bool {
	min, ok := Matrix[action]
	if !ok {
		return false
	}
	return Role(role).AtLeast(min)
}

// Allowed lists every action role may perform (for the UI's `can`).
func Allowed(role string) []string {
	out := []string{}
	for action := range Matrix {
		if Can(role, action) {
			out = append(out, action)
		}
	}
	return out
}

// TablePolicy is who may read and who may write a generated resource.
type TablePolicy struct{ Read, Write Role }

/*
Generated maps each generated REST resource (the /api/<table> tree registered
by rest/registry.gen.go) to its policy. It is default-deny: a resource added by
`togo make:resource` is unreachable over the generic API until it is listed
here — so a new table is closed until someone decides otherwise, rather than
open until someone notices.

Membership itself is enforced before this (tenant.Require, 404), and RLS
confines every row to the caller's workspace after it.

Writes to the tenancy tables are None on purpose: the generic handlers take
workspace_id / user_id / role / token_hash straight from the request body. Those
changes go through the hand-written /api/w/* and /api/me/* endpoints, which
derive them from the session.
*/
var Generated = map[string]TablePolicy{
	"workspaces":        {Read: Member, Write: None},
	"workspace_members": {Read: Member, Write: None},
	"invitations":       {Read: Admin, Write: None},
	"mcp_tokens":        {Read: None, Write: None},
}

// CanTable reports whether role may perform an HTTP method on a generated table.
func CanTable(role, table, method string) bool {
	p, ok := Generated[table]
	if !ok {
		return false
	}
	switch method {
	case http.MethodGet, http.MethodHead:
		return Role(role).AtLeast(p.Read)
	default:
		return Role(role).AtLeast(p.Write)
	}
}
