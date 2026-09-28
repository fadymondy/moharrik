package mcp

import "github.com/fadymondy/moharrik/internal/authz"

// ScopeDef is one grantable scope and the lowest workspace role that may hold it.
type ScopeDef struct {
	Scope       string     `json:"scope"`
	Description string     `json:"description"`
	MinRole     authz.Role `json:"min_role"`
}

/*
UserScopes is the closed list of user-MCP scopes (the plan's list). A token can
carry only these; a tool must declare one of these (scopes_test.go). There is
no "*": owners pick scopes like everyone else (an owner decision still open;
explicit scopes are the safe default until it is decided).

Scopes for tools that do not exist yet are listed so tokens can be minted
ahead of the phases that add tools — they grant nothing until a tool declares them.
*/
var UserScopes = []ScopeDef{
	{"members:read", "See the workspace, your role and its members (whoami, list_members)", authz.Member},
	// The workspace's own settings (MH-680), each on its authz line: renaming
	// it or changing its picture is authz.WorkspaceUpdate; inviting, revoking,
	// changing a role and removing a member is authz.MembersManage. Deleting
	// the workspace has no scope: it cannot be undone, so it stays on the page.
	{"workspace:update", "Rename the workspace and change its picture", authz.Admin},
	{"members:manage", "Invite people, revoke invitations, change members' roles and remove members", authz.Admin},
	{"contacts:read", "Read CRM contacts, companies, deals and the timeline", authz.Member},
	{"contacts:write", "Create and change CRM contacts, companies, deals and the timeline", authz.Member},
	// crm:manage is authz.PipelineManage's line (MH-395): the ideal-customer
	// profile decides how every contact in the workspace is ranked, the same
	// weight as reshaping the pipeline, so it is a manager's.
	// The pipeline's stages are the same line (MH-680).
	{"crm:manage", "Change the pipeline's stages and the ideal-customer profile every contact and company is scored against", authz.Manager},
	// Leads have their own pair (MH-390): a lead is unverified text from a
	// stranger's browser, and an agent that triages the inbound queue should
	// not need the whole CRM to do it. convert_lead needs contacts:write as
	// well, because converting is what creates the CRM rows.
	{"leads:read", "Read leads from website inquiry forms, with their message and attribution", authz.Member},
	{"leads:write", "Change a lead's status and convert it (converting also needs contacts:write)", authz.Member},
	{"inbox:read", "Read conversations across chat, email and WhatsApp, and the WhatsApp number's status", authz.Member},
	{"inbox:write", "Reply to, assign and close conversations", authz.Member},
	// inbox:manage is the line authz.InboxManage draws: linking the
	// workspace's WhatsApp number decides what can write into its inbox, the
	// same weight as creating a chat widget (MH-409).
	{"inbox:manage", "Link the workspace's WhatsApp number", authz.Manager},
	// whatsapp:send is its own scope, apart from inbox:write (MH-409):
	// answering somebody who wrote in is not the same act as an agent
	// messaging a customer first. It reaches only contacts who opted in to
	// WhatsApp and have not said STOP (MH-320), and a new thread needs a
	// one-time confirmation code.
	{"whatsapp:send", "Message an opted-in CRM contact on WhatsApp (a new thread needs a confirmation code)", authz.Member},
	{"campaigns:read", "Read email and WhatsApp campaigns and their results", authz.Member},
	{"campaigns:write", "Draft, edit and send campaigns", authz.Manager},
	// mail:settings is authz.MailSettings (MH-680): the SMTP services every
	// email leaves through, credentials included, so an admin's.
	{"mail:settings", "Add, change and remove the SMTP services mail is sent through, and choose the defaults", authz.Admin},
	{"automation:read", "Read automations, browser agents and their runs", authz.Member},
	{"automation:run", "Start and cancel automation runs", authz.Manager},
	// automation:manage is authz.AutomationManage (MH-478): changing where a
	// browser may go is the guardrail itself, so it is an admin's, exactly as
	// on the Automation page (browser_set_allowed_sites).
	{"automation:manage", "Change the sites a browser may drive", authz.Admin},
	// Notifications follow authz's own split (MH-380): marking and saving
	// items is a member right (notifications:write), while creating, rotating,
	// enabling or disabling a webhook source changes what the whole workspace
	// collects and shows a secret once, so it is a manager's (…:manage).
	{"notifications:read", "Read the notification center, its webhook sources and its collected sources", authz.Member},
	{"notifications:write", "Mark notifications read or unread, and save them for later", authz.Member},
	{"notifications:manage", "Create, rotate, enable and disable webhook sources; add, change and collect collected sources", authz.Manager},
	{"research:read", "Read research runs and collected evidence", authz.Member},
	{"research:run", "Start research runs", authz.Member},
	// Trends (MH-386): reading and saving are member rights, as in the REST
	// routes; adding or changing a feed, or fetching them all now, changes
	// what the whole workspace collects, so it is a manager's (authz.TrendsManage).
	{"trends:read", "Read sources, the catalog, the budget, collected items and trending topics", authz.Member},
	{"trends:write", "Save items, and review, dismiss or save trending topics", authz.Member},
	{"trends:manage", "Add and change sources, and collect now (spends the daily budget)", authz.Manager},
	// Google data (MH-447): what the workspace reads from GA4, Search Console
	// and YouTube. The write scope spends a site's shared URL-inspection
	// budget and submits sitemaps to Google, so it is a manager's, like
	// search_console:write in internal/authz.
	{"analytics:read", "Read the workspace's Google Analytics, Search Console and YouTube data", authz.Member},
	{"search_console:write", "Inspect URLs and submit sitemaps in Search Console", authz.Manager},
	// integrations:manage is authz.IntegrationsManage (MH-680): which Google
	// accounts stay connected and what the workspace reads through them.
	{"integrations:manage", "See and disconnect connected Google accounts, and switch their properties, sites and channels on or off", authz.Admin},
	// The audit log (MH-392): every member's changes with their addresses, so
	// the owners' and admins' — authz.AuditRead.
	{"audit:read", "Read the workspace's audit log: who changed what, when and from where", authz.Admin},
	{"audit:manage", "Change how long the audit log is kept", authz.Admin},
}

// AdminScopes is the closed list for the platform admin MCP. Admin tokens are
// held only by platform admins (users.roles contains "admin"); the scope still
// narrows what one token can do.
var AdminScopes = []ScopeDef{
	{"admin:workspaces:read", "List and inspect workspaces", authz.None},
	{"admin:workspaces:write", "Rename workspaces, change plans, add/remove members and roles", authz.None},
	{"admin:users:read", "List and inspect users", authz.None},
}

func userScope(name string) (ScopeDef, bool) {
	for _, s := range UserScopes {
		if s.Scope == name {
			return s, true
		}
	}
	return ScopeDef{}, false
}

func adminScope(name string) bool {
	for _, s := range AdminScopes {
		if s.Scope == name {
			return true
		}
	}
	return false
}

// RoleMayHold reports whether a workspace role may hold a user scope.
func RoleMayHold(role, scope string) bool {
	def, ok := userScope(scope)
	return ok && authz.Role(role).AtLeast(def.MinRole)
}

// Effective narrows a token's stored scopes to what its user's CURRENT role
// allows. A demoted member's token shrinks with them — an agent never exceeds
// its human.
func Effective(role string, stored []string) []string {
	out := []string{}
	for _, s := range stored {
		if RoleMayHold(role, s) {
			out = append(out, s)
		}
	}
	return out
}
