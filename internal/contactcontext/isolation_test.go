package contactcontext

import (
	"testing"

	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
MH-586 privacy/RLS gate: workspace B must never be able to read workspace A's
contact context — imports, notes, retrieval passages, ask answers or
reports — even when it has A's real contact id in hand (an id is not a
secret; the workspace boundary is what has to hold). Two layers are exercised
together here, the way a real call would hit them: the app-level WHERE
workspace_id=$1 AND contact_id=$2 filter AND the Postgres RLS policy
(internal/db/post/174_contact_context.sql) driven by
tenant.WithWorkspace's session variable — either one failing alone should
still leave the other closed.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/contactcontext/ -run Isolation -v
*/

func TestIsolationCrossWorkspaceContextIsInvisible(t *testing.T) {
	a := testApp(t)
	fa := newWorkspaceWithContact(t, a) // workspace A + contact "Sara Ali"
	fb := newWorkspaceWithContact(t, a) // an unrelated workspace B

	// Fill A with everything this feature writes: a WhatsApp import, a note
	// and a generated report.
	commit, err := Commit(fa.ctx, a, fa.wsID, fa.contactID, fa.userID, "android_export.txt", androidFixtureBytes(t), "Sara Ali")
	if err != nil {
		t.Fatalf("seeding A's import: %v", err)
	}
	if _, err := AddNote(fa.ctx, a, fa.wsID, fa.contactID, "note", "Renewal", "Sara asked about renewing her plan next month.", "", fa.userID); err != nil {
		t.Fatalf("seeding A's note: %v", err)
	}
	if _, err := GenerateReport(fa.ctx, a, nil, fa.wsID, fa.contactID, fa.userID); err != nil {
		t.Fatalf("seeding A's report: %v", err)
	}

	// B's own tenant-scoped ctx (its session variable is set to B), but every
	// call below is asked about A's contactID — the cross-tenant attempt.
	bctx := fb.ctx

	t.Run("GetContextSummary", func(t *testing.T) {
		sum, err := GetContextSummary(bctx, a, fb.wsID, fa.contactID)
		if err == nil {
			t.Fatalf("expected an error (contact not found under B), got a summary: %+v", sum)
		}
	})

	t.Run("Retrieve", func(t *testing.T) {
		passages, err := Retrieve(bctx, a, fb.wsID, fa.contactID, "renewing")
		if err != nil {
			t.Fatalf("Retrieve should not error, just find nothing: %v", err)
		}
		if len(passages) != 0 {
			t.Fatalf("B retrieved %d passages from A's contact context: %+v", len(passages), passages)
		}
	})

	t.Run("AskAboutContact", func(t *testing.T) {
		// loadContactBrief fails (no such contact under B), so the whole call
		// must fail rather than answer with an empty-but-200 result that could
		// be mistaken for "this contact really has no notes".
		ans, err := AskAboutContact(bctx, a, nil, fb.wsID, fa.contactID, "What did Sara ask about?")
		if err == nil {
			t.Fatalf("expected an error asking about A's contact from B, got: %+v", ans)
		}
	})

	t.Run("GenerateReport", func(t *testing.T) {
		rep, err := GenerateReport(bctx, a, nil, fb.wsID, fa.contactID, fb.userID)
		if err == nil {
			t.Fatalf("expected an error generating a report for A's contact from B, got: %+v", rep)
		}
	})

	t.Run("ListReports", func(t *testing.T) {
		list, err := ListReports(bctx, a, fb.wsID, fa.contactID)
		if err != nil {
			t.Fatalf("ListReports should not error, just find nothing: %v", err)
		}
		if len(list) != 0 {
			t.Fatalf("B listed %d of A's reports: %+v", len(list), list)
		}
	})

	t.Run("RemoveImport refused across workspaces", func(t *testing.T) {
		if err := RemoveImport(bctx, a, fb.wsID, fa.contactID, commit.ImportID); err == nil {
			t.Fatal("expected RemoveImport to refuse deleting A's import when called as B")
		}
		// And A's import really does still exist — the refusal did not
		// silently half-succeed.
		imports, err := ListImports(fa.ctx, a, fa.wsID, fa.contactID)
		if err != nil {
			t.Fatalf("ListImports (A, after B's refused attempt): %v", err)
		}
		if len(imports) != 1 {
			t.Fatalf("A's import did not survive B's cross-workspace RemoveImport attempt: %+v", imports)
		}
	})

	// Belt-and-suspenders: even with B's RLS session variable, a raw query
	// naively scoped only by contact_id (the mistake this whole test guards
	// against) must still return nothing — RLS is the second lock, not the
	// only one.
	t.Run("raw RLS-scoped query sees none of A's rows", func(t *testing.T) {
		var count int
		if err := a.SQLDB.QueryRowContext(tenant.WithWorkspace(bctx, fb.wsID), `
			SELECT count(*) FROM contact_context_notes WHERE contact_id = $1`, fa.contactID).Scan(&count); err != nil {
			t.Fatalf("raw notes query: %v", err)
		}
		if count != 0 {
			t.Fatalf("RLS let B's session see %d of A's contact_context_notes rows by contact_id alone", count)
		}
		if err := a.SQLDB.QueryRowContext(tenant.WithWorkspace(bctx, fb.wsID), `
			SELECT count(*) FROM contact_context_imports WHERE contact_id = $1`, fa.contactID).Scan(&count); err != nil {
			t.Fatalf("raw imports query: %v", err)
		}
		if count != 0 {
			t.Fatalf("RLS let B's session see %d of A's contact_context_imports rows by contact_id alone", count)
		}
		if err := a.SQLDB.QueryRowContext(tenant.WithWorkspace(bctx, fb.wsID), `
			SELECT count(*) FROM contact_context_reports WHERE contact_id = $1`, fa.contactID).Scan(&count); err != nil {
			t.Fatalf("raw reports query: %v", err)
		}
		if count != 0 {
			t.Fatalf("RLS let B's session see %d of A's contact_context_reports rows by contact_id alone", count)
		}
	})
}
