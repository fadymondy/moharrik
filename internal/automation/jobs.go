package automation

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Server jobs on the automation clock (MH-478; fadymondy.com's FM-269).

Some recurring work is not a browser workflow: collecting the workspace's news
sources, syncing its Google data, running a research question, sweeping old
runs. It still wants exactly what a schedule already gives — a cron in a
timezone, "advance the clock first", no overlap, missed and failed runs
recorded on the row, the kill switch — so it is an ordinary automation_schedules
row with a job_key, fired by the one scheduler. There is no second scheduler.

Every job runs INSIDE ITS WORKSPACE: the scheduler hands it the schedule's
workspace and the member who created it, and the job's work runs on the app
pool under RLS like everything else. A job states the permission it needs,
checked when the schedule is saved (and against the creator's role again when
it fires), so a member cannot schedule on a clock what they could not do by
hand.

What differs from a workflow schedule is only what "fire" does:

  - the kill switch PAUSES a job rather than failing it: the row says "paused",
    stays due, and runs on the first tick after someone resumes.
  - a job this process does not know is recorded as missed, never disabled.
  - the job runs in its own goroutine, so a slow job does not hold up the
    workflow schedules due in the same tick.
*/

// Job statuses besides ok and failed. Each is a recorded reason for doing
// nothing, never an error.
const (
	JobOK      = "ok"
	JobFailed  = "failed"
	JobPaused  = "paused"
	JobSkipped = "skipped"
)

// JobResult is what a job reports back to its schedule row.
type JobResult struct {
	Status string
	Detail string
}

// JobRun is everything a job is given when it fires.
type JobRun struct {
	WorkspaceID string
	UserID      string
	ScheduleID  string
	Values      map[string]string
}

// Job is one kind of server work a schedule may run.
type Job struct {
	Key string
	// The authz action a member needs to schedule it — the one they would
	// need to do the same thing by hand.
	Permission string
	// The values it asks for, filled from the schedule (same shape as a
	// workflow's parameters; a job has no secret ones).
	Params []Param
	Run    func(ctx context.Context, a *app.App, in JobRun) JobResult
}

var (
	jobsMu sync.RWMutex
	jobs   = map[string]Job{}
)

// RegisterJob makes a job schedulable in this process. Called from server.Boot,
// which is the one place that imports every package a job lives in.
func RegisterJob(j Job) {
	jobsMu.Lock()
	defer jobsMu.Unlock()
	jobs[j.Key] = j
}

// LookupJob returns a registered job.
func LookupJob(key string) (Job, bool) {
	jobsMu.RLock()
	defer jobsMu.RUnlock()
	j, ok := jobs[key]
	return j, ok
}

// JobOut is a job as the schedule form offers it.
type JobOut struct {
	Key     string  `json:"key"`
	Params  []Param `json:"params"`
	Allowed bool    `json:"allowed" doc:"Whether the caller's role may schedule it."`
}

// Jobs lists the registered jobs, and whether a role may schedule each.
func Jobs(role string) []JobOut {
	jobsMu.RLock()
	defer jobsMu.RUnlock()
	out := make([]JobOut, 0, len(jobs))
	for _, j := range jobs {
		params := j.Params
		if params == nil {
			params = []Param{}
		}
		out = append(out, JobOut{Key: j.Key, Params: params, Allowed: authz.Can(role, j.Permission)})
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Key < out[k].Key })
	return out
}

// memberRole is the role a user holds in a workspace now, or "" — a schedule
// outlives the membership of whoever created it.
func memberRole(ctx context.Context, a *app.App, workspaceID, userID string) string {
	var role string
	_ = a.SysDB.QueryRowContext(ctx, `
		SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2 AND status = 'active'`,
		workspaceID, userID).Scan(&role)
	return role
}

// jobWG lets tests wait for the goroutines fireJob starts.
var jobWG sync.WaitGroup

// WaitJobs blocks until every job started by the scheduler has returned.
func WaitJobs() { jobWG.Wait() }

// RunJobNow starts a schedule's job by hand ("Run now"), with the same checks
// and the same record on the row as the clock.
func (h *Hub) RunJobNow(ctx context.Context, a *app.App, d due) error {
	sctx := tenant.WithWorkspace(ctx, d.WorkspaceID)
	j, ok := LookupJob(d.JobKey)
	if !ok {
		return ErrUnknownJob
	}
	if paused, why, _ := IsPaused(ctx, a, d.WorkspaceID); paused {
		return pausedErr(why)
	}
	if !h.claimJob(d.ID) {
		return ErrBusy
	}
	h.startJob(sctx, a, d, j)
	return nil
}

