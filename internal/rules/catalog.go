package rules

import (
	"github.com/fadymondy/moharrik/internal/triggers"
)

/*
The catalog: which events a rule can listen to, what each carries, and which
actions a rule can take with which settings. The page builds its pickers from
it, the MCP tools describe themselves with it, and validation reads it — one
list, so the three cannot disagree about what exists.

Labels are not here: the page translates trigger.<type>, field.<name> and
action.<type> in web/lang, so the catalog stays one language-free list.
*/

// Field is one field an event carries.
type EventField struct {
	Name string `json:"name"`
	// string | number | bool
	Type string `json:"type"`
}

// TriggerDef is one event type.
type TriggerDef struct {
	Type string `json:"type"`
	// What the event's subject is (a lead, a deal…): the record "once per
	// record" counts, and the field an action falls back to.
	Subject string       `json:"subject"`
	Fields  []EventField `json:"fields"`
	// A realistic event to test against; the page lets the person change it.
	Sample map[string]any `json:"sample"`
}

// ActionParam is one setting of an action.
type ActionParam struct {
	Name string `json:"name"`
	// text | textarea | member | members | list | stage | canned | smtp | workflow | browser | contact_ref | bool | number | json | url
	Kind     string `json:"kind"`
	Required bool   `json:"required,omitempty"`
	// Filled in for a new action, e.g. "{{contact_id}}".
	Default string `json:"default,omitempty"`
}

// ActionDef is one kind of action.
type ActionDef struct {
	Type   string        `json:"type"`
	Params []ActionParam `json:"params"`
	// Sends something to a customer: consent rules apply (see actions.go).
	ToCustomer bool `json:"to_customer,omitempty"`
}

func s(names ...string) []EventField {
	out := make([]EventField, 0, len(names))
	for _, n := range names {
		out = append(out, EventField{Name: n, Type: "string"})
	}
	return out
}

func num(names ...string) []EventField {
	out := make([]EventField, 0, len(names))
	for _, n := range names {
		out = append(out, EventField{Name: n, Type: "number"})
	}
	return out
}

