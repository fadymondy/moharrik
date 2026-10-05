package mcp

// Docs for fadymondy.com's raw browser_* tools, ported into a workspace
// (user_tools_browser_run.go; MH-478 phase 4): browser_agents, browser_extract,
// browser_run, browser_result, browser_cancel. See docs.go.

var docsBrowserRun = map[string]ToolDoc{
	"browser_agents": {
		AR: "المتصفّحات المقترنة بهذه المساحة: اسم كل منها، وهل هو متصل الآن، وما يعمل عليه، وآخر مرة شوهد فيها، والمواقع التي يحق " +
			"له العمل عليها. هذه متصفّحات حقيقية، مسجَّلة دخولها بحسابات أصحابها.",
		Example: map[string]any{},
		Result: `{"count":2,"items":[` +
			`{"id":"3a9c1e57-2b4d-4f60-8e1a-7c5d9b2f0e14","name":"Sara's Chrome","platform":"macOS","extension_version":"0.5.0",` +
			`"status":"online","allowed_hosts":["acme.example","crm.acme.example"],"last_seen_at":"2026-09-24T10:20:00Z"},` +
			`{"id":"b81f0d26-9e3a-4c75-a2d8-4f6e1c0b93a7","name":"Office laptop","platform":"Windows","extension_version":"0.5.0",` +
			`"status":"offline","allowed_hosts":[],"last_seen_at":"2026-09-23T18:05:00Z"}]}`,
	},
	"browser_extract": {
		AR: "يفتح رابطًا في أحد متصفّحات مساحة العمل ويستخرج بيانات من الصفحة، مستخدمًا الجلسة التي سجّل المتصفّح دخوله فيها بالفعل. " +
			"مرِّر fields كخريطة اسم ← مُحدِّد CSS، وlist عندما تتكرّر الصفوف في الصفحة (يُقرأ كل حقل داخل كل صف حينها). يُعيد البيانات " +
			"المستخرَجة، أو مفتاح تشغيل تستطلعه إن كانت الصفحة بطيئة. يجب أن يكون موقع الرابط ضمن مواقع المتصفّح المسموحة. يقود متصفّحًا " +
			"حقيقيًا، مرئيًا على شاشة صاحبه أثناء عمله.",
		Example: map[string]any{
			"url":    "https://importer.example/prices",
			"list":   "table tbody tr",
			"fields": map[string]any{"item": "td:nth-child(1)", "price": "td:nth-child(2)"},
		},
		Result: `{"run_id":"6d2e8f41-7a3b-4c19-9e05-b1f7a4c2d680","status":"ok","error":"","step_count":2,"ok_count":2,"notes":[],"steps":[` +
			`{"position":0,"command":"navigate","status":"ok","summary":"https://importer.example/prices","error":"","duration_ms":1180},` +
			`{"position":1,"command":"extract","status":"ok","summary":"3 row(s)","error":"","duration_ms":95,` +
			`"result":"[{\"item\":\"Ethiopia Yirgacheffe\",\"price\":\"$6.40/kg\"},{\"item\":\"Colombia Huila\",\"price\":\"$5.90/kg\"},{\"item\":\"Brazil Cerrado\",\"price\":\"$4.75/kg\"}]"}],` +
			`"data":"step 2: [{\"item\":\"Ethiopia Yirgacheffe\",\"price\":\"$6.40/kg\"},{\"item\":\"Colombia Huila\",\"price\":\"$5.90/kg\"},{\"item\":\"Brazil Cerrado\",\"price\":\"$4.75/kg\"}]"}`,
	},
	"browser_run": {
		AR: "يشغّل سلسلة أوامر متصفّح صريحة على متصفّح من متصفّحات مساحة العمل. الأوامر: navigate وwait_for وclick وtype وpress وscroll " +
			"وselect وupload وextract وscreenshot وeval_readonly. كل خطوة على الشكل {\"command\": ..., \"args\": {...}}. فضِّل " +
			"browser_extract لـ\"افتح هذا واقرأ ذاك\" البسيط؛ استخدم هذه حين تحتاج البيانات تفاعلًا أولًا (تصفية، تقليب صفحات). يجب أن " +
			"يكون كل موقع تذهب إليه ضمن مواقع المتصفّح المسموحة.",
		Example: map[string]any{
			"steps": []any{
				map[string]any{"command": "navigate", "args": map[string]any{"url": "https://acme.example/pricing"}},
				map[string]any{"command": "click", "args": map[string]any{"selector": "text=Annual"}},
				map[string]any{"command": "extract", "args": map[string]any{"selector": "h1"}},
			},
		},
		Result: `{"run_id":"8b1c4e70-5a3d-4f92-b6e7-1d4a9c0f7b23","status":"ok","error":"","step_count":3,"ok_count":3,"notes":[],"steps":[` +
			`{"position":0,"command":"navigate","status":"ok","summary":"https://acme.example/pricing","error":"","duration_ms":1240},` +
			`{"position":1,"command":"click","status":"ok","summary":"text=Annual","error":"","duration_ms":310},` +
			`{"position":2,"command":"extract","status":"ok","summary":"\"Save 20% billed yearly\"","error":"","duration_ms":60,` +
			`"result":"\"Save 20% billed yearly\""}],` +
			`"data":"step 3: \"Save 20% billed yearly\""}`,
	},
	"browser_result": {
		AR:      "يجلب تشغيل متصفّح بمفتاحه: حالته، وكل خطوة مع توقيتها، وسبب التوقّف إن فشل، والبيانات المستخرَجة إن انتهى.",
		Example: map[string]any{"run": "6d2e8f41-7a3b-4c19-9e05-b1f7a4c2d680"},
		Result: `{"run_id":"6d2e8f41-7a3b-4c19-9e05-b1f7a4c2d680","status":"ok","error":"","step_count":2,"ok_count":2,"notes":[],"steps":[` +
			`{"position":0,"command":"navigate","status":"ok","summary":"https://importer.example/prices","error":"","duration_ms":1180},` +
			`{"position":1,"command":"extract","status":"ok","summary":"3 row(s)","error":"","duration_ms":95,` +
			`"result":"[{\"item\":\"Ethiopia Yirgacheffe\",\"price\":\"$6.40/kg\"},{\"item\":\"Colombia Huila\",\"price\":\"$5.90/kg\"},{\"item\":\"Brazil Cerrado\",\"price\":\"$4.75/kg\"}]"}],` +
			`"data":"step 2: [{\"item\":\"Ethiopia Yirgacheffe\",\"price\":\"$6.40/kg\"},{\"item\":\"Colombia Huila\",\"price\":\"$5.90/kg\"},{\"item\":\"Brazil Cerrado\",\"price\":\"$4.75/kg\"}]"}`,
	},
	"browser_cancel": {
		AR:      "يوقف تشغيل متصفّح. يتوقّف بعد الخطوة الجارية حاليًا.",
		Example: map[string]any{"run": "8b1c4e70-5a3d-4f92-b6e7-1d4a9c0f7b23"},
		Result:  `{"ok":true,"cancelled":true,"run_id":"8b1c4e70-5a3d-4f92-b6e7-1d4a9c0f7b23"}`,
	},
}

