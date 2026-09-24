package migrate

import (
	"reflect"
	"testing"
)

func TestSortMigrationsIsNumeric(t *testing.T) {
	got := []string{"post/115_smtp.sql", "post/96_audit.sql", "post/10_constraints.sql", "post/20_rls.sql", "post/100_x.sql", "post/9_early.sql"}
	sortMigrations(got)
	want := []string{"post/9_early.sql", "post/10_constraints.sql", "post/20_rls.sql", "post/96_audit.sql", "post/100_x.sql", "post/115_smtp.sql"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	schema := []string{"s/workspace.sql", "s/0001_users.sql", "s/audit.sql", "s/0000_schema.sql"}
	sortMigrations(schema)
	if want := []string{"s/0000_schema.sql", "s/0001_users.sql", "s/audit.sql", "s/workspace.sql"}; !reflect.DeepEqual(schema, want) {
		t.Fatalf("schema order changed: %v", schema)
	}
}
