package rules

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/alerts"
	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/automation"
	"github.com/fadymondy/moharrik/internal/crm"
	"github.com/fadymondy/moharrik/internal/inbox"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/notifications"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
The actions. Each goes through the service a member's request uses, with the
rule acting as its member (the scope on x.ctx), so the validation, the consent
rules and the limits are the ones already there — nothing here re-implements
them. What IS here is what a rule adds on top, because nobody is watching:

  - email goes only to the workspace's members and to its contacts who have
    not asked to stop (a bounce or an unsubscribe with no list left), at most
    EmailHourlyCap an hour from all rules together;
  - WhatsApp to a contact needs their WhatsApp opt-in and no STOP, and meets
    MH-410's per-contact limits (inbox.SendWhatsAppToContact);
  - a canned reply goes at most once a day per conversation per rule, so two
    autoresponders cannot answer each other for ever;
  - adding to a list never re-adds someone who unsubscribed from it or
    bounced, and a double opt-in list still sends its confirmation.

In a test (x.dry) nothing is written or sent: each action says what it would
do, with its templates filled in, and names what would stop it where that can
be known without doing it (no SMTP service, no linked WhatsApp, a list that
no longer exists).
*/

// EmailHourlyCap bounds the email all of a workspace's rules send in an hour.
const EmailHourlyCap = 100

type execCtx struct {
	a    *app.App
	ctx  context.Context
	ws   string
	s    *tenant.Scope
	rule Rule
	def  TriggerDef
	// The event's fields, plus what earlier actions made (contact_id after
	// create_contact, deal_id after create_deal), so later actions can use it.
	data      map[string]any
	subjectID string
	eventID   string
	dry       bool
}

func (x *execCtx) param(act RuleAction, name string) string {
	return strings.TrimSpace(Render(act.Params[name], x.data))
}

// ref is a record id an action works on: the setting, or the event's own field
// of the same name when the setting is empty.
func (x *execCtx) ref(act RuleAction, name string) string {
	if v := x.param(act, name); v != "" {
		return v
	}
	return strings.TrimSpace(text(x.data[name]))
}

// msg is a structured message: a stable code plus the values it needs (MH-524).
// A UI looks the code up in its own dictionary and fills args in; args values
// are never translated (record names, titles, ids — as given). code == ""
// means no code was assigned (an arbitrary error from another package): a UI
// falls back to the plain English text stored alongside it.
type msg struct {
	Code string
	Args map[string]string
}

// m builds a msg. kv is name, value, name, value...; odd ones out are dropped.
func m(code string, kv ...string) msg {
	if len(kv) == 0 {
		return msg{Code: code}
	}
	args := make(map[string]string, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		args[kv[i]] = kv[i+1]
	}
	return msg{Code: code, Args: args}
}

// generic wraps an arbitrary error from outside internal/rules: a code that
// says nothing but "here is the detail text", left untranslated on purpose.
func generic(err error) msg { return m("generic_error", "detail", errText(err)) }

func ok(t, summary, ref string, sm msg) RuleActionResult {
	return RuleActionResult{Type: t, Status: "ok", Summary: summary, Ref: ref, SummaryCode: sm.Code, SummaryArgs: sm.Args}
}

func would(t, summary string, sm msg) RuleActionResult {
	return RuleActionResult{Type: t, Status: "would", Summary: summary, SummaryCode: sm.Code, SummaryArgs: sm.Args}
}

func fail(t, summary string, err error, sm, em msg) RuleActionResult {
	return RuleActionResult{Type: t, Status: "failed", Summary: summary, Error: clip(errText(err), 300),
		SummaryCode: sm.Code, SummaryArgs: sm.Args, ErrorCode: em.Code, ErrorArgs: em.Args}
}

func skip(t, summary, why string, sm, em msg) RuleActionResult {
	return RuleActionResult{Type: t, Status: "skipped", Summary: summary, Error: why,
		SummaryCode: sm.Code, SummaryArgs: sm.Args, ErrorCode: em.Code, ErrorArgs: em.Args}
}

// errText is what a person reads in the run log: a huma status error's own
// message rather than its "422 Unprocessable Entity" prefix.
func errText(err error) string {
	if err == nil {
		return ""
	}
	var em *huma.ErrorModel
	if errors.As(err, &em) && em.Detail != "" {
		return em.Detail
	}
	return err.Error()
}

func (x *execCtx) run(i int, act RuleAction) RuleActionResult {
	switch act.Type {
	case ActNotifyMembers:
		return x.notifyMembers(i, act)
	case ActCreateDeal:
		return x.createDeal(act)
	case ActUpdateDeal:
		return x.updateDeal(act)
	case ActCreateContact:
		return x.createContact(act)
	case ActUpdateContact:
		return x.updateContact(act)
	case ActCreateTask, ActAddNote:
		return x.activity(act)
	case ActAssignMember:
		return x.assign(act)
	case ActCannedReply:
		return x.cannedReply(act)
	case ActSendEmail:
		return x.sendEmail(act)
	case ActWhatsAppContact:
		return x.whatsAppContact(act)
	case ActAddToList:
		return x.addToList(act)
	case ActRunWorkflow:
		return x.runWorkflow(act)
	case ActWebhook:
		return x.webhook(act)
	}
	return fail(act.Type, "", fmt.Errorf("%q is not an action", act.Type), msg{}, m("unknown_action", "type", act.Type))
}

