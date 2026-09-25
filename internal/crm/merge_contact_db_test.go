package crm

/*
MergeContact (MH-647), against a real Postgres database with RLS on: the
route (crm.MergeContact — internal/crm/routes.go's w-merge-contact wraps it
directly) must never 5xx on a bad request. Self-merge, a missing contact and
another workspace's contact all come back a 4xx huma.StatusError instead.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/crm/ -run MergeContact -v
*/

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var se huma.StatusError
	if !errors.As(err, &se) {
		t.Fatalf("error %v does not carry an HTTP status (not a huma.StatusError)", err)
	}
	return se.GetStatus()
}

func TestMergeContactRefusesSelfMerge(t *testing.T) {
	a := testApp(t)
	f := newCRMWorkspace(t, a)
	contactID := newContact(t, a, f.wsID, "Nour")

	_, err := MergeContact(f.ctx, a.SQLDB, contactID, MergeContactInput{SourceID: contactID})
	if err == nil {
		t.Fatal("merging a contact into itself must fail")
	}
	if status := statusOf(t, err); status < 400 || status >= 500 {
		t.Fatalf("self-merge status = %d, want a 4xx", status)
	}
}

func TestMergeContactRefusesMissingSource(t *testing.T) {
	a := testApp(t)
	f := newCRMWorkspace(t, a)
	dstID := newContact(t, a, f.wsID, "Yara")

	_, err := MergeContact(f.ctx, a.SQLDB, dstID, MergeContactInput{SourceID: "00000000-0000-0000-0000-000000000000"})
	if err == nil {
		t.Fatal("merging a nonexistent source contact must fail")
	}
	if status := statusOf(t, err); status != http.StatusNotFound {
		t.Fatalf("missing source status = %d, want 404", status)
	}
}

func TestMergeContactRefusesMissingDestination(t *testing.T) {
	a := testApp(t)
	f := newCRMWorkspace(t, a)
	srcID := newContact(t, a, f.wsID, "Hana")

	_, err := MergeContact(f.ctx, a.SQLDB, "00000000-0000-0000-0000-000000000000", MergeContactInput{SourceID: srcID})
	if err == nil {
		t.Fatal("merging into a nonexistent destination contact must fail")
	}
	if status := statusOf(t, err); status != http.StatusNotFound {
		t.Fatalf("missing destination status = %d, want 404", status)
	}
}

func TestMergeContactRefusesAnotherWorkspacesContact(t *testing.T) {
	a := testApp(t)
	f1 := newCRMWorkspace(t, a)
	f2 := newCRMWorkspace(t, a)
	dstID := newContact(t, a, f1.wsID, "Workspace One's Contact")
	otherID := newContact(t, a, f2.wsID, "Workspace Two's Contact")

	// From workspace one's scope, workspace two's contact is invisible under
	// RLS — same as any other lookup — and must come back 404, not a leak
	// and not a 500.
	if _, err := MergeContact(f1.ctx, a.SQLDB, dstID, MergeContactInput{SourceID: otherID}); err == nil {
		t.Error("merging in another workspace's contact as the source must fail")
	} else if status := statusOf(t, err); status != http.StatusNotFound {
		t.Errorf("cross-workspace source status = %d, want 404", status)
	}
	if _, err := MergeContact(f1.ctx, a.SQLDB, otherID, MergeContactInput{SourceID: dstID}); err == nil {
		t.Error("merging into another workspace's contact as the destination must fail")
	} else if status := statusOf(t, err); status != http.StatusNotFound {
		t.Errorf("cross-workspace destination status = %d, want 404", status)
	}

	// Neither contact should have been touched.
	var count int
	if err := a.SysDB.QueryRowContext(context.Background(), `SELECT count(*) FROM contacts WHERE id IN ($1, $2)`,
		dstID, otherID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("both contacts must still exist untouched, found %d", count)
	}
}

func TestMergeContactSurvivesAndReturnsIt(t *testing.T) {
	a := testApp(t)
	f := newCRMWorkspace(t, a)
	dstID := newContact(t, a, f.wsID, "Survivor")
	srcID := newContact(t, a, f.wsID, "Duplicate")

	c, err := MergeContact(f.ctx, a.SQLDB, dstID, MergeContactInput{SourceID: srcID})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if c.ID != dstID {
		t.Fatalf("returned contact id = %q, want the destination %q", c.ID, dstID)
	}
	var count int
	if err := a.SysDB.QueryRowContext(context.Background(), `SELECT count(*) FROM contacts WHERE id = $1`, srcID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("the source contact must be gone after a merge")
	}
}
