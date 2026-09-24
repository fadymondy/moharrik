package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Editing a saved workflow, and keeping every version of it (MH-400).

A workflow could be created and run, never changed: a recording with one wrong
selector had to be deleted and recorded again, and anything scheduled against
it lost its schedule. Now it can be saved over — and because the people who
edit a workflow are rarely the people who wrote it, and the thing it breaks is
a run at 4am with nobody watching, EVERY SAVE IS KEPT:

	A SAVE WRITES A NEW VERSION. The whole workflow — name, steps, parameters —
	is copied into automation_workflow_versions, numbered 1, 2, 3 … The
	versions are never updated afterwards.

	RESTORING IS A SAVE. "Restore v1" makes v1's content the current workflow as
	a NEW version (v3), so the restore can itself be undone and the history
	never loses the version that was replaced.

	A SAVE NAMES THE VERSION IT EDITED. Two people with the editor open would
	otherwise each silently overwrite the other; the second save is refused
	(ErrStaleVersion) and told which version is current now.
*/

// ErrStaleVersion is a save made against a version that is no longer current.
var ErrStaleVersion = errors.New("this workflow was changed since you opened it")

// ErrNameTaken is a workflow name already used in the workspace.
var ErrNameTaken = errors.New("a workflow with that name already exists")

// Version notes: how a version came to be.
const (
	NoteCreated  = "created"
	NoteRecorded = "recorded"
	NoteEdited   = "edited"
	NoteRestored = "restored"
)

// WorkflowVersion is one saved state of a workflow.
type WorkflowVersion struct {
	Version      int       `json:"version"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Steps        []Step    `json:"steps"`
	Params       []Param   `json:"params"`
	Note         string    `json:"note" doc:"created | recorded | edited | restored"`
	RestoredFrom *int      `json:"restored_from,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// WorkflowInput is what an editor saves.
type WorkflowInput struct {
	Name string `json:"name" minLength:"1" maxLength:"80"`
	// How a tool call names it (MH-478). Made from the name when left empty.
	Slug        string  `json:"slug,omitempty" maxLength:"60"`
	// Long enough for a preset's notes: what it does, its fields, and when
	// its selectors were last verified (MH-478).
	Description string  `json:"description,omitempty" maxLength:"2000"`
	Steps       []Step  `json:"steps" maxItems:"200"`
	Params      []Param `json:"params,omitempty" maxItems:"40"`
}

// The step vocabulary the extension's driver understands (extension/driver.js).
// A step outside it saves happily and then fails on every run, which is the
// failure MH-398 was — so a save is refused here instead.
var knownCommands = map[string]bool{
	"goto": true, "navigate": true, "open": true,
	"click": true, "type": true, "fill": true,
	"press": true, "key": true, "select": true, "scroll": true,
	"extract": true, "read": true, "wait": true, "close": true,
	// The runtime's commands (MH-478, fadymondy.com's FM-91 set).
	"wait_for": true, "upload": true, "screenshot": true, "eval_readonly": true, "release": true,
}

var paramNameRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`)
var slugJunk = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify makes a slug of a name: latin letters and digits, hyphens between.
// A name with none of them (an Arabic one) gives "workflow".
func Slugify(name string) string {
	s := strings.Trim(slugJunk.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(s) > 50 {
		s = strings.TrimRight(s[:50], "-")
	}
	if s == "" {
		return "workflow"
	}
	return s
}

// ErrSlugTaken is a slug another workflow of the workspace already has.
var ErrSlugTaken = errors.New("another workflow already has that slug")

// ValidateWorkflow checks what an editor sends before anything is written.
//
// It does NOT check the steps against a browser's allowed sites: the steps
// keep their placeholders, and a host only known once values are supplied
// cannot be judged yet. That check happens when a run starts, on the filled-in
// steps (workflows.go).
func ValidateWorkflow(in WorkflowInput) error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("a workflow needs a name")
	}
	if len(in.Steps) == 0 {
		return errors.New("a workflow needs at least one step")
	}
	if slug := strings.TrimSpace(in.Slug); slug != "" && !slugRE.MatchString(slug) {
		return fmt.Errorf("%q cannot be a slug: use lowercase letters, digits and hyphens", in.Slug)
	}
	if len(in.Steps) > MaxSteps {
		return fmt.Errorf("a workflow can hold at most %d steps", MaxSteps)
	}
	for i, s := range in.Steps {
		c := strings.ToLower(strings.TrimSpace(s.Command))
		if c == "" {
			return fmt.Errorf("step %d needs a command", i+1)
		}
		if !knownCommands[c] {
			return fmt.Errorf("step %d: the browser does not know the command %q", i+1, s.Command)
		}
	}
	seen := map[string]bool{}
	for _, p := range in.Params {
		name := strings.TrimSpace(p.Name)
		if !paramNameRE.MatchString(name) {
			return fmt.Errorf("%q cannot be a parameter name: use letters, digits and _", p.Name)
		}
		if seen[name] {
			return fmt.Errorf("the parameter %q is declared twice", name)
		}
		seen[name] = true
		// A secret's default would be stored in the clear — in the workflow and
		// in every version of it — which is precisely what "secret" rules out.
		if p.Secret && p.Default != "" {
			return fmt.Errorf("the secret parameter %q cannot have a default value", name)
		}
	}
	return nil
}

// cleanInput normalises what is stored: trimmed names, lower-case commands,
// a non-nil parameter list.
func cleanInput(in WorkflowInput) WorkflowInput {
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.ToLower(strings.TrimSpace(in.Slug))
	in.Description = strings.TrimSpace(in.Description)
	for i := range in.Steps {
		in.Steps[i].Command = strings.ToLower(strings.TrimSpace(in.Steps[i].Command))
	}
	for i := range in.Params {
		in.Params[i].Name = strings.TrimSpace(in.Params[i].Name)
	}
	if in.Params == nil {
		in.Params = []Param{}
	}
	return in
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func insertVersion(ctx context.Context, tx execer, workspaceID, workflowID string, version int, in WorkflowInput, note string, restoredFrom *int, by string) error {
	steps, err := json.Marshal(in.Steps)
	if err != nil {
		return err
	}
	params, err := json.Marshal(firstNonNil(in.Params))
	if err != nil {
		return err
	}
	var from any
	if restoredFrom != nil {
		from = *restoredFrom
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO automation_workflow_versions
			(workspace_id, workflow_id, version, name, description, steps, params, note, restored_from, created_by)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10)`,
		workspaceID, workflowID, version, in.Name, in.Description, string(steps), string(params), note, from, by)
	return err
}

