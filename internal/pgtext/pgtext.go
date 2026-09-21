// Package pgtext reads and writes Postgres text[] values without pulling in a
// driver-specific array type.
//
// It lives in its own package because two modules need it — internal/trends for
// a news item's topic ids, internal/automation for a browser's allow-list — and
// a second copy of an array parser is a second place for the quoting rules to
// be got subtly wrong.
package pgtext

import (
	"fmt"
	"strings"
)

// Array scans a Postgres text[].
type Array []string

func (a *Array) Scan(src any) error {
	*a = nil
	var s string
	switch v := src.(type) {
	case nil:
		return nil
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("pgtext: cannot scan %T into text[]", src)
	}
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil
	}
	s = s[1 : len(s)-1]
	if s == "" {
		return nil
	}
	for _, part := range split(s) {
		*a = append(*a, part)
	}
	return nil
}

// split splits on commas that are not inside quotes, and unquotes.
func split(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes, escaped := false, false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuotes = !inQuotes
		case r == ',' && !inQuotes:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	out = append(out, cur.String())
	return out
}

// Literal renders []string as a Postgres array literal.
func Literal(vals []string) string {
	if len(vals) == 0 {
		return "{}"
	}
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		v = strings.ReplaceAll(v, `\`, `\\`)
		v = strings.ReplaceAll(v, `"`, `\"`)
		parts = append(parts, `"`+v+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}
