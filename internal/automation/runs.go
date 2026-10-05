package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/channels"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// ErrRevoked marks a token that exists but has been taken away.
var ErrRevoked = errors.New("this browser's token was revoked")

// Step is one command as submitted.
type Step struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
	// ContinueOnError keeps the run going when this step fails. Off by default:
	// a run whose first step failed is usually not worth continuing, and a step
	// sequence that carries on regardless produces results nobody can trust.
	ContinueOnError bool `json:"continue_on_error,omitempty"`
	// TimeoutSeconds lets one slow step (an upload, a page that loads
	// forever) wait longer than DefaultStepTimeout, as fadymondy.com's
	// plugins do. Capped at MaxStepTimeout so no step holds a browser for good.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// MaxStepTimeout is the longest any one step may wait for its browser.
const MaxStepTimeout = 5 * time.Minute

// stepTimeout is how long the server waits for this step's reply.
func (s Step) stepTimeout() time.Duration {
	if s.TimeoutSeconds <= 0 {
		return DefaultStepTimeout
	}
	d := time.Duration(s.TimeoutSeconds) * time.Second
	if d > MaxStepTimeout {
		return MaxStepTimeout
	}
	return d
}

// Run is the record of what a browser was asked to do.
type Run struct {
	ID          string     `json:"id"`
	AgentID     string     `json:"agent_id"`
	AgentName   string     `json:"agent_name"`
	Trigger     string     `json:"trigger"`
	RequestedBy string     `json:"requested_by"`
	Status      string     `json:"status"`
	Error       string     `json:"error"`
	StepCount   int        `json:"step_count"`
	OKCount     int        `json:"ok_count"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	CreatedAt   time.Time  `json:"created_at"`
	// Which workflow — and which version of it — and which schedule this run
	// came from, so history reads the same after the workflow is edited (MH-478).
	WorkflowID      string `json:"workflow_id"`
	WorkflowName    string `json:"workflow_name"`
	WorkflowVersion int    `json:"workflow_version"`
	ScheduleID      string `json:"schedule_id"`
	ScheduleName    string `json:"schedule_name"`
	// How many of its steps kept a screenshot or extracted data.
	Screenshots int `json:"screenshots"`
	Extracts    int `json:"extracts"`
	// Lines about the run itself, first of all what was done before its first
	// step: "Added news.google.com to Office Chrome's allowed sites" (MH-517).
	Notes []string `json:"notes"`
	// The run this one repeated, when started with "run again" (MH-1067). Empty
	// when it is not a re-run or the original was pruned.
	RerunOf string `json:"rerun_of"`
}

// runSelect is every column a Run is read from, joined with the names a
// person reads it by.
const runSelect = `
	SELECT r.id, r.agent_id, COALESCE(b.name, ''), r.trigger, r.requested_by, r.status, r.error,
	       r.step_count, r.ok_count, r.started_at, r.finished_at, r.created_at,
	       r.workflow_id, COALESCE(w.name, r.params->>'workflow', ''), r.workflow_version, r.schedule_id, COALESCE(sc.name, ''),
	       (SELECT count(*) FROM automation_run_steps st WHERE st.workspace_id = r.workspace_id AND st.run_id = r.id AND st.screenshot_path <> ''),
	       (SELECT count(*) FROM automation_run_steps st WHERE st.workspace_id = r.workspace_id AND st.run_id = r.id AND st.status = 'ok' AND st.command IN ('extract', 'read')),
	       r.notes,
	       COALESCE((SELECT o.id FROM automation_runs o WHERE o.workspace_id = r.workspace_id AND o.id = r.rerun_of), '')
	FROM automation_runs r
	LEFT JOIN browser_agents b ON b.workspace_id = r.workspace_id AND b.id = r.agent_id
	LEFT JOIN automation_workflows w ON w.workspace_id = r.workspace_id AND w.id = r.workflow_id
	LEFT JOIN automation_schedules sc ON sc.workspace_id = r.workspace_id AND sc.id = r.schedule_id`

func scanRun(row rowScanner) (Run, error) {
	var r Run
	var started, finished sql.NullTime
	var notes pgtext.Array
	err := row.Scan(&r.ID, &r.AgentID, &r.AgentName, &r.Trigger, &r.RequestedBy, &r.Status, &r.Error,
		&r.StepCount, &r.OKCount, &started, &finished, &r.CreatedAt,
		&r.WorkflowID, &r.WorkflowName, &r.WorkflowVersion, &r.ScheduleID, &r.ScheduleName, &r.Screenshots, &r.Extracts, &notes, &r.RerunOf)
	r.StartedAt, r.FinishedAt = timep(started), timep(finished)
	r.Notes = notes
	if r.Notes == nil {
		r.Notes = []string{}
	}
	return r, err
}

// RunStep is one step and what came back for it.
type RunStep struct {
	// A screenshot was kept for this step (MH-478); it is served by
	// GET /api/w/runs/{id}/steps/{position}/screenshot.
	HasScreenshot bool `json:"has_screenshot"`
	Position   int    `json:"position"`
	Command    string `json:"command"`
	Args       string `json:"args"`
	Status     string `json:"status"`
	Summary    string `json:"summary"`
	Result     string `json:"result"`
	Error      string `json:"error"`
	DurationMS int    `json:"duration_ms"`
}

// ── the kill switch ──────────────────────────────────────────────────────

// IsPaused reports whether this workspace's automation is stopped, and why.
// The absence of a row means "not paused", so a workspace that never touched
// automation needs no row.
func IsPaused(ctx context.Context, a *app.App, workspaceID string) (bool, string, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var paused bool
	var reason string
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT paused, reason FROM automation_settings WHERE workspace_id = $1`, workspaceID).Scan(&paused, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		// Fail CLOSED: if we cannot tell whether automation is stopped, it is
		// stopped. The alternative is driving someone's signed-in browser on the
		// strength of a failed query.
		return true, "the kill switch could not be read", err
	}
	return paused, reason, nil
}

