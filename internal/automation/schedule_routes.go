package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
)

// RegisterSchedules mounts the schedule endpoints.
//
// Reading a schedule is automation:read; creating, changing or deleting one is
// automation:run — a schedule is a run that happens without anyone present, so
// it is not a lighter right than starting one by hand.
func RegisterSchedules(api huma.API, a *app.App, hub *Hub) {
	db := func() *sql.DB { return a.SQLDB }

	huma.Register(api, huma.Operation{
		OperationID: "w-schedules", Method: http.MethodGet, Path: "/api/w/schedules",
		Summary: "Recurring runs", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[struct {
		Schedules []Schedule `json:"schedules"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		rows, err := db().QueryContext(ctx, `
			SELECT s.id, s.name, s.agent_id, COALESCE(b.name, ''), s.cron, s.timezone, s.catch_up, s.enabled,
			       s.steps::text, s.next_run_at, s.last_run_at, s.last_status, s.last_error, s.last_run_id, s.created_at
			FROM automation_schedules s
			LEFT JOIN browser_agents b ON b.workspace_id = s.workspace_id AND b.id = s.agent_id
			WHERE s.workspace_id = $1 ORDER BY s.created_at DESC`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the schedules")
		}
		defer rows.Close()

		list := []Schedule{}
		for rows.Next() {
			var x Schedule
			var steps string
			var next time.Time
			var last sql.NullTime
			if err := rows.Scan(&x.ID, &x.Name, &x.AgentID, &x.AgentName, &x.Cron, &x.Timezone, &x.CatchUp, &x.Enabled,
				&steps, &next, &last, &x.LastStatus, &x.LastError, &x.LastRunID, &x.CreatedAt); err != nil {
				return nil, huma.Error500InternalServerError("could not read the schedules")
			}
			_ = json.Unmarshal([]byte(steps), &x.Steps)
			n := next
			x.NextRunAt = &n
			x.LastRunAt = timep(last)
			list = append(list, x)
		}
		return out(struct {
			Schedules []Schedule `json:"schedules"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-schedule", Method: http.MethodPost, Path: "/api/w/schedules",
		DefaultStatus: http.StatusCreated,
		Summary:       "Run a sequence of steps on a clock", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Name     string `json:"name" minLength:"1" maxLength:"80"`
			AgentID  string `json:"agent_id"`
			Cron     string `json:"cron" minLength:"1" maxLength:"100" doc:"Five-field cron, or a descriptor like @daily."`
			Timezone string `json:"timezone,omitempty" maxLength:"60" doc:"IANA name. Defaults to UTC."`
			CatchUp  string `json:"catch_up,omitempty" enum:"skip,once" doc:"What to do when the browser was shut at the due time."`
			Steps    []Step `json:"steps" maxItems:"200"`
		}
	}) (*body[struct {
		Schedule Schedule `json:"schedule"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		zone := firstNonEmpty(in.Body.Timezone, "UTC")
		catchUp := firstNonEmpty(in.Body.CatchUp, CatchUpSkip)
		if err := ValidateSchedule(in.Body.Cron, zone, catchUp); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}

		// The steps are checked against the browser's allow-list NOW, not only
		// when the schedule fires at 4am with nobody watching.
		var hosts pgtext.Array
		var revoked sql.NullTime
		err = db().QueryRowContext(ctx, `
			SELECT allowed_hosts, revoked_at FROM browser_agents WHERE workspace_id = $1 AND id = $2`,
			s.WorkspaceID, in.Body.AgentID).Scan(&hosts, &revoked)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("no such browser")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read that browser")
		}
		if revoked.Valid {
			return nil, huma.Error409Conflict(ErrRevoked.Error())
		}
		if err := CheckSteps(in.Body.Steps, hosts); err != nil {
			return nil, runError(err)
		}

		next, err := NextAfter(in.Body.Cron, zone, time.Now().UTC())
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		steps, err := json.Marshal(in.Body.Steps)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not store the steps")
		}

		var id string
		err = db().QueryRowContext(ctx, `
			INSERT INTO automation_schedules (workspace_id, name, agent_id, steps, cron, timezone, catch_up, next_run_at, created_by)
			VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7, $8, $9) RETURNING id`,
			s.WorkspaceID, strings.TrimSpace(in.Body.Name), in.Body.AgentID, string(steps),
			strings.TrimSpace(in.Body.Cron), zone, catchUp, next, s.UserID).Scan(&id)
		if pgCode(err) == "23505" {
			return nil, huma.Error409Conflict("a schedule with that name already exists")
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not save the schedule")
		}

		got, err := getSchedule(ctx, db(), s.WorkspaceID, id)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Schedule Schedule `json:"schedule"`
		}{got}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-schedule", Method: http.MethodPatch, Path: "/api/w/schedules/{id}",
		Summary: "Pause, resume or re-time a schedule", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Enabled  *bool   `json:"enabled,omitempty"`
			Cron     *string `json:"cron,omitempty" maxLength:"100"`
			Timezone *string `json:"timezone,omitempty" maxLength:"60"`
			CatchUp  *string `json:"catch_up,omitempty" enum:",skip,once"`
		}
	}) (*body[struct {
		Schedule Schedule `json:"schedule"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		cur, err := getSchedule(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, err
		}

		cronExpr, zone, catchUp := cur.Cron, cur.Timezone, cur.CatchUp
		if in.Body.Cron != nil {
			cronExpr = strings.TrimSpace(*in.Body.Cron)
		}
		if in.Body.Timezone != nil {
			zone = firstNonEmpty(*in.Body.Timezone, "UTC")
		}
		if in.Body.CatchUp != nil && *in.Body.CatchUp != "" {
			catchUp = *in.Body.CatchUp
		}
		if err := ValidateSchedule(cronExpr, zone, catchUp); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		// Re-timed from NOW: leaving the old next_run_at would fire the new
		// expression at the old moment once.
		next, err := NextAfter(cronExpr, zone, time.Now().UTC())
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		enabled := cur.Enabled
		if in.Body.Enabled != nil {
			enabled = *in.Body.Enabled
		}

		if _, err := db().ExecContext(ctx, `
			UPDATE automation_schedules SET cron = $3, timezone = $4, catch_up = $5, enabled = $6,
			    next_run_at = $7, last_error = CASE WHEN $6 THEN last_error ELSE last_error END, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`,
			s.WorkspaceID, in.ID, cronExpr, zone, catchUp, enabled, next); err != nil {
			return nil, huma.Error500InternalServerError("could not update the schedule")
		}
		got, err := getSchedule(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Schedule Schedule `json:"schedule"`
		}{got}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-schedule", Method: http.MethodDelete, Path: "/api/w/schedules/{id}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Delete a schedule", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		res, err := db().ExecContext(ctx, `DELETE FROM automation_schedules WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the schedule")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("no such schedule")
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-run-schedule", Method: http.MethodPost, Path: "/api/w/schedules/{id}/run",
		DefaultStatus: http.StatusCreated,
		Summary:       "Run a schedule now, without waiting for its time", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[struct {
		RunID string `json:"run_id"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		got, err := getSchedule(ctx, db(), s.WorkspaceID, in.ID)
		if err != nil {
			return nil, err
		}
		// Through the same Start as everything else: a schedule run by hand
		// gets the kill switch, the allow-list and one-at-a-time exactly like
		// any other run.
		runID, err := hub.Start(ctx, a, s.WorkspaceID, s.UserID, "schedule", got.AgentID, got.Steps)
		if err != nil {
			return nil, runError(err)
		}
		_, _ = db().ExecContext(ctx, `
			UPDATE automation_schedules SET last_run_at = now(), last_status = 'running', last_error = '', last_run_id = $3, updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID, runID)
		return out(struct {
			RunID string `json:"run_id"`
		}{runID}), nil
	})
}

func getSchedule(ctx context.Context, db *sql.DB, workspaceID, id string) (Schedule, error) {
	var x Schedule
	var steps string
	var next time.Time
	var last sql.NullTime
	err := db.QueryRowContext(ctx, `
		SELECT s.id, s.name, s.agent_id, COALESCE(b.name, ''), s.cron, s.timezone, s.catch_up, s.enabled,
		       s.steps::text, s.next_run_at, s.last_run_at, s.last_status, s.last_error, s.last_run_id, s.created_at
		FROM automation_schedules s
		LEFT JOIN browser_agents b ON b.workspace_id = s.workspace_id AND b.id = s.agent_id
		WHERE s.workspace_id = $1 AND s.id = $2`, workspaceID, id,
	).Scan(&x.ID, &x.Name, &x.AgentID, &x.AgentName, &x.Cron, &x.Timezone, &x.CatchUp, &x.Enabled,
		&steps, &next, &last, &x.LastStatus, &x.LastError, &x.LastRunID, &x.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return x, huma.Error404NotFound("no such schedule")
	}
	if err != nil {
		return x, huma.Error500InternalServerError("could not read that schedule")
	}
	_ = json.Unmarshal([]byte(steps), &x.Steps)
	n := next
	x.NextRunAt = &n
	x.LastRunAt = timep(last)
	return x, nil
}
