package automation

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestHostAllowedMatchesSubdomainsButNotSuffixes(t *testing.T) {
	list := []string{"example.com", "Mail.Google.com "}

	for _, host := range []string{"example.com", "www.example.com", "app.example.com", "EXAMPLE.com", "example.com."} {
		if !HostAllowed(list, host) {
			t.Errorf("%q should be allowed by example.com", host)
		}
	}
	for _, host := range []string{
		"notexample.com",      // the classic suffix bug
		"example.com.evil.io", // and its mirror image
		"evil-example.com",
		"google.com",        // the entry is mail.google.com, not the apex
		"other.example.org", // different TLD
		"",
		"   ",
	} {
		if HostAllowed(list, host) {
			t.Errorf("%q must NOT be allowed", host)
		}
	}
	if HostAllowed(nil, "example.com") {
		t.Error("an empty allow-list must allow nothing — that is the default for a new browser")
	}
}

func TestNormaliseHostTakesWhatPeopleActuallyType(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com/path?q=1": "example.com",
		"HTTP://Example.COM":           "example.com",
		"example.com:8443":             "example.com",
		"example.com/":                 "example.com",
		"  example.com  ":              "example.com",
		"not a host":                   "",
		"https://":                     "",
	} {
		if got := normaliseHost(in); got != want {
			t.Errorf("normaliseHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStepHostOnlyChecksNavigations(t *testing.T) {
	host, navigates := StepHost("goto", map[string]any{"url": "https://example.com/inbox"})
	if !navigates || host != "example.com" {
		t.Errorf("goto = %q/%v", host, navigates)
	}
	if _, navigates := StepHost("click", map[string]any{"selector": "#x"}); navigates {
		t.Error("a click does not navigate; it is constrained by the navigation that preceded it")
	}
	// A navigation with nothing usable in it must still be treated as a
	// navigation, or it would skip the allow-list entirely.
	if host, navigates := StepHost("goto", map[string]any{}); !navigates || host != "" {
		t.Errorf("goto with no url = %q/%v — must be a navigation to nowhere, i.e. refused", host, navigates)
	}
	if host, navigates := StepHost("open", map[string]any{"url": "example.com/x"}); !navigates || host != "example.com" {
		t.Errorf("bare host = %q/%v", host, navigates)
	}
}

func TestCheckStepsRefusesBeforeAnythingIsQueued(t *testing.T) {
	allowed := []string{"example.com"}

	if err := CheckSteps(nil, allowed); err == nil {
		t.Error("an empty run should be refused")
	}

	ok := []Step{
		{Command: "goto", Args: map[string]any{"url": "https://app.example.com/"}},
		{Command: "click", Args: map[string]any{"selector": "#go"}},
	}
	if err := CheckSteps(ok, allowed); err != nil {
		t.Errorf("a run within the allow-list was refused: %v", err)
	}

	bad := []Step{
		{Command: "goto", Args: map[string]any{"url": "https://example.com/"}},
		{Command: "goto", Args: map[string]any{"url": "https://evil.test/"}},
	}
	err := CheckSteps(bad, allowed)
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("second step should be refused by the allow-list, got %v", err)
	}
	// The message has to say WHICH step and which host, or the person fixing it
	// is guessing.
	if !strings.Contains(err.Error(), "step 2") || !strings.Contains(err.Error(), "evil.test") {
		t.Errorf("unhelpful refusal: %q", err.Error())
	}

	var many []Step
	for i := 0; i <= MaxSteps; i++ {
		many = append(many, Step{Command: "click"})
	}
	if err := CheckSteps(many, allowed); err == nil {
		t.Error("a run past MaxSteps should be refused")
	}

	if err := CheckSteps([]Step{{Command: "  "}}, allowed); err == nil {
		t.Error("a step with no command should be refused")
	}
}

// The allow-list rule is implemented twice — here, and in the extension's
// hosts.js, which checks the page a run's tab actually lands on (MH-422). Both
// run this one table, so the two cannot drift into "similar" rules: a host the
// server would refuse must never be one the browser acts on.
func TestHostMatchingSharedWithTheExtension(t *testing.T) {
	raw, err := os.ReadFile("testdata/host_matching.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Normalise [][2]string `json:"normalise"`
		Allowed   []struct {
			List    []string `json:"list"`
			Host    string   `json:"host"`
			Allowed bool     `json:"allowed"`
		} `json:"allowed"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases.Normalise) == 0 || len(cases.Allowed) == 0 {
		t.Fatal("the shared table is empty")
	}
	for _, c := range cases.Normalise {
		if got := normaliseHost(c[0]); got != c[1] {
			t.Errorf("normaliseHost(%q) = %q, want %q", c[0], got, c[1])
		}
	}
	for _, c := range cases.Allowed {
		if got := HostAllowed(c.List, c.Host); got != c.Allowed {
			t.Errorf("HostAllowed(%q, %q) = %v, want %v", c.List, c.Host, got, c.Allowed)
		}
	}
}

// A refusal from the browser that carries the left-the-sites code is
// recognisable as one; any other refusal is not.
func TestLeftAllowedSitesIsRecognisedByItsCode(t *testing.T) {
	left := error(AgentError{Message: "step 4 went to evil.example, which isn't on this browser's allowed sites", Code: CodeLeftAllowedSites})
	if !errors.Is(left, ErrLeftAllowedSites) {
		t.Error("a coded refusal must match ErrLeftAllowedSites")
	}
	if left.Error() != "step 4 went to evil.example, which isn't on this browser's allowed sites" {
		t.Errorf("the browser's own sentence must survive: %q", left.Error())
	}
	if errors.Is(AgentError{Message: "nothing on the page matches #x"}, ErrLeftAllowedSites) {
		t.Error("an ordinary refusal must not read as leaving the sites")
	}
	if errors.Is(AgentError{Message: "x", Code: "something_else"}, ErrLeftAllowedSites) {
		t.Error("another code must not read as leaving the sites")
	}
}
