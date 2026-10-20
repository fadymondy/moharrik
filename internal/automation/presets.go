package automation

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
The preset library (MH-478, phase 3a): every browser plugin fadymondy.com runs,
as a starting point any workspace can add as its own workflow.

presets/presets.json is built by presets/import.mjs from fadymondy.com's
production plugins, copied faithfully — names, slugs, descriptions, sites,
inputs, steps, icons, and the "(publishes)" / dry-run markers — and
de-personalised rather than dropped: the message-to-yourself WhatsApp preset
sends to the WORKSPACE's own number, and the CV-based job applications take
their defaults from the WORKSPACE PROFILE. Both are filled in when a preset is
added, so what is saved is an ordinary workflow the workspace owns and edits.

Adding a preset can also add its sites to one browser's allowed sites, in the
same step — that is an admin's decision (automation:manage), exactly as on the
Automation page, and it is pushed to the browser at once. Nothing else a preset
does can widen what a browser may visit: its steps are checked against the
browser's list on every run.
*/

//go:embed presets/presets.json
var presetsRaw []byte

// The plain names and one-line summaries, per language (MH-518): what the
// gallery leads with, the technical notes kept under "Technical details".
//
//go:embed presets/plain.json
var presetsPlainRaw []byte

// PlainText is a preset's name and one-line summary in one language.
type PlainText struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// PresetInput is one value a preset asks for.
type PresetInput struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Required bool   `json:"required,omitempty"`
	Default  string `json:"default,omitempty"`
	Help     string `json:"help,omitempty"`
}

// Preset is one entry of the library.
type Preset struct {
	Slug        string        `json:"slug"`
	Name        string        `json:"name"`
	Group       string        `json:"group" doc:"The platform it belongs to: youtube, linkedin, google-news, jobs, …"`
	Icon        string        `json:"icon"`
	Colour      string        `json:"colour"`
	Description string        `json:"description"`
	Hosts       []string      `json:"hosts"`
	Inputs      []PresetInput `json:"inputs"`
	Steps       []Step        `json:"steps"`
	Writes      bool          `json:"writes" doc:"Publishes, sends or submits as the signed-in account."`
	DryRun      string        `json:"dry_run,omitempty" doc:"The slug of its rehearsal twin, which never posts."`
	DryRunOf    string        `json:"dry_run_of,omitempty" doc:"The preset this one rehearses."`
	// What it needs from the workspace when it is added.
	// Plain words for people, by language: "en" and "ar" (MH-518). Name and
	// Description above are fadymondy.com's technical ones.
	Plain         map[string]PlainText `json:"plain"`
	NeedsProfile  []string             `json:"needs_profile" doc:"Workspace profile fields its defaults come from."`
	NeedsWhatsApp bool                 `json:"needs_whatsapp" doc:"Sends to the workspace's own WhatsApp number."`
}

type presetLibrary struct {
	Version int      `json:"version"`
	Source  string   `json:"source"`
	Presets []Preset `json:"presets"`
}

var (
	presetsOnce sync.Once
	library     presetLibrary
	bySlug      map[string]*Preset
)

var (
	profileRE  = regexp.MustCompile(`\{\{\s*profile\.([a-z_]+)\s*\}\}`)
	whatsAppRE = regexp.MustCompile(`\{\{\s*workspace\.whatsapp_number\s*\}\}`)
)

func loadPresets() {
	presetsOnce.Do(func() {
		if err := json.Unmarshal(presetsRaw, &library); err != nil {
			panic("automation: presets.json does not parse: " + err.Error())
		}
		plain := map[string]map[string]PlainText{}
		if err := json.Unmarshal(presetsPlainRaw, &plain); err != nil {
			panic("automation: presets/plain.json does not parse: " + err.Error())
		}
		bySlug = map[string]*Preset{}
		for i := range library.Presets {
			p := &library.Presets[i]
			p.Plain = plain[p.Slug]
			if p.Plain == nil {
				p.Plain = map[string]PlainText{}
			}
			seen := map[string]bool{}
			p.NeedsProfile = []string{}
			for _, in := range p.Inputs {
				for _, m := range profileRE.FindAllStringSubmatch(in.Default, -1) {
					if !seen[m[1]] {
						seen[m[1]] = true
						p.NeedsProfile = append(p.NeedsProfile, m[1])
					}
				}
			}
			raw, _ := json.Marshal(p.Steps)
			p.NeedsWhatsApp = whatsAppRE.Match(raw)
			if p.Hosts == nil {
				p.Hosts = []string{}
			}
			bySlug[p.Slug] = p
		}
	})
}

