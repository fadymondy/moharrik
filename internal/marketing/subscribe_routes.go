package marketing

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// tags is shared by every endpoint in this package; it was function-local
// until the subscribe forms needed it too.
var tags = []string{"Email marketing"}

/*
Subscribe forms: the public entry point, and the workspace side that creates
them (MH-318).

The public endpoint is the interesting half. It has no session, so its
authorization is the unguessable key in the path plus the browser Origin —
the same pair the chat widget uses (internal/inbox), and for the same reason:
a widget embedded on a customer's site cannot carry a secret, because anyone
who views the page can read it.

What this endpoint does that the chat one does not have to think about: it
sends EMAIL to an address the caller chose. That makes two things necessary.

	IT ANSWERS THE SAME WAY EVERY TIME. Whether the address was new, already
	pending, or already subscribed, the visitor is told the same thing. A form
	that says "you are already subscribed" is an oracle: post an address, read
	the answer, and you have learned who is on somebody's list.

	IT WILL NOT MAIL THE SAME ADDRESS REPEATEDLY. Subscribe() holds back the
	confirmation for an address that is already waiting on one. Without that,
	replaying one submission is a way to mail-bomb a stranger from a domain
	they trust.
*/

// SubscribeResponse is what a visitor's browser gets back.
//
// A named type on purpose: an anonymous struct puts its entire Go definition
// into the $schema URL, and this is the one response in this API that a
// stranger's website reads.
type SubscribeResponse struct {
	OK     bool   `json:"ok"`
	Thanks string `json:"thanks" doc:"What to show the visitor, in their language."`
}

// Form kinds (MH-390). A subscribe form puts an address on a list; an inquiry
// form files a lead (internal/leads). They share this table, the key namespace,
// the origin allow-list and this page, because the risk they carry is the same
// one: a public key that lets a stranger's browser write into a workspace.
const (
	FormSubscribe = "subscribe"
	FormInquiry   = "inquiry"
)

// FormOut is a public form as the workspace sees it.
type FormOut struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind" enum:"subscribe,inquiry"`
	ListID         string    `json:"list_id" doc:"Empty for an inquiry form, which files a lead instead."`
	ListName       string    `json:"list_name"`
	Name           string    `json:"name"`
	PublicKey      string    `json:"public_key"`
	AllowedOrigins []string  `json:"allowed_origins"`
	Enabled        bool      `json:"enabled"`
	ThanksEN       string    `json:"thanks_en"`
	ThanksAR       string    `json:"thanks_ar"`
	CreatedAt      time.Time `json:"created_at"`
	// Where a customer's page posts, and a minimal form that does it. Built
	// here rather than in the page because only the server knows its public
	// URL — the same reason the chat widget's snippet is (internal/inbox).
	Endpoint string `json:"endpoint"`
	Embed    string `json:"embed" doc:"A minimal HTML form to paste on the customer's site."`
}

func subscribeEndpoint(key string) string { return publicBase() + "/api/subscribe/" + key }

// subscribeSnippet is a dependency-free form: email, optional name and phone,
// and the WhatsApp consent as its OWN unticked box (MH-320) — sent only with a
// phone, because consent to a number nobody gave means nothing.
func subscribeSnippet(key string) string {
	id := "moharrik-subscribe-" + key
	return `<form id="` + id + `">
  <input type="email" name="email" required placeholder="Email">
  <input type="text" name="name" placeholder="Name (optional)">
  <input type="tel" name="phone" placeholder="Phone (optional)">
  <label><input type="checkbox" name="whatsapp_opt_in"> Also message me on WhatsApp</label>
  <button type="submit">Subscribe</button>
  <p role="status" data-message></p>
</form>
<script>
(function () {
  var f = document.getElementById("` + id + `");
  f.addEventListener("submit", function (e) {
    e.preventDefault();
    var d = new FormData(f), msg = f.querySelector("[data-message]"), phone = (d.get("phone") || "").trim();
    fetch("` + subscribeEndpoint(key) + `", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        email: d.get("email"),
        name: d.get("name") || "",
        phone: phone,
        whatsapp_opt_in: !!phone && d.get("whatsapp_opt_in") === "on",
        locale: (document.documentElement.lang || "en").slice(0, 2)
      })
    }).then(function (r) {
      return r.json().then(function (j) {
        msg.textContent = r.ok ? j.thanks : (j.error || j.detail || "Something went wrong.");
        if (r.ok) f.reset();
      });
    }).catch(function () { msg.textContent = "Could not reach the server."; });
  });
})();
</script>`
}

