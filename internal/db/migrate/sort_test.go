package migrate

import (
	"reflect"
	"testing"
)

// A three-digit post file must run after the two-digit ones it builds on; a
// string sort put "105_" before "10_" and ran it before the CRM tables had
// their constraints.
func TestSortMigrationsNumeric(t *testing.T) {
	got := []string{"x/85_automation_connect.sql", "x/105_crm_reporting.sql", "x/10_constraints.sql", "x/16_crm.sql", "x/2_a.sql", "x/100_k.sql"}
	sortMigrations(got)
	want := []string{"x/2_a.sql", "x/10_constraints.sql", "x/16_crm.sql", "x/85_automation_connect.sql", "x/100_k.sql", "x/105_crm_reporting.sql"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	// Schema fragments: numbered first, then by name, as the string sort had them.
	got = []string{"s/crm.sql", "s/0001_users.sql", "s/admin_mcp_token.sql", "s/0000_schema.sql"}
	sortMigrations(got)
	want = []string{"s/0000_schema.sql", "s/0001_users.sql", "s/admin_mcp_token.sql", "s/crm.sql"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}