// ---- members ---------------------------------------------------------------------

type member struct{ UserID, Role, Email string }

// activeMembers reads the workspace's active members; emails come from the
// owner pool (the app role has no grant on users), predicated on membership.
func activeMembers(ctx context.Context, a *app.App, wsID string) ([]member, error) {
	rows, err := a.SysDB.QueryContext(ctx, `
		SELECT wm.user_id, wm.role, coalesce(u.email, '') FROM workspace_members wm
		LEFT JOIN users u ON u.id::text = wm.user_id
		WHERE wm.workspace_id = $1 AND wm.status = 'active' ORDER BY wm.created_at`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.UserID, &m.Role, &m.Email); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func memberLabel(m member) string {
	if m.Email != "" {
		return m.Email
	}
	return m.UserID
}

// ---- notify members ----------------------------------------------------------------

func (x *execCtx) notifyMembers(i int, act RuleAction) RuleActionResult {
	title := clip(x.param(act, "title"), 200)
	body := clip(x.param(act, "body"), 500)
	if title == "" {
		return fail(act.Type, "", errors.New("the title came out empty; check its {{fields}}"), msg{}, m("empty_field", "field", "title"))
	}
	all, err := activeMembers(x.ctx, x.a, x.ws)
	if err != nil {
		return fail(act.Type, "", err, msg{}, generic(err))
	}
	want := strings.TrimSpace(act.Params["members"])
	var to []member
	if want == "" || want == "all" {
		for _, m := range all {
			if authz.Can(m.Role, authz.NotificationsRead) {
				to = append(to, m)
			}
		}
	} else {
		pick := map[string]bool{}
		for _, id := range strings.Split(want, ",") {
			pick[strings.TrimSpace(id)] = true
		}
		for _, m := range all {
			if pick[m.UserID] {
				to = append(to, m)
			}
		}
		if len(to) == 0 {
			return fail(act.Type, "", errors.New("none of the chosen members is an active member of this workspace any more"), msg{}, m("no_active_members"))
		}
	}
	names := make([]string, 0, len(to))
	for _, mb := range to {
		names = append(names, memberLabel(mb))
	}
	onWhatsApp := act.Params["whatsapp"] == "true"
	summary := fmt.Sprintf("notify %s: “%s”", strings.Join(names, ", "), title)
	sm := m("notify_members", "names", strings.Join(names, ", "), "title", title)
	if onWhatsApp {
		summary += " — in the app and on WhatsApp"
		sm = m("notify_members_wa", "names", strings.Join(names, ", "), "title", title)
	}
	if x.dry {
		if onWhatsApp && !whatsapp.LoadConfig().Configured() {
			r := RuleActionResult{Type: act.Type, Status: "would", Summary: summary, Error: "WhatsApp is not configured on this server; only the in-app notice would go"}
			r.SummaryCode, r.SummaryArgs = sm.Code, sm.Args
			r.ErrorCode = "whatsapp_not_configured"
			return r
		}
		return would(act.Type, summary, sm)
	}

	link := x.subjectLink()
	dedupe := "rule:" + x.rule.ID + ":" + x.eventID + ":" + strconv.Itoa(i)
	if err := notifications.Emit(x.ctx, x.a.SQLDB, x.ws, notifications.Internal{
		SourceName: "Rule: " + x.rule.Name, ExternalID: dedupe, Kind: "rule", Severity: "info",
		Title: title, Body: body, URL: link,
	}); err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	// The tab and the lock screen: everyone who reads notifications sees the
	// item; the chosen members are the ones alerted.
	source := "Rule: " + x.rule.Name
	if want == "" || want == "all" {
		alerts.Emit(x.ctx, x.a, x.ws, realtime.Event{Kind: "notification", Title: title, Severity: "info", Source: source,
			Permission: authz.NotificationsRead, Alerting: true})
	} else {
		for _, m := range to {
			alerts.Emit(x.ctx, x.a, x.ws, realtime.Event{Kind: "notification", Title: title, Severity: "info", Source: source,
				Permission: authz.NotificationsRead, Alerting: true, AlertOnly: m.UserID})
		}
	}
	if onWhatsApp {
		var queued []string
		var held []string
		for _, m := range to {
			why, err := whatsapp.QueueNotice(x.ctx, x.a, x.ws, m.UserID, title, body, x.subjectPath(), dedupe)
			switch {
			case err != nil:
				held = append(held, memberLabel(m)+": "+err.Error())
			case why != "":
				held = append(held, memberLabel(m)+": "+why)
			default:
				queued = append(queued, memberLabel(m))
			}
		}
		if len(queued) > 0 {
			summary += fmt.Sprintf(" (WhatsApp queued for %s)", strings.Join(queued, ", "))
			sm = m("notify_members_wa_queued", "names", strings.Join(names, ", "), "title", title, "queued", strings.Join(queued, ", "))
		}
		if len(held) > 0 {
			r := ok(act.Type, summary, "", sm)
			r.Error = "not on WhatsApp — " + strings.Join(held, "; ")
			r.ErrorCode, r.ErrorArgs = "whatsapp_held", map[string]string{"detail": strings.Join(held, "; ")}
			if len(queued) == 0 && len(to) == len(held) && want != "all" && want != "" {
				// The whole point of naming the member was WhatsApp.
				r.Status = "failed"
			}
			return r
		}
	}
	return ok(act.Type, summary, "", sm)
}

// subjectPath is the event's record inside the app, without the origin:
// /{locale}/w/{slug}/leads/{id}. Empty when the event has no page.
func (x *execCtx) subjectPath() string {
	id := x.subjectID
	if id == "" {
		return ""
	}
	var rest string
	switch x.def.Subject {
	case "lead":
		rest = "/leads/" + id
	case "deal":
		rest = "/deals/" + id
	case "contact":
		rest = "/contacts/" + id
	case "conversation":
		rest = "/inbox?c=" + id
	case "notification":
		rest = "/notifications"
	case "topic":
		rest = "/trends"
	case "run":
		rest = "/automation/runs/" + id
	case "campaign":
		rest = "/campaigns"
	default:
		return ""
	}
	locale := "en"
	if x.s.WorkspaceSlug == "" {
		return ""
	}
	var wsLocale string
	_ = x.a.SQLDB.QueryRowContext(x.ctx, `SELECT locale FROM workspaces WHERE id = $1`, x.ws).Scan(&wsLocale)
	if wsLocale == "ar" {
		locale = "ar"
	}
	return "/" + locale + "/w/" + x.s.WorkspaceSlug + rest
}

func (x *execCtx) subjectLink() string {
	p := x.subjectPath()
	if p == "" {
		return ""
	}
	return publicBase() + p
}

func publicBase() string {
	for _, k := range []string{"PUBLIC_URL", "APP_URL"} {
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv(k)), "/"); v != "" {
			return v
		}
	}
	return ""
}

