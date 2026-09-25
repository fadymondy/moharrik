package mcp

// Docs for the vCard import/export tools (user_tools_vcard.go, MH-584).
// Grouped under "crm" — its scopes, contacts:read/contacts:write, are
// already in that section (see docGroups in docs.go).

// argsARVcard is the Arabic for every non-empty argument description
// user_tools_vcard.go uses, keyed by the exact English text (docs_test.go
// fails the build if one is missing).
var argsARVcard = map[string]string{
	descVcardText:    "نص vCard خام بإصدار 2.1 أو 3.0 أو 4.0 (كتلة BEGIN:VCARD…END:VCARD واحدة لكل جهة اتصال، بأي عدد)",
	descVcardCountry: "رمز دولة بمعيار ISO 3166-1 alpha-2 (مثل \"EG\") لتحويل الأرقام بلا رمز دولة؛ اتركه لاستخدام الدولة الافتراضية لمساحة العمل",
	descVcardIDs:     "معرّفات جهات الاتصال المراد تصديرها؛ اتركه لتصدير كل جهات الاتصال في مساحة العمل",
	// MH-585: Google People import/sync joins the same preview -> commit flow.
	descContactSource: "مصدر البطاقات: \"vcard\" (الافتراضي، نص vCard ملصوق)، أو \"google_csv\" (ملف تصدير جهات اتصال Google، بلا OAuth)، " +
		"أو \"google\" (حساب Google متصل — يجلب البيانات مباشرة عبر People API؛ الالتزام هنا يشغّل مزامنة كاملة، تمامًا مثل google_contacts_sync).",
	descGoogleCSV:    "نص خام لملف تصدير جهات اتصال Google (خيار \"Google CSV\" ضمن contacts.google.com ← تصدير). مطلوب عندما يكون المصدر \"google_csv\".",
	descGoogleConnID: "معرّف google_connections من w-google-connections، بصلاحية الوصول إلى جهات الاتصال. مطلوب عندما يكون المصدر \"google\".",
	// MH-646: shared verbatim by google_contacts_sync and
	// import_contacts_commit{source:"google"} — see descFullResync.
	descFullResync: "تجاهل رمز المزامنة المخزَّن وإعادة استيراد كل جهة اتصال (MH-646: يملأ الهواتف التي يمكن الآن لدولة افتراضية جديدة حلّها). " +
		"لا يُنشئ تكرارًا أبدًا ولا يستبدل هاتفًا أدخله أحد يدويًا.",
}

const vcardExFixture = `BEGIN:VCARD\nVERSION:3.0\nFN:Rana Fathy\nTEL;TYPE=CELL:+201015550101\nEMAIL:rana@example.test\nEND:VCARD`

const vcardExPreview = `{"card_count":1,"new_count":1,"merge_count":0,"invalid_count":0,"file_hash":"3f9...",` +
	`"rows":[{"index":0,"name":"Rana Fathy","phones":["+201015550101"],"emails":["rana@example.test"],"status":"new"}]}`

const vcardExCommit = `{"card_count":1,"created_count":1,"merged_count":0,"invalid_count":0,"contact_ids":["c_3f9a..."]}`

var docsVcard = map[string]ToolDoc{
	"import_contacts_preview": {
		AR: "تحليل نص vCard وتصنيف كل بطاقة: جديدة، أو ستُدمَج في جهة اتصال موجودة (بمطابقة أي هاتف أو بريد أو UID)، أو غير صالحة مع " +
			"سبب ذلك — دون كتابة أي شيء بعد. أعد المحاولة بعد تغيير default_country لمعرفة عدد الأرقام التي سيتم تخمين دولتها من إعداد " +
			"مساحة العمل الافتراضي.",
		Example: map[string]any{"vcard": vcardExFixture},
		Group:   "crm",
		Result:  vcardExPreview,
	},
	"import_contacts_commit": {
		AR: "استيراد نص vCard: إنشاء جهة اتصال لكل بطاقة جديدة، ودمج الباقي في جهة الاتصال المطابقة بملء الحقول الفارغة فقط وإضافة " +
			"الهواتف والبريد الجديد (لا يُستبدل شيء موجود أبدًا)، وتجاهل أي بطاقة غير صالحة دون تسجيلها. إعادة تنفيذ الاستيراد بالنص نفسه " +
			"لا تُنشئ شيئًا جديدًا — قارن أولًا بنتيجة import_contacts_preview.",
		Example: map[string]any{"vcard": vcardExFixture},
		Group:   "crm",
		Result:  vcardExCommit,
	},
	"export_contacts_vcard": {
		AR:      "تصدير جهة اتصال واحدة أو مجموعة محدَّدة أو كل جهات الاتصال في مساحة العمل كنص vCard بإصدار 3.0، جاهز لأي دفتر عناوين.",
		Example: map[string]any{"ids": []any{"c_3f9a..."}},
		Group:   "crm",
		Result:  vcardExFixture,
	},
	// MH-585
	"google_contacts_sync": {
		AR: "مزامنة الآن: سحب كل جهة اتصال أو تغيير من People API الخاص بحساب Google متصل إلى جهات اتصال مساحة العمل. تدريجية بعد أول " +
			"مزامنة (فقط ما تغيّر منذ آخر مرة)، وتعود تلقائيًا إلى مزامنة كاملة إذا أبلغت Google بانتهاء صلاحية رمز المزامنة المخزَّن. " +
			"جهة اتصال أبلغت Google بحذفها تُعلَّم (google_deleted_at) ولا تُحذف أبدًا.",
		Example: map[string]any{"connection_id": "conn_9a3f..."},
		Group:   "crm",
		Result:  `{"added":2,"updated":1,"flagged_deleted":0,"invalid":0,"full_resync":false}`,
	},
}
