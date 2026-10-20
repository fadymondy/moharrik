package server

/*
TestWhatsAppMultiNumberMigration runs 225_whatsapp_multi_number.sql on a
database put back into the shape it had before (whatsapp_links keyed by
workspace_id, one WhatsApp thread per customer per workspace) with one
linked number, a thread and a send on it, then runs it again:

  - the one number keeps its instance, hook id and token, gets a row id as
    the primary key, and becomes the default;
  - the existing thread and send are put on that number;
  - the old one-thread-per-workspace index is gone, so the same customer can
    have a thread on a second number;
  - a second run changes nothing.

	TEST_DATABASE_URL=... TEST_APP_DATABASE_URL=... go test -count=1 ./internal/server/ -run WhatsAppMultiNumberMigration -v
*/

import (
	"database/sql"
	"os"
	"testing"

	"github.com/fadymondy/moharrik/internal/testdb"
)

func TestWhatsAppMultiNumberMigration(t *testing.T) {
	owner, _ := testdb.Setup(t, "wamigration", "../..")
	db, err := sql.Open("pgx", owner)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%v\n%s", err, q)
		}
	}

	// Back to the shape before multi-number. This database is this test's own
	// (and kept between runs), so the rows a previous run left go first.
	exec(`
		DELETE FROM whatsapp_sends;
		DELETE FROM conversations WHERE channel = 'whatsapp';
		DELETE FROM whatsapp_links;
		DROP INDEX IF EXISTS whatsapp_links_default_uniq;
		DROP INDEX IF EXISTS whatsapp_links_workspace_idx;
		ALTER TABLE whatsapp_links DROP CONSTRAINT IF EXISTS whatsapp_links_daily_cap_ck;
		ALTER TABLE whatsapp_links DROP CONSTRAINT whatsapp_links_pkey;
		ALTER TABLE whatsapp_links DROP COLUMN id, DROP COLUMN label, DROP COLUMN is_default, DROP COLUMN daily_cap;
		ALTER TABLE whatsapp_links ADD PRIMARY KEY (workspace_id);
		DROP INDEX IF EXISTS conversations_whatsapp_phone_uniq;
		DROP INDEX IF EXISTS conversations_whatsapp_link_idx;
		ALTER TABLE conversations DROP COLUMN whatsapp_link_id;
		CREATE UNIQUE INDEX conversations_phone_uniq ON conversations (workspace_id, customer_phone)
			WHERE customer_phone <> '' AND channel = 'whatsapp';
		ALTER TABLE whatsapp_sends DROP COLUMN link_id;
		ALTER TABLE campaigns DROP COLUMN whatsapp_link_id;`)

	// One workspace with one linked number, a thread and a send on it.
	var userID, ws string
	if err := db.QueryRow(`INSERT INTO users (id, email, password_hash, created_at) VALUES ($1, $2, 'x', now()::text) RETURNING id`, "u-"+rnd(), "wamig-"+rnd()+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO workspaces (slug, name, created_by) VALUES ($1, 'Mig', $2) RETURNING id`, "wamig-"+rnd(), userID).Scan(&ws); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO whatsapp_links (workspace_id, instance, hook_id, token_sealed, state, phone)
		VALUES ($1, 'moharrik-legacy', 'wh_legacy', 'sealed-token', 'open', '201207860084')`, ws)
	exec(`INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, subject)
		VALUES ($1, 'whatsapp', 'Customer', '201700001234', 'hi')`, ws)
	exec(`INSERT INTO whatsapp_sends (workspace_id, kind, phone) VALUES ($1, 'reply', '201700001234')`, ws)

	sqlFile, err := os.ReadFile("../../internal/db/post/225_whatsapp_multi_number.sql")
	if err != nil {
		t.Fatal(err)
	}
	type snapshot struct {
		ID, Instance, HookID, Token string
		IsDefault                   bool
		ThreadLink, SendLink        string
	}
	read := func() snapshot {
		t.Helper()
		var s snapshot
		if err := db.QueryRow(`SELECT id, instance, hook_id, token_sealed, is_default FROM whatsapp_links WHERE workspace_id = $1`, ws).
			Scan(&s.ID, &s.Instance, &s.HookID, &s.Token, &s.IsDefault); err != nil {
			t.Fatal(err)
		}
		_ = db.QueryRow(`SELECT whatsapp_link_id FROM conversations WHERE workspace_id = $1 AND subject = 'hi'`, ws).Scan(&s.ThreadLink)
		_ = db.QueryRow(`SELECT link_id FROM whatsapp_sends WHERE workspace_id = $1`, ws).Scan(&s.SendLink)
		return s
	}

	exec(string(sqlFile))
	got := read()
	if got.ID == "" || got.Instance != "moharrik-legacy" || got.HookID != "wh_legacy" || got.Token != "sealed-token" || !got.IsDefault {
		t.Fatalf("the existing number after the migration: %+v", got)
	}
	if got.ThreadLink != got.ID || got.SendLink != got.ID {
		t.Errorf("the existing thread and send are not on the number: %+v", got)
	}
	var pk string
	_ = db.QueryRow(`SELECT string_agg(a.attname, ',') FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		WHERE c.conrelid = 'whatsapp_links'::regclass AND c.contype = 'p'`).Scan(&pk)
	if pk != "id" {
		t.Errorf("whatsapp_links primary key is %q, want id", pk)
	}

	// A second number, and the same customer writing to it: a second thread.
	exec(`INSERT INTO whatsapp_links (workspace_id, instance, hook_id) VALUES ($1, 'moharrik-second', 'wh_second')`, ws)
	var secondID string
	_ = db.QueryRow(`SELECT id FROM whatsapp_links WHERE hook_id = 'wh_second'`).Scan(&secondID)
	exec(`INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, subject, whatsapp_link_id)
		VALUES ($1, 'whatsapp', 'Customer', '201700001234', 'hi again', $2)`, ws, secondID)
	if _, err := db.Exec(`INSERT INTO conversations (workspace_id, channel, customer_name, customer_phone, subject, whatsapp_link_id)
		VALUES ($1, 'whatsapp', 'Customer', '201700001234', 'dup', $2)`, ws, secondID); err == nil {
		t.Error("two threads for one customer on the same number were allowed")
	}

	// Run again: nothing moves, the second number does not become a default too.
	exec(string(sqlFile))
	if again := read(); again != got {
		t.Errorf("a second run changed the number: %+v → %+v", got, again)
	}
	var defaults int
	_ = db.QueryRow(`SELECT count(*) FROM whatsapp_links WHERE workspace_id = $1 AND is_default`, ws).Scan(&defaults)
	if defaults != 1 {
		t.Errorf("%d default numbers after a second run, want 1", defaults)
	}
}
