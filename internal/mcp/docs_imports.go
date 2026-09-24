package mcp

// Docs for the generic import surface (user_tools_imports.go, MH-562). See
// docs.go. Grouped under "inbox" (its scopes, inbox:read/inbox:write, are
// already in that section) rather than a new section.

// argsARImports is the Arabic for every non-empty argument description
// user_tools_imports.go uses, keyed by the exact English text (docs_test.go
// fails the build if one is missing).
var argsARImports = map[string]string{
	descImportSource:         "مصدر من inbox_import_sources، مثل \"whatsapp\"",
	descImportConnID:         "معرّف اتصال من inbox_import_sources؛ اتركه لاستخدام اتصال المصدر الافتراضي",
	descImportQ:              "التصفية بالاسم أو الرقم أو العنوان",
	descImportKind:           "direct = شخص واحد، group = محادثة جماعية",
	descImportFlagged:        "عرض العناصر التي وسمها المصدر أيضًا (كمرسلي القوائم البريدية وعدم الرد في البريد) — مستبعدة افتراضيًا",
	descImportItems:          "معرّفات عناصر من inbox_import_preview",
	descImportHistoryDepth:   "عدد الرسائل لكل محادثة، الأحدث أولًا؛ اتركه لقيمة المصدر الافتراضية",
	descImportParticipants:   "للعناصر الجماعية فقط: أضِف أيضًا جهات اتصال لأعضاء المجموعة",
	descImportConversationID: "معرّف محادثة، من get_conversation أو نتيجة inbox_import",
}

const importsExSources = `{"sources":[{"source":"whatsapp","connections":[{"id":"default","label":"+201000000000","ready":true}]}]}`

const importsExItem = `{"id":"201015550101@s.whatsapp.net","kind":"direct","name":"Rana Fathy","identity":"+201015550101",` +
	`"last_message":"هل وصل طلبي رقم NC-20417؟","last_message_at":"2026-09-24T11:57:02Z","already_imported":false}`

const importsExGroup = `{"id":"120363011234567890@g.us","kind":"group","name":"عملاء القهوة - القاهرة","identity":"120363011234567890@g.us",` +
	`"last_message":"الشحنة الجديدة وصلت اليوم","last_message_at":"2026-09-24T12:27:02Z","message_count":2,` +
	`"participants":[{"identity":"+201015550101","name":"Rana Fathy"},{"identity":"+201098765432","name":"Laila Hassan"}],"already_imported":false}`

const importsExJob = `{"id":"0dca8d00-02b9-4c27-a662-07885916e044","source":"whatsapp","connection_id":"","status":"done",` +
	`"total":2,"done":2,"contacts_created":2,"contacts_reused":0,"conversations_created":2,"conversations_reused":0,` +
	`"messages_imported":5,"skipped":0,"created_at":"2026-09-24T15:57:20Z","updated_at":"2026-09-24T15:57:21Z"}`

var docsImports = map[string]ToolDoc{
	"inbox_import_sources": {
		AR: "كل مصدر استيراد يعرفه التطبيق (واتساب، والبريد، وأي قنوات تُضاف لاحقًا)، مع اتصالات مساحة العمل لكل مصدر وحالة جهوزيتها " +
			"للاستيراد منها. ابدأ من هنا لمعرفة أي source تمرّره إلى inbox_import_preview وinbox_import.",
		Group:  "inbox",
		Result: importsExSources,
	},
	"inbox_import_preview": {
		AR: "معاينة ما يملكه اتصال واحد قبل كتابة أي شيء: محادثات مباشرة أو جماعية مع آخر رسالة لكل منها. already_imported صحيحة " +
			"حين تكون تلك المحادثة موجودة في صندوق الوارد بالفعل — إعادة استيرادها لن تكرّرها. مرّر kind أو q لتضييق النتائج.",
		Example: map[string]any{"source": "whatsapp", "kind": "direct"},
		Group:   "inbox",
		Result:  `{"source":"whatsapp","items":[` + importsExItem + `,` + importsExGroup + `]}`,
	},
	"inbox_import": {
		AR: "استيراد العناصر المحدَّدة (معرّفاتها من inbox_import_preview) من اتصال واحد: جهات اتصال (بقاعدة MH-561 نفسها — بريد أو " +
			"هاتف أو معرّف موجود يُستخدم بدل تكراره) وخيوط صندوق وارد بسجلّها. متكرّر بأمان: إعادة استيراد محادثة مستوردة لا تضيف شيئًا " +
			"مرتين. يعمل في الخلفية ويعيد job برقمه فورًا؛ تابع حالته لاحقًا. لا يعني الاستيراد موافقة على أي تواصل مستقبلي.",
		Example: map[string]any{"source": "whatsapp", "items": []any{"201015550101@s.whatsapp.net", "120363011234567890@g.us"}, "import_participants": true},
		Group:   "inbox",
		Result:  `{"job":` + importsExJob + `}`,
	},
	"inbox_chat_summary": {
		AR: "تحليلات محادثة واحدة: عدد الرسائل والأعضاء، والتوزيع بين الوارد والصادر، وتوقيت أول وآخر رسالة — لأي محادثة، مستوردة " +
			"كانت أو لا.",
		Example: map[string]any{"conversation_id": "2b21441a-4c28-4a76-b1eb-4ea1664a47d8"},
		Group:   "inbox",
		Result: `{"conversation_id":"2b21441a-4c28-4a76-b1eb-4ea1664a47d8","channel":"whatsapp","is_group":true,"message_count":2,` +
			`"inbound_count":2,"outbound_count":0,"participant_count":3,"first_message_at":"2026-09-24T14:57:02Z","last_message_at":"2026-09-24T15:27:02Z"}`,
	},
	"whatsapp_chats": {
		AR:      "مرادف inbox_import_preview(source=\"whatsapp\", kind=\"direct\"): محادثات واتساب الفردية في مساحة العمل مع آخر رسالة لكل منها.",
		Example: map[string]any{"q": "Rana"},
		Group:   "inbox",
		Result:  `{"source":"whatsapp","items":[` + importsExItem + `]}`,
	},
	"whatsapp_groups": {
		AR:     "مرادف inbox_import_preview(source=\"whatsapp\", kind=\"group\"): مجموعات واتساب في مساحة العمل مع أعضائها وآخر رسالة.",
		Group:  "inbox",
		Result: `{"source":"whatsapp","items":[` + importsExGroup + `]}`,
	},
	"whatsapp_import": {
		AR:      "مرادف inbox_import(source=\"whatsapp\"): استيراد محادثات أو مجموعات واتساب المحدَّدة (معرّفاتها من whatsapp_chats أو whatsapp_groups).",
		Example: map[string]any{"items": []any{"201015550101@s.whatsapp.net"}},
		Group:   "inbox",
		Result:  `{"job":` + importsExJob + `}`,
	},
	"whatsapp_chat_summary": {
		AR:      "مرادف inbox_chat_summary لمحادثة واتساب.",
		Example: map[string]any{"conversation_id": "2b21441a-4c28-4a76-b1eb-4ea1664a47d8"},
		Group:   "inbox",
		Result: `{"conversation_id":"2b21441a-4c28-4a76-b1eb-4ea1664a47d8","channel":"whatsapp","is_group":true,"message_count":2,` +
			`"inbound_count":2,"outbound_count":0,"participant_count":3,"first_message_at":"2026-09-24T14:57:02Z","last_message_at":"2026-09-24T15:27:02Z"}`,
	},
}