// ---- deals -------------------------------------------------------------------------

func (x *execCtx) stageID(name string) (string, string, error) {
	if name == "" {
		return "", "", nil
	}
	stages, err := crm.ListStages(x.ctx, x.a.SQLDB)
	if err != nil {
		return "", "", err
	}
	for _, s := range stages {
		if s.ID == name || strings.EqualFold(s.Name, name) {
			return s.ID, s.Name, nil
		}
	}
	return "", "", fmt.Errorf("the pipeline has no stage %q", name)
}

func minor(v string) (*int64, error) {
	if v == "" {
		return nil, nil
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", ""), 64)
	if err != nil || f < 0 {
		return nil, fmt.Errorf("the value %q is not an amount", v)
	}
	m := int64(math.Round(f * 100))
	return &m, nil
}

func strp(v string) *string { return &v }

func (x *execCtx) exists(table, id string) bool {
	if id == "" {
		return false
	}
	var n int
	_ = x.a.SQLDB.QueryRowContext(x.ctx, `SELECT count(*) FROM `+table+` WHERE workspace_id = $1 AND id = $2`, x.ws, id).Scan(&n)
	return n > 0
}

func (x *execCtx) createDeal(act RuleAction) RuleActionResult {
	title := clip(x.param(act, "title"), 160)
	if title == "" {
		return fail(act.Type, "", errors.New("the title came out empty; check its {{fields}}"), msg{}, m("empty_field", "field", "title"))
	}
	value, err := minor(x.param(act, "value"))
	if err != nil {
		return fail(act.Type, "", err, msg{}, generic(err))
	}
	stageID, stageName, err := x.stageID(x.param(act, "stage"))
	if err != nil {
		return fail(act.Type, "", err, msg{}, generic(err))
	}
	contactID := x.ref(act, "contact_id")
	summary := "open a deal “" + title + "”"
	if stageName != "" {
		summary += " in " + stageName
	}
	if value != nil {
		summary += fmt.Sprintf(", %.2f", float64(*value)/100)
	}
	dealArgs := []string{"title", title}
	if stageName != "" {
		dealArgs = append(dealArgs, "stage", stageName)
	}
	if value != nil {
		dealArgs = append(dealArgs, "value", fmt.Sprintf("%.2f", float64(*value)/100))
	}
	sm := m("create_deal", dealArgs...)
	if x.dry {
		// The steps after it can name the deal it would open.
		x.data["deal_id"] = "(the new deal)"
		return would(act.Type, summary, sm)
	}
	f := crm.DealFields{Title: &title, ValueMinor: value}
	if c := strings.ToUpper(x.param(act, "currency")); c != "" {
		f.Currency = &c
	}
	if stageID != "" {
		f.StageID = &stageID
	}
	// A contact the event names but the workspace no longer has is left off
	// rather than failing the deal.
	if contactID != "" && x.exists("contacts", contactID) {
		f.ContactID = &contactID
	}
	if o := x.param(act, "owner"); o != "" {
		f.OwnerUserID = &o
	}
	d, err := crm.CreateDeal(x.ctx, x.a.SQLDB, f)
	if err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	x.data["deal_id"] = d.ID
	return ok(act.Type, summary, d.ID, sm)
}

func (x *execCtx) updateDeal(act RuleAction) RuleActionResult {
	id := x.ref(act, "deal_id")
	if id == "" {
		return fail(act.Type, "", errors.New("the event names no deal"), msg{}, m("no_ref", "noun", "deal"))
	}
	value, err := minor(x.param(act, "value"))
	if err != nil {
		return fail(act.Type, "", err, msg{}, generic(err))
	}
	stageID, stageName, err := x.stageID(x.param(act, "stage"))
	if err != nil {
		return fail(act.Type, "", err, msg{}, generic(err))
	}
	owner := x.param(act, "owner")
	var parts []string
	updateArgs := []string{"id", id}
	if stageName != "" {
		parts = append(parts, "move it to "+stageName)
		updateArgs = append(updateArgs, "stage", stageName)
	}
	if value != nil {
		parts = append(parts, fmt.Sprintf("set its value to %.2f", float64(*value)/100))
		updateArgs = append(updateArgs, "value", fmt.Sprintf("%.2f", float64(*value)/100))
	}
	if owner != "" {
		parts = append(parts, "give it to "+owner)
		updateArgs = append(updateArgs, "owner", owner)
	}
	if len(parts) == 0 {
		return skip(act.Type, "update the deal", "nothing to change", msg{}, m("nothing_to_change"))
	}
	sm := m("update_deal", updateArgs...)
	summary := "deal " + id + ": " + strings.Join(parts, ", ")
	if x.dry {
		return would(act.Type, summary, sm)
	}
	f := crm.DealFields{ValueMinor: value}
	if stageID != "" {
		f.StageID = &stageID
	}
	if owner != "" {
		f.OwnerUserID = &owner
	}
	if _, err := crm.UpdateDeal(x.ctx, x.a.SQLDB, id, f); err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	return ok(act.Type, summary, id, sm)
}

// ---- contacts ----------------------------------------------------------------------

func (x *execCtx) createContact(act RuleAction) RuleActionResult {
	email := strings.ToLower(x.param(act, "email"))
	if _, err := mail.ParseAddress(email); err != nil || email == "" {
		return fail(act.Type, "", fmt.Errorf("%q is not an email address", email), msg{}, m("invalid_email", "value", email))
	}
	name, phone := x.param(act, "name"), x.param(act, "phone")
	locale := x.param(act, "locale")
	if locale != "ar" {
		locale = "en"
	}
	summary := "add " + email + " as a contact"
	sm := m("create_contact", "email", email)
	if x.dry {
		if _, ok := x.data["contact_id"]; !ok {
			x.data["contact_id"] = "(the new contact)"
		}
		return would(act.Type, summary, sm)
	}
	res, err := marketing.AddContact(x.ctx, x.a, marketing.AddContactInput{Email: email, Name: clip(name, 120), Locale: locale})
	if err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	id := res.Contact.ID
	if phone != "" && res.Contact.Phone == "" {
		p := clip(phone, 40)
		if _, err := crm.UpdateContact(x.ctx, x.a.SQLDB, id, crm.ContactFields{Phone: &p}); err != nil {
			return fail(act.Type, summary, err, sm, generic(err))
		}
	}
	x.data["contact_id"] = id
	return ok(act.Type, summary, id, sm)
}

func (x *execCtx) updateContact(act RuleAction) RuleActionResult {
	id := x.ref(act, "contact_id")
	if id == "" {
		return fail(act.Type, "", errors.New("the event names no contact"), msg{}, m("no_ref", "noun", "contact"))
	}
	f := crm.ContactFields{}
	var parts []string
	updateArgs := []string{"id", id}
	if l := strings.ToLower(x.param(act, "lifecycle")); l != "" {
		f.Lifecycle = &l
		parts = append(parts, "set the stage to "+l)
		updateArgs = append(updateArgs, "lifecycle", l)
	}
	if o := x.param(act, "owner"); o != "" {
		f.OwnerUserID = &o
		parts = append(parts, "give it to "+o)
		updateArgs = append(updateArgs, "owner", o)
	}
	var add []string
	for _, t := range strings.Split(x.param(act, "add_tags"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			add = append(add, t)
		}
	}
	if len(add) > 0 {
		parts = append(parts, "tag it "+strings.Join(add, ", "))
		updateArgs = append(updateArgs, "tags", strings.Join(add, ", "))
	}
	if len(parts) == 0 {
		return skip(act.Type, "update the contact", "nothing to change", msg{}, m("nothing_to_change"))
	}
	sm := m("update_contact", updateArgs...)
	summary := "contact " + id + ": " + strings.Join(parts, ", ")
	if x.dry {
		return would(act.Type, summary, sm)
	}
	if len(add) > 0 {
		var cur []string
		var raw string
		if err := x.a.SQLDB.QueryRowContext(x.ctx, `SELECT coalesce(array_to_json(tags)::text, '[]') FROM contacts WHERE workspace_id = $1 AND id = $2`, x.ws, id).Scan(&raw); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fail(act.Type, summary, errors.New("contact not found"), sm, m("not_found", "thing", "contact"))
			}
			return fail(act.Type, summary, err, sm, generic(err))
		}
		_ = json.Unmarshal([]byte(raw), &cur)
		tags := append(cur, add...)
		f.Tags = &tags
	}
	if _, err := crm.UpdateContact(x.ctx, x.a.SQLDB, id, f); err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	return ok(act.Type, summary, id, sm)
}

