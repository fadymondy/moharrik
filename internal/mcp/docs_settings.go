package mcp

// Docs for the workspace-settings tools (user_tools_settings.go, MH-680). See docs.go.

var docsSettings = map[string]ToolDoc{
	"update_workspace": {
		AR:      "إعادة تسمية مساحة العمل (من 1 إلى 80 حرفًا). لا يتغيّر عنوانها (slug). تُعيد مساحة العمل.",
		Example: map[string]any{"name": "Acme Trading"},
		Result:  `{"workspace":{"id":"0b6f1c2e-5d7a-4e0b-9a41-3c8e2f1d7a90","slug":"acme","name":"Acme Trading","avatar_url":""}}`,
	},
	"set_workspace_picture": {
		AR: "رفع صورة مساحة العمل أو استبدالها، وهي تظهر في قائمة مساحات العمل والمبدِّل والشريط الجانبي. " +
			"PNG أو JPEG أو WEBP بترميز base64، حتى 2 ميغابايت. تُعيد مساحة العمل مع avatar_url.",
		Example: map[string]any{"data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==", "content_type": "image/png"},
		Result: `{"workspace":{"id":"0b6f1c2e-5d7a-4e0b-9a41-3c8e2f1d7a90","slug":"acme","name":"Acme Trading",` +
			`"avatar_url":"https://app.moharrik.com/api/me/workspaces/0b6f1c2e-5d7a-4e0b-9a41-3c8e2f1d7a90/avatar?v=1790000000000"}}`,
	},
	"remove_workspace_picture": {
		AR:      "إزالة صورة مساحة العمل، فيظهر الحرف الأول من اسمها من جديد. تُعيد مساحة العمل.",
		Example: map[string]any{},
		Result:  `{"workspace":{"id":"0b6f1c2e-5d7a-4e0b-9a41-3c8e2f1d7a90","slug":"acme","name":"Acme Trading","avatar_url":""}}`,
	},
	"invite_member": {
		AR: "دعوة شخص إلى مساحة العمل بالبريد الإلكتروني بدور مسؤول أو مدير أو عضو أو عميل — ولا يتجاوز أبدًا دور عضو هذا الرمز. " +
			"لا يُرسل أي بريد: مرّر accept_url إليه بنفسك. يظهر الرابط هذه المرة فقط، وهو مرتبط بذلك البريد وتنتهي صلاحيته بعد 7 أيام. " +
			"يُرفض إن كان عضوًا بالفعل.",
		Example: map[string]any{"email": "omar@mail.example", "role": "member"},
		Result: `{"invitation":{"id":"5e1a9c3f-2d7b-4f08-b6e4-9a0c3d8f1e27","email":"omar@mail.example","role":"member",` +
			`"expires_at":"2026-10-05T09:00:00Z","created_at":"2026-09-28T09:00:00Z"},` +
			`"accept_url":"https://app.moharrik.com/invite/Xk3v…"}`,
	},
	"list_invitations": {
		AR:      "الدعوات المعلّقة في مساحة العمل: المعرّف والبريد والدور وموعد انتهاء كل منها. لا تُعرض الروابط مرة أخرى.",
		Example: map[string]any{},
		Result: `{"count":1,"invitations":[{"id":"5e1a9c3f-2d7b-4f08-b6e4-9a0c3d8f1e27","email":"omar@mail.example","role":"member",` +
			`"expires_at":"2026-10-05T09:00:00Z","created_at":"2026-09-28T09:00:00Z"}]}`,
	},
	"revoke_invitation": {
		AR:      "إلغاء دعوة معلّقة بمعرّفها (list_invitations)، فيتوقف رابطها عن العمل.",
		Example: map[string]any{"id": "5e1a9c3f-2d7b-4f08-b6e4-9a0c3d8f1e27"},
		Result:  `{"ok":true,"revoked":"5e1a9c3f-2d7b-4f08-b6e4-9a0c3d8f1e27"}`,
	},
	"set_member_role": {
		AR: "تغيير دور عضو (مالك أو مسؤول أو مدير أو عضو أو عميل) بمعرّف المستخدم أو البريد (list_members). لا يمنح أحد دورًا أعلى " +
			"من دوره؛ ولا يغيّر المالكَ أو يجعل أحدًا مالكًا إلا مالك؛ ولا يُخفَّض آخر مالك. تُعيد الأعضاء.",
		Example: map[string]any{"member": "omar@mail.example", "role": "manager"},
		Result: `{"count":2,"members":[` +
			`{"user_id":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47","email":"sara@mail.example","role":"owner","status":"active","joined_at":"2026-08-02T10:14:00Z"},` +
			`{"user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","email":"omar@mail.example","role":"manager","status":"active","joined_at":"2026-08-11T08:40:00Z"}]}`,
	},
	"remove_member": {
		AR: "إزالة عضو من مساحة العمل بمعرّف المستخدم أو البريد (list_members). تُلغى معه رموزه وتطبيقاته المتصلة في مساحة العمل هذه. " +
			"لا يزيل المالكَ إلا مالك، ويبقى آخر مالك. ولا يزيل الرمز عضوه نفسه — فالمغادرة تكون من الصفحة. تُعيد الأعضاء.",
		Example: map[string]any{"member": "c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30"},
		Result: `{"count":1,"members":[` +
			`{"user_id":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47","email":"sara@mail.example","role":"owner","status":"active","joined_at":"2026-08-02T10:14:00Z"}]}`,
	},
	"set_pipeline_stages": {
		AR: "استبدال مسار الصفقات: إعادة تسمية المراحل وترتيبها وإضافتها وإزالتها في استدعاء واحد. أعطِ كل مرحلة بترتيبها الجديد — " +
			"الموجودة بمعرّفها (تُعيدها list_deals) والجديدة بدونه. المرحلة المحذوفة من القائمة تُزال، ويُرفض ذلك ما دامت فيها صفقات. " +
			"النوع open أو won أو lost؛ وتبقى مرحلة مفتوحة واحدة على الأقل، وتُفتح صفقات المرحلة التي يتغيّر نوعها أو تُغلق معها. " +
			"من 1 إلى 20 مرحلة. تُعيد المسار.",
		Example: map[string]any{"stages": []any{
			map[string]any{"id": "1c7e4a92-3f5b-4d08-9a6e-2b8d0f1c7e35", "name": "New", "kind": "open"},
			map[string]any{"name": "Proposal sent", "kind": "open"},
			map[string]any{"id": "8b2f6d14-7e9a-4c3b-a051-6d4e9f2b8c17", "name": "Won", "kind": "won"},
			map[string]any{"id": "e4a90c37-1b6d-4f28-8e5a-0c3f7b9d2a61", "name": "Lost", "kind": "lost"},
		}},
		Result: `{"stages":[` +
			`{"id":"1c7e4a92-3f5b-4d08-9a6e-2b8d0f1c7e35","name":"New","position":0,"kind":"open","deal_count":4,"value_minor":1250000},` +
			`{"id":"3d8f1b60-5a2c-4e97-b4d1-7f0e2c9a6b58","name":"Proposal sent","position":1,"kind":"open","deal_count":0,"value_minor":0},` +
			`{"id":"8b2f6d14-7e9a-4c3b-a051-6d4e9f2b8c17","name":"Won","position":2,"kind":"won","deal_count":7,"value_minor":4800000},` +
			`{"id":"e4a90c37-1b6d-4f28-8e5a-0c3f7b9d2a61","name":"Lost","position":3,"kind":"lost","deal_count":2,"value_minor":300000}]}`,
	},
	"smtp_services": {
		AR: "خدمات SMTP التي يُرسل بريد مساحة العمل عبرها، كاملةً للمسؤولين الذين يديرونها (أما list_smtp_services فتعطي كتّاب الحملات أسماءها فقط): المعرّف والمضيف والمنفذ والمرسِل وTLS، وأيها الافتراضية (البريد التشغيلي) وأيها " +
			"افتراضية الحملات، ونتيجة آخر اختبار. لا تُعاد كلمات المرور أبدًا؛ يقول has_password فقط إن كانت محفوظة.",
		Example: map[string]any{},
		Result: `{"count":1,"services":[{"id":"9d4c2e81-6a3f-4b07-8e1d-5c9a0f3b7e24","name":"Main","host":"smtp.mail.example","port":587,` +
			`"username":"sara@mail.example","has_password":true,"from_name":"Acme","from_email":"hello@acme.example","tls_mode":"starttls",` +
			`"is_default":true,"is_campaign_default":true,"last_test_at":"2026-09-20T12:00:00Z","last_test_ok":true,"last_test_error":"",` +
			`"campaigns":3,"created_at":"2026-08-02T10:20:00Z","updated_at":"2026-09-20T12:00:00Z"}]}`,
	},
	"create_smtp_service": {
		AR: "إضافة خدمة SMTP. تصبح الأولى الافتراضية وافتراضية الحملات. كلمة المرور مختومة عند التخزين ولا تعيدها أي أداة. " +
			"اختبرها من الإعدادات ← البريد قبل الاعتماد عليها.",
		Example: map[string]any{"name": "Main", "host": "smtp.mail.example", "port": 587, "username": "sara@mail.example",
			"password": "app-password", "from_name": "Acme", "from_email": "hello@acme.example", "tls_mode": "starttls"},
		Result: `{"service":{"id":"9d4c2e81-6a3f-4b07-8e1d-5c9a0f3b7e24","name":"Main","host":"smtp.mail.example","port":587,` +
			`"username":"sara@mail.example","has_password":true,"from_name":"Acme","from_email":"hello@acme.example","tls_mode":"starttls",` +
			`"is_default":true,"is_campaign_default":true,"last_test_at":null,"last_test_ok":null,"last_test_error":"","campaigns":0,` +
			`"created_at":"2026-09-28T09:00:00Z","updated_at":"2026-09-28T09:00:00Z"}}`,
	},
	"update_smtp_service": {
		AR: "تغيير خدمة SMTP بمعرّفها (smtp_services). أعطِ الخدمة كاملة؛ وإن حُذفت كلمة المرور بقيت المحفوظة، وإن كانت فارغة مُسحت.",
		Example: map[string]any{"id": "9d4c2e81-6a3f-4b07-8e1d-5c9a0f3b7e24", "name": "Main", "host": "smtp.mail.example", "port": 465,
			"username": "sara@mail.example", "from_name": "Acme", "from_email": "hello@acme.example", "tls_mode": "tls"},
		Result: `{"service":{"id":"9d4c2e81-6a3f-4b07-8e1d-5c9a0f3b7e24","name":"Main","host":"smtp.mail.example","port":465,` +
			`"username":"sara@mail.example","has_password":true,"from_name":"Acme","from_email":"hello@acme.example","tls_mode":"tls",` +
			`"is_default":true,"is_campaign_default":true,"last_test_at":null,"last_test_ok":null,"last_test_error":"","campaigns":3,` +
			`"created_at":"2026-08-02T10:20:00Z","updated_at":"2026-09-28T09:00:00Z"}}`,
	},
	"delete_smtp_service": {
		AR:      "حذف خدمة SMTP بمعرّفها. لا تُحذف الافتراضية ما دامت هناك خدمات أخرى — اجعل غيرها الافتراضية أولًا (set_default_smtp_service).",
		Example: map[string]any{"id": "9d4c2e81-6a3f-4b07-8e1d-5c9a0f3b7e24"},
		Result:  `{"ok":true,"deleted":"9d4c2e81-6a3f-4b07-8e1d-5c9a0f3b7e24"}`,
	},
	"set_default_smtp_service": {
		AR: "جعل خدمة SMTP الافتراضية للبريد التشغيلي (التأكيدات وردود صندوق الوارد)، أو مع campaign=true الخدمة التي تستخدمها " +
			"الحملات حين لا تسمّي خدمة. تُعيد الخدمات.",
		Example: map[string]any{"id": "9d4c2e81-6a3f-4b07-8e1d-5c9a0f3b7e24", "campaign": true},
		Result: `{"count":2,"services":[` +
			`{"id":"9d4c2e81-6a3f-4b07-8e1d-5c9a0f3b7e24","name":"Main","host":"smtp.mail.example","port":587,"username":"sara@mail.example",` +
			`"has_password":true,"from_name":"Acme","from_email":"hello@acme.example","tls_mode":"starttls","is_default":true,` +
			`"is_campaign_default":true,"last_test_at":null,"last_test_ok":null,"last_test_error":"","campaigns":3,` +
			`"created_at":"2026-08-02T10:20:00Z","updated_at":"2026-09-28T09:00:00Z"},` +
			`{"id":"4a7e0c95-2d1b-4f38-9c6a-8e5d3b0f1a72","name":"Bulk","host":"smtp.bulk.example","port":465,"username":"acme",` +
			`"has_password":true,"from_name":"Acme News","from_email":"news@acme.example","tls_mode":"tls","is_default":false,` +
			`"is_campaign_default":false,"last_test_at":null,"last_test_ok":null,"last_test_error":"","campaigns":0,` +
			`"created_at":"2026-09-01T08:00:00Z","updated_at":"2026-09-28T09:00:00Z"}]}`,
	},
	"list_google_connections": {
		AR: "حسابات Google المتصلة بمساحة العمل، وما مُنح كل منها (GA4 وSearch Console ويوتيوب)، ومن وصله، وهل يحتاج إلى إعادة " +
			"الاتصال. وصل حساب جديد يتم من الصفحة: فـGoogle تسأل صاحب الحساب نفسه.",
		Example: map[string]any{},
		Result: `{"count":1,"connections":[{"id":"2f8a6c31-9d4e-4b07-a1c5-8e3b0d7f2a94","email":"sara@mail.example",` +
			`"products":["ga4","gsc"],"scopes":["https://www.googleapis.com/auth/analytics.readonly","https://www.googleapis.com/auth/webmasters"],` +
			`"revenue":false,"status":"","auth_error":"","connected_by":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47",` +
			`"connected_by_email":"sara@mail.example","created_at":"2026-08-05T11:00:00Z","updated_at":"2026-09-27T06:00:00Z"}]}`,
	},
	"disconnect_google_connection": {
		AR: "فصل حساب Google بمعرّفه (list_google_connections): تُلغى صلاحيته لدى Google ما لم يكن الحساب نفسه متصلًا في مكان آخر " +
			"من محرّك (shared=true)، وتتوقف مزامنة خصائصه ومواقعه وقنواته هنا.",
		Example: map[string]any{"id": "2f8a6c31-9d4e-4b07-a1c5-8e3b0d7f2a94"},
		Result:  `{"revoked":true,"shared":false}`,
	},
	"list_google_resources": {
		AR: "كل خاصية GA4 وموقع Search Console وقناة يوتيوب تراها الحسابات المتصلة، مفعّلة كانت أم لا، مع الحساب الذي تُقرأ عبره. " +
			"أما google_resources فتسرد المفعّلة فقط. ومع rediscover=true تُسأل Google من جديد أولًا.",
		Example: map[string]any{"rediscover": false},
		Result: `{"count":2,"resources":[{"id":"6b1d9f42-3c8a-4e75-b0d6-2a9e4f1c7b38","connection_id":"2f8a6c31-9d4e-4b07-a1c5-8e3b0d7f2a94",` +
			`"connection_email":"sara@mail.example","kind":"ga4_property","external_id":"properties/312345678","display_name":"acme.example",` +
			`"parent_name":"Acme","details":{},"available":true,"enabled":true,"auto_enabled":true,"last_synced_at":"2026-09-28T06:00:00Z"},` +
			`{"id":"a3c85e17-4f2d-4b90-8d6e-1b7f0c9a2e46","connection_id":"2f8a6c31-9d4e-4b07-a1c5-8e3b0d7f2a94",` +
			`"connection_email":"sara@mail.example","kind":"gsc_site","external_id":"sc-domain:acme.example","display_name":"acme.example",` +
			`"parent_name":"","details":{},"available":true,"enabled":false,"auto_enabled":false,"last_synced_at":null}]}`,
	},
	"set_google_resource": {
		AR:      "تشغيل خاصية GA4 أو موقع Search Console أو قناة يوتيوب في مساحة العمل هذه أو إيقافها، بمعرّفها (list_google_resources). التشغيل يبدأ مزامنتها.",
		Example: map[string]any{"id": "6b1d9f42-3c8a-4e75-b0d6-2a9e4f1c7b38", "enabled": true},
		Result: `{"resource":{"id":"6b1d9f42-3c8a-4e75-b0d6-2a9e4f1c7b38","kind":"ga4_property","external_id":"properties/312345678",` +
			`"connection_id":"2f8a6c31-9d4e-4b07-a1c5-8e3b0d7f2a94","display_name":"acme.example","parent_name":"Acme","details":{},` +
			`"available":true,"enabled":true,"auto_enabled":false,"last_synced_at":null}}`,
	},
	"set_audit_retention": {
		AR:      "مدة الاحتفاظ بسجل التدقيق بالأيام (من 30 إلى 3650). تُحذف الإدخالات الأقدم. والتغيير نفسه يُسجَّل.",
		Example: map[string]any{"days": 365},
		Result:  `{"retention_days":365}`,
	},
}

