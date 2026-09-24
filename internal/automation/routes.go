package automation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Automation"}

// ---- wire types -----------------------------------------------------------

type AgentOut struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Platform         string `json:"platform"`
	BrowserVersion   string `json:"browser_version"`
	ExtensionVersion string `json:"extension_version"`
	// Older than the extension this server offers (MH-399). The extension
	// reports its version on every connect, so this changes by itself once
	// the browser is updated.
	UpdateAvailable bool       `json:"update_available"`
	Status          string     `json:"status" doc:"online | offline — live, from the socket registry."`
	AllowedHosts    []string   `json:"allowed_hosts" doc:"Empty allows nothing."`
	// Chrome's side (MH-483), live while the browser is connected: "all" —
	// a site added here works at once — or "sites", where each waits for an
	// OK in the extension; PendingHosts are this list's sites still waiting.
	// Empty when the browser is offline or too old to say.
	SiteAccess   string   `json:"site_access" doc:"all | sites | empty when not known."`
	PendingHosts []string `json:"pending_hosts" doc:"Allowed sites still waiting for an OK in the browser."`
	TokenPrefix     string     `json:"token_prefix"`
	LastSeenAt      time.Time  `json:"last_seen_at"`
	RevokedAt       *time.Time `json:"revoked_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

type SettingsOut struct {
	Paused   bool       `json:"paused"`
	Reason   string     `json:"reason"`
	PausedAt *time.Time `json:"paused_at"`
	PausedBy string     `json:"paused_by"`
	// The workspace's ceilings, and how much of the parallel one is in use
	// right now (MH-234). Carried here because a page that shows the kill
	// switch is the page that shows the limits.
	MaxParallelRuns int `json:"max_parallel_runs"`
	RetentionDays   int `json:"retention_days"`
	LiveRuns        int `json:"live_runs"`
}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

func scope(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

func timep(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	v := n.Time
	return &v
}

// ---- pairing codes --------------------------------------------------------

func newSecret(prefix string) string {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		panic("automation: no entropy: " + err.Error())
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw)
}

func hash(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

// ---- registration ---------------------------------------------------------

// Register mounts the automation endpoints. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App, hub *Hub) {
	db := func() *sql.DB { return a.SQLDB }

	// ---- browsers ---------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-browsers", Method: http.MethodGet, Path: "/api/w/browsers",
		Summary: "The browsers paired with this workspace", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[struct {
		Browsers []AgentOut `json:"browsers"`
		Latest   string     `json:"latest_extension_version" doc:"The extension version this server offers; empty when none is packaged."`
	}], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		rows, err := db().QueryContext(ctx, `
			SELECT id, name, platform, browser_version, extension_version, allowed_hosts, token_prefix,
			       last_seen_at, revoked_at, created_at
			FROM browser_agents WHERE workspace_id = $1 ORDER BY revoked_at NULLS FIRST, created_at DESC`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the browsers")
		}
		defer rows.Close()

		// Liveness comes from the socket registry, not from the status column:
		// the column is the last thing that was written, and a process that
		// restarted would otherwise show every browser as still online.
		online := hub.OnlineIn(s.WorkspaceID)
		access := hub.AccessIn(s.WorkspaceID)
		latest := LatestExtension()

		list := []AgentOut{}
		for rows.Next() {
			var x AgentOut
			var hosts pgtext.Array
			var revoked sql.NullTime
			if err := rows.Scan(&x.ID, &x.Name, &x.Platform, &x.BrowserVersion, &x.ExtensionVersion,
				&hosts, &x.TokenPrefix, &x.LastSeenAt, &revoked, &x.CreatedAt); err != nil {
				return nil, huma.Error500InternalServerError("could not read the browsers")
			}
			x.AllowedHosts = hosts
			if x.AllowedHosts == nil {
				x.AllowedHosts = []string{}
			}
			x.RevokedAt = timep(revoked)
			x.Status = "offline"
			if online[x.ID] {
				x.Status = "online"
			}
			x.UpdateAvailable = x.RevokedAt == nil && Outdated(x.ExtensionVersion, latest)
			applyAccess(&x, access)
			list = append(list, x)
		}

		latestVersion := ""
		if latest != nil {
			latestVersion = latest.Version
		}
		return out(struct {
			Browsers []AgentOut `json:"browsers"`
			Latest   string     `json:"latest_extension_version" doc:"The extension version this server offers; empty when none is packaged."`
		}{list, latestVersion}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-browser", Method: http.MethodPatch, Path: "/api/w/browsers/{id}",
		Summary: "Rename a browser, or change where it may be driven", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Name         *string   `json:"name,omitempty" maxLength:"80"`
			AllowedHosts *[]string `json:"allowed_hosts,omitempty" maxItems:"100"`
		}
	}) (*body[struct {
		Browser AgentOut `json:"browser"`
	}], error) {
		// Changing where a browser may be driven is the allow-list itself, so it
		// is an admin action like pairing.
		s, err := scope(ctx, authz.AutomationManage)
		if err != nil {
			return nil, err
		}
		if in.Body.Name != nil {
			name := strings.TrimSpace(*in.Body.Name)
			if name == "" {
				return nil, huma.Error422UnprocessableEntity("a browser needs a name")
			}
			if _, err := db().ExecContext(ctx, `
				UPDATE browser_agents SET name = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
				s.WorkspaceID, in.ID, name); err != nil {
				return nil, huma.Error500InternalServerError("could not rename that browser")
			}
		}
		if in.Body.AllowedHosts != nil {
			if _, err := db().ExecContext(ctx, `
				UPDATE browser_agents SET allowed_hosts = $3::text[], updated_at = now() WHERE workspace_id = $1 AND id = $2`,
				s.WorkspaceID, in.ID, pgtext.Literal(cleanHosts(*in.Body.AllowedHosts))); err != nil {
				return nil, huma.Error500InternalServerError("could not update the allow-list")
			}
		}
		got, err := getAgent(ctx, db(), hub, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, err
		}
		// The browser's popup and Sites page learn the change now, not at its
		// next reconnect (MH-478). Every step carries the list regardless.
		PushAgentUpdate(ctx, a, hub, s.WorkspaceID, in.ID)
		return out(struct {
			Browser AgentOut `json:"browser"`
		}{got}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-revoke-browser", Method: http.MethodDelete, Path: "/api/w/browsers/{id}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Revoke a browser's token and drop its socket", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.AutomationManage)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `
			UPDATE browser_agents SET revoked_at = now(), status = 'offline', updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND revoked_at IS NULL`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not revoke that browser")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("no such browser")
		}
		// A revoked browser that keeps its socket keeps its access.
		hub.Disconnect(in.ID, 4003, "this browser's token was revoked")
		return nil, nil
	})

	// ---- the kill switch --------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-automation-settings", Method: http.MethodGet, Path: "/api/w/automation/settings",
		Summary: "Whether automation is stopped for this workspace", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[SettingsOut], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		var o SettingsOut
		var pausedAt sql.NullTime
		err = db().QueryRowContext(ctx, `
			SELECT paused, reason, paused_at, paused_by FROM automation_settings WHERE workspace_id = $1`,
			s.WorkspaceID).Scan(&o.Paused, &o.Reason, &pausedAt, &o.PausedBy)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error500InternalServerError("could not read the setting")
		}
		o.PausedAt = timep(pausedAt)

		limits := ReadLimits(ctx, a, s.WorkspaceID)
		o.MaxParallelRuns = limits.MaxParallelRuns
		o.RetentionDays = limits.RetentionDays
		o.LiveRuns, _ = LiveRuns(ctx, a, s.WorkspaceID)
		return out(o), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-automation-pause", Method: http.MethodPost, Path: "/api/w/automation/settings",
		Summary: "Stop or resume all automation in this workspace", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Paused bool   `json:"paused"`
			Reason string `json:"reason,omitempty" maxLength:"200"`
			// Pointers, so "not sent" is distinguishable from "set to zero":
			// the pause button posts neither, and must not reset the limits
			// somebody configured (MH-234).
			MaxParallelRuns *int `json:"max_parallel_runs,omitempty" minimum:"1" maximum:"50"`
			RetentionDays   *int `json:"retention_days,omitempty" minimum:"0" maximum:"3650"`
		}
	}) (*body[SettingsOut], error) {
		s, err := scope(ctx, authz.AutomationManage)
		if err != nil {
			return nil, err
		}
		if in.Body.Paused {
			if err := Pause(ctx, a, s.WorkspaceID, s.UserID, in.Body.Reason); err != nil {
				return nil, huma.Error500InternalServerError("could not stop automation")
			}
			// Stopping has to STOP: cancel what is queued or running, and drop
			// the sockets. A flag that only refuses the next run is not a kill
			// switch.
			CancelWorkspaceRuns(ctx, a, s.WorkspaceID, "automation was stopped")
			hub.DisconnectWorkspace(s.WorkspaceID, "automation is stopped")
		} else if err := Resume(ctx, a, s.WorkspaceID); err != nil {
			return nil, huma.Error500InternalServerError("could not resume automation")
		}
		if in.Body.MaxParallelRuns != nil || in.Body.RetentionDays != nil {
			limits := ReadLimits(ctx, a, s.WorkspaceID)
			if in.Body.MaxParallelRuns != nil {
				limits.MaxParallelRuns = *in.Body.MaxParallelRuns
			}
			if in.Body.RetentionDays != nil {
				limits.RetentionDays = *in.Body.RetentionDays
			}
			if err := SetLimits(ctx, a, s.WorkspaceID, limits); err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
		}

		paused, reason, _ := IsPaused(ctx, a, s.WorkspaceID)
		limits := ReadLimits(ctx, a, s.WorkspaceID)
		live, _ := LiveRuns(ctx, a, s.WorkspaceID)
		return out(SettingsOut{
			Paused: paused, Reason: reason,
			MaxParallelRuns: limits.MaxParallelRuns,
			RetentionDays:   limits.RetentionDays,
			LiveRuns:        live,
		}), nil
	})

	// ---- runs -------------------------------------------------------------

	huma.Register(api, huma.Operation{
		OperationID: "w-runs", Method: http.MethodGet, Path: "/api/w/runs",
		Summary: "Recent runs", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Agent  string `query:"agent"`
		Status string `query:"status" enum:",queued,running,ok,failed,cancelled"`
		Limit  int    `query:"limit" minimum:"1" maximum:"200"`
	}) (*body[struct {
		Runs []Run `json:"runs"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		limit := in.Limit
		if limit == 0 {
			limit = 50
		}
		rows, err := db().QueryContext(ctx, `
			SELECT r.id, r.agent_id, COALESCE(b.name, ''), r.trigger, r.requested_by, r.status, r.error,
			       r.step_count, r.ok_count, r.started_at, r.finished_at, r.created_at
			FROM automation_runs r
			LEFT JOIN browser_agents b ON b.workspace_id = r.workspace_id AND b.id = r.agent_id
			WHERE r.workspace_id = $1
			  AND ($2 = '' OR r.agent_id = $2)
			  AND ($3 = '' OR r.status = $3)
			ORDER BY r.created_at DESC LIMIT $4`,
			s.WorkspaceID, in.Agent, in.Status, limit)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the runs")
		}
		defer rows.Close()
		list := []Run{}
		for rows.Next() {
			var r Run
			var started, finished sql.NullTime
			if err := rows.Scan(&r.ID, &r.AgentID, &r.AgentName, &r.Trigger, &r.RequestedBy, &r.Status, &r.Error,
				&r.StepCount, &r.OKCount, &started, &finished, &r.CreatedAt); err != nil {
				return nil, huma.Error500InternalServerError("could not read the runs")
			}
			r.StartedAt, r.FinishedAt = timep(started), timep(finished)
			list = append(list, r)
		}
		return out(struct {
			Runs []Run `json:"runs"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-run", Method: http.MethodGet, Path: "/api/w/runs/{id}",
		Summary: "One run and every step of it", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[struct {
		Run   Run       `json:"run"`
		Steps []RunStep `json:"steps"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		var r Run
		var started, finished sql.NullTime
		err = db().QueryRowContext(ctx, `
			SELECT r.id, r.agent_id, COALESCE(b.name, ''), r.trigger, r.requested_by, r.status, r.error,
			       r.step_count, r.ok_count, r.started_at, r.finished_at, r.created_at
			FROM automation_runs r
			LEFT JOIN browser_agents b ON b.workspace_id = r.workspace_id AND b.id = r.agent_id
			WHERE r.workspace_id = $1 AND r.id = $2`, s.WorkspaceID, in.ID,
		).Scan(&r.ID, &r.AgentID, &r.AgentName, &r.Trigger, &r.RequestedBy, &r.Status, &r.Error,
			&r.StepCount, &r.OKCount, &started, &finished, &r.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("no such run")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the run")
		}
		r.StartedAt, r.FinishedAt = timep(started), timep(finished)

		rows, err := db().QueryContext(ctx, `
			SELECT position, command, args::text, status, summary, result, error, duration_ms, screenshot_path <> ''
			FROM automation_run_steps WHERE workspace_id = $1 AND run_id = $2 ORDER BY position`,
			s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the steps")
		}
		defer rows.Close()
		steps := []RunStep{}
		for rows.Next() {
			var st RunStep
			if err := rows.Scan(&st.Position, &st.Command, &st.Args, &st.Status, &st.Summary, &st.Result, &st.Error, &st.DurationMS, &st.HasScreenshot); err != nil {
				return nil, huma.Error500InternalServerError("could not read the steps")
			}
			steps = append(steps, st)
		}
		return out(struct {
			Run   Run       `json:"run"`
			Steps []RunStep `json:"steps"`
		}{r, steps}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-start-run", Method: http.MethodPost, Path: "/api/w/runs",
		DefaultStatus: http.StatusCreated,
		Summary:       "Drive a browser through a sequence of steps", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			AgentID string `json:"agent_id"`
			Steps   []Step `json:"steps" maxItems:"200"`
		}
	}) (*body[struct {
		RunID string `json:"run_id"`
	}], error) {
		// Starting a run drives a signed-in browser: manager, not member.
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		runID, err := hub.Start(ctx, a, s.WorkspaceID, s.UserID, "manual", in.Body.AgentID, in.Body.Steps)
		if err != nil {
			return nil, runError(err)
		}
		return out(struct {
			RunID string `json:"run_id"`
		}{runID}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-cancel-run", Method: http.MethodPost, Path: "/api/w/runs/{id}/cancel",
		Summary: "Stop a run that is queued or going", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[struct {
		Cancelled bool `json:"cancelled"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `
			UPDATE automation_runs SET status = 'cancelled', error = 'cancelled', finished_at = now(), updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND status IN ('queued', 'running')`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not cancel that run")
		}
		n, _ := res.RowsAffected()
		return out(struct {
			Cancelled bool `json:"cancelled"`
		}{n > 0}), nil
	})

}

// ---- helpers --------------------------------------------------------------

func getAgent(ctx context.Context, db *sql.DB, hub *Hub, workspaceID, id string) (AgentOut, error) {
	var x AgentOut
	var hosts pgtext.Array
	var revoked sql.NullTime
	err := db.QueryRowContext(ctx, `
		SELECT id, name, platform, browser_version, extension_version, allowed_hosts, token_prefix,
		       last_seen_at, revoked_at, created_at
		FROM browser_agents WHERE workspace_id = $1 AND id = $2`, workspaceID, id,
	).Scan(&x.ID, &x.Name, &x.Platform, &x.BrowserVersion, &x.ExtensionVersion, &hosts, &x.TokenPrefix,
		&x.LastSeenAt, &revoked, &x.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return x, huma.Error404NotFound("no such browser")
	}
	if err != nil {
		return x, huma.Error500InternalServerError("could not read that browser")
	}
	x.AllowedHosts = hosts
	if x.AllowedHosts == nil {
		x.AllowedHosts = []string{}
	}
	x.RevokedAt = timep(revoked)
	x.Status = "offline"
	if hub.Online(x.ID) {
		x.Status = "online"
	}
	x.UpdateAvailable = x.RevokedAt == nil && Outdated(x.ExtensionVersion, LatestExtension())
	applyAccess(&x, hub.AccessIn(workspaceID))
	return x, nil
}

// applyAccess fills in what the browser last said about Chrome's grant. Only
// hosts still on this browser's list are reported waiting: one removed on the
// site since the browser spoke is no longer anyone's business.
func applyAccess(x *AgentOut, access map[string]SiteAccess) {
	x.PendingHosts = []string{}
	sa, ok := access[x.ID]
	if !ok {
		return
	}
	x.SiteAccess = sa.Mode
	for _, p := range sa.Pending {
		for _, h := range x.AllowedHosts {
			if normaliseHost(h) == p {
				x.PendingHosts = append(x.PendingHosts, p)
				break
			}
		}
	}
}

// cleanHosts normalises what someone typed and drops what cannot be a host, so
// a typo becomes "not allowed" rather than an entry that silently matches
// nothing (or worse, something).
func cleanHosts(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, v := range in {
		h := normaliseHost(v)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// runError turns the guardrails' own errors into the right status, so the UI
// can say what was refused instead of "something went wrong".
func runError(err error) error {
	switch {
	case errors.Is(err, ErrPaused):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrBusy):
		return huma.Error409Conflict(err.Error())
	// Distinct from ErrBusy on purpose: "that browser is occupied" is fixed by
	// choosing another one, "the workspace is full" is not (MH-234).
	case errors.Is(err, ErrAtCapacity):
		return huma.Error429TooManyRequests(err.Error())
	case errors.Is(err, ErrOffline):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrRevoked):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrHostNotAllowed):
		return huma.Error422UnprocessableEntity(err.Error())
	}
	var missing *MissingParam
	if errors.As(err, &missing) {
		return huma.Error422UnprocessableEntity(missing.Error())
	}
	var se *StepError
	if errors.As(err, &se) {
		return huma.Error422UnprocessableEntity(se.Error())
	}
	if strings.Contains(err.Error(), "no such browser") {
		return huma.Error404NotFound("no such browser")
	}
	return huma.Error500InternalServerError("could not start that run")
}

// pgCode returns a Postgres error code, so a unique-violation can be answered
// as a conflict rather than a 500.
func pgCode(err error) string {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code
	}
	return ""
}