// ---- tasks and notes -----------------------------------------------------------------

func (x *execCtx) activity(act RuleAction) RuleActionResult {
	body := clip(x.param(act, "body"), 5000)
	if body == "" {
		return fail(act.Type, "", errors.New("the text came out empty; check its {{fields}}"), msg{}, m("empty_field", "field", "text"))
	}
	contactID, dealID := x.ref(act, "contact_id"), x.ref(act, "deal_id")
	if contactID != "" && !x.dry && !x.exists("contacts", contactID) {
		contactID = ""
	}
	if dealID != "" && !x.dry && !x.exists("deals", dealID) {
		dealID = ""
	}
	kind, what := "note", "add a note"
	if act.Type == ActCreateTask {
		kind, what = "task", "add a task"
	}
	on := []string{}
	if contactID != "" {
		on = append(on, "contact "+contactID)
	}
	if dealID != "" {
		on = append(on, "deal "+dealID)
	}
	summary := what + " “" + clip(body, 80) + "”"
	if len(on) > 0 {
		summary += " on " + strings.Join(on, " and ")
	}
	activityArgs := []string{"body", clip(body, 80)}
	if contactID != "" {
		activityArgs = append(activityArgs, "contact_id", contactID)
	}
	if dealID != "" {
		activityArgs = append(activityArgs, "deal_id", dealID)
	}
	in := crm.ActivityInput{Kind: kind, Body: body, ContactID: contactID, DealID: dealID}
	if kind == "task" {
		days := 1
		if d := x.param(act, "due_in_days"); d != "" {
			n, err := strconv.Atoi(d)
			if err != nil || n < 0 || n > 365 {
				return fail(act.Type, summary, fmt.Errorf("due_in_days is a whole number of days from 0 to 365, not %q", d), msg{}, m("invalid_due_days", "value", d))
			}
			days = n
		}
		in.DueAt = time.Now().UTC().AddDate(0, 0, days).Format(time.RFC3339)
		summary += fmt.Sprintf(", due in %d day(s)", days)
		activityArgs = append(activityArgs, "due_days", strconv.Itoa(days))
	}
	sm := m(act.Type, activityArgs...)
	if contactID == "" && dealID == "" {
		return fail(act.Type, summary, errors.New("a note or task belongs to a contact or a deal, and this event names neither (add a create contact or create deal step before it)"),
			sm, m("no_activity_subject"))
	}
	if x.dry {
		return would(act.Type, summary, sm)
	}
	a, err := crm.CreateActivity(x.ctx, x.a.SQLDB, in)
	if err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	return ok(act.Type, summary, a.ID, sm)
}