func withEmbed(f FormOut) FormOut {
	if f.Kind == FormInquiry {
		f.Endpoint = InquiryEndpoint(f.PublicKey)
		f.Embed = inquirySnippet(f.PublicKey)
		return f
	}
	f.Endpoint = subscribeEndpoint(f.PublicKey)
	f.Embed = subscribeSnippet(f.PublicKey)
	return f
}

// InquiryEndpoint is where an inquiry form posts (internal/leads registers it).
func InquiryEndpoint(key string) string { return publicBase() + "/api/inquiry/" + key }

// inquirySnippet is a dependency-free contact form that files a lead (MH-390).
//
// The attribution is captured HERE, in the visitor's browser, because it is the
// only place it exists: the server sees the page that posted, not the ad that
// brought them. On the first page view — or any later one that arrives with
// campaign parameters, which is a new touch worth keeping — it stores the UTM
// parameters, gclid, document.referrer and the landing URL in localStorage, so
// a visitor who lands on the home page from an ad and fills the form on
// /contact is still attributed to the ad. The honeypot is a field people never
// see; a bot that fills it is thanked and filed nowhere.
func inquirySnippet(key string) string {
	id := "moharrik-inquiry-" + key
	return `<form id="` + id + `">
  <input type="text" name="name" autocomplete="name" placeholder="Name">
  <input type="email" name="email" required autocomplete="email" placeholder="Email">
  <input type="tel" name="phone" autocomplete="tel" placeholder="Phone (optional)">
  <input type="text" name="company" autocomplete="organization" placeholder="Company (optional)">
  <input type="text" name="budget" inputmode="decimal" maxlength="80" placeholder="Budget (optional)">
  <textarea name="message" required rows="4" placeholder="How can we help?"></textarea>
  <div aria-hidden="true" style="position:absolute;left:-10000px;width:1px;height:1px;overflow:hidden">
    <label>Leave this empty <input type="text" name="nickname" tabindex="-1" autocomplete="off"></label>
  </div>
  <button type="submit">Send</button>
  <p role="status" data-message></p>
</form>
<script>
(function () {
  var K = "moharrik-attribution", F = ["utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "gclid"];
  var q = new URLSearchParams(location.search), a = null;
  try { a = JSON.parse(localStorage.getItem(K) || "null"); } catch (e) {}
  if (!a || F.some(function (k) { return q.get(k); })) {
    a = { referrer: document.referrer || "", landing_page: location.href };
    F.forEach(function (k) { a[k] = q.get(k) || ""; });
    try { localStorage.setItem(K, JSON.stringify(a)); } catch (e) {}
  }
  var f = document.getElementById("` + id + `");
  f.addEventListener("submit", function (e) {
    e.preventDefault();
    var d = new FormData(f), msg = f.querySelector("[data-message]"), btn = f.querySelector("button");
    var body = { locale: (document.documentElement.lang || "en").slice(0, 2) };
    ["name", "email", "phone", "company", "budget", "message", "nickname"].forEach(function (k) { body[k] = String(d.get(k) || "").trim(); });
    F.concat(["referrer", "landing_page"]).forEach(function (k) { body[k] = String(a[k] || "").slice(0, 2000); });
    btn.disabled = true;
    fetch("` + InquiryEndpoint(key) + `", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    }).then(function (r) {
      return r.json().then(function (j) {
        msg.textContent = r.ok ? j.thanks : (j.error || j.detail || "Something went wrong.");
        if (r.ok) f.reset();
      });
    }).catch(function () { msg.textContent = "Could not reach the server."; })
      .then(function () { btn.disabled = false; });
  });
})();
</script>`
}

// PublicForm is what a public endpoint knows once a key has resolved.
type PublicForm struct {
	ID          string
	WorkspaceID string
	Name        string
	ListID      string
	ThanksEN    string
	ThanksAR    string
}

