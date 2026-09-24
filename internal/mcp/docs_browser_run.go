package mcp

// Docs for fadymondy.com's raw browser_* tools, ported into a workspace
// (user_tools_browser_run.go; MH-478 phase 4): browser_agents, browser_extract,
// browser_run, browser_result, browser_cancel. See docs.go.

var docsBrowserRun = map[string]ToolDoc{
	"browser_agents": {
		AR: "المتصفّحات المقترنة بهذه المساحة: اسم كل منها، وهل هو متصل الآن، وما يعمل عليه، وآخر مرة شوهد فيها، والمواقع التي يحق " +
			"له العمل عليها. هذه متصفّحات حقيقية، مسجَّلة دخولها بحسابات أصحابها.",
		Example: map[string]any{},
		Result: "2 paired browser(s):\n\n" +
			"- Sara's Chrome (3a9c1e57-2b4d-4f60-8e1a-7c5d9b2f0e14) — online, macOS, extension 0.5.0, last seen 2026-09-24T10:20:00Z\n" +
			"    may be driven on: acme.example, crm.acme.example\n" +
			"- Office laptop (b81f0d26-9e3a-4c75-a2d8-4f6e1c0b93a7) — offline, Windows, extension 0.5.0, last seen 2026-09-23T18:05:00Z\n" +
			"    may be driven on: nothing yet — an admin adds sites (browser_set_allowed_sites, or the Automation page)\n",
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
		Result: "Run 6d2e8f41-7a3b-4c19-9e05-b1f7a4c2d680 — ok (2/2 steps ok)\n" +
			"\nSteps:\n" +
			"  1. navigate — ok (1180ms) https://importer.example/prices\n" +
			"  2. extract — ok (95ms) 3 row(s)\n" +
			"\nExtracted data (exactly as the browser sent it):\n\n" +
			`step 2: [{"item":"Ethiopia Yirgacheffe","price":"$6.40/kg"},{"item":"Colombia Huila","price":"$5.90/kg"},{"item":"Brazil Cerrado","price":"$4.75/kg"}]` + "\n",
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
		Result: "Run 8b1c4e70-5a3d-4f92-b6e7-1d4a9c0f7b23 — ok (3/3 steps ok)\n" +
			"\nSteps:\n" +
			"  1. navigate — ok (1240ms) https://acme.example/pricing\n" +
			"  2. click — ok (310ms) text=Annual\n" +
			"  3. extract — ok (60ms) \"Save 20% billed yearly\"\n" +
			"\nExtracted data (exactly as the browser sent it):\n\n" +
			`step 3: "Save 20% billed yearly"` + "\n",
	},
	"browser_result": {
		AR:      "يجلب تشغيل متصفّح بمفتاحه: حالته، وكل خطوة مع توقيتها، وسبب التوقّف إن فشل، والبيانات المستخرَجة إن انتهى.",
		Example: map[string]any{"run": "6d2e8f41-7a3b-4c19-9e05-b1f7a4c2d680"},
		Result: "Run 6d2e8f41-7a3b-4c19-9e05-b1f7a4c2d680 — ok (2/2 steps ok)\n" +
			"\nSteps:\n" +
			"  1. navigate — ok (1180ms) https://importer.example/prices\n" +
			"  2. extract — ok (95ms) 3 row(s)\n" +
			"\nExtracted data (exactly as the browser sent it):\n\n" +
			`step 2: [{"item":"Ethiopia Yirgacheffe","price":"$6.40/kg"},{"item":"Colombia Huila","price":"$5.90/kg"},{"item":"Brazil Cerrado","price":"$4.75/kg"}]` + "\n",
	},
	"browser_cancel": {
		AR:      "يوقف تشغيل متصفّح. يتوقّف بعد الخطوة الجارية حاليًا.",
		Example: map[string]any{"run": "8b1c4e70-5a3d-4f92-b6e7-1d4a9c0f7b23"},
		Result:  "Run 8b1c4e70-5a3d-4f92-b6e7-1d4a9c0f7b23 is cancelled; it stops after the step in flight.",
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
}