// Pause throws the kill switch.
func Pause(ctx context.Context, a *app.App, workspaceID, userID, reason string) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, err := a.SQLDB.ExecContext(sctx, `
		INSERT INTO automation_settings (workspace_id, paused, paused_at, paused_by, reason)
		VALUES ($1, true, now(), $2, $3)
		ON CONFLICT (workspace_id) DO UPDATE SET
			paused = true, paused_at = now(), paused_by = EXCLUDED.paused_by,
			reason = EXCLUDED.reason, updated_at = now()`,
		workspaceID, userID, trim(reason, 200))
	return err
}

// Resume lifts it.
func Resume(ctx context.Context, a *app.App, workspaceID string) error {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, err := a.SQLDB.ExecContext(sctx, `
		INSERT INTO automation_settings (workspace_id, paused) VALUES ($1, false)
		ON CONFLICT (workspace_id) DO UPDATE SET
			paused = false, paused_at = NULL, paused_by = '', reason = '', updated_at = now()`, workspaceID)
	return err
}

// CancelWorkspaceRuns marks everything queued or running as cancelled. The
// dispatcher notices on its next step; this is what makes the record honest in
// the meantime, so nothing sits "running" for ever after a stop.
func CancelWorkspaceRuns(ctx context.Context, a *app.App, workspaceID, reason string) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE automation_runs SET status = 'cancelled', error = $2, finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND status IN ('queued', 'running')`, workspaceID, trim(reason, 200))
}

// ── starting a run ───────────────────────────────────────────────────────

// Start checks every guardrail, records the run, and dispatches it in the
// background. It returns as soon as the run exists, because a sequence of
// steps in a real browser takes longer than a request should.
//
// The order of the checks is deliberate: the kill switch first (it is the
// control someone reaches for in a hurry), then the browser, then whether it is
// busy, then the steps themselves. Nothing is written until all of them pass.
func (h *Hub) Start(ctx context.Context, a *app.App, workspaceID, userID, trigger, agentID string, steps []Step) (string, error) {
	if paused, why, _ := IsPaused(ctx, a, workspaceID); paused {
		if why != "" {
			return "", fmt.Errorf("%w: %s", ErrPaused, why)
		}
		return "", ErrPaused
	}

	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var name string
	var hosts pgtext.Array
	var revoked sql.NullTime
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT name, allowed_hosts, revoked_at FROM browser_agents WHERE workspace_id = $1 AND id = $2`,
		workspaceID, agentID).Scan(&name, &hosts, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("no such browser")
	}
	if err != nil {
		return "", err
	}
	if revoked.Valid {
		return "", ErrRevoked
	}
	if !h.Online(agentID) {
		return "", ErrOffline
	}
	if err := CheckSteps(steps, hosts); err != nil {
		return "", err
	}

	params, err := json.Marshal(map[string]any{"steps": steps})
	if err != nil {
		return "", err
	}

	// The two capacity checks and the insert happen together, under a lock and
	// in one transaction (MH-234).
	//
	// They used to be a bare count followed by an insert, with a comment
	// claiming the partial index enforced it. The index was not unique, so it
	// enforced nothing: two requests arriving together both counted zero and
	// both inserted, and the same signed-in browser was driven twice at once.
	// The index IS unique now, and this lock is what turns the resulting
	// constraint violation into a queue rather than an error — and what makes
	// the workspace-wide count safe, which no index can express.
	runID, err := h.claimSlot(sctx, a, workspaceID, agentID, trigger, userID, string(params), steps)
	if err != nil {
		return "", err
	}

	// Detached from the request: the caller gets the run id now and watches it.
	go h.dispatch(a, workspaceID, agentID, runID, steps)
	return runID, nil
}

