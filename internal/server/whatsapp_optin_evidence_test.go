package server

import (
	"encoding/json"
	"strings"
	"testing"
)

/*
TestWhatsAppOptInEvidence covers recording a WhatsApp opt-in with its evidence
over MCP (record_whatsapp_opt_in) and REST (the opt-in route's body). What it
pins down:

  - the evidence must hold up: a known source, and a link or a note (a public
    request needs the link), and the tool needs contacts:write;
  - once recorded, send_whatsapp passes the consent gate, and the contact page
    shows how they agreed, the proof and the member who recorded it;
  - the recording is in the audit log, against the token and its member;
  - a STOP wins: a contact who said STOP is refused and left untouched.
*/
func TestWhatsAppOptInEvidence(t *testing.T) {
	sf := bootSurface(t)
	alice, evo := sf.alice, sf.evo
	agent, _ := mint(t, alice, "outreach agent", allUserScopes())
	alice.must("POST", "/api/w/whatsapp/link", map[string]any{}, 200)

	newContact := func(name, phone string) string {
		t.Helper()
		ct := alice.must("POST", "/api/w/contacts", map[string]any{"email": strings.ToLower(name) + "-" + rnd() + "@mail.example", "name": name}, 201)["contact"].(map[string]any)
		id := ct["id"].(string)
		alice.must("PATCH", "/api/w/contacts/"+id, map[string]any{"phone": phone}, 200)
		return id
	}
	post := "https://www.facebook.com/groups/cairo.devs/posts/1234567890"

	// ---- the gate is closed until an opt-in is recorded ---------------------------
	ctID := newContact("Hany", "+20 100 555 0303")
	if txt := mcpRefused(t, agent, "send_whatsapp", map[string]any{"contact_id": ctID, "text": "Hi Hany"}); !strings.Contains(txt, "not opted in") {
		t.Fatalf("send_whatsapp without an opt-in: %s", txt)
	}

	// ---- evidence that does not hold up is refused --------------------------------
	for name, args := range map[string]map[string]any{
		"a public request with no link": {"contact_id": ctID, "source": "public_request", "note": "saw it somewhere"},
		"no evidence at all":            {"contact_id": ctID, "source": "verbal"},
		"a link that is not http(s)":    {"contact_id": ctID, "source": "public_request", "evidence_url": "javascript:alert(1)"},
		"an unknown source":             {"contact_id": ctID, "source": "scraped", "evidence_url": post},
	} {
		if txt := mcpRefused(t, agent, "record_whatsapp_opt_in", args); txt == "" {
			t.Errorf("%s was accepted", name)
		}
	}
	reader, _ := mint(t, alice, "reader", []string{"contacts:read"})
	if txt := mcpRefused(t, reader, "record_whatsapp_opt_in", map[string]any{"contact_id": ctID, "source": "public_request", "evidence_url": post}); !strings.Contains(txt, "contacts:write") {
		t.Errorf("a contacts:read token recorded an opt-in: %s", txt)
	}
	if st := alice.must("GET", "/api/w/contacts/"+ctID+"/whatsapp", nil, 200); st["reason"] != "not_opted_in" {
		t.Fatalf("a refused recording changed the consent: %v", st)
	}

	// ---- recorded: the gate opens, with the evidence on the contact ---------------
	rec := mcpJSON(t, agent, "record_whatsapp_opt_in", map[string]any{
		"contact_id": ctID, "source": "public_request", "evidence_url": post,
		"note": "Asked for a Laravel developer: \"contact me on WhatsApp 01005550303\"",
	})
	opt := obj(rec["whatsapp_opt_in"])
	if rec["ok"] != true || rec["contact_id"] != ctID || opt["source"] != "public_request" || opt["evidence_url"] != post ||
		!strings.HasPrefix(opt["by"].(string), "surface-") || opt["at"] == nil {
		t.Fatalf("record_whatsapp_opt_in: %v", rec)
	}
	st := alice.must("GET", "/api/w/contacts/"+ctID+"/whatsapp", nil, 200)
	if st["can_send"] != true || st["whatsapp_opt_in_at"] == nil {
		t.Fatalf("the contact page after the opt-in: %v", st)
	}
	if o := obj(st["whatsapp_opt_in"]); o["source"] != "public_request" || o["evidence_url"] != post || o["by"] != opt["by"] {
		t.Fatalf("the contact page's opt-in evidence: %v", st["whatsapp_opt_in"])
	}
	first := mcpJSON(t, agent, "send_whatsapp", map[string]any{"contact_id": ctID, "text": "Hi Hany, saw your post about the Laravel role."})
	code6, _ := first["code"].(string)
	if first["confirmation_required"] != true || len(code6) != 6 {
		t.Fatalf("send_whatsapp after the opt-in did not pass the gate: %v", first)
	}
	sent := mcpJSON(t, agent, "send_whatsapp", map[string]any{"contact_id": ctID, "text": "Hi Hany, saw your post about the Laravel role.", "code": code6})
	if sent["sent"] != true || len(evo.sends()) != 1 {
		t.Fatalf("send_whatsapp with the code: %v (sends %v)", sent, evo.sends())
	}

	// ---- the audit log has it, against the token and its member ------------------
	var actorKind, via, userID, changes string
	if err := sf.sys.QueryRow(`
		SELECT actor_kind, via, user_id, changes::text FROM audit_log
		WHERE workspace_id = $1 AND entity_type = 'contacts' AND entity_id = $2 AND action = 'update'
		  AND operation = 'mcp:record_whatsapp_opt_in'
		ORDER BY occurred_at DESC LIMIT 1`, sf.ws, ctID).Scan(&actorKind, &via, &userID, &changes); err != nil {
		t.Fatalf("no audit entry for the recording: %v", err)
	}
	var diff map[string]map[string]any
	if err := json.Unmarshal([]byte(changes), &diff); err != nil {
		t.Fatal(err)
	}
	if actorKind != "token" || via != "mcp" || userID == "" ||
		diff["whatsapp_opt_in_source"]["after"] != "public_request" || diff["whatsapp_opt_in_evidence_url"]["after"] != post ||
		diff["whatsapp_opt_in_by"]["after"] != userID || diff["whatsapp_opt_in_at"] == nil {
		t.Fatalf("the audit entry: kind=%s via=%s user=%s changes=%s", actorKind, via, userID, changes)
	}

	// ---- a STOP wins --------------------------------------------------------------
	stopped := newContact("Mona", "+20 100 555 0404")
	if _, err := sf.sys.Exec(`UPDATE contacts SET whatsapp_opt_out_at = now() WHERE id = $1`, stopped); err != nil {
		t.Fatal(err)
	}
	if txt := mcpRefused(t, agent, "record_whatsapp_opt_in", map[string]any{"contact_id": stopped, "source": "public_request", "evidence_url": post}); !strings.Contains(txt, "asked to stop") {
		t.Fatalf("record_whatsapp_opt_in for a contact who said STOP: %s", txt)
	}
	var optedIn bool
	var source string
	if err := sf.sys.QueryRow(`SELECT whatsapp_opt_in_at IS NOT NULL, whatsapp_opt_in_source FROM contacts WHERE id = $1`, stopped).Scan(&optedIn, &source); err != nil {
		t.Fatal(err)
	}
	if optedIn || source != "" {
		t.Fatalf("a refused recording wrote to a contact who said STOP: opted_in=%v source=%q", optedIn, source)
	}
	if txt := mcpRefused(t, agent, "send_whatsapp", map[string]any{"contact_id": stopped, "text": "Hi Mona"}); !strings.Contains(txt, "asked to stop") {
		t.Errorf("send_whatsapp to a contact who said STOP: %s", txt)
	}
	// And a STOP after the recording closes the gate again.
	if _, err := sf.sys.Exec(`UPDATE contacts SET whatsapp_opt_out_at = now() WHERE id = $1`, ctID); err != nil {
		t.Fatal(err)
	}
	if txt := mcpRefused(t, agent, "send_whatsapp", map[string]any{"contact_id": ctID, "text": "One more thing"}); !strings.Contains(txt, "asked to stop") {
		t.Errorf("send_whatsapp after a STOP: %s", txt)
	}
	if txt := mcpRefused(t, agent, "record_whatsapp_opt_in", map[string]any{"contact_id": ctID, "source": "verbal", "note": "said yes again"}); !strings.Contains(txt, "asked to stop") {
		t.Errorf("re-recording after a STOP: %s", txt)
	}
	if len(evo.sends()) != 1 {
		t.Errorf("a message reached a contact who said STOP: %v", evo.sends())
	}

	// ---- REST takes the same evidence -----------------------------------------
	met := newContact("Sara", "+20 100 555 0505")
	if code, b := alice.do("POST", "/api/w/contacts/"+met+"/whatsapp/opt-in", map[string]any{"source": "public_request"}); code != 422 || !strings.Contains(string(b), "evidence_url") {
		t.Fatalf("REST public_request with no link: %d %s", code, b)
	}
	alice.must("POST", "/api/w/contacts/"+met+"/whatsapp/opt-in", map[string]any{"source": "verbal", "note": "Agreed on the call on Oct 12"}, 200)
	if o := obj(alice.must("GET", "/api/w/contacts/"+met+"/whatsapp", nil, 200)["whatsapp_opt_in"]); o["source"] != "verbal" || o["note"] != "Agreed on the call on Oct 12" || o["by"] != opt["by"] {
		t.Fatalf("REST opt-in with evidence: %v", o)
	}
}
