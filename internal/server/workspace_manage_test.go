package server

/*
Managing a workspace (MH-679), against the real stack: renaming it, its
picture, members' roles, removing and leaving, and deleting the workspace
with everything in it — including the tables that carry a workspace_id with
no foreign key to cascade from.
*/

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWorkspaceManage(t *testing.T) {
	a, base := bootAudit(t)

	alice, bob, carol, dave := newClient(t, base), newClient(t, base), newClient(t, base), newClient(t, base)
	emails := map[*client]string{}
	ids := map[*client]string{}
	for _, c := range []*client{alice, bob, carol, dave} {
		emails[c] = "manage-" + rnd() + "@example.test"
		c.must("POST", "/api/auth/register", map[string]string{"email": emails[c], "password": "correct-horse-9"}, 201)
		var id string
		if err := a.SysDB.QueryRow(`SELECT id FROM users WHERE email = $1`, emails[c]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[c] = id
	}
	ws := alice.must("POST", "/api/me/workspaces", map[string]string{"name": "Manage " + rnd()}, 201)["workspace"].(map[string]any)
	wsID, slug := ws["id"].(string), ws["slug"].(string)
	alice.ws = wsID
	join := func(c *client, role string) {
		inv := alice.must("POST", "/api/w/invitations", map[string]string{"email": emails[c], "role": role}, 201)
		c.must("POST", "/api/invitations/accept", map[string]string{"token": strings.TrimPrefix(inv["accept_url"].(string), "/invite/")}, 200)
		c.ws = wsID
	}
	join(bob, "admin")
	join(carol, "member")
	dave.ws = wsID // not a member: every workspace call must 404

	// ---- rename -------------------------------------------------------------
	carol.must("PUT", "/api/w/workspace", map[string]string{"name": "Carol's"}, 403)
	dave.must("PUT", "/api/w/workspace", map[string]string{"name": "Dave's"}, 404)
	alice.must("PUT", "/api/w/workspace", map[string]string{"name": "  "}, 422)
	got := bob.must("PUT", "/api/w/workspace", map[string]string{"name": "Nile Team"}, 200)
	if got["name"] != "Nile Team" || got["slug"] != slug {
		t.Fatalf("rename: %v", got)
	}

	// ---- picture --------------------------------------------------------------
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(1, 1, color.RGBA{75, 0, 182, 255})
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	pic := base64.StdEncoding.EncodeToString(buf.Bytes())

	carol.must("PUT", "/api/w/workspace/avatar", map[string]string{"data": pic, "content_type": "image/png"}, 403)
	alice.must("PUT", "/api/w/workspace/avatar", map[string]string{"data": pic, "content_type": "image/gif"}, 422)
	alice.must("PUT", "/api/w/workspace/avatar", map[string]string{"data": "not base64!", "content_type": "image/png"}, 422)
	big := base64.StdEncoding.EncodeToString(make([]byte, 2<<20+1))
	alice.must("PUT", "/api/w/workspace/avatar", map[string]string{"data": big, "content_type": "image/png"}, 422)
	url1 := bob.must("PUT", "/api/w/workspace/avatar", map[string]string{"data": pic, "content_type": "image/png"}, 200)["avatar_url"].(string)
	if !strings.HasPrefix(url1, "/api/me/workspaces/"+wsID+"/avatar?v=") {
		t.Fatalf("avatar url %q", url1)
	}
	fetch := func(c *client, u string) (int, string, []byte) {
		res, err := c.http.Get(base + u)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header.Get("Content-Type"), b
	}
	if code, ct, b := fetch(carol, url1); code != 200 || ct != "image/png" || !bytes.Equal(b, buf.Bytes()) {
		t.Fatalf("member reads the picture: %d %s %d bytes", code, ct, len(b))
	}
	if code, _, _ := fetch(dave, url1); code != 404 {
		t.Fatalf("a non-member reads the picture: %d", code)
	}
	if code, _, _ := fetch(newClient(t, base), url1); code != 401 {
		t.Fatalf("anonymous reads the picture: %d", code)
	}
	url2 := alice.must("PUT", "/api/w/workspace/avatar", map[string]string{"data": pic, "content_type": "image/png"}, 200)["avatar_url"].(string)
	if url2 == url1 {
		t.Fatal("a replaced picture kept its URL, so a cached copy would show")
	}

	mine := carol.must("GET", "/api/me/workspaces", nil, 200)["workspaces"].([]any)
	if len(mine) != 1 {
		t.Fatalf("carol's workspaces: %v", mine)
	}
	row := mine[0].(map[string]any)
	if row["avatar_url"] != url2 || row["member_count"].(float64) != 3 || row["name"] != "Nile Team" {
		t.Fatalf("workspaces list row: %v", row)
	}
	if cur := carol.must("GET", "/api/w/current", nil, 200)["workspace"].(map[string]any); cur["avatar_url"] != url2 {
		t.Fatalf("current workspace avatar: %v", cur["avatar_url"])
	}
	if got := alice.must("DELETE", "/api/w/workspace/avatar", nil, 200); got["avatar_url"] != "" {
		t.Fatalf("removed picture still has a URL: %v", got)
	}
	if code, _, _ := fetch(carol, url2); code != 404 {
		t.Fatalf("a removed picture still serves: %d", code)
	}

	// ---- roles ---------------------------------------------------------------
	role := func(c *client) string {
		var r string
		_ = a.SysDB.QueryRow(`SELECT role FROM workspace_members WHERE workspace_id = $1 AND user_id = $2`, wsID, ids[c]).Scan(&r)
		return r
	}
	carol.must("PUT", "/api/w/members/"+ids[bob], map[string]string{"role": "member"}, 403) // members manage nobody
	bob.must("PUT", "/api/w/members/"+ids[alice], map[string]string{"role": "admin"}, 403) // admins don't touch owners
	bob.must("PUT", "/api/w/members/"+ids[carol], map[string]string{"role": "owner"}, 403) // …or make one
	bob.must("PUT", "/api/w/members/"+ids[dave], map[string]string{"role": "member"}, 404)
	bob.must("PUT", "/api/w/members/"+ids[carol], map[string]string{"role": "boss"}, 422)
	list := bob.must("PUT", "/api/w/members/"+ids[carol], map[string]string{"role": "manager"}, 200)["members"].([]any)
	if role(carol) != "manager" || len(list) != 3 {
		t.Fatalf("carol is %q, list %d", role(carol), len(list))
	}
	alice.must("PUT", "/api/w/members/"+ids[alice], map[string]string{"role": "admin"}, 409) // the only owner
	alice.must("DELETE", "/api/w/members/"+ids[alice], nil, 409)                              // can't leave either
	bob.must("DELETE", "/api/w/members/"+ids[alice], nil, 403)

	// Carol leaves on her own; her token for the workspace goes with her.
	tok := carol.must("POST", "/api/w/mcp_tokens", map[string]any{"name": "carol", "scopes": []string{"members:read"}}, 201)
	_ = tok
	carol.must("DELETE", "/api/w/members/"+ids[carol], nil, 200)
	if n := len(carol.must("GET", "/api/me/workspaces", nil, 200)["workspaces"].([]any)); n != 0 {
		t.Fatalf("carol still sees %d workspaces", n)
	}
	carol.must("GET", "/api/w/current", nil, 404)
	var live int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM mcp_tokens WHERE workspace_id = $1 AND user_id = $2 AND revoked_at IS NULL`, wsID, ids[carol]).Scan(&live)
	if live != 0 {
		t.Fatalf("carol kept %d live tokens after leaving", live)
	}

	// Two owners: now either can step down, and an owner can remove an owner.
	alice.must("PUT", "/api/w/members/"+ids[bob], map[string]string{"role": "owner"}, 200)
	bob.must("PUT", "/api/w/members/"+ids[alice], map[string]string{"role": "member"}, 200)
	alice.must("PUT", "/api/w/members/"+ids[alice], map[string]string{"role": "owner"}, 403) // no climbing back
	bob.must("DELETE", "/api/w/members/"+ids[alice], nil, 200)
	if role(alice) != "" {
		t.Fatalf("alice is still %q", role(alice))
	}

	// ---- delete --------------------------------------------------------------
	join2 := func(c *client, role string) {
		inv := bob.must("POST", "/api/w/invitations", map[string]string{"email": emails[c], "role": role}, 201)
		c.must("POST", "/api/invitations/accept", map[string]string{"token": strings.TrimPrefix(inv["accept_url"].(string), "/invite/")}, 200)
		c.ws = wsID
	}
	join2(alice, "admin")
	bob.must("PUT", "/api/w/workspace/avatar", map[string]string{"data": pic, "content_type": "image/png"}, 200)
	bob.must("POST", "/api/w/contacts", map[string]any{"name": "Someone", "email": "someone-" + rnd() + "@mail.example"}, 201)

	alice.must("DELETE", "/api/w/workspace?confirm="+slug, nil, 403) // admins can't
	bob.must("DELETE", "/api/w/workspace", nil, 422)
	bob.must("DELETE", "/api/w/workspace?confirm=not-"+slug, nil, 422)
	if code, _ := bob.do("DELETE", "/api/w/workspace?confirm="+slug, nil); code != http.StatusNoContent {
		t.Fatalf("delete: %d", code)
	}
	var left int
	_ = a.SysDB.QueryRow(`SELECT count(*) FROM workspaces WHERE id = $1`, wsID).Scan(&left)
	if left != 0 {
		t.Fatal("the workspace is still there")
	}
	// Not a row anywhere still names it — the cascaded tables and the ones
	// with no foreign key alike.
	rows, err := a.SysDB.Query(`
		SELECT c.table_name FROM information_schema.columns c
		JOIN information_schema.tables t USING (table_schema, table_name)
		WHERE c.table_schema = 'public' AND c.column_name = 'workspace_id' AND t.table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var tn string
		_ = rows.Scan(&tn)
		tables = append(tables, tn)
	}
	rows.Close()
	for _, tn := range tables {
		var n int
		q := `SELECT count(*) FROM "` + tn + `" WHERE workspace_id::text = $1`
		if tn == "audit_log" {
			// Except the record of the deletion itself, which the audit
			// middleware writes after the handler: who deleted it, and when.
			q += ` AND operation <> 'w-delete-workspace'`
		}
		if err := a.SysDB.QueryRow(q, wsID).Scan(&n); err != nil {
			t.Fatal(tn, err)
		}
		if n != 0 {
			t.Errorf("%s still holds %d rows of the deleted workspace", tn, n)
		}
	}
	var trail string
	_ = a.SysDB.QueryRow(`SELECT actor_id FROM audit_log WHERE workspace_id = $1 AND operation = 'w-delete-workspace' ORDER BY occurred_at DESC LIMIT 1`, wsID).Scan(&trail)
	if trail != ids[bob] {
		t.Errorf("the deletion is not on record as bob's: %q", trail)
	}
	if n := len(bob.must("GET", "/api/me/workspaces", nil, 200)["workspaces"].([]any)); n != 0 {
		t.Fatalf("bob still sees %d workspaces", n)
	}
	bob.must("GET", "/api/w/current", nil, 404)
}
