package automation

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/fadymondy/moharrik/internal/secret"
)

/*
The values a schedule fills its workflow with (MH-403).

A schedule runs with nobody present, so it must keep every value it needs —
including the ones the workflow marks SECRET, like a password. Those are split
off and sealed (AES-GCM under AUTH_SECRET, internal/secret) into their own
column. They are opened in exactly one place, at the moment the schedule runs,
and never sent back to a page or a model: a schedule reads back as "password:
set", not as the password.

The plain values stay readable in params, because "which account does this
schedule log in as" is exactly what someone reading the schedule list needs.
*/

var scheduleBox = secret.New(secret.DomainScheduleSecret, "a schedule's secret value", "enter it again on the schedule")

// SplitScheduleValues separates the values a schedule keeps into the plain
// ones and the sealed secret ones, by the workflow's own parameter list. An
// empty secret value is dropped rather than sealed: "not given" must stay
// distinguishable from "given as nothing".
func SplitScheduleValues(params []Param, values map[string]string) (plain map[string]string, sealed string, err error) {
	secretName := map[string]bool{}
	for _, p := range params {
		if p.Secret {
			secretName[p.Name] = true
		}
	}
	plain = map[string]string{}
	secrets := map[string]string{}
	for k, v := range values {
		if secretName[k] {
			if strings.TrimSpace(v) != "" {
				secrets[k] = v
			}
			continue
		}
		plain[k] = v
	}
	if len(secrets) == 0 {
		return plain, "", nil
	}
	raw, err := json.Marshal(secrets)
	if err != nil {
		return nil, "", err
	}
	sealed, err = scheduleBox.Seal(string(raw))
	return plain, sealed, err
}

// MergeScheduleValues opens the sealed values and lays them over the plain
// ones — what a run is actually filled with.
func MergeScheduleValues(plain map[string]string, sealed string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range plain {
		out[k] = v
	}
	if sealed == "" {
		return out, nil
	}
	opened, err := scheduleBox.Open(sealed)
	if err != nil {
		return nil, err
	}
	var secrets map[string]string
	if err := json.Unmarshal([]byte(opened), &secrets); err != nil {
		return nil, err
	}
	for k, v := range secrets {
		out[k] = v
	}
	return out, nil
}

// sealedNames lists which secret values a schedule holds, without handing any
// of them out. A value that can no longer be opened (AUTH_SECRET changed) is
// listed as nothing, which is what it now is.
func sealedNames(sealed string) []string {
	names := []string{}
	if sealed == "" {
		return names
	}
	opened, err := scheduleBox.Open(sealed)
	if err != nil {
		return names
	}
	var secrets map[string]string
	if json.Unmarshal([]byte(opened), &secrets) != nil {
		return names
	}
	for k := range secrets {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
