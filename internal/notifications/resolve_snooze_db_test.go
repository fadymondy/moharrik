package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Resolve and snooze (MH-1069) against a real database: the migration's columns,
the one "actionable" definition behind the default list and every count, the
endpoints' validation and permission, and workspace isolation.

	source ~/notify-env.sh && go test -count=1 ./internal/notifications/ -run Resolve -v
*/

func (f *hookFixture) seedItem(t *testing.T, title string, read bool) string {
	t.Helper()
	readExpr := "NULL"
	if read {
		readExpr = "now()"
	}
	var id string
	if err := f.a.SysDB.QueryRowContext(context.Background(), `
		INSERT INTO notification_items (workspace_id, source_name, kind, severity, title, occurred_at, read_at)
		VALUES ($1, 'Test', 'issue', 'info', $2, now(), `+readExpr+`) RETURNING id`, f.ws, title).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *hookFixture) list(t *testing.T, query string) ItemList {
	t.Helper()
	res := f.api.GetCtx(f.ctx, "/api/w/notifications"+query)
	if res.Code != http.StatusOK {
		t.Fatalf("list %q: %d %s", query, res.Code, res.Body)
	}
	var out ItemList
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func titles(l ItemList) string {
	var t []string
	for _, it := range l.Items {
		t = append(t, it.Title)
	}
	return strings.Join(t, ",")
}

func TestResolveAndSnooze(t *testing.T) {
	f := newHookFixture(t)
	open := f.seedItem(t, "open", false)
	toResolve := f.seedItem(t, "to-resolve", false)
	toSnooze := f.seedItem(t, "to-snooze", false)
	f.seedItem(t, "already-read", true)
	base := "/api/w/notifications/"

	l := f.list(t, "")
	if l.Total != 4 || l.ActionableCount != 4 || l.UnreadCount != 3 || l.ResolvedCount != 0 || l.SnoozedCount != 0 {
		t.Fatalf("start: total=%d actionable=%d unread=%d resolved=%d snoozed=%d", l.Total, l.ActionableCount, l.UnreadCount, l.ResolvedCount, l.SnoozedCount)
	}
	for _, it := range l.Items {
		if it.State != StateActionable || it.ResolvedAt != nil || it.SnoozedUntil != nil {
			t.Fatalf("fresh item %s: %+v", it.Title, it)
		}
	}

	// ---- resolve: gone from the default list and the unread count, still under resolved/all
	res := f.api.PostCtx(f.ctx, base+toResolve+"/resolve", map[string]any{})
	if res.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", res.Code, res.Body)
	}
	var one struct{ Item NotificationItemOut }
	_ = json.Unmarshal(res.Body.Bytes(), &one)
	if one.Item.State != StateResolved || one.Item.ResolvedAt == nil || one.Item.ReadAt != nil {
		t.Fatalf("resolving must set resolved_at and leave read_at alone: %+v", one.Item)
	}
	l = f.list(t, "")
	if l.Total != 3 || l.UnreadCount != 2 || l.ResolvedCount != 1 || strings.Contains(titles(l), "to-resolve") {
		t.Fatalf("after resolve: total=%d unread=%d resolved=%d %s", l.Total, l.UnreadCount, l.ResolvedCount, titles(l))
	}
	if l := f.list(t, "?view=resolved"); l.Total != 1 || titles(l) != "to-resolve" {
		t.Fatalf("view=resolved: %s", titles(l))
	}
	if l := f.list(t, "?view=all"); l.Total != 4 {
		t.Fatalf("view=all: %d", l.Total)
	}
	if res := f.api.PostCtx(f.ctx, base+toResolve+"/unresolve", map[string]any{}); res.Code != http.StatusOK {
		t.Fatalf("unresolve: %d %s", res.Code, res.Body)
	}
	if l := f.list(t, ""); l.Total != 4 || l.ResolvedCount != 0 {
		t.Fatalf("after unresolve: %d", l.Total)
	}

	// ---- snooze: hidden until the time, then back with no job in between
	until := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	res = f.api.PostCtx(f.ctx, base+toSnooze+"/snooze", map[string]any{"until": until})
	if res.Code != http.StatusOK {
		t.Fatalf("snooze: %d %s", res.Code, res.Body)
	}
	_ = json.Unmarshal(res.Body.Bytes(), &one)
	if one.Item.State != StateSnoozed || one.Item.SnoozedUntil == nil {
		t.Fatalf("snoozed item: %+v", one.Item)
	}
	l = f.list(t, "")
	if l.Total != 3 || l.UnreadCount != 2 || l.SnoozedCount != 1 || strings.Contains(titles(l), "to-snooze") {
		t.Fatalf("after snooze: total=%d unread=%d snoozed=%d %s", l.Total, l.UnreadCount, l.SnoozedCount, titles(l))
	}
	if l := f.list(t, "?view=snoozed"); titles(l) != "to-snooze" {
		t.Fatalf("view=snoozed: %s", titles(l))
	}
	// A snooze whose time has passed is not a snooze: simulate the clock moving.
	if _, err := f.a.SysDB.Exec(`UPDATE notification_items SET snoozed_until = now() - interval '1 minute' WHERE id = $1`, toSnooze); err != nil {
		t.Fatal(err)
	}
	if l := f.list(t, ""); l.Total != 4 || l.SnoozedCount != 0 || l.UnreadCount != 3 {
		t.Fatalf("an expired snooze must be actionable again: total=%d snoozed=%d unread=%d", l.Total, l.SnoozedCount, l.UnreadCount)
	}
	// Unsnooze brings it back now.
	f.api.PostCtx(f.ctx, base+toSnooze+"/snooze", map[string]any{"until": until})
	if res := f.api.PostCtx(f.ctx, base+toSnooze+"/unsnooze", map[string]any{}); res.Code != http.StatusOK {
		t.Fatalf("unsnooze: %d %s", res.Code, res.Body)
	}
	if l := f.list(t, ""); l.Total != 4 {
		t.Fatalf("after unsnooze: %d", l.Total)
	}

	// ---- validation
	for name, bad := range map[string]string{
		"past":         time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"too far":      time.Now().Add(400 * 24 * time.Hour).UTC().Format(time.RFC3339),
		"a second ago": time.Now().Add(-time.Second).UTC().Format(time.RFC3339),
	} {
		if res := f.api.PostCtx(f.ctx, base+open+"/snooze", map[string]any{"until": bad}); res.Code != http.StatusUnprocessableEntity {
			t.Errorf("snooze %s: got %d want 422: %s", name, res.Code, res.Body)
		}
	}
	if res := f.api.PostCtx(f.ctx, base+open+"/snooze", map[string]any{}); res.Code < 400 {
		t.Errorf("snooze without until must fail, got %d", res.Code)
	}
	if res := f.api.GetCtx(f.ctx, "/api/w/notifications?view=bogus"); res.Code < 400 {
		t.Errorf("bad view must fail, got %d", res.Code)
	}
	// A resolved item cannot be snoozed; resolving drops an existing snooze.
	f.api.PostCtx(f.ctx, base+open+"/resolve", map[string]any{})
	if res := f.api.PostCtx(f.ctx, base+open+"/snooze", map[string]any{"until": until}); res.Code != http.StatusConflict {
		t.Errorf("snoozing a resolved item: got %d want 409", res.Code)
	}
	f.api.PostCtx(f.ctx, base+toSnooze+"/snooze", map[string]any{"until": until})
	f.api.PostCtx(f.ctx, base+toSnooze+"/resolve", map[string]any{})
	if l := f.list(t, "?view=snoozed"); l.Total != 0 {
		t.Errorf("a resolved item must not also count as snoozed: %s", titles(l))
	}
	if res := f.api.PostCtx(f.ctx, base+"00000000-0000-0000-0000-000000000000/resolve", map[string]any{}); res.Code != http.StatusNotFound {
		t.Errorf("unknown id: got %d want 404", res.Code)
	}

	// ---- mark-all-read leaves non-actionable items alone
	f.api.PostCtx(f.ctx, base+toSnooze+"/unresolve", map[string]any{})
	f.api.PostCtx(f.ctx, base+toSnooze+"/snooze", map[string]any{"until": until})
	if res := f.api.PostCtx(f.ctx, base+"mark_all_read", map[string]any{}); res.Code != http.StatusOK {
		t.Fatalf("mark all: %d", res.Code)
	}
	var readAt *time.Time
	if err := f.a.SysDB.QueryRow(`SELECT read_at FROM notification_items WHERE id = $1`, toSnooze).Scan(&readAt); err != nil || readAt != nil {
		t.Errorf("mark_all_read must not read a snoozed item: %v %v", readAt, err)
	}
	if l := f.list(t, ""); l.UnreadCount != 0 {
		t.Errorf("unread after mark all: %d", l.UnreadCount)
	}

	// ---- saved is a shelf: a resolved saved item is still found
	f.api.PatchCtx(f.ctx, base+open, map[string]any{"saved": true})
	if l := f.list(t, "?saved=true"); titles(l) != "open" {
		t.Errorf("saved across views: %q", titles(l))
	}
}

