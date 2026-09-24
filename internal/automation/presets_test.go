package automation

import (
	"encoding/json"
	"strings"
	"testing"
)

// Every preset in the library becomes a workflow the editor would accept:
// known commands, valid parameter names, a valid slug — so "Add" never fails
// on the library's own content (MH-478).
func TestEveryPresetIsAValidWorkflow(t *testing.T) {
	version, list := Presets()
	if version < 1 || len(list) < 100 {
		t.Fatalf("library v%d with %d presets", version, len(list))
	}
	profile := map[string]string{"first_name": "Mona", "city": "Alexandria"}
	writes := 0
	for i := range list {
		p := &list[i]
		wf, err := PresetWorkflow(p, profile, "201000000001")
		if err != nil {
			t.Fatalf("%s: %v", p.Slug, err)
		}
		if err := ValidateWorkflow(wf); err != nil {
			t.Errorf("%s: %v", p.Slug, err)
		}
		if !slugRE.MatchString(wf.Slug) {
			t.Errorf("%s: slug", p.Slug)
		}
		// A preset names its sites, unless it opens whatever address it is
		// given (news-read-article): then the browser's own list decides.
		if len(p.Hosts) == 0 && !strings.HasPrefix(strings.TrimSpace(anyURL(p.Steps)), "{{") {
			t.Errorf("%s names no site", p.Slug)
		}
		raw, _ := json.Marshal(wf)
		if strings.Contains(string(raw), "{{profile.") || strings.Contains(string(raw), "{{workspace.") {
			t.Errorf("%s: a workspace placeholder survived: %s", p.Slug, raw)
		}
		if p.Writes {
			writes++
		}
		if p.DryRun != "" && FindPreset(p.DryRun) == nil {
			t.Errorf("%s: its dry-run twin %s is missing", p.Slug, p.DryRun)
		}
	}
	if writes < 20 {
		t.Fatalf("only %d presets are marked as publishing", writes)
	}
}

// Nothing personal from fadymondy.com is in the library.
func TestPresetsCarryNoPersonalData(t *testing.T) {
	_, list := Presets()
	raw, _ := json.Marshal(list)
	for _, bad := range []string{"Fady", "fadymondy", "201207860084", "201098937831", "engfadymondy", "Heliopolis"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("presets.json contains %q", bad)
		}
	}
}

// The WhatsApp-to-yourself preset goes to the workspace's own number and
// refuses to be added without one; job applications take the profile.
func TestPresetsArePersonalisedWhenAdded(t *testing.T) {
	self := FindPreset("whatsapp-send-self")
	if self == nil || !self.NeedsWhatsApp || !self.Writes {
		t.Fatalf("whatsapp-send-self: %+v", self)
	}
	if _, err := PresetWorkflow(self, nil, ""); err != ErrNeedsWhatsApp {
		t.Fatalf("no number: %v", err)
	}
	wf, err := PresetWorkflow(self, nil, "+20 100 000 0001")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(wf.Steps)
	if !strings.Contains(string(raw), "phone=201000000001") {
		t.Fatalf("the number is not in the steps: %s", raw)
	}
	for _, in := range wf.Params {
		if in.Name == "phone" || in.Name == "to" {
			t.Fatalf("a recipient input survived: %+v", wf.Params)
		}
	}

	var job *Preset
	for _, s := range []string{"job-apply-greenhouse", "job-apply-lever", "linkedin-easy-apply"} {
		if job = FindPreset(s); job != nil && len(job.NeedsProfile) > 0 {
			break
		}
	}
	if job == nil || len(job.NeedsProfile) == 0 {
		t.Fatal("no job-apply preset uses the profile")
	}
	wf, _ = PresetWorkflow(job, map[string]string{"first_name": "Mona", "email": "mona@x.test"}, "")
	got := map[string]string{}
	for _, p := range wf.Params {
		got[p.Name] = p.Default
	}
	if got["first_name"] != "Mona" {
		t.Fatalf("defaults: %v", got)
	}
}

func anyURL(steps []Step) string {
	for _, s := range steps {
		if u, ok := s.Args["url"].(string); ok {
			return u
		}
	}
	return ""
}
