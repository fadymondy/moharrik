package server

import (
	"encoding/json"
	"regexp"
	"sort"
	"testing"
)

// Every schema in the OpenAPI export has a real name. An anonymous struct as a
// response body gets its Go type string as a name: production published
// GET /api/w/company-suggestions with a $schema of
// ".../schemas/BodyStruct { Suggestions ListCompanySuggestion "json:\"suggestions\""; ... }Body.json".
// That name is also every client generator's type name, so none may come back:
// a response body is a named type.
func TestOpenAPISchemaNamesAreNames(t *testing.T) {
	spec := openAPISpec()
	// Letters, digits, _ and -: Huma's names for inline request bodies carry the
	// operation id ("Accept-invitationRequest"), which is a name. A space, a
	// brace or a quote is a Go type string that leaked.
	valid := regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	var bad []string
	for name := range spec.Components.Schemas.Map() {
		if !valid.MatchString(name) {
			bad = append(bad, name)
		}
	}
	// And every reference, in case a name is only ever reached through one.
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`"\$ref":"#/components/schemas/([^"]*)"`).FindAllStringSubmatch(string(raw), -1) {
		if !valid.MatchString(m[1]) {
			bad = append(bad, "ref "+m[1])
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("OpenAPI schema name is not a name (an anonymous struct as a body?): %q", b)
	}
	// The one production showed, by the name it has now.
	if op := spec.Paths["/api/w/company-suggestions"]; op == nil || op.Get == nil ||
		op.Get.Responses["200"].Content["application/json"].Schema.Ref != "#/components/schemas/CompanySuggestionList" {
		t.Error("GET /api/w/company-suggestions does not answer a CompanySuggestionList")
	}
	if len(spec.Components.Schemas.Map()) < 50 {
		t.Fatalf("only %d schemas — the export is broken", len(spec.Components.Schemas.Map()))
	}
}