func join(parts ...[]EventField) []EventField {
	var out []EventField
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

var dealFields = join(s("deal_id", "title", "currency", "stage_id", "stage", "stage_kind", "from_stage", "from_stage_kind",
	"contact_id", "contact_email", "contact_name", "company_id", "company_name", "owner_user_id"), num("value", "value_minor"))

var dealSample = map[string]any{
	"deal_id": "sample-deal", "title": "Website redesign", "value": 4500, "value_minor": 450000, "currency": "USD",
	"stage_id": "sample-stage", "stage": "Proposal", "stage_kind": "open", "from_stage": "Qualified", "from_stage_kind": "open",
	"contact_id": "sample-contact", "contact_email": "rana@example.com", "contact_name": "Rana Adel",
	"company_id": "sample-company", "company_name": "Acme",
}

var conversationFields = s("conversation_id", "message_id", "channel", "text", "customer_name", "customer_email",
	"customer_phone", "contact_id", "assignee_user_id", "subject", "locale")

var conversationSample = map[string]any{
	"conversation_id": "sample-conversation", "message_id": "sample-message", "channel": "chat",
	"text": "Hi, do you offer a monthly plan?", "customer_name": "Omar", "customer_email": "omar@example.com",
	"customer_phone": "", "subject": "", "locale": "en",
}

// Triggers is every event a rule can listen to, in the order the picker lists them.
var Triggers = []TriggerDef{
	{Type: triggers.LeadCreated, Subject: "lead",
		Fields: s("lead_id", "name", "email", "phone", "company", "message", "form_name", "locale", "budget", "utm_source", "utm_medium", "utm_campaign", "status"),
		Sample: map[string]any{"lead_id": "sample-lead", "name": "Rana Adel", "email": "rana@example.com", "phone": "+20 100 000 0000",
			"company": "Acme", "message": "We need a new website before March.", "form_name": "Contact form", "locale": "en",
			"budget": "5,000", "utm_source": "google", "status": "new"}},
	{Type: triggers.FormSubmitted, Subject: "contact",
		Fields: s("form_id", "form_name", "email", "name", "list_id", "list_name", "status", "contact_id", "locale"),
		Sample: map[string]any{"form_id": "sample-form", "form_name": "Newsletter", "email": "omar@example.com", "name": "Omar",
			"list_id": "sample-list", "list_name": "Newsletter", "status": "pending", "contact_id": "sample-contact", "locale": "en"}},
	{Type: triggers.ConversationCreated, Subject: "conversation", Fields: conversationFields, Sample: conversationSample},
	{Type: triggers.MessageReceived, Subject: "conversation", Fields: conversationFields, Sample: conversationSample},
	{Type: triggers.DealCreated, Subject: "deal", Fields: dealFields, Sample: dealSample},
	{Type: triggers.DealStageChanged, Subject: "deal", Fields: dealFields, Sample: dealSample},
	{Type: triggers.DealWon, Subject: "deal", Fields: dealFields, Sample: dealSample},
	{Type: triggers.DealLost, Subject: "deal", Fields: dealFields, Sample: dealSample},
	{Type: triggers.ContactLifecycleChanged, Subject: "contact",
		Fields: s("contact_id", "email", "name", "phone", "locale", "from_stage", "to_stage", "source"),
		Sample: map[string]any{"contact_id": "sample-contact", "email": "rana@example.com", "name": "Rana Adel", "locale": "en",
			"from_stage": "lead", "to_stage": "qualified", "source": "manual"}},
	{Type: triggers.ContactOptedIn, Subject: "contact",
		Fields: s("contact_id", "email", "name", "phone", "locale", "channel", "list_id", "list_name"),
		Sample: map[string]any{"contact_id": "sample-contact", "email": "rana@example.com", "name": "Rana Adel", "phone": "+20 100 000 0000",
			"locale": "en", "channel": "email", "list_id": "sample-list", "list_name": "Newsletter"}},
	{Type: triggers.CampaignSent, Subject: "campaign",
		Fields: join(s("campaign_id", "subject", "channel", "list_id", "list_name"), num("sent_count", "failed_count", "recipient_count")),
		Sample: map[string]any{"campaign_id": "sample-campaign", "subject": "September news", "channel": "email", "list_id": "sample-list",
			"list_name": "Newsletter", "sent_count": 120, "failed_count": 2, "recipient_count": 122}},
	{Type: triggers.AutomationRunFailed, Subject: "run",
		Fields: s("run_id", "error", "trigger", "workflow_id", "workflow_name", "browser_id", "browser_name"),
		Sample: map[string]any{"run_id": "sample-run", "error": "the selector #submit was not found", "trigger": "schedule",
			"workflow_name": "Daily export", "browser_name": "Office laptop"}},
	{Type: triggers.NotificationReceived, Subject: "notification",
		Fields: s("item_id", "source_name", "source_kind", "kind", "severity", "title", "body", "url", "actor"),
		Sample: map[string]any{"item_id": "sample-item", "source_name": "Sentry", "source_kind": "sentry", "kind": "error",
			"severity": "error", "title": "TypeError: cannot read properties of undefined", "body": "checkout.js in submitOrder",
			"url": "https://sentry.example.com/issues/1", "actor": ""}},
	{Type: triggers.TrendTopic, Subject: "topic",
		Fields: join(s("topic_id", "title", "summary", "reason", "outlets"), num("score", "source_count", "item_count")),
		Sample: map[string]any{"topic_id": "sample-topic", "title": "Open-weight models reach GPT-4 level", "summary": "Three labs released…",
			"score": 72, "source_count": 4, "item_count": 6, "reason": "Carried by 4 sources", "outlets": "The Verge, Ars Technica"}},
	{Type: triggers.Schedule, Subject: "",
		Fields: s("fired_at", "date", "weekday", "time"),
		Sample: map[string]any{"fired_at": "2026-09-24T09:00:00Z", "date": "2026-09-24", "weekday": "Thursday", "time": "09:00"}},
}

// Action types.
const (
	ActNotifyMembers   = "notify_members"
	ActCreateContact   = "create_contact"
	ActUpdateContact   = "update_contact"
	ActCreateDeal      = "create_deal"
	ActUpdateDeal      = "update_deal"
	ActCreateTask      = "create_task"
	ActAddNote         = "add_note"
	ActAssignMember    = "assign_member"
	ActCannedReply     = "canned_reply"
	ActSendEmail       = "send_email"
	ActWhatsAppContact = "whatsapp_contact"
	ActAddToList       = "add_to_list"
	ActRunWorkflow     = "run_workflow"
	ActWebhook         = "webhook"
)

// Actions is every action a rule can take, in the order the picker lists them.
var Actions = []ActionDef{
	{Type: ActNotifyMembers, Params: []ActionParam{
		{Name: "members", Kind: "members", Default: "all"},
		{Name: "title", Kind: "text", Required: true},
		{Name: "body", Kind: "textarea"},
		{Name: "whatsapp", Kind: "bool", Default: "false"},
	}},
	{Type: ActCreateDeal, Params: []ActionParam{
		{Name: "title", Kind: "text", Required: true},
		{Name: "value", Kind: "number"},
		{Name: "currency", Kind: "text"},
		{Name: "stage", Kind: "stage"},
		{Name: "contact_id", Kind: "contact_ref", Default: "{{contact_id}}"},
		{Name: "owner", Kind: "member"},
	}},
	{Type: ActUpdateDeal, Params: []ActionParam{
		{Name: "deal_id", Kind: "contact_ref", Default: "{{deal_id}}"},
		{Name: "stage", Kind: "stage"},
		{Name: "value", Kind: "number"},
		{Name: "owner", Kind: "member"},
	}},
	{Type: ActCreateContact, Params: []ActionParam{
		{Name: "email", Kind: "text", Required: true, Default: "{{email}}"},
		{Name: "name", Kind: "text", Default: "{{name}}"},
		{Name: "phone", Kind: "text", Default: "{{phone}}"},
		{Name: "locale", Kind: "text", Default: "{{locale}}"},
	}},
	{Type: ActUpdateContact, Params: []ActionParam{
		{Name: "contact_id", Kind: "contact_ref", Default: "{{contact_id}}"},
		{Name: "lifecycle", Kind: "text"},
		{Name: "add_tags", Kind: "text"},
		{Name: "owner", Kind: "member"},
	}},
	{Type: ActCreateTask, Params: []ActionParam{
		{Name: "body", Kind: "textarea", Required: true},
		{Name: "due_in_days", Kind: "number", Default: "1"},
		{Name: "contact_id", Kind: "contact_ref", Default: "{{contact_id}}"},
		{Name: "deal_id", Kind: "contact_ref", Default: "{{deal_id}}"},
	}},
	{Type: ActAddNote, Params: []ActionParam{
		{Name: "body", Kind: "textarea", Required: true},
		{Name: "contact_id", Kind: "contact_ref", Default: "{{contact_id}}"},
		{Name: "deal_id", Kind: "contact_ref", Default: "{{deal_id}}"},
	}},
	{Type: ActAssignMember, Params: []ActionParam{
		{Name: "member", Kind: "member", Required: true},
	}},
	{Type: ActCannedReply, ToCustomer: true, Params: []ActionParam{
		{Name: "canned_reply_id", Kind: "canned", Required: true},
		{Name: "conversation_id", Kind: "contact_ref", Default: "{{conversation_id}}"},
	}},
	{Type: ActSendEmail, ToCustomer: true, Params: []ActionParam{
		{Name: "to", Kind: "text", Required: true, Default: "{{email}}"},
		{Name: "subject", Kind: "text", Required: true},
		{Name: "body", Kind: "textarea", Required: true},
		{Name: "smtp_service_id", Kind: "smtp"},
	}},
	{Type: ActWhatsAppContact, ToCustomer: true, Params: []ActionParam{
		{Name: "contact_id", Kind: "contact_ref", Default: "{{contact_id}}"},
		{Name: "text", Kind: "textarea", Required: true},
	}},
	{Type: ActAddToList, Params: []ActionParam{
		{Name: "list_id", Kind: "list", Required: true},
		{Name: "contact_id", Kind: "contact_ref", Default: "{{contact_id}}"},
	}},
	{Type: ActRunWorkflow, Params: []ActionParam{
		{Name: "workflow_id", Kind: "workflow", Required: true},
		{Name: "browser_id", Kind: "browser", Required: true},
		{Name: "values", Kind: "json"},
	}},
	{Type: ActWebhook, Params: []ActionParam{
		{Name: "url", Kind: "url", Required: true},
	}},
}

// Operators a condition can use.
var Operators = []string{"equals", "not_equals", "contains", "not_contains", "starts_with", "ends_with",
	"gt", "gte", "lt", "lte", "in", "not_in", "is_empty", "is_not_empty"}

func triggerDef(t string) (TriggerDef, bool) {
	for _, d := range Triggers {
		if d.Type == t {
			return d, true
		}
	}
	return TriggerDef{}, false
}

func actionDef(t string) (ActionDef, bool) {
	for _, d := range Actions {
		if d.Type == t {
			return d, true
		}
	}
	return ActionDef{}, false
}

func validOp(op string) bool {
	for _, o := range Operators {
		if o == op {
			return true
		}
	}
	return false
}

// RuleCatalog is what GET /api/w/rules/catalog returns.
type RuleCatalog struct {
	Triggers  []TriggerDef `json:"triggers"`
	Actions   []ActionDef  `json:"actions"`
	Operators []string     `json:"operators"`
	// The loop guard's depth and the workspace's hourly ceiling, so the page
	// can say them rather than hard-code them.
	MaxDepth           int `json:"max_depth"`
	WorkspaceHourlyCap int `json:"workspace_hourly_cap"`
}

// GetCatalog returns the catalog.
func GetCatalog() RuleCatalog {
	return RuleCatalog{Triggers: Triggers, Actions: Actions, Operators: Operators, MaxDepth: triggers.MaxDepth, WorkspaceHourlyCap: WorkspaceHourlyCap}
}
