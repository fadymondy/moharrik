package automation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
A workflow's sites go onto the browser it runs in (MH-517).

Fady: "when workflow required to access a websites for the first run of
workflow it's must push to browser allow list all of the endpoints or domain
he required to connect to."

A workflow's REQUIRED SITES are the sites it declares (a preset's, or saved
over MCP) and the hosts of its navigation steps whose address is written out.
A host that only a value makes — goto {{url}} — is known once the run's values
are filled in, at run start.

When a run starts, whatever of that is missing from the browser's allowed
sites is added, pushed to the browser at once, recorded by the audit trigger,
and said as the run's first line: "Added news.google.com to Office Chrome's
allowed sites". With every site granted in Chrome (MH-483) the run then
proceeds with no click; without it, its first step on the new site waits on
the extension's "Waiting for your OK" rather than failing.

The guardrails:

  - It happens under the rights of whoever runs it — a schedule's creator, an
    MCP token's member — as they are NOW. Someone who cannot manage browsers
    gets a sentence saying which sites an admin has to add, not the bare
    "not on the allowed sites".
  - A run nobody watches (MCP, a schedule) adds only what the SAVED workflow
    declares, unless the workspace allows sites from values too
    (auto_add_input_sites): a value is not a decision anyone reviewed.
  - The workspace can switch it off (auto_add_sites, on by default).
  - MH-422 is untouched: every step is still checked against the browser's
    list, on the server and in the browser; this only changes the list first,
    visibly, the way an admin would on the Automation page.
*/

// SiteSettings are the workspace's switches for adding a workflow's sites.
type SiteSettings struct {
	AutoAdd    bool `json:"auto_add_sites"`
	FromInputs bool `json:"auto_add_input_sites"`
}

// ReadSiteSettings is the workspace's switches; on and off by default.
func ReadSiteSettings(ctx context.Context, a *app.App, workspaceID string) SiteSettings {
	s := SiteSettings{AutoAdd: true}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT auto_add_sites, auto_add_input_sites FROM automation_settings WHERE workspace_id = $1`,
		workspaceID).Scan(&s.AutoAdd, &s.FromInputs)
	return s
}

// SaveSiteSettings changes whichever switch is given.
func SaveSiteSettings(ctx context.Context, a *app.App, workspaceID string, autoAdd, fromInputs *bool) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, err := a.SQLDB.ExecContext(sctx, `
		INSERT INTO automation_settings (workspace_id, auto_add_sites, auto_add_input_sites)
		VALUES ($1, COALESCE($2, true), COALESCE($3, false))
		ON CONFLICT (workspace_id) DO UPDATE SET
		    auto_add_sites = COALESCE($2, automation_settings.auto_add_sites),
		    auto_add_input_sites = COALESCE($3, automation_settings.auto_add_input_sites),
		    updated_at = now()`, workspaceID, autoAdd, fromInputs)
	return err
}

// RequiredHosts is what a saved workflow says it needs: its declared sites
// and the written-out hosts of its navigation steps, normalised and sorted.
// Hosts that only a value makes are not in it.
func RequiredHosts(declared []string, steps []Step) []string {
	set := map[string]bool{}
	for _, h := range declared {
		if n := normaliseHost(h); n != "" && !strings.Contains(n, "{{") {
			set[n] = true
		}
	}
	for _, st := range steps {
		raw, _ := st.Args["url"].(string)
		if strings.Contains(hostPart(raw), "{{") {
			continue
		}
		if h, nav := StepHost(st.Command, st.Args); nav && h != "" && !strings.Contains(h, "{{") {
			set[h] = true
		}
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// hostPart is the address up to its path: where a {{placeholder}} would make
// the host rather than the path.
func hostPart(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

// runHosts is every host the filled-in steps go to.
func runHosts(steps []Step) []string {
	set := map[string]bool{}
	for _, st := range steps {
		if h, nav := StepHost(st.Command, st.Args); nav && h != "" {
			set[h] = true
		}
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// ErrSitesNeedAdmin is a run that needs sites its runner may not add.
var ErrSitesNeedAdmin = errors.New("needs sites that aren't on the browser's allowed sites yet")

// SitesNeedAdminError names them, and who can add them.
type SitesNeedAdminError struct {
	Hosts   []string
	Browser string
}

func (e *SitesNeedAdminError) Error() string {
	return fmt.Sprintf("this workflow needs %s, which %s may not visit yet, and only an admin can add sites to a browser: ask one to run it once, or to add them on the Automation page",
		strings.Join(e.Hosts, ", "), e.Browser)
}

func (e *SitesNeedAdminError) Is(target error) bool { return target == ErrSitesNeedAdmin }

// addRequiredSites adds to the browser what this run needs and the browser
// does not allow yet, within the guardrails above. Returns the note for the
// run's first line, or "" when nothing was added. An error means the run must
// not start.
func addRequiredSites(ctx context.Context, a *app.App, hub *Hub, workspaceID, userID, trigger, agentID string, declared []string, saved, run []Step) (string, error) {
	settings := ReadSiteSettings(ctx, a, workspaceID)
	if !settings.AutoAdd || hub == nil || !hub.Online(agentID) {
		return "", nil // an offline browser gets nothing added; Start says it is offline
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var name string
	var hosts pgtext.Array
	var revoked sql.NullTime
	if err := a.SQLDB.QueryRowContext(sctx, `SELECT name, allowed_hosts, revoked_at FROM browser_agents WHERE workspace_id = $1 AND id = $2`,
		workspaceID, agentID).Scan(&name, &hosts, &revoked); err != nil || revoked.Valid {
		return "", nil // Start says what is wrong with the browser
	}
	required := RequiredHosts(declared, saved)
	unattended := trigger == "schedule" || trigger == "mcp"
	var missing []string
	seen := map[string]bool{}
	for _, h := range append(append([]string{}, required...), runHosts(run)...) {
		if seen[h] || HostAllowed(hosts, h) {
			continue
		}
		seen[h] = true
		// A host only a value made, on a run nobody watches: not added unless
		// the workspace said so. Start then refuses it the usual way.
		if unattended && !settings.FromInputs && !HostAllowed(required, h) {
			continue
		}
		missing = append(missing, h)
	}
	if len(missing) == 0 {
		return "", nil
	}
	var role string
	_ = a.SysDB.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2 AND status = 'active'`,
		workspaceID, userID).Scan(&role)
	if !authz.Can(role, authz.AutomationManage) {
		return "", &SitesNeedAdminError{Hosts: missing, Browser: name}
	}
	if _, _, err := ChangeAllowedHosts(ctx, a, hub, workspaceID, agentID, HostChange{Add: missing}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Added %s to %s's allowed sites", strings.Join(missing, ", "), name), nil
}

// addRunNote puts a line at the top of a run's record.
func addRunNote(ctx context.Context, a *app.App, workspaceID, runID, note string) {
	if note == "" {
		return
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `UPDATE automation_runs SET notes = array_append(notes, $3) WHERE workspace_id = $1 AND id = $2`,
		workspaceID, runID, note)
}
