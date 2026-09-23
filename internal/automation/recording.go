package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Recordings (MH-235): what an operator did in a browser, saved as a workflow.

Ported from fadymondy.com's internal/automation/recording.go (FM-144), with the
one structural change that matters here: there a recording becomes a "plugin",
and here it becomes a WORKFLOW (MH-233). That is not a rename — it is the whole
point of doing it this way. A recording is an ordinary workflow the moment it
lands, so Fill, the placeholder rules, schedules and the MCP tools all work on
it with nothing recording-specific downstream.

The extension is trusted to have recorded, not to have recorded SAFELY. It runs
in somebody's browser and its code can be read by anyone, so every rule the
recorder claims to follow is enforced again here:

  - Only commands a recording can produce are kept. Anything else is dropped
    rather than passed through to the runner.
  - A step on a host outside the browser's allowed sites refuses the WHOLE
    recording. That is a policy line, not a stray step to tidy away: a
    recording that quietly dropped the off-limits half would replay as
    something the operator never did.
  - A step marked secret — a password field — never keeps its text. It becomes
    a {{placeholder}} with a secret parameter, which MH-233 already redacts
    from the run record. Recording a password into a workflow would put it in
    the database, in every run it produced, and in front of any model handed
    those runs.
*/

// RecordedStep is one step as the extension sends it.
type RecordedStep struct {
	Command string          `json:"command"`
	Args    json.RawMessage `json:"args,omitempty"`
	// Secret marks a value typed into a password field. The text is discarded
	// before anything is written.
	Secret bool `json:"secret,omitempty"`
}

// Recording is the extension's payload when recording stops.
type Recording struct {
	Name     string         `json:"name,omitempty"`
	StartURL string         `json:"start_url"`
	Steps    []RecordedStep `json:"steps"`
}

// Saved is what the browser is told after a recording lands.
type Saved struct {
	WorkflowID string `json:"workflow_id"`
	Name       string `json:"name"`
	Steps      int    `json:"steps"`
}

// The commands a recording may contain. Deliberately a subset of what the
// runner understands: "close" ends a run and "extract" reads a page, and
// neither is something the operator can be observed doing.
var recordableCommands = map[string]bool{
	"navigate": true, "click": true, "type": true,
	"press": true, "select": true, "scroll": true, "wait": true,
}

// A recording longer than this is a recorder left running, not a workflow.
const maxRecordedSteps = 200

var (
	secretNamePattern = regexp.MustCompile(`[^a-z0-9_]+`)
	// Hints in a field's selector that name the secret better than "password"
	// would: a one-time code recorded as {{password}} reads as the wrong thing
	// when somebody later runs it.
	secretHints = []string{"pin", "otp", "code", "secret", "token"}
)