// argsARBrowserRun is Arabic for the raw browser-run tools' argument
// descriptions. Their own "agent" text differs, character for character, from
// user_tools_browser.go's ("Browser name or id. Omit to use the only
// connected one." vs "browser name or id; omitted = the only paired one"),
// so it needs its own entry here rather than reusing argsARTools'.
var argsARBrowserRun = map[string]string{
	"Browser name or id. Omit to use the only connected one.":                         "اسم المتصفّح أو معرّفه. اتركه فارغًا لاستخدام المتصفّح المتصل الوحيد",
	"CSS selector for repeating rows, e.g. 'table tbody tr'":                          "مُحدِّد CSS للصفوف المتكرّرة، مثل 'table tbody tr'",
	"name -> CSS selector. Append '|@href' to read an attribute, '|html' for markup.": "اسم ← مُحدِّد CSS. أضِف '|@href' لقراءة خاصية، أو '|html' لقراءة الترميز",
	"Selector to wait for before extracting.":                                         "المُحدِّد الذي يُنتظر قبل الاستخراج",
	"[{command, args, continue_on_error, timeout_seconds}]":                           "كل عنصر على الشكل {command, args, continue_on_error, timeout_seconds}",
	"From list_workflows (an id or a slug)": "من list_workflows (معرّف أو slug)",
	"Which browser to run on; defaults to the one the original ran on": "المتصفّح الذي يجري عليه التشغيل؛ الافتراضي هو الذي جرى عليه الأصل",
	"Required for a workflow that publishes, sends or submits": "مطلوب لسير عمل ينشر أو يرسل أو يقدّم",
	"The run to repeat, from get_browser_run": "التشغيل المراد تكراره، من get_browser_run",
}