// ---- assign --------------------------------------------------------------------------

func (x *execCtx) assign(act RuleAction) RuleActionResult {
	who := x.param(act, "member")
	all, err := activeMembers(x.ctx, x.a, x.ws)
	if err != nil {
		return fail(act.Type, "", err, msg{}, generic(err))
	}
	label := ""
	for _, mb := range all {
		if mb.UserID == who {
			label = memberLabel(mb)
		}
	}
	if label == "" {
		return fail(act.Type, "", errors.New("that person is not an active member of this workspace any more"), msg{}, m("member_gone"))
	}
	var what, subject, id string
	switch x.def.Subject {
	case "conversation":
		what, subject, id = "the conversation", "conversation", x.ref(act, "conversation_id")
	case "deal":
		what, subject, id = "the deal", "deal", x.ref(act, "deal_id")
	case "contact":
		what, subject, id = "the contact", "contact", x.ref(act, "contact_id")
	default:
		return fail(act.Type, "", fmt.Errorf("%s events have nothing to assign; assigning works on conversations, deals and contacts", x.rule.Trigger),
			msg{}, m("unsupported_subject", "trigger", x.rule.Trigger))
	}
	summary := "assign " + what + " to " + label
	sm := m("assign_member", "who", label, "subject", subject)
	if id == "" {
		return fail(act.Type, summary, errors.New("the event names no record to assign"), sm, m("no_ref", "noun", "record to assign"))
	}
	if x.dry {
		return would(act.Type, summary, sm)
	}
	switch x.def.Subject {
	case "conversation":
		_, err = inbox.UpdateConversation(x.ctx, x.a, id, inbox.ConversationPatch{AssigneeUserID: &who})
	case "deal":
		_, err = crm.UpdateDeal(x.ctx, x.a.SQLDB, id, crm.DealFields{OwnerUserID: &who})
	case "contact":
		_, err = crm.UpdateContact(x.ctx, x.a.SQLDB, id, crm.ContactFields{OwnerUserID: &who})
	}
	if err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	return ok(act.Type, summary, id, sm)
}

