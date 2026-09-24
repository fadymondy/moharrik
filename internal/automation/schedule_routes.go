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
	"github.com/fadymondy/moharrik/internal/tenant"
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
		rows, err := db().QueryContext(ctx, scheduleSelect+`
			WHERE s.workspace_id = $1 ORDER BY s.created_at DESC`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the schedules")
		}
		defer rows.Close()

		list := []Schedule{}
		for rows.Next() {
			x, err := scanSchedule(rows)
			if err != nil {
				return nil, huma.Error500InternalServerError("could not read the schedules")
			}
			list = append(list, x)
		}
		return out(struct {
			Schedules []Schedule `json:"schedules"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-automation-jobs", Method: http.MethodGet, Path: "/api/w/automation/jobs",
		Summary: "The server jobs a schedule can run, and whether your role may schedule each (MH-478)", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[struct {
		Jobs []JobOut `json:"jobs"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Jobs []JobOut `json:"jobs"`
		}{Jobs(s.Role)}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-schedule", Method: http.MethodPost, Path: "/api/w/schedules",
		DefaultStatus: http.StatusCreated,
		Summary:       "Run a sequence of steps on a clock", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Name     string `json:"name" minLength:"1" maxLength:"80"`
			AgentID  string `json:"agent_id,omitempty" doc:"Empty: any connected browser of the workspace."`
			Cron     string `json:"cron" minLength:"1" maxLength:"100" doc:"Five-field cron, or a descriptor like @daily."`
			Timezone string `json:"timezone,omitempty" maxLength:"60" doc:"IANA name. Defaults to UTC."`
			CatchUp  string `json:"catch_up,omitempty" enum:"skip,once" doc:"What to do when the browser was shut at the due time."`
			Steps    []Step `json:"steps,omitempty" maxItems:"200" doc:"The steps to run. Leave empty and give workflow_id instead."`
			// A schedule either carries its own steps or runs a saved workflow —
			// or a server job (MH-478), which takes no browser at all.
			WorkflowID string            `json:"workflow_id,omitempty"`
			JobKey     string            `json:"job_key,omitempty" maxLength:"60"`
			Values     map[string]string `json:"values,omitempty" doc:"Values for the workflow's placeholders."`
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

		var agentArg any // NULL: any connected browser of the workspace (MH-478)
		if in.Body.AgentID != "" {
			agentArg = in.Body.AgentID
		}
		plainValues, sealed := map[string]string{}, ""

		if in.Body.JobKey != "" {
			// A server job: no browser, no steps, no workflow.
			if in.Body.WorkflowID != "" || len(in.Body.Steps) > 0 || in.Body.AgentID != "" {
				return nil, huma.Error422UnprocessableEntity("a job schedule runs on the server: it takes no browser, steps or workflow")
			}
			j, ok := LookupJob(in.Body.JobKey)
			if !ok {
				return nil, huma.Error422UnprocessableEntity("this server does not run the job " + in.Body.JobKey)
			}
			// What a member may not do by hand, they may not put on a clock.
			if !authz.Can(s.Role, j.Permission) {
				return nil, huma.Error403Forbidden("your role cannot do what this job does in this workspace")
			}
			if _, _, ferr := Fill(nil, j.Params, in.Body.Values); ferr != nil {
				return nil, huma.Error422UnprocessableEntity(ferr.Error())
			}
			for k, v := range in.Body.Values {
				plainValues[k] = v
			}
		} else {
			// A workflow's steps are filled in here too, so the allow-list check
			// below sees the hosts this schedule will ACTUALLY visit rather than
			// a placeholder that could be anything.
			steps2 := in.Body.Steps
			if in.Body.WorkflowID != "" {
				if len(in.Body.Steps) > 0 {
					return nil, huma.Error422UnprocessableEntity("a schedule runs its own steps or a saved workflow, not both")
				}
				w, err := getWorkflow(ctx, db(), s.WorkspaceID, in.Body.WorkflowID)
				if err != nil {
					return nil, err
				}
				filled, _, ferr := Fill(w.Steps, w.Params, in.Body.Values)
				if ferr != nil {
					return nil, huma.Error422UnprocessableEntity(ferr.Error())
				}
				steps2 = filled
				// The secret values are sealed here and never stored in the clear
				// (schedule_values.go).
				plainValues, sealed, err = SplitScheduleValues(w.Params, in.Body.Values)
				if err != nil {
					return nil, huma.Error500InternalServerError("could not keep the schedule's secret values")
				}
			}
			if len(steps2) == 0 {
				return nil, huma.Error422UnprocessableEntity("a schedule needs steps, a workflow or a job to run")
			}
			// The steps are checked against the allowed sites NOW, not only when
			// the schedule fires at 4am with nobody watching: the named browser's,
			// or — with none named — at least one browser's of the workspace.
			if err := checkScheduleHosts(ctx, db(), s.WorkspaceID, in.Body.AgentID, steps2); err != nil {
				return nil, err
			}
		}

		next, err := NextAfter(in.Body.Cron, zone, time.Now().UTC())
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		ownSteps := in.Body.Steps
		if ownSteps == nil {
			ownSteps = []Step{}
		}
		steps, err := json.Marshal(ownSteps)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not store the steps")
		}

		var id string
		err = db().QueryRowContext(ctx, `
			INSERT INTO automation_schedules (workspace_id, name, agent_id, steps, cron, timezone, catch_up, next_run_at, created_by, workflow_id, params, secret_values, job_key)
			VALUES ($1, $2, $3, $4::jsonb, $5, $6, $7, $8, $9, $10, $11::jsonb, $12, $13) RETURNING id`,
			s.WorkspaceID, strings.TrimSpace(in.Body.Name), agentArg, string(steps),
			strings.TrimSpace(in.Body.Cron), zone, catchUp, next, s.UserID,
			in.Body.WorkflowID, jsonOrEmpty(plainValues), sealed, in.Body.JobKey).Scan(&id)
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
		Summary: "Edit a schedule: pause or resume it, re-time it, or change its name, browser, workflow and values",
		Description: "Only what is sent changes. values replaces the whole set of the workflow's values; a SECRET value " +
			"left out or blank keeps the one already sealed, and a new one replaces it. Changing the workflow, its values or " +
			"the browser checks the steps against the browser's allow-list again, exactly as creating does (MH-471).",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Enabled    *bool             `json:"enabled,omitempty"`
			Cron       *string           `json:"cron,omitempty" maxLength:"100"`
			Timezone   *string           `json:"timezone,omitempty" maxLength:"60"`
			CatchUp    *string           `json:"catch_up,omitempty" enum:",skip,once"`
			Name       *string           `json:"name,omitempty" maxLength:"80"`
			AgentID    *string           `json:"agent_id,omitempty" doc:"Another browser, or empty for any connected browser (MH-478)."`
			WorkflowID *string           `json:"workflow_id,omitempty" doc:"Run this saved workflow instead. A schedule of its own steps can be switched to a workflow; not back."`
			Values     map[string]string `json:"values,omitempty" doc:"The workflow's values. A blank secret keeps the sealed one."`
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
		name := cur.Name
		if in.Body.Name != nil {
			if name = strings.TrimSpace(*in.Body.Name); name == "" {
				return nil, huma.Error422UnprocessableEntity("a schedule needs a name")
			}
		}
		agentID := cur.AgentID
		if in.Body.AgentID != nil && cur.JobKey == "" {
			agentID = strings.TrimSpace(*in.Body.AgentID) // "" is any connected browser
		}
		workflowID := cur.WorkflowID
		if in.Body.WorkflowID != nil && strings.TrimSpace(*in.Body.WorkflowID) != "" {
			workflowID = strings.TrimSpace(*in.Body.WorkflowID)
		}

		// What it runs, when that is being changed: the values sealed again,
		// the steps filled and checked against the browser like a new schedule.
		ownSteps, plainJSON, sealed := "", "", ""
		// A server job runs in no browser and has no steps to check.
		whatChanged := cur.JobKey == "" && (workflowID != cur.WorkflowID || in.Body.Values != nil || agentID != cur.AgentID)
		if whatChanged {
			steps := cur.Steps
			if workflowID != "" {
				w, err := getWorkflow(ctx, db(), s.WorkspaceID, workflowID)
				if err != nil {
					return nil, err
				}
				var oldSealed string
				if err := db().QueryRowContext(ctx, `SELECT secret_values FROM automation_schedules WHERE workspace_id = $1 AND id = $2`,
					s.WorkspaceID, in.ID).Scan(&oldSealed); err != nil {
					return nil, huma.Error500InternalServerError("could not read that schedule")
				}
				given := in.Body.Values
				if given == nil {
					given = cur.Values // the browser or the workflow changed; the values did not
				}
				merged, plain, sealedNow, err := EditScheduleValues(w.Params, given, oldSealed)
				if err != nil {
					return nil, huma.Error500InternalServerError("could not keep the schedule's secret values")
				}
				filled, _, ferr := Fill(w.Steps, w.Params, merged)
				if ferr != nil {
					return nil, huma.Error422UnprocessableEntity(ferr.Error())
				}
				steps = filled
				plainJSON, sealed, ownSteps = jsonOrEmpty(plain), sealedNow, "[]"
			}
			if len(steps) == 0 {
				return nil, huma.Error422UnprocessableEntity("a schedule needs steps, or a workflow to run")
			}
			// A named browser must allow every step; "any browser" needs one
			// of the workspace's browsers that does (MH-478).
			if err := checkScheduleHosts(ctx, db(), s.WorkspaceID, agentID, steps); err != nil {
				return nil, err
			}
		}

		// The run columns change only when what it runs was edited ($9) and a
		// workflow is what it runs: a schedule of its own steps keeps them.
		_, err = db().ExecContext(ctx, `
			UPDATE automation_schedules SET cron = $3, timezone = $4, catch_up = $5, enabled = $6,
			    next_run_at = $7, name = $8, agent_id = NULLIF($10, ''), workflow_id = $11,
			    steps = CASE WHEN $9 AND $11 <> '' THEN $12::jsonb ELSE steps END,
			    params = CASE WHEN $9 AND $11 <> '' THEN $13::jsonb ELSE params END,
			    secret_values = CASE WHEN $9 AND $11 <> '' THEN $14 ELSE secret_values END,
			    updated_at = now()
			WHERE workspace_id = $1 AND id = $2`,
			s.WorkspaceID, in.ID, cronExpr, zone, catchUp, enabled, next, name, whatChanged, agentID, workflowID,
			firstNonEmpty(ownSteps, "[]"), firstNonEmpty(plainJSON, "{}"), sealed)
		if pgCode(err) == "23505" {
			return nil, huma.Error409Conflict("a schedule with that name already exists")
		}
		if err != nil {
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
		OperationID: "w-schedule-preview", Method: http.MethodGet, Path: "/api/w/schedules/preview",
		Summary: "The next times a cron expression would run, before it is saved (MH-403)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Cron     string `query:"cron" maxLength:"100" doc:"Five-field cron, or a descriptor like @daily."`
		Timezone string `query:"timezone" maxLength:"60" doc:"IANA name. Defaults to the workspace's time zone."`
		Count    int    `query:"count" minimum:"0" maximum:"20" doc:"How many times to list; 5 when not given."`
	}) (*body[SchedulePreview], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		zone := strings.TrimSpace(in.Timezone)
		if zone == "" {
			// The workspace's own zone, which is what the form offers by default.
			_ = db().QueryRowContext(ctx, `SELECT timezone FROM workspaces WHERE id = $1`, s.WorkspaceID).Scan(&zone)
		}
		zone = firstNonEmpty(zone, "UTC")
		n := in.Count
		if n == 0 {
			n = 5
		}
		times, err := NextTimes(in.Cron, zone, time.Now().UTC(), n)
		if err != nil {
			// Answered, not refused: the form asks on every keystroke, and "not
			// a cron expression yet" is the ordinary state of a field someone
			// is still typing into.
			return out(SchedulePreview{Timezone: zone, Valid: false, Error: err.Error(), Times: []time.Time{}}), nil
		}
		return out(SchedulePreview{Timezone: zone, Valid: true, Times: times}), nil
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
		// A server job runs in the background, recorded on the row exactly as
		// the clock would record it (MH-478).
		if got.JobKey != "" {
			if j, ok := LookupJob(got.JobKey); ok && !authz.Can(s.Role, j.Permission) {
				return nil, huma.Error403Forbidden("your role cannot do what this job does in this workspace")
			}
			d, derr := loadDue(ctx, a, s.WorkspaceID, in.ID)
			if derr != nil {
				return nil, huma.Error500InternalServerError("could not read that schedule")
			}
			if err := hub.RunJobNow(ctx, a, d); err != nil {
				if errors.Is(err, ErrUnknownJob) {
					return nil, huma.Error409Conflict(err.Error())
				}
				if errors.Is(err, ErrBusy) {
					return nil, huma.Error409Conflict("this job is already running")
				}
				return nil, runError(err)
			}
			return out(struct {
				RunID string `json:"run_id"`
			}{""}), nil
		}
		// Through the same Start as everything else: a schedule run by hand
		// gets the kill switch, the allow-list and one-at-a-time exactly like
		// any other run — on its named browser, or on whichever connected one
		// takes it when it names none.
		d, derr := loadDue(ctx, a, s.WorkspaceID, in.ID)
		if derr != nil {
			return nil, huma.Error409Conflict("this schedule's secret values can no longer be read; create it again")
		}
		d.RequestedBy = s.UserID
		candidates, why := hub.pickAgents(ctx, a, d)
		if len(candidates) == 0 {
			return nil, huma.Error409Conflict(why)
		}
		runID, err := hub.startScheduled(ctx, a, d, candidates)
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

// SchedulePreview is what the form shows under the cron field.
type SchedulePreview struct {
	Timezone string      `json:"timezone"`
	Valid    bool        `json:"valid"`
	Error    string      `json:"error,omitempty"`
	Times    []time.Time `json:"times" doc:"UTC instants; the page renders them in timezone."`
}

// scheduleSelect is every column a Schedule is read from. The sealed secret
// values are read only to name which are set; they never leave this package.
const scheduleSelect = `
	SELECT s.id, s.name, COALESCE(s.agent_id, ''), COALESCE(b.name, ''), s.cron, s.timezone, s.catch_up, s.enabled,
	       s.steps::text, s.next_run_at, s.last_run_at, s.last_status, s.last_error, s.last_run_id, s.created_at,
	       s.workflow_id, COALESCE(w.name, ''), s.params::text, s.secret_values,
	       s.job_key, s.failure_count, s.missed_count
	FROM automation_schedules s
	LEFT JOIN browser_agents b ON b.workspace_id = s.workspace_id AND b.id = s.agent_id
	LEFT JOIN automation_workflows w ON w.workspace_id = s.workspace_id AND w.id = s.workflow_id`

func scanSchedule(r rowScanner) (Schedule, error) {
	var x Schedule
	var steps, values, sealed string
	var next time.Time
	var last sql.NullTime
	if err := r.Scan(&x.ID, &x.Name, &x.AgentID, &x.AgentName, &x.Cron, &x.Timezone, &x.CatchUp, &x.Enabled,
		&steps, &next, &last, &x.LastStatus, &x.LastError, &x.LastRunID, &x.CreatedAt,
		&x.WorkflowID, &x.WorkflowName, &values, &sealed,
		&x.JobKey, &x.FailureCount, &x.MissedCount); err != nil {
		return x, err
	}
	x.Failing = x.FailureCount >= failureNotifyAt
	_ = json.Unmarshal([]byte(steps), &x.Steps)
	if x.Steps == nil {
		x.Steps = []Step{}
	}
	_ = json.Unmarshal([]byte(values), &x.Values)
	if x.Values == nil {
		x.Values = map[string]string{}
	}
	x.SecretParams = sealedNames(sealed)
	n := next
	x.NextRunAt = &n
	x.LastRunAt = timep(last)
	return x, nil
}

func getSchedule(ctx context.Context, db *sql.DB, workspaceID, id string) (Schedule, error) {
	x, err := scanSchedule(db.QueryRowContext(ctx, scheduleSelect+`
		WHERE s.workspace_id = $1 AND s.id = $2`, workspaceID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return x, huma.Error404NotFound("no such schedule")
	}
	if err != nil {
		return x, huma.Error500InternalServerError("could not read that schedule")
	}
	return x, nil
}

// scheduleValues is every value a schedule fills its workflow with, the
// sealed ones opened — for running it, and nothing else.
func scheduleValues(ctx context.Context, db *sql.DB, workspaceID, id string) (map[string]string, error) {
	var values, sealed string
	if err := db.QueryRowContext(ctx, `
		SELECT params::text, secret_values FROM automation_schedules WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id).Scan(&values, &sealed); err != nil {
		return nil, err
	}
	plain := map[string]string{}
	_ = json.Unmarshal([]byte(values), &plain)
	return MergeScheduleValues(plain, sealed)
}

// jsonOrEmpty renders a value map for storage, never NULL: the scheduler reads
// this column into a map and a NULL would be one more nil to handle.
func jsonOrEmpty(v map[string]string) string {
	if len(v) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// checkScheduleHosts refuses a schedule whose steps no browser may run: the
// named one's allowed sites, or — with none named — at least one of the
// workspace's browsers' (MH-478).
func checkScheduleHosts(ctx context.Context, db *sql.DB, workspaceID, agentID string, steps []Step) error {
	if agentID != "" {
		var hosts pgtext.Array
		var revoked sql.NullTime
		err := db.QueryRowContext(ctx, `
			SELECT allowed_hosts, revoked_at FROM browser_agents WHERE workspace_id = $1 AND id = $2`,
			workspaceID, agentID).Scan(&hosts, &revoked)
		if errors.Is(err, sql.ErrNoRows) {
			return huma.Error404NotFound("no such browser")
		}
		if err != nil {
			return huma.Error500InternalServerError("could not read that browser")
		}
		if revoked.Valid {
			return huma.Error409Conflict(ErrRevoked.Error())
		}
		if err := CheckSteps(steps, hosts); err != nil {
			return runError(err)
		}
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT allowed_hosts FROM browser_agents WHERE workspace_id = $1 AND revoked_at IS NULL`, workspaceID)
	if err != nil {
		return huma.Error500InternalServerError("could not read the browsers")
	}
	defer rows.Close()
	var firstErr error = huma.Error422UnprocessableEntity("this workspace has no browser to run it in yet")
	for rows.Next() {
		var hosts pgtext.Array
		if rows.Scan(&hosts) != nil {
			continue
		}
		if err := CheckSteps(steps, hosts); err == nil {
			return nil
		} else {
			firstErr = runError(err)
		}
	}
	return firstErr
}

// loadDue reads one schedule as the scheduler sees it.
func loadDue(ctx context.Context, a *app.App, workspaceID, id string) (due, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	return scanDue(a.SQLDB.QueryRowContext(sctx, `SELECT `+dueColumns+` FROM automation_schedules WHERE workspace_id = $1 AND id = $2`, workspaceID, id))
}