// argsARSettings is Arabic for the settings tools' argument descriptions.
var argsARSettings = map[string]string{
	"The new name": "الاسم الجديد",
	"The image, base64-encoded (no data: prefix)":                      "الصورة بترميز base64 (بدون البادئة data:)",
	"Their email address; they accept with an account on this address": "بريده الإلكتروني؛ يقبل الدعوة بحساب على هذا العنوان",
	"Invitation id":                                            "معرّف الدعوة",
	"The member's user id or email":                            "معرّف مستخدم العضو أو بريده",
	"A name to tell services apart":                            "اسم للتمييز بين الخدمات",
	"SMTP host, e.g. smtp.gmail.com":                           "مضيف SMTP، مثل smtp.gmail.com",
	"Usually 587 (starttls) or 465 (tls)":                      "عادةً 587 (starttls) أو 465 (tls)",
	"Write-only. Omit to keep the stored one; empty clears it": "للكتابة فقط. احذفها لإبقاء المحفوظة، واتركها فارغة لمسحها",
	"The sender's name":                                        "اسم المرسِل",
	"The sender's address":                                     "عنوان المرسِل",
	"Defaults to starttls":                                     "القيمة الافتراضية starttls",
	"SMTP service id":                                          "معرّف خدمة SMTP",
	"true: the campaign default; false or omitted: the transactional default": "true: افتراضية الحملات؛ false أو بدون قيمة: افتراضية البريد التشغيلي",
	"Connection id": "معرّف الاتصال",
	"Ask Google again what every connected account can see": "اسأل Google من جديد عمّا تراه كل الحسابات المتصلة",
	"Resource id": "معرّف المورد",
	"30 to 3650":  "من 30 إلى 3650",
}
