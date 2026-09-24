package mcp

// Docs for the automation rules tools (user_tools_rules.go; MH-427):
// when something happens, do something. See docs.go.

var docsRules = map[string]ToolDoc{
	"rule_catalog": {
		AR: "ما يمكن لقاعدة أتمتة الاستماع إليه: كل حدث ومعه الحقول التي يحملها ومثال عليها، والمؤثِّرات (operators) التي يمكن أن يستخدمها " +
			"شرط، والإجراءات وإعدادات كل منها. كما تُدرج عمق حارس الحلقات وسقف مساحة العمل بالساعة، بدل تخمينهما. مرَّر المفتاح trigger أو type " +
			"إلى create_rule.",
		Example: map[string]any{},
		Result: `{"triggers":[` +
			`{"type":"lead.created","subject":"lead","fields":[{"name":"lead_id","type":"string"},{"name":"name","type":"string"},` +
			`{"name":"email","type":"string"},{"name":"phone","type":"string"},{"name":"company","type":"string"},{"name":"message","type":"string"},` +
			`{"name":"form_name","type":"string"},{"name":"locale","type":"string"},{"name":"budget","type":"string"},` +
			`{"name":"utm_source","type":"string"},{"name":"utm_medium","type":"string"},{"name":"utm_campaign","type":"string"},{"name":"status","type":"string"}],` +
			`"sample":{"lead_id":"sample-lead","name":"Rana Adel","email":"rana@example.com","phone":"+20 100 000 0000","company":"Acme",` +
			`"message":"We need a new website before March.","form_name":"Contact form","locale":"en","budget":"5,000","utm_source":"google","status":"new"}},` +
			`{"type":"schedule","subject":"","fields":[{"name":"fired_at","type":"string"},{"name":"date","type":"string"},{"name":"weekday","type":"string"},{"name":"time","type":"string"}],` +
			`"sample":{"fired_at":"2026-09-24T09:00:00Z","date":"2026-09-24","weekday":"Thursday","time":"09:00"}}` +
			`],"actions":[` +
			`{"type":"notify_members","params":[{"name":"members","kind":"members","default":"all"},{"name":"title","kind":"text","required":true},` +
			`{"name":"body","kind":"textarea"},{"name":"whatsapp","kind":"bool","default":"false"}]},` +
			`{"type":"create_deal","params":[{"name":"title","kind":"text","required":true},{"name":"value","kind":"number"},{"name":"currency","kind":"text"},` +
			`{"name":"stage","kind":"stage"},{"name":"contact_id","kind":"contact_ref","default":"{{contact_id}}"},{"name":"owner","kind":"member"}]}` +
			`],"operators":["equals","not_equals","contains","not_contains","starts_with","ends_with","gt","gte","lt","lte","in","not_in","is_empty","is_not_empty"],` +
			`"max_depth":3,"workspace_hourly_cap":500}`,
	},
	"list_rules": {
		AR:      "قواعد الأتمتة في مساحة العمل: معرّف كل قاعدة واسمها والحدث الذي تستمع إليه وهل هي مفعَّلة، وكيف سارت آخر مرة شُغِّلت فيها.",
		Example: map[string]any{},
		Result: `{"rules":[` +
			`{"id":"2f7c9b31-6a48-4e05-9c1d-7b3f8a2e5d60","name":"New lead — notify and create a deal","trigger":"lead.created","enabled":true,` +
			`"conditions":1,"actions":2,"run_count":14,"last_status":"ok"},` +
			`{"id":"9a41e6d0-3c85-4f92-b6e7-1d4a9c0f7b23","name":"Sentry error — WhatsApp the on-call member","trigger":"notification.received",` +
			`"enabled":true,"conditions":2,"actions":1,"run_count":3,"last_status":"ok"}]}`,
	},
	"get_rule": {
		AR:      "قاعدة أتمتة واحدة كاملةً، مع أحدث عشر عمليات تشغيل لها.",
		Example: map[string]any{"id": "2f7c9b31-6a48-4e05-9c1d-7b3f8a2e5d60"},
		Result: `{"rule":{"id":"2f7c9b31-6a48-4e05-9c1d-7b3f8a2e5d60","name":"New lead — notify and create a deal","description":"",` +
			`"enabled":true,"trigger":"lead.created","match":"all","conditions":[{"field":"budget","op":"is_not_empty"}],` +
			`"actions":[{"type":"notify_members","params":{"members":"all","title":"New lead: {{name}}","body":"{{message}}"}},` +
			`{"type":"create_deal","params":{"title":"{{name}} — {{company}}"}}],"cron":"","timezone":"UTC","next_run_at":null,` +
			`"once_per_subject":false,"max_runs_per_hour":30,"run_count":14,"last_run_at":"2026-09-24T10:12:01Z","last_status":"ok","last_error":"",` +
			`"has_webhook_secret":false,"created_by":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47","updated_by":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47",` +
			`"created_at":"2026-09-10T08:00:00Z","updated_at":"2026-09-20T09:30:00Z"},"runs":[` +
			`{"id":"r1a2b3c4-5d6e-7f80-9a1b-2c3d4e5f6a70","rule_id":"2f7c9b31-6a48-4e05-9c1d-7b3f8a2e5d60","rule_name":"New lead — notify and create a deal",` +
			`"event_id":"e5f6a7b8-9c0d-1e2f-3a4b-5c6d7e8f9a01","trigger":"lead.created","subject_id":"lead-9931","status":"ok","dry_run":false,"reason":"",` +
			`"event":{"lead_id":"lead-9931","name":"Mona Kamal","company":"Nile Roasters","message":"Interested in wholesale pricing.","budget":"5,000"},` +
			`"conditions":[{"field":"budget","op":"is_not_empty","actual":"5,000","passed":true}],` +
			`"actions":[{"type":"notify_members","status":"ok","summary":"Notified 3 members"},` +
			`{"type":"create_deal","status":"ok","summary":"Created deal \"Mona Kamal — Nile Roasters\"","ref":"d9e0f1a2-3b4c-5d6e-7f80-9a1b2c3d4e5f"}],` +
			`"depth":0,"tested_by":"","started_at":"2026-09-24T10:12:00Z","finished_at":"2026-09-24T10:12:01Z"}]}`,
	},
	"create_rule": {
		AR: "يضيف قاعدة أتمتة: عندما يقع trigger، وإذا تحقّقت الشروط، تُنفَّذ الإجراءات actions بالترتيب. تُحفظ مطفأة ما لم تُمرَّر " +
			"enabled:true — اختبرها أولًا بـ test_rule. تعمل بصفتك أنت، وبدورك عند التشغيل.",
		Example: map[string]any{
			"name":    "New lead — notify and create a deal",
			"trigger": "lead.created",
			"match":   "all",
			"conditions": []any{
				map[string]any{"field": "budget", "op": "is_not_empty"},
			},
			"actions": []any{
				map[string]any{"type": "notify_members", "params": map[string]any{"members": "all", "title": "New lead: {{name}}", "body": "{{message}}"}},
				map[string]any{"type": "create_deal", "params": map[string]any{"title": "{{name}} — {{company}}"}},
			},
		},
		Result: `{"rule":{"id":"2f7c9b31-6a48-4e05-9c1d-7b3f8a2e5d60","name":"New lead — notify and create a deal","description":"",` +
			`"enabled":false,"trigger":"lead.created","match":"all","conditions":[{"field":"budget","op":"is_not_empty"}],` +
			`"actions":[{"type":"notify_members","params":{"members":"all","title":"New lead: {{name}}","body":"{{message}}"}},` +
			`{"type":"create_deal","params":{"title":"{{name}} — {{company}}"}}],"cron":"","timezone":"UTC","next_run_at":null,` +
			`"once_per_subject":false,"max_runs_per_hour":30,"run_count":0,"last_run_at":null,"last_status":"","last_error":"",` +
			`"has_webhook_secret":false,"created_by":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47","updated_by":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47",` +
			`"created_at":"2026-09-24T10:40:00Z","updated_at":"2026-09-24T10:40:00Z"}}`,
	},
	"update_rule": {
		AR: "يغيّر قاعدة أتمتة: تفعيلها أو إطفاؤها (enabled)، أو تغيير أي جزء من تعريفها — لا يتغيّر إلا ما مُرِّر. حفظها (أو تفعيلها) " +
			"يجعلك أنت العضو الذي تعمل بصفته.",
		Example: map[string]any{"id": "2f7c9b31-6a48-4e05-9c1d-7b3f8a2e5d60", "enabled": true},
		Result: `{"rule":{"id":"2f7c9b31-6a48-4e05-9c1d-7b3f8a2e5d60","name":"New lead — notify and create a deal","description":"",` +
			`"enabled":true,"trigger":"lead.created","match":"all","conditions":[{"field":"budget","op":"is_not_empty"}],` +
			`"actions":[{"type":"notify_members","params":{"members":"all","title":"New lead: {{name}}","body":"{{message}}"}},` +
			`{"type":"create_deal","params":{"title":"{{name}} — {{company}}"}}],"cron":"","timezone":"UTC","next_run_at":null,` +
			`"once_per_subject":false,"max_runs_per_hour":30,"run_count":0,"last_run_at":null,"last_status":"","last_error":"",` +
			`"has_webhook_secret":false,"created_by":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47","updated_by":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47",` +
			`"created_at":"2026-09-24T10:40:00Z","updated_at":"2026-09-24T10:41:00Z"}}`,
	},
	"test_rule": {
		AR: "يختبر قاعدة أتمتة محفوظة مقابل حدث عيّنة. لا شيء يُرسل أو يُكتب: تقول الإجابة هل تحقّقت الشروط وماذا كان سيفعل كل إجراء، مع " +
			"ملء عناصره {{field}}. الحقول المتروكة تُؤخَذ من عيّنة الحدث في rule_catalog.",
		Example: map[string]any{
			"id":    "9a41e6d0-3c85-4f92-b6e7-1d4a9c0f7b23",
			"event": map[string]any{"severity": "error", "source_kind": "sentry", "title": "TypeError: cannot read properties of undefined"},
		},
		Result: `{"run":{"id":"t3f4a5b6-7c8d-9e0f-1a2b-3c4d5e6f7a80","rule_id":"9a41e6d0-3c85-4f92-b6e7-1d4a9c0f7b23",` +
			`"rule_name":"Sentry error — WhatsApp the on-call member","event_id":"","trigger":"notification.received","subject_id":"",` +
			`"status":"ok","dry_run":true,"reason":"","event":{"severity":"error","source_kind":"sentry",` +
			`"title":"TypeError: cannot read properties of undefined","source_name":"Sentry","kind":"error"},` +
			`"conditions":[{"field":"source_kind","op":"equals","value":"sentry","actual":"sentry","passed":true},` +
			`{"field":"severity","op":"equals","value":"error","actual":"error","passed":true}],` +
			`"actions":[{"type":"whatsapp_contact","status":"would","summary":"Would message +20 100 000 0000: \"TypeError: cannot read properties of undefined\""}],` +
			`"depth":0,"tested_by":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47","started_at":"2026-09-24T10:42:00Z","finished_at":"2026-09-24T10:42:00Z"}}`,
	},
	"list_rule_runs": {
		AR:      "سجل تشغيل قواعد الأتمتة، من الأحدث: كل عملية تقييم مع حدثها، والشروط التي تحقّقت، وما فعله كل إجراء.",
		Example: map[string]any{"status": "ok", "limit": 5},
		Result: `{"runs":[` +
			`{"id":"r1a2b3c4-5d6e-7f80-9a1b-2c3d4e5f6a70","rule_id":"2f7c9b31-6a48-4e05-9c1d-7b3f8a2e5d60","rule_name":"New lead — notify and create a deal",` +
			`"event_id":"e5f6a7b8-9c0d-1e2f-3a4b-5c6d7e8f9a01","trigger":"lead.created","subject_id":"lead-9931","status":"ok","dry_run":false,"reason":"",` +
			`"event":{"lead_id":"lead-9931","name":"Mona Kamal","budget":"5,000"},` +
			`"conditions":[{"field":"budget","op":"is_not_empty","actual":"5,000","passed":true}],` +
			`"actions":[{"type":"notify_members","status":"ok","summary":"Notified 3 members"},` +
			`{"type":"create_deal","status":"ok","summary":"Created deal \"Mona Kamal — Nile Roasters\"","ref":"d9e0f1a2-3b4c-5d6e-7f80-9a1b2c3d4e5f"}],` +
			`"depth":0,"tested_by":"","started_at":"2026-09-24T10:12:00Z","finished_at":"2026-09-24T10:12:01Z"}` +
			`],"next":"a1b2c3d4-5e6f-7a80-9b1c-2d3e4f5a6b70"}`,
	},
}

