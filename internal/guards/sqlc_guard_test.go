// Package guards holds repository-wide CI checks that are not about one package.
package guards

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tenantTables are the tables whose rows belong to a workspace. `workspaces`
// is the tenant itself: its predicate is its own id.
var tenantTables = map[string]string{
	"workspaces":        `\bid\s*=\s*\$\d+`,
	"workspace_members": `\bworkspace_id\s*=\s*\$\d+`,
	"invitations":       `\bworkspace_id\s*=\s*\$\d+`,
	"mcp_tokens":        `\bworkspace_id\s*=\s*\$\d+`,
}

var tableRef = regexp.MustCompile(`(?i)\b(?:from|join|update|into)\s+([a-z_]+)`)

/*
TestSqlcQueriesCarryTenantPredicate fails any sqlc query on a tenant table that
does not constrain the workspace (plan, tenancy step 4). RLS would still stop a
cross-tenant read through the app pool; this makes the query itself correct so
that the policy is the backstop and not the only fence.

An INSERT must list workspace_id among its columns. A query that is genuinely
cross-tenant must say so with `-- guard:cross-tenant <reason>`, which a
reviewer sees.
*/
func TestSqlcQueriesCarryTenantPredicate(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "db", "queries", "*.sql"))
	if len(files) == 0 {
		t.Fatal("no query files found — has the layout moved?")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range strings.Split(string(raw), "-- name:")[1:] {
			name := strings.Fields(block)[0]
			if strings.Contains(block, "-- guard:cross-tenant") {
				continue
			}
			sql := stripComments(block)
			for _, m := range tableRef.FindAllStringSubmatch(sql, -1) {
				table := strings.ToLower(m[1])
				pred, tenant := tenantTables[table]
				if !tenant {
					continue
				}
				if regexp.MustCompile(`(?i)^\s*insert\b`).MatchString(sql) {
					if !strings.Contains(sql, "workspace_id") {
						t.Errorf("%s: %s inserts into %s without workspace_id", filepath.Base(f), name, table)
					}
					continue
				}
				if !regexp.MustCompile(`(?i)` + pred).MatchString(sql) {
					t.Errorf("%s: %s touches %s without a tenant predicate (%s)", filepath.Base(f), name, table, pred)
				}
			}
		}
	}
}

func stripComments(block string) string {
	lines := strings.Split(block, "\n")[1:] // first line is the query name
	out := lines[:0]
	for _, l := range lines {
		if !strings.HasPrefix(strings.TrimSpace(l), "--") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
