package mcp

// Docs for the contact-context tools (user_tools_contactcontext.go, MH-586).
// Grouped under "crm" — its scopes, contacts:read/contacts:write, are
// already in that section (see docGroups in docs.go).

// argsARContactContext is the Arabic for every non-empty argument
// description user_tools_contactcontext.go uses, keyed by the exact English
// text (docs_test.go fails the build if one is missing).
var argsARContactContext = map[string]string{
	"The contact's id": "معرّف جهة الاتصال",
	"The free-text question to answer about this contact":                                  "سؤال حر يُطرح عن جهة الاتصال هذه للإجابة عنه",
	"\"note\" (default) or \"file\"":                                                       "\"note\" (افتراضي) أو \"file\"",
	"Optional short title":                                                                 "عنوان قصير اختياري",
	"The note text, or the file's text content":                                            "نص الملاحظة، أو محتوى الملف النصي",
	"Set when kind=\"file\"":                                                               "يُستخدم عندما تكون kind=\"file\"",
	"Original filename (.txt or .zip)":                                                     "اسم الملف الأصلي (.txt أو .zip)",
	"Base64-encoded file bytes":                                                            "محتوى الملف مُرمَّزًا بـ base64",
	"Original filename (.txt or .zip) — the same one passed to preview":                    "اسم الملف الأصلي (.txt أو .zip) — نفسه الذي مُرِّر إلى المعاينة",
	"Base64-encoded file bytes — the same bytes passed to preview":                         "محتوى الملف مُرمَّزًا بـ base64 — نفس البيانات التي مُرِّرت إلى المعاينة",
	"Which sender name in the export is this contact (from the preview's senders list)":    "اسم المرسل في التصدير الذي يمثّل جهة الاتصال هذه (من قائمة المرسلين في المعاينة)",
	"The import's id (from preview/commit's reply, or get_contact_context's imports list)": "معرّف عملية الاستيراد (من رد المعاينة أو الاعتماد، أو من قائمة الاستيرادات في get_contact_context)",
}

const ccExContactID = "c_3f9a..."

const ccExSummary = `{"contact":{"id":"c_3f9a...","name":"Sara Ali","email":"sara@example.test","phone":"+201015550101"},` +
	`"recent_notes":[{"kind":"note","id":"n_1","text":"Renewal: Sara asked about renewing her plan next month.","when":"2026-09-01T10:00:00Z"}],` +
	`"recent_messages":[{"kind":"message","id":"m_1","text":"them: Hi! Are we still on for tomorrow?","when":"2026-08-30T09:00:00Z","conversation_id":"cv_1"}],` +
	`"imports":[{"id":"imp_1","filename":"android_export.txt","contact_sender":"Sara Ali","message_count":7,"media_count":0,"skipped_count":0,"created_at":"2026-08-30T09:05:00Z"}],` +
	`"open_deals":[]}`

const ccExAnswer = `{"text":"No AI provider is configured, so here are the most relevant passages found in Sara Ali's context — read them yourself:\n\n1. [note] Renewal: Sara asked about renewing her plan next month.\n",` +
	`"citations":[{"kind":"note","id":"n_1","when":"2026-09-01 10:00","snippet":"Renewal: Sara asked about renewing her plan next month."}],"no_provider":true}`

const ccExAddNote = `{"id":"n_2"}`

const ccExReport = `{"id":"r_1","version":1,"markdown":"# Relationship report: Sara Ali\n\n_Generated without an AI provider configured...\n\n## Last touch\n\n...\n\n## Timeline (cited)\n\n1. **2026-09-01 10:00** [note:n_1] Renewal: Sara asked about renewing her plan next month.\n",` +
	`"no_provider":true}`

const ccExReportList = `[{"id":"r_1","version":1,"markdown":"# Relationship report: Sara Ali...","no_provider":true}]`

const ccExPreview = `{"filename":"android_export.txt","message_count":8,"system_count":1,"media_count":0,"senders":["Sara Ali","Me"],` +
	`"date_from":"2026-08-30T09:00:00Z","date_to":"2026-08-30T09:10:00Z","sample":[{"time":"2026-08-30T09:00:00Z","sender":"Sara Ali","text":"Hi! Are we still on for tomorrow?"}],` +
	`"needs_sender_pick":false,"guessed_contact":"Sara Ali","already_imported_estimate":0}`

const ccExCommit = `{"import_id":"imp_1","conversation_id":"cv_1","messages_added":7,"messages_skipped_duplicate":0,"media_stored":0}`