// argsARRules is Arabic for the automation-rules tools' top-level argument
// descriptions (user_tools_rules.go). The nested condition/action item
// schemas (field/op/value/type/params) carry no description on the
// "conditions"/"actions" array property itself, so docs_test.go never asks
// for their Arabic — only these do.
var argsARRules = map[string]string{
	"An event type from rule_catalog, e.g. lead.created.":                    "نوع حدث من rule_catalog، مثل lead.created",
	"Default false: test it first.":                                          "الافتراضي false: اختبرها أولًا",
	"Run at most once per record (lead, deal, story…).":                      "تعمل مرة واحدة على الأكثر لكل سجل (عميل محتمل، صفقة، قصة...)",
	"The rule's own brake; default 30.":                                      "الكابح الخاص بالقاعدة نفسها؛ الافتراضي 30",
	"schedule trigger only, e.g. 0 9 * * 1-5":                                "لمحفّز schedule فقط، مثل 0 9 * * 1-5",
	"schedule trigger only, an IANA name":                                    "لمحفّز schedule فقط، اسم منطقة زمنية من IANA",
	`The sample's fields, e.g. {"severity":"error","source_kind":"sentry"}.`: `حقول العيّنة، مثل {"severity":"error","source_kind":"sentry"}`,
	"The record the sample is about, for once-per-record rules.":             "السجل الذي تدور العيّنة حوله، للقواعد التي تعمل مرة واحدة لكل سجل",
	"Leave out tests.":            "استبعد الاختبارات",
	"next from the previous page": "قيمة next من الصفحة السابقة",
	"1–100, default 50":           "من 1 إلى 100، الافتراضي 50",
}
