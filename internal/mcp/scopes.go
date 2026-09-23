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
	{"contacts:read", "Read CRM contacts, companies, deals and the timeline", authz.Member},
	{"contacts:write", "Create and change CRM contacts, companies, deals and the timeline", authz.Member},
	// Leads have their own pair (MH-390): a lead is unverified text from a
	// stranger's browser, and an agent that triages the inbound queue should
	// not need the whole CRM to do it. convert_lead needs contacts:write as
	// well, because converting is what creates the CRM rows.
	{"leads:read", "Read leads from website inquiry forms, with their message and attribution", authz.Member},
	{"leads:write", "Change a lead's status and convert it (converting also needs contacts:write)", authz.Member},
	{"inbox:read", "Read conversations across chat, email and WhatsApp", authz.Member},
	{"inbox:write", "Reply to, assign and close conversations", authz.Member},
	{"campaigns:read", "Read email and WhatsApp campaigns and their results", authz.Member},
	{"campaigns:write", "Draft, edit and send campaigns", authz.Manager},
	{"automation:read", "Read automations, browser agents and their runs", authz.Member},
	{"automation:run", "Start and cancel automation runs", authz.Manager},
	{"notifications:read", "Read the notification center", authz.Member},
	{"notifications:write", "Mark, save and configure notification sources and webhooks", authz.Manager},
	{"research:read", "Read research runs and collected evidence", authz.Member},
	{"research:run", "Start research runs", authz.Member},
	{"trends:read", "Read trend topics and news items", authz.Member},
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