const ccExRemove = `{"removed":"imp_1"}`

var docsContactContext = map[string]ToolDoc{
	"get_contact_context": {
		AR: "كل ما هو مسجَّل عن جهة اتصال في استدعاء واحد: أحدث الملاحظات والملفات، وأحدث الرسائل عبر كل القنوات، وتصديرات واتساب المستوردة، " +
			"والصفقات المفتوحة. استخدمه قبل ask_about_contact عندما تريد الصورة الكاملة بدل إجابة عن سؤال واحد.",
		Example: map[string]any{"contact_id": ccExContactID},
		Group:   "crm",
		Result:  ccExSummary,
	},
	"ask_about_contact": {
		AR: "اطرح سؤالًا حرًّا عن جهة اتصال، وتكون الإجابة مبنيّة فقط على سياقها المسترجَع (ملاحظات ورسائل وصفقات) مع مراجع لمصادرها. " +
			"يعمل حتى بلا أي مزوِّد ذكاء اصطناعي: تكون الإجابة عندها المقاطع ذات الصلة كما هي بدل جملة مُولَّدة (انظر \"no_provider\" في الرد) — لا يفشل أبدًا لعدم وجود نموذج.",
		Example: map[string]any{"contact_id": ccExContactID, "question": "What did Sara ask about?"},
		Group:   "crm",
		Result:  ccExAnswer,
	},
	"add_contact_context": {
		AR:      "إضافة سياق نصي حر إلى جهة اتصال: ملاحظة، أو محتوى ملف نصي صغير مفكوك الترميز. يُفهرَس فورًا لأجل get_contact_context وask_about_contact وgenerate_contact_report.",
		Example: map[string]any{"contact_id": ccExContactID, "content": "Sara asked about renewing her plan next month."},
		Group:   "crm",
		Result:  ccExAddNote,
	},
	"generate_contact_report": {
		AR: "توليد تقرير علاقة جديد ومرقَّم بالإصدار لجهة اتصال: ملخّص وموضوعات ووعود مفتوحة ومشاعر وآخر تواصل وخطوة تالية مقترحة، مع سجل " +
			"زمني موثَّق المصادر أسفله. كل ادّعاء يُنسب إلى مصدره. يعمل بشكل حتمي بلا أي مزوِّد ذكاء اصطناعي — انظر \"no_provider\" في الرد.",
		Example: map[string]any{"contact_id": ccExContactID},
		Group:   "crm",
		Result:  ccExReport,
	},
	"list_contact_reports": {
		AR:      "سرد تقارير العلاقة السابقة لجهة اتصال، الأحدث إصدارًا أولًا.",
		Example: map[string]any{"contact_id": ccExContactID},
		Group:   "crm",
		Result:  ccExReportList,
	},
	"import_contact_whatsapp_preview": {
		AR: "معاينة تصدير واتساب (.txt أو .zip) لجهة اتصال قبل اعتماده: عدد الرسائل والوسائط، والمرسلون الموجودون، وعيّنة قصيرة، وعدد " +
			"الرسائل من العيّنة الموجودة مسبقًا لهذه الجهة. لا يكتب أي شيء.",
		Example: map[string]any{"contact_id": ccExContactID, "filename": "android_export.txt", "data_base64": "MDAvMDAvMDAsIDAwOjAwIC0gU2FyYSBBbGk6IEhp"},
		Group:   "crm",
		Result:  ccExPreview,
	},
	"import_contact_whatsapp_commit": {
		AR: "استيراد تصدير واتساب مُعايَن إلى محادثة جهة الاتصال. إعادة اعتماد البيانات نفسها لا تضيف شيئًا (إزالة تكرار بالدقيقة والمرسل " +
			"والنص). لا يُطلق هذا أبدًا أتمتة أو إشعارًا أو إعلانًا — يُسجَّل السجل المستورد بصمت.",
		Example: map[string]any{"contact_id": ccExContactID, "filename": "android_export.txt", "data_base64": "MDAvMDAvMDAsIDAwOjAwIC0gU2FyYSBBbGk6IEhp", "contact_sender": "Sara Ali"},
		Group:   "crm",
		Result:  ccExCommit,
	},
	"remove_contact_whatsapp_import": {
		AR:      "إزالة عملية استيراد واتساب سابقة: حذف رسائلها ومرفقاتها فقط، وأي ملاحظات موسومة بها.",
		Example: map[string]any{"contact_id": ccExContactID, "import_id": "imp_1"},
		Group:   "crm",
		Result:  ccExRemove,
	},
}