// dispatch walks the steps, one command at a time, and records each result as
// it arrives rather than at the end — a run that dies halfway should still show
// what it managed to do.
func (h *Hub) dispatch(a *app.App, workspaceID, agentID, runID string, steps []Step) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultRunDeadline)
	defer cancel()
	sctx := tenant.WithWorkspace(ctx, workspaceID)

	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE automation_runs SET status = 'running', started_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status = 'queued'`, workspaceID, runID)

	okCount := 0
	runErr := ""
	// The kept site tab this run is bound to, if any (MH-478): decided once,
	// from the steps, and sent with every step so the browser keeps the run in
	// that one tab.
	keptTab := resolvePersistent(steps)
	var lastCommandAt time.Time

	// stop ends the run at step i: that step failed, and nothing after it runs.
	// The remaining steps are marked skipped rather than left pending: "pending"
	// on a finished run reads as "still going".
	stop := func(i int, runError string) {
		_, _ = a.SQLDB.ExecContext(sctx, `
			UPDATE automation_run_steps SET status = 'skipped', updated_at = now()
			WHERE workspace_id = $1 AND run_id = $2 AND position > $3 AND status = 'pending'`,
			workspaceID, runID, i)
		h.finish(sctx, a, workspaceID, runID, "failed", runError, okCount)
	}
	failStep := func(i int, stepErr string) {
		_, _ = a.SQLDB.ExecContext(sctx, `
			UPDATE automation_run_steps SET status = 'failed', error = $4, updated_at = now()
			WHERE workspace_id = $1 AND run_id = $2 AND position = $3`,
			workspaceID, runID, i, trim(stepErr, 500))
	}

	for i, step := range steps {
		// The kill switch is re-read between steps: "stop" has to stop a run
		// that is already going, not only refuse the next one.
		if paused, why, _ := IsPaused(ctx, a, workspaceID); paused {
			runErr = "automation was stopped"
			if why != "" {
				runErr += ": " + why
			}
			h.finish(sctx, a, workspaceID, runID, "cancelled", runErr, okCount)
			return
		}
		if cancelled(sctx, a, workspaceID, runID) {
			// Someone cancelled it; the row already says so. The browser is
			// still told the run is over, or the run's tab stays open in a
			// signed-in browser on whatever page it had reached.
			h.endRun(workspaceID, runID)
			Settle(sctx, a, workspaceID, runID)
			return
		}

		// Push to inbox (MH-564) is the one command dispatch runs itself: it
		// touches no page and needs no browser, so it never reaches h.Call.
		// Every other step below this point assumes a browser round trip.
		if strings.EqualFold(strings.TrimSpace(step.Command), "push_inbox") {
			if err := h.dispatchPushInbox(sctx, a, workspaceID, runID, i, step); err != nil {
				if !step.ContinueOnError {
					stop(i, (&StepError{Position: i, Err: err}).Error())
					return
				}
				runErr = (&StepError{Position: i, Err: err}).Error()
			} else {
				okCount++
			}
			notifyRun(a, workspaceID)
			continue
		}

		// The allowed sites are re-read for every step, not taken from when the
		// run was accepted: a site removed from the list mid-run is off it for
		// the rest of the run. They travel WITH the step, because the browser
		// checks the page it lands on against them (MH-422) and the list it was
		// given at connect goes stale the moment someone edits it.
		hosts, err := allowedHosts(sctx, a, workspaceID, agentID)
		if err != nil {
			// Fail closed: a run that cannot say where it may go goes nowhere.
			failStep(i, "the allowed sites could not be read")
			stop(i, (&StepError{Position: i, Err: errors.New("the allowed sites could not be read")}).Error())
			return
		}
		if host, navigates := StepHost(step.Command, step.Args); navigates && !HostAllowed(hosts, host) {
			se := &StepError{Position: i, Host: host, Err: ErrHostNotAllowed}
			failStep(i, se.Error())
			stop(i, se.Error())
			return
		}

		args, _ := json.Marshal(step.Args)
		stepPayload := map[string]any{
			"command":       step.Command,
			"args":          json.RawMessage(args),
			"allowed_hosts": hosts,
			"step":          i + 1, // 1-based, as a person counts, so the browser can name it
		}
		if keptTab != "" {
			stepPayload["tab"] = keptTab
		}
		timeout := step.stepTimeout()
		// The browser holds sites one by one and some of its list is still
		// waiting for the person's OK in Chrome (MH-517): the step waits for
		// it there instead of failing, and the server waits that long too.
		if acc, ok := h.AccessIn(workspaceID)[agentID]; ok && acc.Mode == "sites" && len(acc.Pending) > 0 {
			timeout = MaxStepTimeout
			stepPayload["grant_wait_ms"] = (MaxStepTimeout - 20*time.Second).Milliseconds()
		}
		if step.TimeoutSeconds > 0 || timeout != step.stepTimeout() {
			stepPayload["timeout_ms"] = timeout.Milliseconds()
		}
		payload, _ := json.Marshal(stepPayload)

		// Steps are spaced out, so no workflow can hammer a site faster than a
		// person could click (fadymondy.com's minCommandGap).
		if gap := time.Since(lastCommandAt); !lastCommandAt.IsZero() && gap < minCommandGap {
			time.Sleep(minCommandGap - gap)
		}
		lastCommandAt = time.Now()

		started := time.Now()
		reply, err := h.Call(ctx, workspaceID, agentID, Envelope{Type: "run.step", Run: runID, Payload: payload}, timeout)
		duration := int(time.Since(started).Milliseconds())

		status, summary, result, stepErr, shot := "ok", "", "", "", ""
		if err != nil {
			status, stepErr = "failed", err.Error()
		} else {
			// Stored as it arrived. A JSON round trip through a Go map reorders
			// keys and renders numbers differently — the kind of quiet difference
			// that makes a model's answer unreproducible. The one exception is a
			// screenshot, whose PNG goes to storage rather than into the text.
			body, key, serr := takeScreenshot(a, workspaceID, runID, i, reply.Payload)
			if serr != nil {
				status, stepErr = "failed", serr.Error()
				err = serr
			} else {
				result, shot = string(body), key
				summary = summarise(body)
				okCount++
			}
		}

		_, _ = a.SQLDB.ExecContext(sctx, `
			UPDATE automation_run_steps SET status = $4, summary = $5, result = $6, error = $7, duration_ms = $8,
			    screenshot_path = $9, updated_at = now()
			WHERE workspace_id = $1 AND run_id = $2 AND position = $3`,
			workspaceID, runID, i, status, trim(summary, 300), trim(result, 200_000), trim(stepErr, 500), duration, shot)
		notifyRun(a, workspaceID)

		// Leaving the allowed sites is not a step that failed; it is the run
		// breaking the one promise a paired browser makes. It stops the run even
		// where the step said to continue on error, and its reason is the
		// browser's own sentence — it already names the step and the host.
		if errors.Is(err, ErrLeftAllowedSites) {
			h.log.Warn("run left the allowed sites", "run", runID, "agent", agentID, "step", i+1, "reason", err.Error())
			stop(i, err.Error())
			return
		}
		if err != nil && !step.ContinueOnError {
			stop(i, (&StepError{Position: i, Err: err}).Error())
			return
		}
		if err != nil {
			runErr = (&StepError{Position: i, Err: err}).Error() // kept, but the run goes on
		}
	}

	status := "ok"
	if runErr != "" {
		status = "failed" // a step failed and was continued past; the run is not "ok"
	}
	h.finish(sctx, a, workspaceID, runID, status, runErr, okCount)
}

// dispatchPushInbox runs the "push_inbox" step (MH-564): it takes the rows
// every earlier extract/read step in THIS run collected (ReadRunData reads
// only steps already written, which at position i is exactly the steps
// before it — dispatch writes each step's row before moving to the next) and
// files each one as an inbox message on the named workflow channel, deduped
// by (thread, message) so a re-run of the same workflow never duplicates.
//
// args: workflow_channel_id (required), and optionally thread_field,
// message_field, profile_field, name_field, text_field to rename which
// column of an extracted row holds each part — default thread_id, message_id,
// profile_url, name, text, the shape the four read presets (MH-564) produce.
func (h *Hub) dispatchPushInbox(sctx context.Context, a *app.App, workspaceID, runID string, i int, step Step) error {
	started := time.Now()
	write := func(status, summary, result, stepErr string) {
		_, _ = a.SQLDB.ExecContext(sctx, `
			UPDATE automation_run_steps SET status = $4, summary = $5, result = $6, error = $7, duration_ms = $8, updated_at = now()
			WHERE workspace_id = $1 AND run_id = $2 AND position = $3`,
			workspaceID, runID, i, status, trim(summary, 300), trim(result, 200_000), trim(stepErr, 500),
			int(time.Since(started).Milliseconds()))
	}

	channelID := strings.TrimSpace(stringArg(step.Args, "workflow_channel_id", ""))
	if channelID == "" {
		err := errors.New(`push_inbox needs a workflow_channel_id argument`)
		write("failed", "", "", err.Error())
		return err
	}

	data, err := ReadRunData(sctx, a, workspaceID, runID)
	if err != nil {
		write("failed", "", "", err.Error())
		return err
	}
	threadField := stringArg(step.Args, "thread_field", "thread_id")
	messageField := stringArg(step.Args, "message_field", "message_id")
	profileField := stringArg(step.Args, "profile_field", "profile_url")
	nameField := stringArg(step.Args, "name_field", "name")
	textField := stringArg(step.Args, "text_field", "text")

	items := make([]channels.PushItem, 0, len(data.Rows))
	for _, row := range data.Rows {
		items = append(items, channels.PushItem{
			ThreadID:   rowString(row, threadField),
			MessageID:  rowString(row, messageField),
			ProfileURL: rowString(row, profileField),
			Name:       rowString(row, nameField),
			Text:       rowString(row, textField),
			SentAt:     time.Now(),
		})
	}

	res, err := channels.PushMessages(sctx, a, workspaceID, channelID, items)
	if err != nil {
		channels.RecordPushError(sctx, a, workspaceID, channelID, err)
		write("failed", "", "", err.Error())
		return err
	}
	resultJSON, _ := json.Marshal(map[string]any{"pushed": res.Pushed, "deduped": res.Deduped})
	summary := fmt.Sprintf("pushed %d, deduped %d", res.Pushed, res.Deduped)
	write("ok", summary, string(resultJSON), "")
	return nil
}

// stringArg reads a string argument, falling back to def when it is absent,
// empty, or not a string.
func stringArg(args map[string]any, key, def string) string {
	if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

// rowString stringifies one field of an extracted row: a string as itself, a
// missing or nil field as "", anything else (a number, an object) as JSON —
// an extracted row's shape is whatever the workflow's own fields produced,
// not something this step controls.
func rowString(row map[string]any, key string) string {
	v, ok := row[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func (h *Hub) finish(ctx context.Context, a *app.App, workspaceID, runID, status, errText string, okCount int) {
	h.endRun(workspaceID, runID)
	// A run that was cancelled while a step was in flight STAYS cancelled. The
	// kill switch drops the browser's socket, so the step in flight comes back
	// "this browser is not connected" — and writing that over the row would
	// record a stop someone pressed as a failure nobody explained.
	if _, err := a.SQLDB.ExecContext(ctx, `
		UPDATE automation_runs SET
			status = CASE WHEN status = 'cancelled' THEN status ELSE $3 END,
			error = CASE WHEN status = 'cancelled' AND error <> '' THEN error ELSE $4 END,
			ok_count = $5, finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, runID, status, trim(errText, 500), okCount); err != nil {
		h.log.Error("run finish write failed", "run", runID, "err", err)
	}
	// If a schedule started this run, its row says "running" until now.
	Settle(ctx, a, workspaceID, runID)
}