// SaveRecording validates a recording from a browser and stores it as a
// workflow in that browser's workspace.
//
// The workspace comes from the SOCKET, never from the payload: the browser
// authenticates as itself, and letting it name a workspace would let a paired
// browser write into somebody else's.
func SaveRecording(ctx context.Context, a *app.App, workspaceID, agentID string, rec Recording) (*Saved, error) {
	if len(rec.Steps) == 0 {
		return nil, errors.New("nothing was recorded")
	}
	if len(rec.Steps) > maxRecordedSteps {
		return nil, fmt.Errorf("a recording can hold at most %d steps", maxRecordedSteps)
	}

	sctx := tenant.WithWorkspace(ctx, workspaceID)

	var agentName string
	var allowed pgtext.Array
	if err := a.SQLDB.QueryRowContext(sctx, `
		SELECT name, allowed_hosts FROM browser_agents
		WHERE workspace_id = $1 AND id = $2 AND revoked_at IS NULL`,
		workspaceID, agentID).Scan(&agentName, &allowed); err != nil {
		return nil, errors.New("unknown browser")
	}

	start, err := url.Parse(strings.TrimSpace(rec.StartURL))
	if err != nil || (start.Scheme != "http" && start.Scheme != "https") || start.Hostname() == "" {
		return nil, errors.New("a recording has to start on a web page")
	}
	startHost := normaliseHost(start.Hostname())
	if !HostAllowed(allowed, startHost) {
		return nil, fmt.Errorf("%s is not on this browser's allow-list, so the recording was not saved", startHost)
	}

	steps := make([]Step, 0, len(rec.Steps)+1)
	var params []Param
	seenParam := map[string]bool{}

	// Every recording opens where it started, so it replays from a known page
	// rather than from whatever happened to be on screen.
	if rec.Steps[0].Command != "navigate" {
		steps = append(steps, Step{Command: "navigate", Args: map[string]any{"url": start.String()}})
	}

	for i, recorded := range rec.Steps {
		command := strings.ToLower(strings.TrimSpace(recorded.Command))
		if !recordableCommands[command] {
			continue
		}

		args := map[string]any{}
		if len(recorded.Args) > 0 {
			if json.Unmarshal(recorded.Args, &args) != nil {
				return nil, fmt.Errorf("step %d is not readable", i+1)
			}
		}

		// Belt and braces: a field whose selector says "password" is a secret
		// whether or not the extension marked it. The extension is the thing
		// this check exists to not depend on.
		selector, _ := args["selector"].(string)
		if command == "type" && (recorded.Secret || strings.Contains(strings.ToLower(selector), "password")) {
			name := secretParamName(selector, seenParam)
			args["text"] = "{{" + name + "}}"
			if !seenParam[name] {
				seenParam[name] = true
				params = append(params, Param{
					Name:     name,
					Label:    strings.ToUpper(name[:1]) + name[1:],
					Required: true,
					Secret:   true,
				})
			}
		}

		step := Step{Command: command, Args: args}
		if host, ok := StepHost(command, args); ok {
			if !HostAllowed(allowed, host) {
				return nil, fmt.Errorf("%s is not on this browser's allow-list, so the recording was not saved", host)
			}
		}
		steps = append(steps, step)
	}

	if len(steps) == 0 {
		return nil, errors.New("nothing in that recording could be replayed")
	}

	name := strings.TrimSpace(rec.Name)
	if name == "" {
		name = fmt.Sprintf("Recorded on %s, %s", startHost, time.Now().UTC().Format("2 Jan 15:04"))
	}
	name = trim(name, 120)

	if params == nil {
		params = []Param{}
	}

	description := fmt.Sprintf("Recorded in %s. Review the steps before scheduling it.", agentName)

	// Through CreateWorkflow, so a recording starts its history as version 1
	// ("recorded") like any other workflow, and the first edit of it can be
	// undone (MH-400).
	workflowID, err := CreateWorkflow(ctx, a, workspaceID, "agent:"+agentID, WorkflowInput{
		Name: uniqueName(sctx, a, workspaceID, name), Description: description, Steps: steps, Params: params,
	}, NoteRecorded)
	if err != nil {
		return nil, fmt.Errorf("could not save the recording: %w", err)
	}

	return &Saved{WorkflowID: workflowID, Name: name, Steps: len(steps)}, nil
}

// uniqueName keeps the workspace's one-name-per-workflow rule from turning a
// second recording of the same site into an error the operator cannot act on.
// Recording twice is normal; being told "that name is taken" is not useful.
func uniqueName(ctx context.Context, a *app.App, workspaceID, name string) string {
	var taken bool
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM automation_workflows WHERE workspace_id = $1 AND lower(name) = lower($2))`,
		workspaceID, name).Scan(&taken); err != nil || !taken {
		return name
	}
	return trim(name, 108) + " " + time.Now().UTC().Format("15:04:05")
}

// secretParamName picks a name for a redacted field, preferring what the
// selector suggests over a generic "password".
func secretParamName(selector string, seen map[string]bool) string {
	base := "password"
	lower := strings.ToLower(selector)
	for _, hint := range secretHints {
		if strings.Contains(lower, hint) {
			base = hint
			break
		}
	}
	base = strings.Trim(secretNamePattern.ReplaceAllString(base, "_"), "_")
	if base == "" {
		base = "password"
	}
	if !seen[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s_%d", base, n)
		if !seen[candidate] {
			return candidate
		}
	}
}