// ResolvePublicForm is the one gate in front of BOTH public form endpoints:
// the key must exist AND be of the kind that endpoint serves, the form must be
// on, and the browser Origin must be on its allow-list (empty allows nothing).
//
// It runs on the OWNER pool — which workspace a key belongs to is a
// cross-tenant question, and there is no session to answer it — and returns
// only what the caller needs to continue inside that workspace. A subscribe key
// posted to the inquiry endpoint (or the other way round) "does not exist": the
// kinds must not be interchangeable, or a newsletter form becomes a way to file
// leads and a contact form a way to subscribe strangers.
func ResolvePublicForm(ctx context.Context, sysDB *sql.DB, key, kind, origin string) (PublicForm, error) {
	noun := "subscribe form"
	if kind == FormInquiry {
		noun = "form"
	}
	var f PublicForm
	var list sql.NullString
	var origins string
	var enabled bool
	err := sysDB.QueryRowContext(ctx, `
		SELECT id, workspace_id, name, list_id, allowed_origins, enabled, thanks_en, thanks_ar
		FROM subscribe_forms WHERE public_key = $1 AND kind = $2`, key, kind).
		Scan(&f.ID, &f.WorkspaceID, &f.Name, &list, &origins, &enabled, &f.ThanksEN, &f.ThanksAR)
	if errors.Is(err, sql.ErrNoRows) {
		return f, huma.Error404NotFound("this " + noun + " does not exist")
	}
	if err != nil {
		return f, huma.Error500InternalServerError("could not resolve the form")
	}
	if !enabled {
		return f, huma.Error403Forbidden("this " + noun + " is turned off")
	}
	if !OriginAllowed(origins, origin) {
		return f, huma.Error403Forbidden("this origin is not allowed for this " + noun)
	}
	f.ListID = list.String
	return f, nil
}

// OriginAllowed checks a browser Origin against a comma-separated allow-list.
// An empty list allows NOTHING: a form with no origins set is closed, which is
// the safe direction for the mistake.
func OriginAllowed(list, origin string) bool {
	origin = strings.TrimRight(strings.TrimSpace(strings.ToLower(origin)), "/")
	if origin == "" {
		return false
	}
	for _, raw := range strings.Split(list, ",") {
		if a := strings.TrimRight(strings.TrimSpace(strings.ToLower(raw)), "/"); a != "" && a == origin {
			return true
		}
	}
	return false
}