// fireJob is fire for a job row. The clock has already been advanced.
func (h *Hub) fireJob(ctx context.Context, a *app.App, d due) {
	sctx := tenant.WithWorkspace(ctx, d.WorkspaceID)
	j, ok := LookupJob(d.JobKey)
	if !ok {
		h.record(sctx, a, d, "missed", "this server does not run "+d.JobKey, "")
		return
	}
	if paused, why, _ := IsPaused(ctx, a, d.WorkspaceID); paused {
		// Recorded once, then quietly due again every tick until resumed.
		if d.LastStatus != JobPaused {
			_, _ = a.SQLDB.ExecContext(sctx, `
				UPDATE automation_schedules SET last_status = 'paused', last_error = $3, updated_at = now()
				WHERE workspace_id = $1 AND id = $2`, d.WorkspaceID, d.ID, trim("paused by the kill switch: "+why, 300))
			notify(a, d.WorkspaceID)
		}
		h.stayDue(sctx, a, d)
		return
	}
	if !h.claimJob(d.ID) {
		h.record(sctx, a, d, "missed", "the previous run was still going", "")
		return
	}
	h.startJob(sctx, a, d, j)
}

func (h *Hub) claimJob(scheduleID string) bool {
	h.jobsMu.Lock()
	defer h.jobsMu.Unlock()
	if h.jobsRunning == nil {
		h.jobsRunning = map[string]bool{}
	}
	if h.jobsRunning[scheduleID] {
		return false
	}
	h.jobsRunning[scheduleID] = true
	return true
}

func (h *Hub) startJob(sctx context.Context, a *app.App, d due, j Job) {
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE automation_schedules SET last_run_at = now(), last_status = 'running', last_error = '', updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, d.WorkspaceID, d.ID)
	notify(a, d.WorkspaceID)

	jobWG.Add(1)
	go func() {
		defer jobWG.Done()
		defer func() {
			h.jobsMu.Lock()
			delete(h.jobsRunning, d.ID)
			h.jobsMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(tenant.WithWorkspace(context.Background(), d.WorkspaceID), 30*time.Minute)
		defer cancel()

		res := JobResult{Status: JobFailed, Detail: "the job crashed"}
		func() {
			defer func() {
				if r := recover(); r != nil {
					a.Log.Error("scheduled job panicked", "schedule", d.ID, "job", d.JobKey, "panic", r)
				}
			}()
			// The creator's role now, not when they saved it: a member who
			// lost the right to do this by hand has lost it on a clock too.
			if !authz.Can(memberRole(ctx, a, d.WorkspaceID, d.RequestedBy), j.Permission) {
				res = JobResult{Status: JobFailed, Detail: "whoever created this schedule may no longer do this in the workspace"}
				return
			}
			res = j.Run(ctx, a, JobRun{WorkspaceID: d.WorkspaceID, UserID: d.RequestedBy, ScheduleID: d.ID, Values: d.Values})
		}()
		if res.Status == "" {
			res.Status = JobOK
		}
		sctx := tenant.WithWorkspace(context.Background(), d.WorkspaceID)
		_, _ = a.SQLDB.ExecContext(sctx, `
			UPDATE automation_schedules SET last_status = $3, last_error = $4,
			    failure_count = CASE WHEN $3 = 'failed' THEN failure_count + 1 WHEN $3 = 'ok' THEN 0 ELSE failure_count END,
			    updated_at = now()
			WHERE workspace_id = $1 AND id = $2`, d.WorkspaceID, d.ID, res.Status, trim(res.Detail, 500))
		if res.Status == JobPaused {
			// The job itself saw the switch thrown mid-run: stay due.
			h.stayDue(sctx, a, d)
		}
		h.warnIfFailing(sctx, a, d.WorkspaceID, d.ID)
		notify(a, d.WorkspaceID)
	}()
}

// ErrUnknownJob is a job key this server does not run.
var ErrUnknownJob = errorString("this server does not run that job")

type errorString string

func (e errorString) Error() string { return string(e) }

func pausedErr(why string) error {
	if strings.TrimSpace(why) == "" {
		return ErrPaused
	}
	return wrapPaused{why}
}

type wrapPaused struct{ why string }

func (w wrapPaused) Error() string { return ErrPaused.Error() + ": " + w.why }
func (w wrapPaused) Unwrap() error { return ErrPaused }
