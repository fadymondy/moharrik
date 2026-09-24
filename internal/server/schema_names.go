package server

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"regexp"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

/*
OpenAPI schema names. Huma names a schema after its Go type.
A response written as body[struct{ … }] has no type name of its own, so Huma
falls back to the generic's type string, and the export published names like

	BodyStruct { Suggestions ListCompanySuggestion "json:\"suggestions\""; Total int "json:\"total\"" }Body

as a component name, in every response's $schema link and in every client a
generator makes from the document. About three hundred of them.

apiConfig keeps Huma's own name whenever it is a name, and gives any other
schema one built from its first fields plus a short hash of its full type, so
two different anonymous structs can never collide:

	SuggestionsTotalBody_3fa2c1

A named response type is still the better answer where a route is touched (the
company suggestions list now has one); this makes sure nothing slips through.
TestOpenAPISchemaNamesAreNames walks the export and fails on any name that is not.
*/

var cleanSchemaName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func schemaNamer(t reflect.Type, hint string) string {
	name := huma.DefaultSchemaNamer(t, hint)
	if cleanSchemaName.MatchString(name) {
		return name
	}
	base := t
	for base.Kind() == reflect.Pointer || base.Kind() == reflect.Slice || base.Kind() == reflect.Array {
		base = base.Elem()
	}
	var b strings.Builder
	if base.Kind() == reflect.Struct {
		n := 0
		for i := 0; i < base.NumField() && n < 3; i++ {
			if f := base.Field(i); f.IsExported() && !f.Anonymous {
				b.WriteString(f.Name)
				n++
			}
		}
	}
	h := sha256.Sum256([]byte(t.String()))
	return b.String() + "Body_" + hex.EncodeToString(h[:3])
}

// apiConfig is Huma's default configuration with schemaNamer, for the live
// API and the OpenAPI export alike (the names must be the same in both).
func apiConfig() huma.Config {
	cfg := huma.DefaultConfig("Moharrik API", "0.1.0")
	cfg.Components.Schemas = huma.NewMapRegistry("#/components/schemas/", schemaNamer)
	return cfg
}
