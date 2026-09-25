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
}