// ---- canned reply ----------------------------------------------------------------------

func (x *execCtx) cannedReply(act RuleAction) RuleActionResult {
	convID := x.ref(act, "conversation_id")
	cannedID := x.param(act, "canned_reply_id")
	var title, en, ar string
	err := x.a.SQLDB.QueryRowContext(x.ctx, `SELECT title, body_en, body_ar FROM canned_replies WHERE workspace_id = $1 AND id = $2`,
		x.ws, cannedID).Scan(&title, &en, &ar)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(act.Type, "", errors.New("that canned reply no longer exists"), msg{}, m("not_found", "thing", "canned_reply"))
	} else if err != nil {
		return fail(act.Type, "", err, msg{}, generic(err))
	}
	locale := strings.ToLower(text(x.data["locale"]))
	body := en
	if (strings.HasPrefix(locale, "ar") && ar != "") || en == "" {
		body = ar
	}
	body = strings.TrimSpace(Render(body, x.data))
	summary := "reply “" + title + "” in the conversation"
	sm := m("canned_reply", "title", title)
	if convID == "" {
		return fail(act.Type, summary, errors.New("the event names no conversation"), sm, m("no_ref", "noun", "conversation"))
	}
	if x.dry {
		return would(act.Type, summary+": "+clip(body, 120), sm)
	}
	// Two autoresponders must not answer each other for ever: one automatic
	// reply per conversation per rule per day.
	var recent int
	_ = x.a.SQLDB.QueryRowContext(x.ctx, `
		SELECT count(*) FROM automation_rule_runs WHERE workspace_id = $1 AND rule_id = $2 AND subject_id = $3 AND NOT dry_run
		  AND started_at > now() - interval '1 day' AND actions @> '[{"type":"canned_reply","status":"ok"}]'`,
		x.ws, x.rule.ID, convID).Scan(&recent)
	if recent > 0 {
		return skip(act.Type, summary, "this rule already replied in this conversation today", sm, m("already_replied_today"))
	}
	rep, err := inbox.Reply(x.ctx, x.a, convID, body)
	if err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	return ok(act.Type, summary, rep.ID, sm)
}

// ---- email ---------------------------------------------------------------------------