// Presets is the library: its version and every preset, in the file's order.
func Presets() (int, []Preset) {
	loadPresets()
	return library.Version, library.Presets
}

// FindPreset is one preset by slug, or nil.
func FindPreset(slug string) *Preset {
	loadPresets()
	return bySlug[strings.ToLower(strings.TrimSpace(slug))]
}

// ProfileFields are the workspace profile's fields, in the order the page
// shows them. A preset may use any of them as a default.
var ProfileFields = []string{"first_name", "last_name", "email", "phone", "city", "location", "current_company", "linkedin", "github", "website"}

// ReadProfile is the workspace profile (automation_settings.profile).
func ReadProfile(ctx context.Context, a *app.App, workspaceID string) (map[string]string, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var raw string
	err := a.SQLDB.QueryRowContext(sctx, `SELECT profile::text FROM automation_settings WHERE workspace_id = $1`, workspaceID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	_ = json.Unmarshal([]byte(raw), &out)
	return out, nil
}

// profileOrEmpty is ReadProfile with nothing to say on error: hello.ok must
// still answer, a browser's autofill panel just starts with an empty profile.
func profileOrEmpty(ctx context.Context, a *app.App, workspaceID string) map[string]string {
	p, err := ReadProfile(ctx, a, workspaceID)
	if err != nil || p == nil {
		return map[string]string{}
	}
	return p
}

// SaveProfile replaces the workspace profile. Unknown fields are dropped.
func SaveProfile(ctx context.Context, a *app.App, workspaceID string, in map[string]string) (map[string]string, error) {
	clean := map[string]string{}
	for _, f := range ProfileFields {
		if v := strings.TrimSpace(in[f]); v != "" {
			if len(v) > 200 {
				v = v[:200]
			}
			clean[f] = v
		}
	}
	raw, _ := json.Marshal(clean)
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, err := a.SQLDB.ExecContext(sctx, `
		INSERT INTO automation_settings (workspace_id, profile) VALUES ($1, $2::jsonb)
		ON CONFLICT (workspace_id) DO UPDATE SET profile = EXCLUDED.profile`, workspaceID, string(raw))
	return clean, err
}

// workspaceWhatsApp is the workspace's default WhatsApp number, digits only.
func workspaceWhatsApp(ctx context.Context, a *app.App, workspaceID string) string {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var phone string
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT phone FROM whatsapp_links WHERE workspace_id = $1
		ORDER BY is_default DESC, created_at, id LIMIT 1`, workspaceID).Scan(&phone)
	return digitsOnly(phone)
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ErrPresetAdded is a preset the workspace already has as a workflow.
var ErrPresetAdded = errors.New("this preset is already one of the workspace's workflows")

// ErrNeedsWhatsApp is a preset that sends to the workspace's own number, added
// before the workspace has one.
var ErrNeedsWhatsApp = errors.New("this preset sends to the workspace's own WhatsApp number: give the number, or link WhatsApp first")

// PresetAdd is what adding a preset may be told.
type PresetAdd struct {
	// A browser whose allowed sites should gain the preset's sites.
	AgentID string
	// The number to send to, for a preset that sends to the workspace's own
	// WhatsApp; the linked number when empty.
	WhatsApp string
	// The language the workflow is named in: "en" or "ar" (MH-518).
	Locale string
}

// PresetWorkflow turns a preset into what is saved: its placeholders for the
// workspace filled in, its inputs as the workflow's parameters.
func PresetWorkflow(p *Preset, profile map[string]string, whatsApp string) (WorkflowInput, error) {
	return PresetWorkflowIn(p, profile, whatsApp, "en")
}

// PresetWorkflowIn is PresetWorkflow named in one language: the plain name,
// and a description that opens with the plain summary and keeps the
// technical notes under it (MH-518).
func PresetWorkflowIn(p *Preset, profile map[string]string, whatsApp, locale string) (WorkflowInput, error) {
	raw, _ := json.Marshal(p.Steps)
	if whatsAppRE.Match(raw) {
		number := digitsOnly(whatsApp)
		if len(number) < 8 || len(number) > 15 {
			return WorkflowInput{}, ErrNeedsWhatsApp
		}
		raw = whatsAppRE.ReplaceAll(raw, []byte(number))
	}
	var steps []Step
	if err := json.Unmarshal(raw, &steps); err != nil {
		return WorkflowInput{}, err
	}
	params := make([]Param, 0, len(p.Inputs))
	for _, in := range p.Inputs {
		def := profileRE.ReplaceAllStringFunc(in.Default, func(m string) string {
			return profile[profileRE.FindStringSubmatch(m)[1]]
		})
		params = append(params, Param{Name: in.Name, Label: in.Label, Required: in.Required, Default: def, Help: in.Help})
	}
	name, description := p.Name, p.Description
	plain, ok := p.Plain[locale]
	if !ok {
		plain = p.Plain["en"]
	}
	if plain.Name != "" {
		name = plain.Name
		description = plain.Summary + "\n\n" + p.Description
		if len(description) > 2000 {
			description = description[:2000]
		}
	}
	return WorkflowInput{Name: name, Slug: p.Slug, Description: description, Steps: steps, Params: params}, nil
}

// AddPreset saves a preset as one of the workspace's workflows and, when
// asked, adds its sites to a browser's allowed sites. Returns the workflow's
// id and the sites that were added.
func AddPreset(ctx context.Context, a *app.App, hub *Hub, s *tenant.Scope, slug string, in PresetAdd) (string, []string, error) {
	p := FindPreset(slug)
	if p == nil {
		return "", nil, huma.Error404NotFound("no such preset")
	}
	sctx := tenant.WithWorkspace(ctx, s.WorkspaceID)
	var exists bool
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT EXISTS (SELECT 1 FROM automation_workflows WHERE workspace_id = $1 AND preset = $2)`,
		s.WorkspaceID, p.Slug).Scan(&exists)
	if exists {
		return "", nil, huma.Error409Conflict(ErrPresetAdded.Error())
	}
	// Adding sites to a browser is an admin's decision, checked before
	// anything is written so a refusal leaves nothing half done.
	if in.AgentID != "" && !authz.Can(s.Role, authz.AutomationManage) {
		return "", nil, huma.Error403Forbidden("only an admin can add sites to a browser")
	}
	profile, err := ReadProfile(ctx, a, s.WorkspaceID)
	if err != nil {
		return "", nil, huma.Error500InternalServerError("could not read the workspace profile")
	}
	number := in.WhatsApp
	if strings.TrimSpace(number) == "" {
		number = workspaceWhatsApp(ctx, a, s.WorkspaceID)
	}
	wf, err := PresetWorkflowIn(p, profile, number, in.Locale)
	if errors.Is(err, ErrNeedsWhatsApp) {
		return "", nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if err != nil {
		return "", nil, huma.Error500InternalServerError("could not read that preset")
	}
	if err := ValidateWorkflow(wf); err != nil {
		return "", nil, huma.Error422UnprocessableEntity(err.Error())
	}
	// Its slug may be taken by a workflow of the workspace's own.
	var slugTaken bool
	_ = a.SQLDB.QueryRowContext(sctx, `SELECT EXISTS (SELECT 1 FROM automation_workflows WHERE workspace_id = $1 AND slug = $2)`,
		s.WorkspaceID, wf.Slug).Scan(&slugTaken)
	if slugTaken {
		wf.Slug = ""
	}
	id, err := CreateWorkflow(ctx, a, s.WorkspaceID, s.UserID, wf, "Added from the preset "+p.Slug)
	if errors.Is(err, ErrNameTaken) {
		return "", nil, huma.Error409Conflict("a workflow called " + p.Name + " already exists")
	}
	if err != nil {
		return "", nil, huma.Error500InternalServerError("could not save the workflow")
	}
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE automation_workflows SET hosts = $3::text[], icon = $4, writes = $5, preset = $6
		WHERE workspace_id = $1 AND id = $2`,
		s.WorkspaceID, id, pgtext.Literal(cleanHosts(p.Hosts)), p.Icon, p.Writes, p.Slug); err != nil {
		return "", nil, huma.Error500InternalServerError("could not save the workflow")
	}
	var added []string
	if in.AgentID != "" {
		_, added, err = ChangeAllowedHosts(ctx, a, hub, s.WorkspaceID, in.AgentID, HostChange{Add: p.Hosts})
		if err != nil {
			return id, nil, err
		}
	}
	return id, added, nil
}

// HostChange edits a browser's allowed sites: Replace sets the whole list
// (when not nil); otherwise Add and Remove edit it.
type HostChange struct {
	Add, Remove []string
	Replace     []string
}

// ChangeAllowedHosts applies a change to one browser's allowed sites and
// pushes the new list to it. Returns the new list and what was added. The
// caller has checked the member may manage automation.
func ChangeAllowedHosts(ctx context.Context, a *app.App, hub *Hub, workspaceID, agentID string, ch HostChange) ([]string, []string, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var hosts pgtext.Array
	var revoked sql.NullTime
	err := a.SQLDB.QueryRowContext(sctx, `SELECT allowed_hosts, revoked_at FROM browser_agents WHERE workspace_id = $1 AND id = $2`,
		workspaceID, agentID).Scan(&hosts, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, huma.Error404NotFound("no such browser")
	}
	if err != nil {
		return nil, nil, huma.Error500InternalServerError("could not read that browser")
	}
	if revoked.Valid {
		return nil, nil, huma.Error409Conflict(ErrRevoked.Error())
	}
	before := cleanHosts(hosts)
	set := map[string]bool{}
	if ch.Replace != nil {
		for _, h := range cleanHosts(ch.Replace) {
			set[h] = true
		}
	} else {
		for _, h := range before {
			set[h] = true
		}
		for _, h := range cleanHosts(ch.Add) {
			set[h] = true
		}
		for _, h := range cleanHosts(ch.Remove) {
			delete(set, h)
		}
	}
	next := make([]string, 0, len(set))
	for h := range set {
		next = append(next, h)
	}
	sort.Strings(next)
	was := map[string]bool{}
	for _, h := range before {
		was[h] = true
	}
	var added []string
	for _, h := range next {
		if !was[h] {
			added = append(added, h)
		}
	}
	if _, err := a.SQLDB.ExecContext(sctx, `UPDATE browser_agents SET allowed_hosts = $3::text[], updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		workspaceID, agentID, pgtext.Literal(next)); err != nil {
		return nil, nil, huma.Error500InternalServerError("could not save the allowed sites")
	}
	PushAgentUpdate(ctx, a, hub, workspaceID, agentID)
	return next, added, nil
}