// allowedHosts is the browser's list as it stands now.
func allowedHosts(ctx context.Context, a *app.App, workspaceID, agentID string) ([]string, error) {
	var hosts pgtext.Array
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT allowed_hosts FROM browser_agents WHERE workspace_id = $1 AND id = $2`,
		workspaceID, agentID).Scan(&hosts); err != nil {
		return nil, err
	}
	if hosts == nil {
		return []string{}, nil // an empty list, sent as [] — never null, which reads as "no list"
	}
	return hosts, nil
}

func cancelled(ctx context.Context, a *app.App, workspaceID, runID string) bool {
	var status string
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT status FROM automation_runs WHERE workspace_id = $1 AND id = $2`, workspaceID, runID).Scan(&status); err != nil {
		return false
	}
	return status == "cancelled"
}

// summarise turns a reply into the one line a list can show. The full payload
// is kept on the step; this is only what fits on a row without opening it.
func summarise(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var generic map[string]any
	if json.Unmarshal(payload, &generic) == nil {
		for _, key := range []string{"summary", "title", "url", "text", "value"} {
			if v, ok := generic[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.Join(strings.Fields(v), " ")
			}
		}
	}
	return strings.Join(strings.Fields(string(payload)), " ")
}

func trim(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// endRun tells the browser it can close the tab this run opened. Best-effort
// and unacknowledged: the run is already over, and a browser that has gone
// away has no tab to close.
func (h *Hub) endRun(workspaceID, runID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.conns {
		if c.agent.WorkspaceID != workspaceID {
			continue
		}
		frame, err := json.Marshal(Envelope{Type: "run.end", Run: runID})
		if err == nil {
			c.trySend(frame)
		}
	}
}

// CancelRun stops a queued or running run of the workspace: the dispatcher
// checks the status before every step, so it stops after the step in flight.
// Reports whether there was a run to stop.
func CancelRun(ctx context.Context, a *app.App, workspaceID, runID string) (bool, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	res, err := a.SQLDB.ExecContext(sctx, `
		UPDATE automation_runs SET status = 'cancelled', error = 'cancelled', finished_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2 AND status IN ('queued', 'running')`, workspaceID, runID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