func (x *execCtx) sendEmail(act RuleAction) RuleActionResult {
	to := strings.ToLower(x.param(act, "to"))
	subject := clip(x.param(act, "subject"), 200)
	body := x.param(act, "body")
	addr, err := mail.ParseAddress(to)
	if err != nil || to == "" {
		return fail(act.Type, "", fmt.Errorf("%q is not an email address", to), msg{}, m("invalid_email", "value", to))
	}
	to = strings.ToLower(addr.Address)
	summary := "email " + to + ": “" + subject + "”"
	sm := m("send_email", "to", to, "subject", subject)
	if subject == "" || body == "" {
		return fail(act.Type, summary, errors.New("the subject or the text came out empty; check their {{fields}}"), sm, m("empty_field", "field", "subject/body"))
	}
	if !authz.Can(x.s.Role, authz.CampaignsSend) {
		return fail(act.Type, summary, errors.New("the member this rule acts as may not send email from the workspace"), sm, m("role_forbidden", "action", "send_email"))
	}
	// Who may be written to: the workspace's own members, or its contacts who
	// have not asked to stop.
	locale := "en"
	isMember := false
	if all, err := activeMembers(x.ctx, x.a, x.ws); err == nil {
		for _, mb := range all {
			if strings.EqualFold(mb.Email, to) {
				isMember = true
			}
		}
	}
	if !isMember {
		var contactLocale string
		var bounced, unsubscribed, active int
		err := x.a.SQLDB.QueryRowContext(x.ctx, `
			SELECT c.locale,
			       count(s.id) FILTER (WHERE s.status = 'bounced'),
			       count(s.id) FILTER (WHERE s.status = 'unsubscribed'),
			       count(s.id) FILTER (WHERE s.status IN ('pending', 'subscribed'))
			FROM contacts c LEFT JOIN list_subscriptions s ON s.workspace_id = c.workspace_id AND s.contact_id = c.id
			WHERE c.workspace_id = $1 AND c.email = $2 GROUP BY c.locale`, x.ws, to).Scan(&contactLocale, &bounced, &unsubscribed, &active)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return fail(act.Type, summary, errors.New("a rule emails only this workspace's members and contacts, and this address is neither"), sm, m("not_recipient"))
		case err != nil:
			return fail(act.Type, summary, err, sm, generic(err))
		case bounced > 0:
			return skip(act.Type, summary, "mail to this address has bounced; it is not written to again", sm, m("bounced"))
		case unsubscribed > 0 && active == 0:
			return skip(act.Type, summary, "this contact unsubscribed and is on no list; a rule does not write to them", sm, m("unsubscribed"))
		}
		locale = contactLocale
	}
	var svc *marketing.SMTP
	if id := x.param(act, "smtp_service_id"); id != "" {
		svc, err = marketing.LoadSMTPService(x.ctx, x.a.SQLDB, x.ws, id)
	} else {
		svc, err = marketing.LoadSMTP(x.ctx, x.a.SQLDB, x.ws)
	}
	if err != nil || svc == nil {
		if err == nil {
			err = marketing.ErrNoService
		}
		return fail(act.Type, summary, err, sm, generic(err))
	}
	if x.dry {
		return would(act.Type, summary+" — "+clip(body, 120), sm)
	}
	var sent int
	_ = x.a.SQLDB.QueryRowContext(x.ctx, `
		SELECT count(*) FROM automation_rule_runs r, jsonb_array_elements(r.actions) a
		WHERE r.workspace_id = $1 AND NOT r.dry_run AND r.started_at > now() - interval '1 hour'
		  AND a ->> 'type' = 'send_email' AND a ->> 'status' = 'ok'`, x.ws).Scan(&sent)
	if sent >= EmailHourlyCap {
		return skip(act.Type, summary, fmt.Sprintf("the workspace's rules already sent %d emails this hour", EmailHourlyCap), sm, m("hourly_cap", "cap", strconv.Itoa(EmailHourlyCap)))
	}
	htmlBody := strings.ReplaceAll(html.EscapeString(body), "\n", "<br>")
	mailMsg := marketing.Message{To: to, Subject: subject, Text: body,
		HTML: marketing.Shell(locale, subject, htmlBody, html.EscapeString(x.s.WorkspaceName))}
	if err := marketing.Send(x.ctx, svc, mailMsg); err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	return ok(act.Type, summary, "", sm)
}

// ---- WhatsApp to a contact ----------------------------------------------------------------

func (x *execCtx) whatsAppContact(act RuleAction) RuleActionResult {
	id := x.ref(act, "contact_id")
	content := x.param(act, "text")
	summary := "WhatsApp the contact: “" + clip(content, 120) + "”"
	sm := m("whatsapp_contact", "text", clip(content, 120))
	if id == "" {
		return fail(act.Type, summary, errors.New("the event names no contact"), sm, m("no_ref", "noun", "contact"))
	}
	if content == "" {
		return fail(act.Type, summary, errors.New("the text came out empty; check its {{fields}}"), sm, m("empty_field", "field", "text"))
	}
	if x.dry {
		if !whatsapp.LoadConfig().Configured() {
			return fail(act.Type, summary, whatsapp.ErrNotConfigured, sm, m("whatsapp_not_configured"))
		}
		if _, err := whatsapp.Load(x.ctx, x.a, x.ws); err != nil {
			return fail(act.Type, summary, err, sm, generic(err))
		}
		return would(act.Type, summary+" — sent only if they opted in to WhatsApp and have not said STOP", sm)
	}
	// The rule is the member's standing confirmation, given when they switched
	// it on: the one-time code a new thread asks for is redeemed at once. The
	// consent rule and MH-410's limits are checked on both calls.
	res, err := inbox.SendWhatsAppToContact(x.ctx, x.a, id, content, "")
	if err == nil && res.ConfirmationRequired {
		res, err = inbox.SendWhatsAppToContact(x.ctx, x.a, id, content, res.Code)
	}
	if err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	return ok(act.Type, summary, res.ConversationID, sm)
}

// ---- lists ---------------------------------------------------------------------------------

