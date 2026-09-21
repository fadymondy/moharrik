package automation

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func steps(t *testing.T, raw string) []Step {
	t.Helper()
	var s []Step
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPlaceholdersAreFoundWhereverTheyAre(t *testing.T) {
	got := Placeholders(steps(t, `[
	  {"command":"goto","args":{"url":"https://{{host}}/orders/{{ order_id }}"}},
	  {"command":"type","args":{"selector":"#q","text":"{{order_id}}"}}
	]`))
	if len(got) != 2 || got[0] != "host" || got[1] != "order_id" {
		t.Errorf("placeholders = %v, want [host order_id] in order of first use", got)
	}
	if got := Placeholders(steps(t, `[{"command":"click"}]`)); len(got) != 0 {
		t.Errorf("a workflow with no placeholders has none, got %v", got)
	}
}

func TestFillSubstitutesIntoACopy(t *testing.T) {
	original := steps(t, `[{"command":"goto","args":{"url":"https://{{host}}/x"}}]`)
	run, _, err := Fill(original, []Param{{Name: "host"}}, map[string]string{"host": "example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if run[0].Args["url"] != "https://example.com/x" {
		t.Errorf("filled url = %v", run[0].Args["url"])
	}
	// The saved workflow keeps its placeholder: the first run must not consume it.
	if original[0].Args["url"] != "https://{{host}}/x" {
		t.Errorf("the stored steps were rewritten to %v", original[0].Args["url"])
	}
}

func TestFillRefusesAMissingRequiredValue(t *testing.T) {
	_, _, err := Fill(steps(t, `[{"command":"goto","args":{"url":"https://{{host}}/"}}]`),
		[]Param{{Name: "host", Required: true}}, map[string]string{})
	var missing *MissingParam
	if !errors.As(err, &missing) || missing.Name != "host" {
		t.Fatalf("err = %v, want a missing-parameter error naming host", err)
	}
	// Blank counts as missing: an empty string in a URL is not a value.
	if _, _, err := Fill(steps(t, `[{"command":"goto"}]`), []Param{{Name: "host", Required: true}}, map[string]string{"host": "   "}); err == nil {
		t.Error("whitespace should not satisfy a required parameter")
	}
}

func TestFillUsesDefaultsAndKeepsUndeclaredValues(t *testing.T) {
	run, _, err := Fill(steps(t, `[{"command":"goto","args":{"url":"https://{{host}}/{{path}}"}}]`),
		[]Param{{Name: "host", Default: "example.com"}},
		map[string]string{"path": "reports"}, // path is not declared, and still works
	)
	if err != nil {
		t.Fatal(err)
	}
	if run[0].Args["url"] != "https://example.com/reports" {
		t.Errorf("url = %v", run[0].Args["url"])
	}
}

func TestFillLeavesAnUnknownPlaceholderVisible(t *testing.T) {
	run, _, err := Fill(steps(t, `[{"command":"goto","args":{"url":"https://{{nobody_set_this}}/x"}}]`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// "https:///x" would be a silent failure; leaving it visible fails where
	// someone can see it — and the allow-list refuses it either way.
	if run[0].Args["url"] != "https://{{nobody_set_this}}/x" {
		t.Errorf("url = %v, want the placeholder left as written", run[0].Args["url"])
	}
}

func TestFillRedactsSecretsFromWhatIsStored(t *testing.T) {
	run, stored, err := Fill(
		steps(t, `[{"command":"type","args":{"selector":"#pass","text":"{{password}}"}}]`),
		[]Param{{Name: "password", Secret: true}},
		map[string]string{"password": "hunter2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if run[0].Args["text"] != "hunter2" {
		t.Errorf("the run must get the real value, got %v", run[0].Args["text"])
	}
	if s, _ := stored[0].Args["text"].(string); strings.Contains(s, "hunter2") {
		t.Errorf("the stored steps contain the secret: %v", stored[0].Args["text"])
	}
}

func TestFillEscapesValuesSoTheStepsStayParseable(t *testing.T) {
	// A value with a quote and a backslash would break the JSON document the
	// substitution happens in, if it were pasted in raw.
	run, _, err := Fill(steps(t, `[{"command":"type","args":{"text":"{{v}}"}}]`), nil,
		map[string]string{"v": `say "hi" \ now`})
	if err != nil {
		t.Fatal(err)
	}
	if run[0].Args["text"] != `say "hi" \ now` {
		t.Errorf("text = %q — the value should survive intact", run[0].Args["text"])
	}
}

func TestFilledStepsAreWhatTheAllowListSees(t *testing.T) {
	// The point of checking AFTER substitution: this workflow looks harmless
	// until someone supplies the host.
	run, _, err := Fill(steps(t, `[{"command":"goto","args":{"url":"https://{{host}}/x"}}]`), nil,
		map[string]string{"host": "evil.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSteps(run, []string{"example.com"}); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("err = %v, want the allow-list to refuse evil.test", err)
	}
	if err := CheckSteps(run, []string{"evil.test"}); err != nil {
		t.Errorf("a host that IS allowed should pass: %v", err)
	}
}
