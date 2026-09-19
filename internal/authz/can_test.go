package authz

import (
	"net/http"
	"testing"
)

func TestRoleOrdering(t *testing.T) {
	cases := []struct {
		role, min Role
		want      bool
	}{
		{Owner, Admin, true},
		{Admin, Owner, false},
		{Member, Member, true},
		{Client, Member, false},
		{Owner, None, false},
		{Role("stranger"), Client, false},
	}
	for _, c := range cases {
		if got := c.role.AtLeast(c.min); got != c.want {
			t.Errorf("%q.AtLeast(%q) = %v, want %v", c.role, c.min, got, c.want)
		}
	}
}

func TestUnknownActionDenied(t *testing.T) {
	if Can("owner", "does:not:exist") {
		t.Fatal("an unknown action must be denied, even to the owner")
	}
}

func TestInviteNeedsAdmin(t *testing.T) {
	if Can("member", MembersInvite) || Can("manager", MembersInvite) {
		t.Fatal("members and managers must not invite")
	}
	if !Can("admin", MembersInvite) || !Can("owner", MembersInvite) {
		t.Fatal("admins and owners invite")
	}
}

func TestGeneratedDefaultDeny(t *testing.T) {
	if CanTable("owner", "some_new_table", http.MethodGet) {
		t.Fatal("a generated table not listed in Generated must be closed")
	}
	for table, p := range Generated {
		_ = p
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			if (table == "workspaces" || table == "workspace_members" || table == "invitations" || table == "mcp_tokens") &&
				CanTable("owner", table, m) {
				t.Errorf("%s %s must not be writable through generated CRUD", m, table)
			}
		}
	}
	if CanTable("owner", "mcp_tokens", http.MethodGet) {
		t.Error("token hashes must not be readable through generated CRUD")
	}
}
