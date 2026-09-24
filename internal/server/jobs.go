package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/research"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/trends"
)

/*
The server jobs a schedule can run (MH-478; fadymondy.com's FM-269 "job:" kinds).

Registered here because this is the one package that imports every package a
job lives in — automation must not import trends, google or research.

Each job runs inside its schedule's workspace, as the member who created the
schedule, through the same function a person reaches by hand: collecting the
sources is trends.CollectWorkspace, the button on the Trends page; research is
the research engine's Start, the Research page's. Nothing here is a second
implementation of anything.
*/
func registerJobs(a *app.App, engine *research.Engine) {
	// Collect every enabled news source of the workspace now, spending its
	// daily fetch budget like Collect now does.
	automation.RegisterJob(automation.Job{
		Key:        "trends.collect",
		Permission: authz.TrendsManage,
		Run: func(ctx context.Context, a *app.App, in automation.JobRun) automation.JobResult {
			res, err := trends.CollectWorkspace(asMember(ctx, a, in), a)
			if err != nil {
				return automation.JobResult{Status: automation.JobFailed, Detail: err.Error()}
			}
			if len(res.Sources) == 0 {
				return automation.JobResult{Status: automation.JobSkipped, Detail: "this workspace has no enabled sources"}
			}
			detail := fmt.Sprintf("%d new items from %d sources", res.Added, len(res.Sources))
			if res.OverBudget > 0 {
				detail += fmt.Sprintf("; %d waited for tomorrow's budget", res.OverBudget)
			}
			if res.Failed > 0 {
				return automation.JobResult{Status: automation.JobFailed, Detail: fmt.Sprintf("%s; %d could not be fetched", detail, res.Failed)}
			}
			return automation.JobResult{Status: automation.JobOK, Detail: detail}
		},
	})

	// Sync every switched-on Google property, site and channel of the workspace.
	automation.RegisterJob(automation.Job{
		Key:        "google.sync",
		Permission: authz.IntegrationsManage,
		Run: func(ctx context.Context, a *app.App, in automation.JobRun) automation.JobResult {
			list, err := google.ListResources(ctx, a, in.WorkspaceID, google.Filter{EnabledOnly: true})
			if err != nil {
				return automation.JobResult{Status: automation.JobFailed, Detail: err.Error()}
			}
			if len(list) == 0 {
				return automation.JobResult{Status: automation.JobSkipped, Detail: "no Google data is switched on in this workspace"}
			}
			var failed []string
			for _, r := range list {
				if err := google.RunSync(ctx, a, in.WorkspaceID, r.ID); err != nil {
					failed = append(failed, r.ExternalID+": "+err.Error())
				}
			}
			if len(failed) > 0 {
				return automation.JobResult{Status: automation.JobFailed, Detail: fmt.Sprintf("%d of %d failed — %s", len(failed), len(list), strings.Join(failed, "; "))}
			}
			return automation.JobResult{Status: automation.JobOK, Detail: fmt.Sprintf("%d synced", len(list))}
		},
	})

	// Ask the web a question on a clock ("what changed in our market this week").
	automation.RegisterJob(automation.Job{
		Key:        "research.run",
		Permission: authz.ResearchRun,
		Params:     []automation.Param{{Name: "topic", Required: true}},
		Run: func(ctx context.Context, a *app.App, in automation.JobRun) automation.JobResult {
			topic := strings.TrimSpace(in.Values["topic"])
			if topic == "" {
				return automation.JobResult{Status: automation.JobFailed, Detail: "this schedule has no topic"}
			}
			id, err := engine.Start(ctx, a, in.WorkspaceID, in.UserID, topic)
			if err != nil {
				return automation.JobResult{Status: automation.JobFailed, Detail: err.Error()}
			}
			return automation.JobResult{Status: automation.JobOK, Detail: "research started: " + id}
		},
	})

	// Delete this workspace's runs past its retention window now, rather than
	// at the next hourly sweep.
	automation.RegisterJob(automation.Job{
		Key:        "automation.retention",
		Permission: authz.AutomationManage,
		Run: func(ctx context.Context, a *app.App, in automation.JobRun) automation.JobResult {
			n, err := automation.SweepWorkspace(ctx, a, in.WorkspaceID, time.Now().UTC())
			if err != nil {
				return automation.JobResult{Status: automation.JobFailed, Detail: err.Error()}
			}
			return automation.JobResult{Status: automation.JobOK, Detail: fmt.Sprintf("%d old runs removed", n)}
		},
	})
}

// asMember is the job's context as the member who created the schedule, with
// their role in the workspace now — what the page's own handler would see.
func asMember(ctx context.Context, a *app.App, in automation.JobRun) context.Context {
	var role string
	_ = a.SysDB.QueryRowContext(ctx, `SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2 AND status = 'active'`,
		in.WorkspaceID, in.UserID).Scan(&role)
	return tenant.With(ctx, &tenant.Scope{WorkspaceID: in.WorkspaceID, UserID: in.UserID, Role: role, Via: "schedule"})
}
