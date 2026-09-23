package marketing_test

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/realtime"
)

/*
MH-413: a campaign's audience narrowed to one language, and the realtime
events the campaigns and contacts pages now listen to instead of polling.
*/

func TestCampaignAudienceLocale(t *testing.T) {
	f := newEditFixture(t)
	marketing.RegisterWhatsAppConsent(f.api, f.a) // the audience counts live there
	sink := startAuthSink(t)
	f.addService(t, "Only", sink.port, "u", "p", "news@acme.test")

	// Three subscribers: one English, two Arabic.
	emails := map[string]string{"en-1@mail.example": "en", "ar-1@mail.example": "ar", "ar-2@mail.example": "ar"}
	for e, l := range emails {
		if res := f.api.PostCtx(f.ctx, "/api/w/contacts", map[string]any{"email": e, "locale": l, "list_id": f.list}); res.Code != http.StatusCreated {
			t.Fatalf("add %s: %d %s", e, res.Code, res.Body)
		}
	}
	for l, want := range map[string]int64{"": 3, "en": 1, "ar": 2} {
		n, err := marketing.CountEmailAudienceFor(f.ctx, f.a.SQLDB, f.ws, f.list, l)
		if err != nil || n != want {
			t.Fatalf("email audience %q: %d %v, want %d", l, n, err, want)
		}
	}
	res := f.api.GetCtx(f.ctx, "/api/w/mail/lists/"+f.list+"/email-audience?locale=ar")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"reachable":2`) {
		t.Fatalf("email-audience?locale=ar: %d %s", res.Code, res.Body)
	}

	// An Arabic-audience campaign reaches the two Arabic contacts only.
	res = f.api.PostCtx(f.ctx, "/api/w/campaigns", map[string]any{
		"list_id": f.list, "subject": "للعربية فقط", "body": "مرحبًا", "locale": "ar", "audience_locale": "ar",
	})
	if res.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", res.Code, res.Body)
	}
	var c struct {
		Campaign marketing.CampaignOut `json:"campaign"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &c)
	if c.Campaign.AudienceLocale != "ar" {
		t.Fatalf("audience_locale not kept: %+v", c.Campaign)
	}
	if res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+c.Campaign.ID+"/send", map[string]any{}); res.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", res.Code, res.Body)
	}
	f.waitDone(t, c.Campaign.ID)
	got := sink.waitFor(t, 2)
	var to []string
	for _, m := range got {
		to = append(to, m.To)
	}
	sort.Strings(to)
	if len(got) != 2 || to[0] != "ar-1@mail.example" || to[1] != "ar-2@mail.example" {
		t.Fatalf("the Arabic campaign went to %v", to)
	}
	if r := f.row(t, c.Campaign.ID); r.Recipients != 2 || r.Sent != 2 {
		t.Fatalf("recipients/sent: %+v", r)
	}

	// Editing it back to everyone keeps working, and a bad value is refused.
	c2 := f.create(t, "email", "Everyone", "Hi", "en")
	if res := f.api.PutCtx(f.ctx, "/api/w/campaigns/"+c2, map[string]any{"list_id": f.list, "subject": "Everyone", "body": "Hi", "audience_locale": "fr"}); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("audience_locale fr: %d", res.Code)
	}

	// WhatsApp counts narrow the same way.
	for e := range emails {
		if _, err := f.a.SQLDB.ExecContext(f.ctx, `UPDATE contacts SET phone = '+20 16 5555 0000', whatsapp_opt_in_at = now() WHERE workspace_id = $1 AND email = $2`, f.ws, e); err != nil {
			t.Fatal(err)
		}
	}
	res = f.api.GetCtx(f.ctx, "/api/w/mail/lists/"+f.list+"/whatsapp-audience?locale=en")
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"reachable":1`) {
		t.Fatalf("whatsapp-audience?locale=en: %d %s", res.Code, res.Body)
	}
}

func TestMarketingPublishesLiveEvents(t *testing.T) {
	f := newEditFixture(t)
	sink := startAuthSink(t)
	f.addService(t, "Only", sink.port, "u", "p", "news@acme.test")

	manager, stopM := f.a.Events.Subscribe(f.ws, realtime.Viewer{UserID: "someone-else", Role: "manager"})
	defer stopM()
	client, stopC := f.a.Events.Subscribe(f.ws, realtime.Viewer{UserID: "a-client", Role: "client"})
	defer stopC()
	kinds := func(ch <-chan []byte, wait time.Duration) map[string]int {
		out := map[string]int{}
		deadline := time.After(wait)
		for {
			select {
			case b := <-ch:
				var ev struct {
					Kind string `json:"kind"`
				}
				_ = json.Unmarshal(b, &ev)
				out[ev.Kind]++
			case <-deadline:
				return out
			}
		}
	}

	// A subscriber arrives, a campaign is drafted and sent.
	if res := f.api.PostCtx(f.ctx, "/api/w/contacts", map[string]any{"email": "live@mail.example", "list_id": f.list}); res.Code != http.StatusCreated {
		t.Fatalf("add: %d", res.Code)
	}
	id := f.create(t, "email", "Live", "Hello", "en")
	if res := f.api.PostCtx(f.ctx, "/api/w/campaigns/"+id+"/send", map[string]any{}); res.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", res.Code, res.Body)
	}
	f.waitDone(t, id)

	got := kinds(manager, 500*time.Millisecond)
	// Drafted, claimed, progress (the first and last recipient), finished.
	if got[marketing.EventContacts] < 1 || got[marketing.EventCampaigns] < 3 {
		t.Fatalf("a manager's tab should hear contacts and campaign events: %v", got)
	}
	if c := kinds(client, 100*time.Millisecond); len(c) != 0 {
		t.Fatalf("a client-role member may read neither campaigns nor contacts, but heard %v", c)
	}
}
