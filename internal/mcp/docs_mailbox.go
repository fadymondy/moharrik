package mcp

import "strings"

// Docs for the IMAP mailbox tools (user_tools_mailbox.go, MH-555). See docs.go.

const mailboxEx = `{"created_at":"2026-09-20T08:12:44Z","enabled":true,"folder":"INBOX","has_password":true,` +
	`"host":"imap.mail.example","id":"5b8d1e2f-7a3c-4e6b-9d0f-2c4a6e8b1d37","is_primary":true,` +
	`"last_error":"","last_polled_at":"2026-09-24T12:30:05Z","last_test_at":"2026-09-20T08:13:02Z",` +
	`"last_test_error":"","last_test_ok":true,"message_count":2429,"name":"Sales","port":993,` +
	`"smtp_service_id":"","smtp_service_name":"","tls_mode":"tls","username":"sales@acme.example"}`

var docsMailbox = map[string]ToolDoc{
	"list_mailboxes": {
		AR: "صناديق بريد IMAP في مساحة العمل: يمكن ربط أكثر من صندوق، ويُستطلع كلٌّ منها وحده، كما يمكن أن تكون لمساحة العمل أكثر من خدمة SMTP. " +
			"يحمل كل صندوق معرّفه id الذي تأخذه update_mailbox وdelete_mailbox وtest_mailbox وcheck_mailbox، ويُبيّن إن كان هو الصندوق " +
			"الأساسي (المستخدم حين لا يكون لمحادثة بريد جديدة صندوق تنتمي إليه). كلمات المرور لا تُعاد أبدًا، بل has_password فقط.",
		Result: `{"count":1,"mailboxes":[` + mailboxEx + `]}`,
	},
	"create_mailbox": {
		AR: "إضافة صندوق بريد يُجمع منه البريد عبر IMAP. يحدد smtp_service_id أيّ خدمات SMTP في مساحة العمل يُرسَل عبرها الرد على " +
			"محادثات هذا الصندوق؛ تركه فارغًا يستخدم الخدمة الافتراضية. تُحفظ كلمة المرور مشفّرة ولا تعيدها أي أداة.",
		Example: map[string]any{"name": "Sales", "host": "imap.mail.example", "username": "sales@acme.example",
			"password": "app-password", "folder": "INBOX"},
		Result: `{"mailbox":` + mailboxEx + `}`,
	},
	"update_mailbox": {
		AR:      "تعديل صندوق بريد. إذا لم تُرسَل كلمة مرور بقيت المحفوظة كما هي. enabled = false يوقف الجمع منه مؤقتًا.",
		Example: map[string]any{"id": "5b8d1e2f-7a3c-4e6b-9d0f-2c4a6e8b1d37", "host": "imap.mail.example", "username": "sales@acme.example", "enabled": false},
		Result:  `{"mailbox":` + strings.Replace(mailboxEx, `"enabled":true`, `"enabled":false`, 1) + `}`,
	},
	"delete_mailbox": {
		AR:      "حذف صندوق بريد. المحادثات والرسائل التي جُمعت منه تبقى، وتفقد فقط ارتباطها به (mailbox_id).",
		Example: map[string]any{"id": "5b8d1e2f-7a3c-4e6b-9d0f-2c4a6e8b1d37"},
		Result:  `{"deleted":true,"id":"5b8d1e2f-7a3c-4e6b-9d0f-2c4a6e8b1d37"}`,
	},
	"test_mailbox": {
		AR: "فحص بيانات دخول IMAP لصندوق بريد بتسجيل الدخول وفتح مجلده — دون جمع أي رسالة أو تعليمها. " +
			"استخدمها قبل الاعتماد على صندوق أُعدّ حديثًا، أو بعد تغيير كلمة مروره.",
		Example: map[string]any{"id": "5b8d1e2f-7a3c-4e6b-9d0f-2c4a6e8b1d37"},
		Result:  `{"mailbox":` + mailboxEx + `,"ok":true}`,
	},
	"check_mailbox": {
		AR:      "جمع البريد غير المقروء من هذا الصندوق الآن، بالمسار نفسه الذي يستخدمه الاستطلاع في الخلفية. filed = عدد الرسائل التي وُضعت في الوارد.",
		Example: map[string]any{"id": "5b8d1e2f-7a3c-4e6b-9d0f-2c4a6e8b1d37"},
		Result:  `{"filed":3,"mailbox":` + mailboxEx + `}`,
	},
}

var argsARMailbox = map[string]string{
	"Defaults to \"Mailbox N\"":                                           "الافتراضي \"Mailbox N\"",
	"IMAP host, e.g. imap.gmail.com":                                      "خادم IMAP، مثل imap.gmail.com",
	"Defaults to 993":                                                     "الافتراضي 993",
	"Defaults to tls":                                                     "الافتراضي tls",
	"Defaults to INBOX":                                                   "الافتراضي INBOX",
	"Empty = the workspace's default SMTP service":                        "فارغ = خدمة SMTP الافتراضية لمساحة العمل",
	"The first mailbox is always primary regardless of this":              "أول صندوق بريد أساسي دائمًا مهما كانت هذه القيمة",
	"Mailbox id (list_mailboxes)":                                         "معرّف صندوق البريد (list_mailboxes)",
	"Mailbox id":                                                          "معرّف صندوق البريد",
	"Leave empty to keep the stored one":                                  "اتركه فارغًا للإبقاء على المحفوظة",
	"false pauses collecting from it":                                     "false يوقف الجمع منه مؤقتًا",
	"true = mark unread, false = mark read":                               "true = غير مقروءة، false = مقروءة",
	"Only email conversations on this mailbox (list_mailboxes shows ids)": "محادثات البريد الواردة على هذا الصندوق فقط (تعرض list_mailboxes المعرّفات)",
	"Full-text search over the customer's name, email and phone, the subject, and every message's body": "بحث نصي كامل في اسم العميل وبريده وهاتفه وفي الموضوع وفي نص كل رسالة",
	"next_cursor from a previous call, for the page after it":                                           "قيمة next_cursor من استدعاء سابق، للصفحة التي تليها",
}