func TestResolveSnoozePermissionsAndIsolation(t *testing.T) {
	f := newHookFixture(t)
	id := f.seedItem(t, "mine", false)
	until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	base := "/api/w/notifications/"

	// A client (below member) can neither resolve nor snooze.
	client := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: f.ws, UserID: f.user, Role: "client", Via: "session"})
	for _, p := range []string{"resolve", "unresolve", "snooze", "unsnooze"} {
		if res := f.api.PostCtx(client, base+id+"/"+p, map[string]any{"until": until}); res.Code != http.StatusForbidden {
			t.Errorf("client %s: got %d want 403", p, res.Code)
		}
	}

	// Another workspace cannot touch this item: 404, and nothing changes.
	var otherWS string
	if err := f.a.SysDB.QueryRow(`INSERT INTO workspaces (name, slug, created_by) VALUES ('Other', $1, $2) RETURNING id`,
		"other-"+time.Now().Format("150405.000000000"), f.user).Scan(&otherWS); err != nil {
		t.Fatal(err)
	}
	other := tenant.With(context.Background(), &tenant.Scope{WorkspaceID: otherWS, UserID: f.user, Role: "owner", Via: "session"})
	for _, p := range []string{"resolve", "unresolve", "snooze", "unsnooze"} {
		if res := f.api.PostCtx(other, base+id+"/"+p, map[string]any{"until": until}); res.Code != http.StatusNotFound {
			t.Errorf("other workspace %s: got %d want 404", p, res.Code)
		}
	}
	var resolved, snoozed *time.Time
	if err := f.a.SysDB.QueryRow(`SELECT resolved_at, snoozed_until FROM notification_items WHERE id = $1`, id).Scan(&resolved, &snoozed); err != nil || resolved != nil || snoozed != nil {
		t.Errorf("another workspace changed the item: %v %v %v", resolved, snoozed, err)
	}
}