func splitOrigins(s string) []string {
	out := []string{}
	for _, raw := range strings.Split(s, ",") {
		if v := strings.TrimRight(strings.TrimSpace(raw), "/"); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// RegisterSubscribeForms mounts both halves.
func RegisterSubscribeForms(api huma.API, a *app.App) {
	registerFormAdmin(api, a)
	registerPublicSubscribe(api, a)
}

func registerFormAdmin(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-subscribe-forms", Method: http.MethodGet, Path: "/api/w/subscribe-forms",
		Summary: "Subscribe forms, and where each may be embedded", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[struct {
		Forms []FormOut `json:"forms"`
	}], error) {
		s, err := scope(ctx, authz.ContactsRead)
		if err != nil {
			return nil, err
		}
		rows, err := a.SQLDB.QueryContext(ctx, `
			SELECT f.id, f.kind, coalesce(f.list_id, ''), coalesce(l.name, ''), f.name, f.public_key, f.allowed_origins,
			       f.enabled, f.thanks_en, f.thanks_ar, f.created_at
			FROM subscribe_forms f
			LEFT JOIN mail_lists l ON l.workspace_id = f.workspace_id AND l.id = f.list_id
			WHERE f.workspace_id = $1 ORDER BY f.created_at DESC`, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the forms")
		}
		defer rows.Close()

		forms := []FormOut{}
		for rows.Next() {
			var f FormOut
			var origins string
			if err := rows.Scan(&f.ID, &f.Kind, &f.ListID, &f.ListName, &f.Name, &f.PublicKey, &origins,
				&f.Enabled, &f.ThanksEN, &f.ThanksAR, &f.CreatedAt); err != nil {
				return nil, huma.Error500InternalServerError("could not read the forms")
			}
			f.AllowedOrigins = splitOrigins(origins)
			forms = append(forms, withEmbed(f))
		}
		return out(struct {
			Forms []FormOut `json:"forms"`
		}{forms}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-subscribe-form-create", Method: http.MethodPost, Path: "/api/w/subscribe-forms",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create a public form: a subscribe form for a list, or an inquiry form that files leads", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			Kind           string   `json:"kind,omitempty" enum:"subscribe,inquiry" doc:"subscribe (default) puts an address on list_id; inquiry files a lead and takes no list."`
			ListID         string   `json:"list_id,omitempty" doc:"Required for a subscribe form; must be empty for an inquiry form."`
			Name           string   `json:"name" minLength:"1" maxLength:"80"`
			AllowedOrigins []string `json:"allowed_origins,omitempty" maxItems:"50" doc:"Browser origins, e.g. https://example.com. Empty allows nothing."`
			ThanksEN       string   `json:"thanks_en,omitempty" maxLength:"300"`
			ThanksAR       string   `json:"thanks_ar,omitempty" maxLength:"300"`
		}
	}) (*body[struct {
		Form FormOut `json:"form"`
	}], error) {
		// Creating a form decides where a list can be filled from, so it is a
		// manager's call rather than every member's — the same line the chat
		// widget and the webhook sources draw.
		s, err := scope(ctx, authz.CampaignsWrite)
		if err != nil {
			return nil, err
		}

		kind := in.Body.Kind
		if kind == "" {
			kind = FormSubscribe
		}
		var listName string
		var listID any // NULL for an inquiry form (the CHECK in 32_leads.sql)
		switch kind {
		case FormInquiry:
			if strings.TrimSpace(in.Body.ListID) != "" {
				return nil, huma.Error422UnprocessableEntity("an inquiry form files a lead, not a subscription — leave the list empty")
			}
		default:
			if err := a.SQLDB.QueryRowContext(ctx,
				`SELECT name FROM mail_lists WHERE workspace_id = $1 AND id = $2`,
				s.WorkspaceID, in.Body.ListID).Scan(&listName); errors.Is(err, sql.ErrNoRows) {
				return nil, huma.Error404NotFound("list not found")
			} else if err != nil {
				return nil, huma.Error500InternalServerError("could not read the list")
			}
			listID = in.Body.ListID
		}

		// The prefix tells a reader of a snippet which endpoint the key is for;
		// the kind column is what is actually enforced.
		key := "sf_" + newToken()
		if kind == FormInquiry {
			key = "if_" + newToken()
		}
		origins := strings.Join(splitOrigins(strings.Join(in.Body.AllowedOrigins, ",")), ",")

		var id string
		var createdAt time.Time
		if err := a.SQLDB.QueryRowContext(ctx, `
			INSERT INTO subscribe_forms (workspace_id, kind, list_id, name, public_key, allowed_origins, thanks_en, thanks_ar, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id, created_at`,
			s.WorkspaceID, kind, listID, strings.TrimSpace(in.Body.Name), key, origins,
			strings.TrimSpace(in.Body.ThanksEN), strings.TrimSpace(in.Body.ThanksAR), s.UserID,
		).Scan(&id, &createdAt); err != nil {
			return nil, huma.Error500InternalServerError("could not create the form")
		}

		return out(struct {
			Form FormOut `json:"form"`
		}{withEmbed(FormOut{
			ID: id, Kind: kind, ListID: strings.TrimSpace(in.Body.ListID), ListName: listName, Name: strings.TrimSpace(in.Body.Name),
			PublicKey: key, AllowedOrigins: splitOrigins(origins), Enabled: true,
			ThanksEN: in.Body.ThanksEN, ThanksAR: in.Body.ThanksAR, CreatedAt: createdAt,
		})}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-subscribe-form-update", Method: http.MethodPatch, Path: "/api/w/subscribe-forms/{id}",
		Summary: "Rename a form, change where it may be embedded, or turn it off", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Name           *string  `json:"name,omitempty" maxLength:"80"`
			ListID         *string  `json:"list_id,omitempty" doc:"Point the form at another list of this workspace."`
			AllowedOrigins []string `json:"allowed_origins,omitempty" maxItems:"50"`
			Enabled        *bool    `json:"enabled,omitempty"`
			ThanksEN       *string  `json:"thanks_en,omitempty" maxLength:"300"`
			ThanksAR       *string  `json:"thanks_ar,omitempty" maxLength:"300"`
		}
	}) (*body[struct {
		OK bool `json:"ok"`
	}], error) {
		s, err := scope(ctx, authz.CampaignsWrite)
		if err != nil {
			return nil, err
		}

		if in.Body.ListID != nil {
			// An inquiry form has no list; giving it one would break the CHECK
			// in 32_leads.sql as a 500. Say so as what it is.
			var kind string
			if err := a.SQLDB.QueryRowContext(ctx,
				`SELECT kind FROM subscribe_forms WHERE workspace_id = $1 AND id = $2`,
				s.WorkspaceID, in.ID).Scan(&kind); errors.Is(err, sql.ErrNoRows) {
				return nil, huma.Error404NotFound("form not found")
			} else if err != nil {
				return nil, huma.Error500InternalServerError("could not read the form")
			} else if kind == FormInquiry {
				return nil, huma.Error422UnprocessableEntity("an inquiry form files a lead, not a subscription — it has no list")
			}
			var ok bool
			if err := a.SQLDB.QueryRowContext(ctx,
				`SELECT EXISTS (SELECT 1 FROM mail_lists WHERE workspace_id = $1 AND id = $2)`,
				s.WorkspaceID, *in.Body.ListID).Scan(&ok); err != nil {
				return nil, huma.Error500InternalServerError("could not read the list")
			} else if !ok {
				return nil, huma.Error404NotFound("list not found")
			}
		}

		// COALESCE on a nullable parameter: a field that was not sent keeps its
		// value, so turning a form off cannot silently wipe its origins.
		var origins *string
		if in.Body.AllowedOrigins != nil {
			joined := strings.Join(splitOrigins(strings.Join(in.Body.AllowedOrigins, ",")), ",")
			origins = &joined
		}

		res, err := a.SQLDB.ExecContext(ctx, `
			UPDATE subscribe_forms SET
			  name = COALESCE($3, name),
			  allowed_origins = COALESCE($4, allowed_origins),
			  enabled = COALESCE($5, enabled),
			  list_id = COALESCE($8, list_id),
			  thanks_en = COALESCE($6, thanks_en),
			  thanks_ar = COALESCE($7, thanks_ar),
			  updated_at = now()
			WHERE workspace_id = $1 AND id = $2`,
			s.WorkspaceID, in.ID, in.Body.Name, origins, in.Body.Enabled, in.Body.ThanksEN, in.Body.ThanksAR, in.Body.ListID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not update the form")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("form not found")
		}
		return out(struct {
			OK bool `json:"ok"`
		}{true}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-subscribe-form-delete", Method: http.MethodDelete, Path: "/api/w/subscribe-forms/{id}",
		Summary: "Delete a subscribe form", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[struct {
		OK bool `json:"ok"`
	}], error) {
		s, err := scope(ctx, authz.CampaignsWrite)
		if err != nil {
			return nil, err
		}
		res, err := a.SQLDB.ExecContext(ctx,
			`DELETE FROM subscribe_forms WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, in.ID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not delete the form")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("form not found")
		}
		return out(struct {
			OK bool `json:"ok"`
		}{true}), nil
	})
}

func registerPublicSubscribe(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID:   "public-subscribe",
		Method:        http.MethodPost,
		Path:          "/api/subscribe/{key}",
		DefaultStatus: http.StatusOK,
		Summary:       "Subscribe to a list (public: the form key plus the Origin allow-list are the authorization)",
		Tags:          tags,
	}, func(ctx context.Context, in *struct {
		Key    string `path:"key"`
		Origin string `header:"Origin"`
		Body   struct {
			Email  string `json:"email" minLength:"3" maxLength:"320"`
			Name   string `json:"name,omitempty" maxLength:"120"`
			Locale string `json:"locale,omitempty" maxLength:"5"`
			Phone  string `json:"phone,omitempty" maxLength:"32"`
			// A SEPARATE tick, never implied by subscribing (MH-320). Agreeing
			// to a newsletter is not agreeing to a message on your phone, and a
			// form that treated it as one would be making a promise on the
			// visitor's behalf.
			WhatsAppOptIn bool `json:"whatsapp_opt_in,omitempty" doc:"The visitor ticked a box agreeing to WhatsApp messages. Requires a phone."`
		}
	}) (*body[SubscribeResponse], error) {
		email := strings.ToLower(strings.TrimSpace(in.Body.Email))
		if !strings.Contains(email, "@") || strings.HasPrefix(email, "@") || strings.HasSuffix(email, "@") {
			return nil, huma.Error422UnprocessableEntity("that does not look like an email address")
		}

		// Resolved on the OWNER pool: which workspace a key belongs to is a
		// cross-tenant question, and there is no session to answer it. The
		// same gate the inquiry endpoint uses (ResolvePublicForm).
		form, err := ResolvePublicForm(ctx, a.SysDB, in.Key, FormSubscribe, in.Origin)
		if err != nil {
			return nil, err
		}
		wsID, listID, thanksEN, thanksAR := form.WorkspaceID, form.ListID, form.ThanksEN, form.ThanksAR

		locale := strings.ToLower(strings.TrimSpace(in.Body.Locale))
		if locale != "ar" {
			locale = "en"
		}

		// Everything from here happens inside the form's workspace.
		sctx := tenant.WithWorkspace(ctx, wsID)

		res, err := Subscribe(sctx, a.SQLDB, wsID, listID, email, in.Body.Name, locale, "form")
		if errors.Is(err, ErrNoSuchList) {
			// The form outlived its list. Not the visitor's problem to decode.
			return nil, huma.Error500InternalServerError("this form is not connected to a list")
		}
		if err != nil {
			a.Log.Error("public subscribe failed", "workspace", wsID, "err", err)
			return nil, huma.Error500InternalServerError("could not record that subscription")
		}

		// The phone and the WhatsApp consent, if the visitor gave them. Recorded
		// after the subscription so a failure here cannot cost the subscription
		// itself — the email consent is the one they definitely gave.
		if phone := strings.TrimSpace(in.Body.Phone); phone != "" {
			if _, err := a.SQLDB.ExecContext(sctx, `
				UPDATE contacts SET phone = $3, updated_at = now()
				WHERE workspace_id = $1 AND id = $2 AND coalesce(phone, '') = ''`,
				wsID, res.ContactID, phone); err != nil {
				a.Log.Warn("could not store a phone from a subscribe form", "workspace", wsID, "err", err)
			}
			if in.Body.WhatsAppOptIn {
				// SetWhatsAppOptIn will not override an earlier STOP, which is
				// the point: a form cannot un-say it on someone's behalf.
				if err := SetWhatsAppOptIn(sctx, a.SQLDB, wsID, res.ContactID); err != nil {
					a.Log.Warn("could not record a WhatsApp opt-in", "workspace", wsID, "err", err)
				}
			}
		}

		if res.NeedsConfirmation {
			if err := sendConfirmation(sctx, a.SQLDB, wsID, email, locale, res.ListName, res.Token); err != nil {
				// The row is written; the email is not. Logged for the workspace,
				// and NOT surfaced — a visitor cannot act on our mail server
				// being down, and the message would only confuse them.
				a.Log.Warn("confirmation email not sent", "workspace", wsID, "err", err)
			}
		}

		thanks := thanksEN
		if locale == "ar" && strings.TrimSpace(thanksAR) != "" {
			thanks = thanksAR
		}
		if strings.TrimSpace(thanks) == "" {
			thanks = defaultThanks(locale, res.Status)
		}

		// The SAME answer in every case — new, pending, or already subscribed.
		// Anything else turns this into a way to find out who is on the list.
		return out(SubscribeResponse{OK: true, Thanks: thanks}), nil
	})
}

// defaultThanks is what a visitor is told when the form has no copy of its own.
// It differs by whether the list asks for confirmation, because "check your
// email" is wrong for a list that does not send one — but NOT by whether this
// particular address was already subscribed, which is the thing not to reveal.
func defaultThanks(locale, status string) string {
	if status == StatusPending {
		if locale == "ar" {
			return "شكرًا لك. أرسلنا رسالة لتأكيد اشتراكك — افتح الرابط بداخلها."
		}
		return "Thank you. Check your email and open the link to confirm your subscription."
	}
	if locale == "ar" {
		return "شكرًا لك. تم تسجيل اشتراكك."
	}
	return "Thank you. You are subscribed."
}
