package automation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Automation"}

// pairingTTL is short on purpose: a pairing code is read off a screen and typed
// in straight away, and a code that is still good an hour later is a code
// somebody can still use an hour later.
const pairingTTL = 10 * time.Minute

// ---- wire types -----------------------------------------------------------

type AgentOut struct {
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Platform         string     `json:"platform"`
	BrowserVersion   string     `json:"browser_version"`
	ExtensionVersion string     `json:"extension_version"`
	Status           string     `json:"status" doc:"online | offline — live, from the socket registry."`
	AllowedHosts     []string   `json:"allowed_hosts" doc:"Empty allows nothing."`
	TokenPrefix      string     `json:"token_prefix"`
	LastSeenAt       time.Time  `json:"last_seen_at"`
	RevokedAt        *time.Time `json:"revoked_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

type PairingOut struct {
	ID            string    `json:"id"`
	RequestedName string    `json:"requested_name"`
	Platform      string    `json:"platform"`
	IP            string    `json:"ip"`
	Status        string    `json:"status"`
	ExpiresAt     time.Time `json:"expires_at"`
	CreatedAt     time.Time `json:"created_at"`
}

type SettingsOut struct {
	Paused   bool       `json:"paused"`
	Reason   string     `json:"reason"`
	PausedAt *time.Time `json:"paused_at"`
	PausedBy string     `json:"paused_by"`
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

// newCode returns the short code a person reads off the extension and types
// into the app. Digits only, in two groups, because it gets read aloud.
//
// Six digits is only a million possibilities, which is why the code is NOT what
// the extension polls with (that is a full-length token), why a pairing expires
// in ten minutes, and why approving one requires an admin who is looking at the
// request on screen.
func newCode() string {
	var b strings.Builder
	for i := 0; i < 6; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			panic("automation: no entropy for a pairing code: " + err.Error())
		}
		b.WriteByte(byte('0' + n.Int64()))
	}
	return b.String()
}

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
		Browsers []AgentOut   `json:"browsers"`
		Pairings []PairingOut `json:"pairings" doc:"Pairing requests still waiting for someone to approve them."`
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
			list = append(list, x)
		}

		pend, err := db().QueryContext(ctx, `
			SELECT id, requested_name, platform, ip, status, expires_at, created_at
			FROM agent_pairings
			WHERE workspace_id = $1 AND status = 'pending' AND expires_at > now()
			ORDER BY created_at DESC`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the pairing requests")
		}
		defer pend.Close()
		pairings := []PairingOut{}
		for pend.Next() {
			var p PairingOut
			if err := pend.Scan(&p.ID, &p.RequestedName, &p.Platform, &p.IP, &p.Status, &p.ExpiresAt, &p.CreatedAt); err != nil {
				return nil, huma.Error500InternalServerError("could not read the pairing requests")
			}
			pairings = append(pairings, p)
		}

		return out(struct {
			Browsers []AgentOut   `json:"browsers"`
			Pairings []PairingOut `json:"pairings" doc:"Pairing requests still waiting for someone to approve them."`
		}{list, pairings}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-approve-browser", Method: http.MethodPost, Path: "/api/w/browsers/approve",
		DefaultStatus: http.StatusCreated,
		Summary:       "Approve a pairing request by its code, and mint that browser's token", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Code         string   `json:"code" minLength:"4" maxLength:"12" doc:"The code shown in the extension."`
			Name         string   `json:"name,omitempty" maxLength:"80" doc:"What to call this browser. Defaults to what the extension suggested."`
			AllowedHosts []string `json:"allowed_hosts,omitempty" maxItems:"100" doc:"Where it may be driven. Empty allows nothing."`
		}
	}) (*body[struct {
		Browser AgentOut `json:"browser"`
	}], error) {
		// Pairing a browser is an ADMIN action: it hands a token to a program
		// that will drive a signed-in browser.
		s, err := scope(ctx, authz.AutomationManage)
		if err != nil {
			return nil, err
		}

		code := strings.TrimSpace(in.Body.Code)
		var pairingID, requested string
		var platform, browserVersion, extVersion, fingerprint string
		err = db().QueryRowContext(ctx, `
			SELECT id, requested_name, platform, browser_version, extension_version, fingerprint
			FROM agent_pairings
			WHERE workspace_id = $1 AND code_hash = $2 AND status = 'pending' AND expires_at > now()`,
			s.WorkspaceID, hash(code),
		).Scan(&pairingID, &requested, &platform, &browserVersion, &extVersion, &fingerprint)
		if errors.Is(err, sql.ErrNoRows) {
			// An expired code, a wrong code and a code from another workspace are
			// one answer on purpose.
			return nil, huma.Error404NotFound("that code is not valid — ask the extension for a new one")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the pairing request")
		}

		name := strings.TrimSpace(in.Body.Name)
		if name == "" {
			name = strings.TrimSpace(requested)
		}
		if name == "" {
			name = "Browser"
		}
		hosts := cleanHosts(in.Body.AllowedHosts)

		token, prefix := NewToken()
		var agentID string
		if err := db().QueryRowContext(ctx, `
			INSERT INTO browser_agents (workspace_id, name, fingerprint, platform, browser_version, extension_version,
			                            token_hash, token_prefix, allowed_hosts, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
			s.WorkspaceID, name, fingerprint, platform, browserVersion, extVersion,
			HashToken(token), prefix, pgtext.Literal(hosts), s.UserID,
		).Scan(&agentID); err != nil {
			return nil, huma.Error500InternalServerError("could not pair that browser")
		}

		// The token is left here for exactly one collection by the extension
		// that started this pairing, then blanked (see the claim route).
		if _, err := db().ExecContext(ctx, `
			UPDATE agent_pairings SET status = 'approved', agent_id = $3, token_once = $4, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, pairingID, agentID, token); err != nil {
			return nil, huma.Error500InternalServerError("could not complete the pairing")
		}

		return out(struct {
			Browser AgentOut `json:"browser"`
		}{AgentOut{ID: agentID, Name: name, Platform: platform, BrowserVersion: browserVersion,
			ExtensionVersion: extVersion, Status: "offline", AllowedHosts: hosts,
			TokenPrefix: prefix, LastSeenAt: time.Now().UTC(), CreatedAt: time.Now().UTC()}}), nil
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
		paused, reason, _ := IsPaused(ctx, a, s.WorkspaceID)
		return out(SettingsOut{Paused: paused, Reason: reason}), nil
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
			SELECT position, command, args::text, status, summary, result, error, duration_ms
			FROM automation_run_steps WHERE workspace_id = $1 AND run_id = $2 ORDER BY position`,
			s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the steps")
		}
		defer rows.Close()
		steps := []RunStep{}
		for rows.Next() {
			var st RunStep
			if err := rows.Scan(&st.Position, &st.Command, &st.Args, &st.Status, &st.Summary, &st.Result, &st.Error, &st.DurationMS); err != nil {
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

	// ---- pairing, from the extension (public) -----------------------------

	huma.Register(api, huma.Operation{
		OperationID: "pair-start", Method: http.MethodPost, Path: "/api/agents/pair/{workspace}",
		DefaultStatus: http.StatusCreated,
		Summary:       "Ask to pair this browser with a workspace (public: creates a request an admin must approve)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Workspace string `path:"workspace" doc:"The workspace slug, as shown on the Automation page."`
		RealIP    string `header:"X-Real-IP"`
		Body      struct {
			Name             string `json:"name,omitempty" maxLength:"80" doc:"A suggestion. The admin approving can change it."`
			Fingerprint      string `json:"fingerprint,omitempty" maxLength:"120"`
			Platform         string `json:"platform,omitempty" maxLength:"80"`
			BrowserVersion   string `json:"browser_version,omitempty" maxLength:"40"`
			ExtensionVersion string `json:"extension_version,omitempty" maxLength:"40"`
		}
	}) (*body[struct {
		Code      string    `json:"code" doc:"Read this out to whoever is approving."`
		PollToken string    `json:"poll_token" doc:"Keep this. It is what collects the browser's token, not the code."`
		ExpiresAt time.Time `json:"expires_at"`
	}], error) {
		// Unauthenticated on purpose: the extension has no credential yet. It
		// can create nothing but a REQUEST, which does nothing until an admin
		// of that workspace approves it while looking at it.
		wsID, err := workspaceBySlug(ctx, a.SysDB, in.Workspace)
		if err != nil {
			return nil, huma.Error404NotFound("no such workspace")
		}

		code := newCode()
		poll := newSecret("moh_pair_")
		sctx := tenant.WithWorkspace(ctx, wsID)
		if _, err := a.SQLDB.ExecContext(sctx, `
			INSERT INTO agent_pairings (workspace_id, code_hash, poll_hash, fingerprint, platform,
			                            browser_version, extension_version, requested_name, ip, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now() + $10::interval)`,
			wsID, hash(code), hash(poll), in.Body.Fingerprint, in.Body.Platform, in.Body.BrowserVersion,
			in.Body.ExtensionVersion, strings.TrimSpace(in.Body.Name), trim(in.RealIP, 60),
			pairingTTL.String()); err != nil {
			return nil, huma.Error500InternalServerError("could not start pairing")
		}
		return out(struct {
			Code      string    `json:"code" doc:"Read this out to whoever is approving."`
			PollToken string    `json:"poll_token" doc:"Keep this. It is what collects the browser's token, not the code."`
			ExpiresAt time.Time `json:"expires_at"`
		}{code, poll, time.Now().UTC().Add(pairingTTL)}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "pair-claim", Method: http.MethodPost, Path: "/api/agents/pair/claim",
		Summary: "Collect the token for an approved pairing (public: the poll token is the authorization)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		Body struct {
			PollToken string `json:"poll_token" minLength:"16" maxLength:"120"`
		}
	}) (*body[struct {
		Status string `json:"status" doc:"pending | approved | expired"`
		Token  string `json:"token,omitempty" doc:"Present exactly once, on the first call after approval."`
	}], error) {
		// The poll token, not the code: the code is short and gets read aloud,
		// and whoever overheard it could otherwise race the extension and
		// collect the browser's token first.
		//
		// This runs on the OWNER pool because the caller has no workspace and no
		// session — the unguessable token in the body is the authorization, the
		// same shape as a webhook id or a widget key. The predicate is explicit.
		var id, workspaceID, status, token string
		err := a.SysDB.QueryRowContext(ctx, `
			SELECT id, workspace_id, status, token_once FROM agent_pairings
			WHERE poll_hash = $1 AND expires_at > now()`, hash(in.Body.PollToken),
		).Scan(&id, &workspaceID, &status, &token)
		if errors.Is(err, sql.ErrNoRows) {
			return out(struct {
				Status string `json:"status" doc:"pending | approved | expired"`
				Token  string `json:"token,omitempty" doc:"Present exactly once, on the first call after approval."`
			}{"expired", ""}), nil
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the pairing")
		}
		if status != "approved" || token == "" {
			return out(struct {
				Status string `json:"status" doc:"pending | approved | expired"`
				Token  string `json:"token,omitempty" doc:"Present exactly once, on the first call after approval."`
			}{"pending", ""}), nil
		}
		// Handed over exactly once, then blanked: a token that stays readable is
		// a token a second caller can read.
		sctx := tenant.WithWorkspace(ctx, workspaceID)
		if _, err := a.SQLDB.ExecContext(sctx, `
			UPDATE agent_pairings SET status = 'claimed', token_once = '', updated_at = now()
			WHERE workspace_id = $1 AND id = $2 AND status = 'approved'`, workspaceID, id); err != nil {
			return nil, huma.Error500InternalServerError("could not complete the pairing")
		}
		return out(struct {
			Status string `json:"status" doc:"pending | approved | expired"`
			Token  string `json:"token,omitempty" doc:"Present exactly once, on the first call after approval."`
		}{"approved", token}), nil
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
	return x, nil
}

// workspaceBySlug resolves a slug with no session in hand, on the owner pool.
// It returns only the id: nothing about the workspace is revealed to a caller
// who guessed a slug.
func workspaceBySlug(ctx context.Context, sys *sql.DB, ref string) (string, error) {
	var id string
	err := sys.QueryRowContext(ctx, `SELECT id FROM workspaces WHERE slug = $1 OR id = $1`, strings.TrimSpace(ref)).Scan(&id)
	return id, err
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
	case errors.Is(err, ErrOffline):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrRevoked):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, ErrHostNotAllowed):
		return huma.Error422UnprocessableEntity(err.Error())
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