func (x *execCtx) addToList(act RuleAction) RuleActionResult {
	listID := x.param(act, "list_id")
	var listName string
	var doi bool
	err := x.a.SQLDB.QueryRowContext(x.ctx, `SELECT name, double_opt_in FROM mail_lists WHERE workspace_id = $1 AND id = $2`, x.ws, listID).Scan(&listName, &doi)
	if errors.Is(err, sql.ErrNoRows) {
		return fail(act.Type, "", errors.New("that list no longer exists"), msg{}, m("not_found", "thing", "list"))
	} else if err != nil {
		return fail(act.Type, "", err, msg{}, generic(err))
	}
	contactID := x.ref(act, "contact_id")
	var email, name, locale string
	if contactID != "" {
		err = x.a.SQLDB.QueryRowContext(x.ctx, `SELECT email, name, locale FROM contacts WHERE workspace_id = $1 AND id = $2`, x.ws, contactID).Scan(&email, &name, &locale)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fail(act.Type, "", err, msg{}, generic(err))
		}
	}
	if email == "" {
		email = strings.ToLower(strings.TrimSpace(text(x.data["email"])))
		name = text(x.data["name"])
		locale = text(x.data["locale"])
	}
	summary := "add " + firstNonEmpty(email, "the contact") + " to “" + listName + "”"
	sm := m("add_to_list", "email", firstNonEmpty(email, ""), "list", listName)
	if doi {
		summary += " (a confirmation email goes first)"
		sm = m("add_to_list_doi", "email", firstNonEmpty(email, ""), "list", listName)
	}
	if email == "" {
		return fail(act.Type, summary, errors.New("the event names no contact or email"), sm, m("no_ref", "noun", "contact or email"))
	}
	if x.dry {
		return would(act.Type, summary, sm)
	}
	var status string
	err = x.a.SQLDB.QueryRowContext(x.ctx, `
		SELECT s.status FROM list_subscriptions s JOIN contacts c ON c.workspace_id = s.workspace_id AND c.id = s.contact_id
		WHERE s.workspace_id = $1 AND s.list_id = $2 AND c.email = $3`, x.ws, listID, email).Scan(&status)
	switch {
	case err == nil && (status == marketing.StatusUnsubscribed || status == marketing.StatusBounced):
		return skip(act.Type, summary, "they unsubscribed from this list (or bounced); a rule never adds them back", sm, m("unsubscribed_from_list"))
	case err == nil && status == marketing.StatusSubscribed:
		return skip(act.Type, summary, "already on the list", sm, m("already_on_list"))
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return fail(act.Type, summary, err, sm, generic(err))
	}
	if locale != "ar" {
		locale = "en"
	}
	res, err := marketing.AddContact(x.ctx, x.a, marketing.AddContactInput{Email: email, Name: clip(name, 120), Locale: locale, ListID: listID})
	if err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	r := ok(act.Type, summary+" — "+res.Status, res.Contact.ID, sm)
	if res.Confirmation == "not_sent" {
		r.Error = "the confirmation email could not be sent: " + res.Error
		r.ErrorCode, r.ErrorArgs = "confirmation_not_sent", map[string]string{"detail": res.Error}
	}
	x.data["contact_id"] = res.Contact.ID
	return r
}

// ---- browser workflow ------------------------------------------------------------------------

func (x *execCtx) runWorkflow(act RuleAction) RuleActionResult {
	wfID, agentID := x.param(act, "workflow_id"), x.param(act, "browser_id")
	var wfName, agentName string
	if err := x.a.SQLDB.QueryRowContext(x.ctx, `SELECT name FROM automation_workflows WHERE workspace_id = $1 AND id = $2`, x.ws, wfID).Scan(&wfName); err != nil {
		return fail(act.Type, "", errors.New("that workflow no longer exists"), msg{}, m("not_found", "thing", "workflow"))
	}
	var revoked sql.NullTime
	if err := x.a.SQLDB.QueryRowContext(x.ctx, `SELECT name, revoked_at FROM browser_agents WHERE workspace_id = $1 AND id = $2`, x.ws, agentID).Scan(&agentName, &revoked); err != nil || revoked.Valid {
		return fail(act.Type, "", errors.New("that browser is no longer paired with this workspace"), msg{}, m("not_found", "thing", "browser"))
	}
	values := map[string]string{}
	if raw := strings.TrimSpace(act.Params["values"]); raw != "" {
		var tpl map[string]string
		if err := json.Unmarshal([]byte(raw), &tpl); err != nil {
			return fail(act.Type, "", errors.New("the values are not a JSON object of text"), msg{}, m("invalid_values_json"))
		}
		for k, v := range tpl {
			values[k] = Render(v, x.data)
		}
	}
	summary := "run “" + wfName + "” on " + agentName
	sm := m("run_workflow", "workflow", wfName, "agent", agentName)
	if !authz.Can(x.s.Role, authz.AutomationRun) {
		return fail(act.Type, summary, errors.New("the member this rule acts as may not start browser runs"), sm, m("role_forbidden", "action", "run_workflow"))
	}
	hub := automation.Default()
	if x.dry {
		if hub == nil || !hub.Online(agentID) {
			r := RuleActionResult{Type: act.Type, Status: "would", Summary: summary, Error: "that browser is not connected right now; the run would fail until it is"}
			r.SummaryCode, r.SummaryArgs = sm.Code, sm.Args
			r.ErrorCode = "browser_offline"
			return r
		}
		return would(act.Type, summary, sm)
	}
	if hub == nil {
		return fail(act.Type, summary, errors.New("the browser registry is not running"), sm, m("browser_registry_down"))
	}
	runID, err := automation.RunWorkflow(x.ctx, x.a, hub, x.ws, x.s.UserID, wfID, agentID, "rule", values)
	if err != nil {
		return fail(act.Type, summary, err, sm, generic(err))
	}
	return ok(act.Type, summary, runID, sm)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