// CreateWorkflow saves a new workflow as version 1.
func CreateWorkflow(ctx context.Context, a *app.App, workspaceID, userID string, in WorkflowInput, note string) (string, error) {
	in = cleanInput(in)
	steps, err := json.Marshal(in.Steps)
	if err != nil {
		return "", err
	}
	params, err := json.Marshal(in.Params)
	if err != nil {
		return "", err
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	tx, err := a.SQLDB.BeginTx(sctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	// The slug: the one asked for, or one made from the name that no other
	// workflow of the workspace has yet.
	slug := in.Slug
	if slug == "" {
		base := Slugify(in.Name)
		slug = base
		for n := 2; n < 100; n++ {
			var taken bool
			if err := tx.QueryRowContext(sctx, `SELECT EXISTS (SELECT 1 FROM automation_workflows WHERE workspace_id = $1 AND slug = $2)`,
				workspaceID, slug).Scan(&taken); err != nil || !taken {
				break
			}
			slug = fmt.Sprintf("%s-%d", base, n)
		}
	}

	var id string
	err = tx.QueryRowContext(sctx, `
		INSERT INTO automation_workflows (workspace_id, name, description, steps, params, created_by, version, slug)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6, 1, $7) RETURNING id`,
		workspaceID, in.Name, in.Description, string(steps), string(params), userID, slug).Scan(&id)
	if pgCode(err) == "23505" {
		if strings.Contains(err.Error(), "slug") {
			return "", ErrSlugTaken
		}
		return "", ErrNameTaken
	}
	if err != nil {
		return "", err
	}
	if err := insertVersion(sctx, tx, workspaceID, id, 1, in, note, nil, userID); err != nil {
		return "", err
	}
	return id, tx.Commit()
}

// SaveWorkflow writes in over a workflow as its next version. baseVersion is
// the version the editor opened; 0 skips the check (MCP, scripts).
func SaveWorkflow(ctx context.Context, a *app.App, workspaceID, userID, id string, in WorkflowInput, baseVersion int) (int, error) {
	return saveVersion(ctx, a, workspaceID, userID, id, cleanInput(in), baseVersion, NoteEdited, nil)
}

// RestoreWorkflow makes an old version's content current, as a new version.
func RestoreWorkflow(ctx context.Context, a *app.App, workspaceID, userID, id string, version, baseVersion int) (int, error) {
	v, err := GetWorkflowVersion(ctx, a, workspaceID, id, version)
	if err != nil {
		return 0, err
	}
	in := WorkflowInput{Name: v.Name, Description: v.Description, Steps: v.Steps, Params: v.Params}
	return saveVersion(ctx, a, workspaceID, userID, id, cleanInput(in), baseVersion, NoteRestored, &version)
}

func saveVersion(ctx context.Context, a *app.App, workspaceID, userID, id string, in WorkflowInput, baseVersion int, note string, restoredFrom *int) (int, error) {
	steps, err := json.Marshal(in.Steps)
	if err != nil {
		return 0, err
	}
	params, err := json.Marshal(in.Params)
	if err != nil {
		return 0, err
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	tx, err := a.SQLDB.BeginTx(sctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	// FOR UPDATE: two saves of one workflow queue here rather than both reading
	// version 4 and both writing version 5.
	var current int
	err = tx.QueryRowContext(sctx, `
		SELECT version FROM automation_workflows WHERE workspace_id = $1 AND id = $2 FOR UPDATE`,
		workspaceID, id).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNoWorkflow
	}
	if err != nil {
		return 0, err
	}
	if baseVersion > 0 && baseVersion != current {
		return current, fmt.Errorf("%w: it is at version %d now, and you opened version %d", ErrStaleVersion, current, baseVersion)
	}

	// A workflow saved before versions existed, whose backfill somehow did not
	// run, still keeps what it was before this save overwrites it.
	if _, err := tx.ExecContext(sctx, `
		INSERT INTO automation_workflow_versions (workspace_id, workflow_id, version, name, description, steps, params, note, created_by, created_at)
		SELECT workspace_id, id, version, name, description, steps, params, 'created', created_by, created_at
		FROM automation_workflows w
		WHERE workspace_id = $1 AND id = $2
		  AND NOT EXISTS (SELECT 1 FROM automation_workflow_versions v
		                  WHERE v.workspace_id = w.workspace_id AND v.workflow_id = w.id AND v.version = w.version)`,
		workspaceID, id); err != nil {
		return 0, err
	}

	next := current + 1
	_, err = tx.ExecContext(sctx, `
		UPDATE automation_workflows
		SET name = $3, description = $4, steps = $5::jsonb, params = $6::jsonb, version = $7,
		    slug = CASE WHEN $8 = '' THEN slug ELSE $8 END, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id, in.Name, in.Description, string(steps), string(params), next, in.Slug)
	if pgCode(err) == "23505" {
		if strings.Contains(err.Error(), "slug") {
			return 0, ErrSlugTaken
		}
		return 0, ErrNameTaken
	}
	if err != nil {
		return 0, err
	}
	if err := insertVersion(sctx, tx, workspaceID, id, next, in, note, restoredFrom, userID); err != nil {
		return 0, err
	}
	return next, tx.Commit()
}

// ErrNoWorkflow is an id that is not a workflow of this workspace.
var ErrNoWorkflow = errors.New("no such workflow")

// ErrNoVersion is a version number the workflow does not have.
var ErrNoVersion = errors.New("that workflow has no such version")

// ListWorkflowVersions returns every version, newest first.
func ListWorkflowVersions(ctx context.Context, a *app.App, workspaceID, id string) ([]WorkflowVersion, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT version, name, description, steps::text, params::text, note, restored_from, created_at
		FROM automation_workflow_versions
		WHERE workspace_id = $1 AND workflow_id = $2
		ORDER BY version DESC LIMIT 200`, workspaceID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []WorkflowVersion{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, v)
	}
	return list, rows.Err()
}

// GetWorkflowVersion returns one version.
func GetWorkflowVersion(ctx context.Context, a *app.App, workspaceID, id string, version int) (WorkflowVersion, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	row := a.SQLDB.QueryRowContext(sctx, `
		SELECT version, name, description, steps::text, params::text, note, restored_from, created_at
		FROM automation_workflow_versions
		WHERE workspace_id = $1 AND workflow_id = $2 AND version = $3`, workspaceID, id, version)
	v, err := scanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNoVersion
	}
	return v, err
}

func scanVersion(r rowScanner) (WorkflowVersion, error) {
	var v WorkflowVersion
	var steps, params string
	var from sql.NullInt64
	if err := r.Scan(&v.Version, &v.Name, &v.Description, &steps, &params, &v.Note, &from, &v.CreatedAt); err != nil {
		return v, err
	}
	_ = json.Unmarshal([]byte(steps), &v.Steps)
	_ = json.Unmarshal([]byte(params), &v.Params)
	if v.Steps == nil {
		v.Steps = []Step{}
	}
	if v.Params == nil {
		v.Params = []Param{}
	}
	if from.Valid {
		n := int(from.Int64)
		v.RestoredFrom = &n
	}
	return v, nil
}
