package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

/*
Workflows (MH-233): a named sequence of steps with placeholders, so a sequence
worth keeping can be run without retyping it.

Three rules decide the shape of this file:

	SUBSTITUTE INTO A COPY. The saved workflow keeps its placeholders for ever;
	a run gets the filled-in steps. Rewriting the stored steps would mean the
	first run consumed the workflow.

	A SECRET NEVER REACHES THE RUN RECORD. A run is read back by a person and
	handed to a model, so a password substituted into step args would live in
	both, for ever. Secret parameters are redacted from what is stored.

	THE ALLOW-LIST IS CHECKED AFTER SUBSTITUTION. A step written as
	{"url": "https://{{host}}/x"} passes any check made before the value is
	known — which is precisely how a placeholder would smuggle a host past it.
*/

// Param is one value a workflow asks for.
type Param struct {
	Name     string `json:"name"`
	Label    string `json:"label,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Secret values are redacted from the run record. Use it for anything that
	// would be embarrassing in a log: a password, a token, a customer's id.
	Secret  bool   `json:"secret,omitempty"`
	Default string `json:"default,omitempty"`
	// A line shown under the field when someone runs or schedules it (MH-478).
	Help string `json:"help,omitempty"`
}

// placeholderRE matches {{name}} with optional spaces.
var placeholderRE = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// Placeholders returns every placeholder name used by these steps, in order of
// first appearance, so the UI can offer the parameters a workflow actually
// needs rather than what someone remembered to declare.
func Placeholders(steps []Step) []string {
	raw, err := json.Marshal(steps)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range placeholderRE.FindAllStringSubmatch(string(raw), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// MissingParam names a required value that was not supplied.
type MissingParam struct{ Name string }

func (e *MissingParam) Error() string {
	return fmt.Sprintf("this workflow needs a value for %q", e.Name)
}

// Fill returns a COPY of steps with the placeholders replaced, plus the same
// copy with secret values redacted — the first is what runs, the second is what
// is stored on the run.
//
// An unknown placeholder is left exactly as written rather than replaced with
// an empty string: "https://{{host}}/x" becoming "https:///x" is a silent
// failure, while leaving it visible fails where someone can see it.
func Fill(steps []Step, params []Param, values map[string]string) (run []Step, stored []Step, err error) {
	filled := map[string]string{}
	redactedValues := map[string]string{}

	for _, p := range params {
		v, ok := values[p.Name]
		if !ok || strings.TrimSpace(v) == "" {
			v = p.Default
		}
		if p.Required && strings.TrimSpace(v) == "" {
			return nil, nil, &MissingParam{Name: p.Name}
		}
		filled[p.Name] = v
		if p.Secret && v != "" {
			redactedValues[p.Name] = "••••••"
		} else {
			redactedValues[p.Name] = v
		}
	}
	// Values supplied for placeholders nobody declared are still used: a
	// workflow edited to add a placeholder should not stop working because its
	// parameter list is out of date.
	for k, v := range values {
		if _, declared := filled[k]; !declared {
			filled[k], redactedValues[k] = v, v
		}
	}

	run, err = substitute(steps, filled)
	if err != nil {
		return nil, nil, err
	}
	stored, err = substitute(steps, redactedValues)
	if err != nil {
		return nil, nil, err
	}
	return run, stored, nil
}

func substitute(steps []Step, values map[string]string) ([]Step, error) {
	raw, err := json.Marshal(steps)
	if err != nil {
		return nil, err
	}
	out := placeholderRE.ReplaceAllStringFunc(string(raw), func(m string) string {
		name := placeholderRE.FindStringSubmatch(m)[1]
		v, ok := values[name]
		if !ok {
			return m // left visible on purpose
		}
		// The steps are JSON, so the value has to be escaped as JSON would
		// escape it — a value containing a quote would otherwise produce a
		// document that no longer parses.
		esc, err := json.Marshal(v)
		if err != nil {
			return m
		}
		return strings.Trim(string(esc), `"`)
	})

	var copied []Step
	if err := json.Unmarshal([]byte(out), &copied); err != nil {
		return nil, errors.New("the filled-in steps are not usable; check the values for stray quotes")
	}
	return copied, nil
}