// PresetOut is a preset as the gallery shows it: whether this workspace has
// added it already, and as which workflow.
type PresetOut struct {
	Preset
	WorkflowID string `json:"workflow_id,omitempty"`
}

// RegisterPresets mounts the gallery's endpoints and the workspace profile.
func RegisterPresets(api huma.API, a *app.App, hub *Hub) {
	huma.Register(api, huma.Operation{
		OperationID: "w-automation-presets", Method: http.MethodGet, Path: "/api/w/automation/presets",
		Summary: "The preset library: fadymondy.com's browser plugins, ready to add as workflows", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[struct {
		Version int         `json:"version"`
		Presets []PresetOut `json:"presets"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		added := map[string]string{}
		rows, err := a.SQLDB.QueryContext(ctx, `SELECT preset, id FROM automation_workflows WHERE workspace_id = $1 AND preset <> ''`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the workflows")
		}
		defer rows.Close()
		for rows.Next() {
			var slug, id string
			if rows.Scan(&slug, &id) == nil {
				added[slug] = id
			}
		}
		version, list := Presets()
		outList := make([]PresetOut, 0, len(list))
		for _, p := range list {
			outList = append(outList, PresetOut{Preset: p, WorkflowID: added[p.Slug]})
		}
		return out(struct {
			Version int         `json:"version"`
			Presets []PresetOut `json:"presets"`
		}{version, outList}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-add-automation-preset", Method: http.MethodPost, Path: "/api/w/automation/presets/{slug}/add",
		Summary: "Add a preset as one of the workspace's workflows, optionally allowing its sites on a browser", Tags: tags,
		Description: "Saves the preset as a workflow the workspace owns. Its profile defaults come from the workspace " +
			"profile; a preset that sends to the workspace's own WhatsApp number takes whatsapp_number, or the linked " +
			"number. agent_id adds the preset's sites to that browser's allowed sites (automation:manage) and pushes " +
			"them to it.",
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Slug string `path:"slug" maxLength:"80"`
		Body struct {
			AgentID        string `json:"agent_id,omitempty" doc:"A browser to allow the preset's sites on."`
			WhatsAppNumber string `json:"whatsapp_number,omitempty" maxLength:"30"`
			Locale         string `json:"locale,omitempty" enum:",en,ar" doc:"The language the new workflow is named in."`
		}
	}) (*body[struct {
		Workflow   Workflow `json:"workflow"`
		HostsAdded []string `json:"hosts_added"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		id, added, err := AddPreset(ctx, a, hub, s, in.Slug, PresetAdd{AgentID: strings.TrimSpace(in.Body.AgentID), WhatsApp: in.Body.WhatsAppNumber, Locale: in.Body.Locale})
		if err != nil {
			return nil, err
		}
		w, err := getWorkflow(ctx, a.SQLDB, s.WorkspaceID, id)
		if err != nil {
			return nil, err
		}
		if added == nil {
			added = []string{}
		}
		return out(struct {
			Workflow   Workflow `json:"workflow"`
			HostsAdded []string `json:"hosts_added"`
		}{w, added}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-automation-profile", Method: http.MethodGet, Path: "/api/w/automation/profile",
		Summary: "The workspace profile presets fill their defaults from", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[struct {
		Profile map[string]string `json:"profile"`
		Fields  []string          `json:"fields"`
	}], error) {
		s, err := scope(ctx, authz.AutomationRead)
		if err != nil {
			return nil, err
		}
		p, err := ReadProfile(ctx, a, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the workspace profile")
		}
		return out(struct {
			Profile map[string]string `json:"profile"`
			Fields  []string          `json:"fields"`
		}{p, ProfileFields}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-save-automation-profile", Method: http.MethodPut, Path: "/api/w/automation/profile",
		Summary: "Replace the workspace profile", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Profile map[string]string `json:"profile"`
		}
	}) (*body[struct {
		Profile map[string]string `json:"profile"`
	}], error) {
		s, err := scope(ctx, authz.AutomationManage)
		if err != nil {
			return nil, err
		}
		p, err := SaveProfile(ctx, a, s.WorkspaceID, in.Body.Profile)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not save the workspace profile")
		}
		// Every connected browser's popup reads the profile it holds now
		// (MH-478); a save reaches them without anyone reconnecting.
		PushProfileUpdate(ctx, a, hub, s.WorkspaceID)
		return out(struct {
			Profile map[string]string `json:"profile"`
		}{p}), nil
	})
}

// PresetSummary is one line about a preset, for a tool's answer.
func PresetSummary(p *Preset) string {
	marker := ""
	if p.Writes {
		marker = " [writes]"
	}
	return fmt.Sprintf("%s — %s%s (sites: %s)", p.Slug, p.Name, marker, strings.Join(p.Hosts, ", "))
}

// NormaliseHost is the allowed-sites rule's host normalisation, for callers
// outside the package (the MCP tools).
func NormaliseHost(v string) string { return normaliseHost(v) }

// PluginSave is browser_plugin_save's input (fadymondy.com's savePlugin).
type PluginSave struct {
	Slug, Name, Description, Icon string
	Hosts                         []string
	Params                        []Param
	Steps                         []Step
	Writes, Enabled               *bool
}

// PluginSaveResult is what SavePlugin answers: ok is false only for a bad
// input (never a database error, which is returned separately).
type PluginSaveResult struct {
	OK              bool   `json:"ok"`
	Reason          string `json:"reason,omitempty"`
	Slug            string `json:"slug,omitempty"`
	Name            string `json:"name,omitempty"`
	Created         bool   `json:"created,omitempty"`
	Changed         bool   `json:"changed,omitempty"`
	Version         int    `json:"version,omitempty"`
	PreviousVersion int    `json:"previous_version,omitempty"`
	StepCount       int    `json:"step_count,omitempty"`
}

// SavePlugin creates or updates a workflow by slug, the way fadymondy.com's
// browser_plugin_save does: the slug is the identity, and a change to the
// steps or inputs keeps the previous definition as a version, while a new
// name, description or list of sites does not. The sites are recorded, never
// allowed: a run is checked against the browser's own list.
func SavePlugin(ctx context.Context, a *app.App, workspaceID, userID string, in PluginSave) (*PluginSaveResult, error) {
	slug := strings.ToLower(strings.TrimSpace(in.Slug))
	if !slugRE.MatchString(slug) {
		return &PluginSaveResult{Reason: "slug must be lowercase letters and digits separated by dashes, e.g. search-google"}, nil
	}
	wf := WorkflowInput{Name: in.Name, Slug: slug, Description: in.Description, Steps: in.Steps, Params: in.Params}
	if err := ValidateWorkflow(wf); err != nil {
		return &PluginSaveResult{Reason: err.Error()}, nil
	}
	wf = cleanInput(wf)
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	var id, steps, params string
	var version int
	err := a.SQLDB.QueryRowContext(sctx, `SELECT id, steps::text, params::text, version FROM automation_workflows WHERE workspace_id = $1 AND slug = $2`,
		workspaceID, slug).Scan(&id, &steps, &params, &version)
	nameTaken := &PluginSaveResult{Reason: "another workflow is already called " + wf.Name + "; pick another name"}
	created, changed := false, false
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id, err = CreateWorkflow(ctx, a, workspaceID, userID, wf, "Saved over MCP")
		if errors.Is(err, ErrNameTaken) {
			return nameTaken, nil
		}
		if err != nil {
			return nil, err
		}
		created, version = true, 1
	case err != nil:
		return nil, err
	default:
		// Compared as stored, after a round trip through the same types.
		var oldSteps []Step
		var oldParams []Param
		_ = json.Unmarshal([]byte(steps), &oldSteps)
		_ = json.Unmarshal([]byte(params), &oldParams)
		a1, _ := json.Marshal(oldSteps)
		b1, _ := json.Marshal(wf.Steps)
		a2, _ := json.Marshal(firstNonNil(oldParams))
		b2, _ := json.Marshal(firstNonNil(wf.Params))
		if string(a1) != string(b1) || string(a2) != string(b2) {
			next, err := SaveWorkflow(ctx, a, workspaceID, userID, id, wf, 0)
			if errors.Is(err, ErrNameTaken) {
				return nameTaken, nil
			}
			if err != nil {
				return nil, err
			}
			version, changed = next, true
		} else if _, err := a.SQLDB.ExecContext(sctx, `UPDATE automation_workflows SET name = $3, description = $4, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			workspaceID, id, wf.Name, wf.Description); pgCode(err) == "23505" {
			return nameTaken, nil
		} else if err != nil {
			return nil, err
		}
	}
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE automation_workflows SET hosts = $3::text[],
		    icon = CASE WHEN $4 = '' THEN icon ELSE $4 END,
		    writes = COALESCE($5, writes), enabled = COALESCE($6, enabled)
		WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id, pgtext.Literal(cleanHosts(in.Hosts)), strings.TrimSpace(in.Icon), in.Writes, in.Enabled); err != nil {
		return nil, err
	}
	res := &PluginSaveResult{OK: true, Slug: slug, Name: wf.Name, Created: created, Changed: changed, Version: version, StepCount: len(wf.Steps)}
	if changed {
		res.PreviousVersion = version - 1
	}
	return res, nil
}
