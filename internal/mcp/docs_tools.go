package mcp

// Docs for the automation, research, Google and audit tools
// (user_tools_automation.go, user_tools_research.go, user_tools_google.go,
// user_tools_audit.go). See docs.go.

var docsAutomation = map[string]ToolDoc{
	"list_browsers": {
		AR: "المتصفّحات المقترنة بمساحة العمل هذه: id واسم ونظام التشغيل وهل هو متصل الآن (online) والمواقع المسموح بقيادته " +
			"إليها وآخر ظهور له.",
		Example: map[string]any{},
		Result: `{"count":2,"items":[{"id":"3a9c1e57-2b4d-4f60-8e1a-7c5d9b2f0e14","name":"Sara's Chrome","platform":"chrome-extension",` +
			`"online":true,"allowed_hosts":["acme.example","crm.acme.example"],"last_seen_at":"2026-09-24T08:00:00Z"},` +
			`{"id":"b81f0d26-9e3a-4c75-a2d8-4f6e1c0b93a7","name":"Office laptop","platform":"chrome-extension","online":false,` +
			`"allowed_hosts":[],"last_seen_at":null}]}`,
	},
	"start_browser_run": {
		AR: "يقود متصفّحًا مقترنًا عبر سلسلة من الخطوات، كل خطوة منها على الشكل {command, args}. " +
			"تُرفض أي خطوة تنقُّل ما لم يكن مضيفها ضمن قائمة المواقع المسموح بها (allow-list) لذلك المتصفّح. " +
			"تُعيد run_id؛ اقرأ نتيجته لاحقًا عبر get_browser_run.",
		Example: map[string]any{
			"browser_id": "3a9c1e57-2b4d-4f60-8e1a-7c5d9b2f0e14",
			"steps": []any{
				map[string]any{"command": "goto", "args": map[string]any{"url": "https://acme.example/pricing"}},
				map[string]any{"command": "extract", "args": map[string]any{"selector": "h1"}},
			},
		},
		Result: `{"ok":true,"run_id":"6d2e8f41-7a3b-4c19-9e05-b1f7a4c2d680"}`,
	},
	"get_browser_run": {
		AR: "تشغيل واحد: حالته، وخطؤه إن وُجد، وstep_count وok_count، والملاحظات المسجَّلة قبل أول خطوة، وكل خطوة " +
			"(position وcommand وstatus وsummary وresult كما أرسلها المتصفّح وerror).",
		Example: map[string]any{"run_id": "6d2e8f41-7a3b-4c19-9e05-b1f7a4c2d680"},
		Result: `{"run_id":"6d2e8f41-7a3b-4c19-9e05-b1f7a4c2d680","status":"ok","error":"","step_count":2,"ok_count":2,` +
			`"notes":["Added acme.example to Sara's Chrome's allowed sites"],"steps":[` +
			`{"position":1,"command":"goto","status":"ok","summary":"Pricing — Acme","error":"",` +
			`"result":"{\"ok\":true,\"command\":\"goto\",\"ms\":1240,\"url\":\"https://acme.example/pricing\",\"title\":\"Pricing — Acme\",\"summary\":\"Pricing — Acme\"}"},` +
			`{"position":2,"command":"extract","status":"ok","summary":"Plans for every team","error":"",` +
			`"result":"{\"ok\":true,\"command\":\"extract\",\"ms\":85,\"value\":\"Plans for every team\",\"summary\":\"Plans for every team\"}"}]}`,
	},
	"list_workflows": {
		AR:      "سير العمل المحفوظة: id واسمها ووصفها وعدد مرّات تشغيلها، والقيم التي يطلبها كلٌّ منها (name وrequired وsecret).",
		Example: map[string]any{},
		Result: `{"count":2,"items":[{"id":"f4a7c2e9-1d38-4b6a-9c50-2e8b7d1f4a63","name":"Look up a company on the directory",` +
			`"description":"Searches the trade directory for a company and reads its listing.","run_count":14,` +
			`"params":[{"name":"company","required":true,"secret":false}]},` +
			`{"id":"0c5b9e14-6f27-4a83-b1d9-8e3a2c7f5d40","name":"Download the monthly invoice","description":"","run_count":3,` +
			`"params":[{"name":"month","required":true,"secret":false},{"name":"password","required":false,"secret":true}]}]}`,
	},
	"start_workflow": {
		AR: "يشغّل سير عمل محفوظًا على متصفّح مقترن، مع تزويده بقيم العناصر النائبة فيه. سير العمل الذي ينشر أو يرسل أو يقدّم " +
			"يحتاج confirm: true — بدونه يعيد needs_confirm: true ولا يشغّل شيئًا.",
		Example: map[string]any{
			"workflow_id": "f4a7c2e9-1d38-4b6a-9c50-2e8b7d1f4a63",
			"browser_id":  "3a9c1e57-2b4d-4f60-8e1a-7c5d9b2f0e14",
			"values":      map[string]any{"company": "Acme Trading"},
		},
		Result: `{"ok":true,"run_id":"9b3f6a20-4e81-4d7c-a5f2-3c0e9d8b1a76"}`,
	},
	"browser_allowed_sites": {
		AR: "يعرض المواقع التي يحق لمتصفّح مقترن قيادتها (قائمته المسموحة). تُحتسب النطاقات الفرعية تلقائيًا: " +
			"google.com تغطي news.google.com. قائمة فارغة تعني أن المتصفّح لا يقود أي موقع.",
		Example: map[string]any{"agent": "Sara's Chrome"},
		Result:  "Sara's Chrome (3a9c1e57-2b4d-4f60-8e1a-7c5d9b2f0e14) may drive 2 sites:\n- acme.example\n- crm.acme.example",
	},
	"browser_set_allowed_sites": {
		AR: "يغيّر المواقع التي يحق لمتصفّح مقترن قيادتها. استخدم add و/أو remove لتعديل القائمة، أو replace لضبطها بالكامل. " +
			"أسماء مضيفين فقط (acme.example) وليست روابط أو مسارات كاملة. يُدفَع التغيير إلى المتصفّح فورًا ويُسجَّل في سجل التدقيق.",
		Example: map[string]any{"agent": "Sara's Chrome", "add": []any{"crm.acme.example"}},
		Result:  "Sara's Chrome now allows 2 sites.\nAdded: crm.acme.example",
	},
	"browser_plugins": {
		AR: "يسرد سير عمل المتصفّح المحفوظة في مساحة العمل (الإضافات)، بما فيها المضافة من نماذج جاهزة أو المسجَّلة من الإضافة: " +
			"المعرّف المختصر (slug)، وما يؤديه كلٌّ منها، والمواقع التي يلمسها، والمدخلات التي يطلبها، وهل ينشر. " +
			"تُدرَج النماذج الجاهزة غير المضافة بعد آخر القائمة. شغّل أحدها بـ browser_plugin_run.",
		Example: map[string]any{},
		Result: "1 workflow(s):\n\n- Check green-coffee supplier prices (slug check-supplier-prices, v1, 3 steps)\n" +
			"    Opens the importer's price page, waits for the table, and reads the current price per kg.\n" +
			"    sites: importer.example\n\n" +
			"Presets not added to this workspace yet (2), added on the Presets gallery:\n" +
			"- linkedin-post-job — Post a job opening on LinkedIn [writes] (sites: linkedin.com)\n" +
			"- google-trends-daily — Read today's trending searches (sites: trends.google.com)\n",
	},
	"browser_plugin_run": {
		AR: "يشغّل سير عمل محفوظًا بمعرّفه المختصر (أو معرّفه) على متصفّح مقترن، مع قيم لمدخلاته. " +
			"سير العمل الذي ينشر أو يرسل أو يقدّم يحتاج confirm: true — شغّل نسخته التجريبية (‎-dryrun‎) أولًا إن وُجدت. " +
			"ينتظر حتى نحو 25 ثانية ثم يجيب بالبيانات، أو بمفتاح التشغيل لتستطلعه لاحقًا عبر browser_result إن كان لا يزال جاريًا (MH-478).",
		Example: map[string]any{"plugin": "check-supplier-prices", "agent": "Sara's Chrome", "confirm": false},
		Result: "Run d4a1e9f7-3c58-4b02-9e6a-1f7b2c9d3e84 — ok (3/3 steps ok)\n" +
			"\nSteps:\n" +
			"  1. navigate — ok (1180ms) https://importer.example/prices\n" +
			"  2. wait_for — ok (340ms) .price-table\n" +
			"  3. extract — ok (95ms) 3 row(s)\n" +
			"\nExtracted data (exactly as the browser sent it):\n\n" +
			`step 3: [{"item":"Ethiopia Yirgacheffe","price":"$6.40/kg"},{"item":"Colombia Huila","price":"$5.90/kg"},{"item":"Brazil Cerrado","price":"$4.75/kg"}]` + "\n",
	},
	"browser_plugin_save": {
		AR: "ينشئ سير عمل متصفّح محفوظًا (إضافة) بمعرّفه المختصر أو يُحدِّثه. تستخدم الخطوات الأوامر نفسها التي يستخدمها start_browser_run؛ " +
			"تُملأ العناصر النائبة {{name}} من المدخلات عند التشغيل. تغييره يُبقي التعريف السابق كنسخة. " +
			"hosts يسجّل المواقع التي يذهب إليها فقط — لا يسمح بها على أي متصفّح (browser_set_allowed_sites يفعل ذلك، ولمشرف فقط).",
		Example: map[string]any{
			"slug":        "check-supplier-prices",
			"name":        "Check green-coffee supplier prices",
			"description": "Opens the importer's price page and reads the current price per kg. Selectors verified 2026-09-23.",
			"hosts":       []any{"importer.example"},
			"steps": []any{
				map[string]any{"command": "goto", "args": map[string]any{"url": "https://importer.example/prices"}},
				map[string]any{"command": "extract", "args": map[string]any{"selector": ".price-table"}},
			},
			"writes":  false,
			"enabled": true,
		},
		Result: "Created check-supplier-prices (v1, 2 steps).",
	},
}

var docsResearch = map[string]ToolDoc{
	"start_research": {
		AR: "يبحث في الويب عن سؤال، ويقرأ أبرز النتائج في متصفّح حقيقي، ويستخرج منها الأدلة (اقتباسات، والشركات التي تسمّيها، " +
			"والإشارة التي تُبلِّغ عنها) ويلخّصها عند ضبط نموذج. أو مرّر mode=urls مع قائمة روابط لقراءة تلك الصفحات بدل البحث عنها. " +
			"يُعيد معرّف التشغيل فورًا لأن البحث يستغرق دقائق، ثم تقرأ النتيجة لاحقًا عبر get_research.",
		Example: map[string]any{"topic": "What are the VAT registration thresholds for small businesses in Egypt in 2026?"},
		Result: "Research started. Run id: 2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75\n" +
			"Read it back with get_research once it has finished.",
	},
	"list_research": {
		AR:      "عمليات البحث السابقة في مساحة العمل هذه، من الأحدث إلى الأقدم، مع حالة كلٍّ منها وعدد المصادر التي عثرت عليها وقرأتها.",
		Example: map[string]any{"limit": 5},
		Result: "- [2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75] What are the VAT registration thresholds for small businesses in Egypt in 2026? — ok, 6 found / 5 read\n" +
			"- [8a1d4f60-3e27-4b95-c0d8-7f2e6a9b1c34] Best CRM tools for Arabic-speaking sales teams — ok, 6 found / 4 read (no model is configured, so the pages were collected but not summarised)\n",
	},
	"get_research": {
		AR: "عملية بحث واحدة: إجابتها وكل مصدر استندت إليه مع روابطه. " +
			"تُدرج المصادر حتى تلك التي تعذّرت قراءتها، فتظهر الإجابة الضعيفة ضعيفةً بوضوح.",
		Example: map[string]any{"run_id": "2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75", "full_text": false},
		Result: "Question: What are the VAT registration thresholds for small businesses in Egypt in 2026?\n" +
			"Status: ok\n" +
			"\nBusinesses must register for VAT once their annual turnover reaches EGP 500,000 [1]. " +
			"Some professional services must register regardless of turnover [2].\n" +
			"\n(summarised by anthropic)\n" +
			"\nSources (6 found, 5 read):\n" +
			"[1] VAT registration guide\n    https://tax.example/vat/registration\n" +
			"[2] Professional services and VAT\n    https://advisors.example/blog/vat-services\n" +
			"[3] VAT FAQ\n    https://news.example/vat-faq\n    not read: the page took too long to load\n" +
			"    snippet: Answers to the most common questions about VAT registration…\n",
	},
}

var docsGoogle = map[string]ToolDoc{
	"google_resources": {
		AR:      "خصائص GA4 ومواقع Search Console وقنوات يوتيوب التي تقرأها مساحة العمل هذه (المفعَّلة منها)، مع معرّفاتها وآخر مزامنة وأي خطأ.",
		Example: map[string]any{},
		Result: `{"count":2,"items":[{"id":"5e1c8a37-9b24-4f06-8d3a-2c7f1e9b4a60","kind":"GA4 property","name":"Acme website",` +
			`"external_id":"properties/412345678","last_synced_at":"2026-09-24T06:00:00Z","needs_reconnecting":false,"last_error":""},` +
			`{"id":"a3f90d12-6c4e-4b87-9e15-7d2b8c0f3e41","kind":"Search Console site","name":"acme.example",` +
			`"external_id":"sc-domain:acme.example","last_synced_at":"2026-09-24T05:30:00Z","needs_reconnecting":false,"last_error":""}]}`,
	},
	"ga4_report": {
		AR: "بيانات GA4 لخاصية مفعَّلة على مدى 7 أو 28 أو 90 يومًا (تنتهي أمس، بتوقيت الخاصية): المستخدمون والجلسات والمشاهدات والتفاعل، " +
			"والاتجاه اليومي، وأبرز الصفحات، والمصدر / الوسيط. تُخزَّن النتيجة مؤقتًا لمدة ساعة.",
		Example: map[string]any{"resource": "5e1c8a37-9b24-4f06-8d3a-2c7f1e9b4a60", "days": 7},
		Result: `{"resource":{"id":"5e1c8a37-9b24-4f06-8d3a-2c7f1e9b4a60","name":"Acme website","external_id":"properties/412345678"},` +
			`"start_date":"2026-09-17","end_date":"2026-09-23","timezone":"Africa/Cairo","stale":false,"note":"",` +
			`"fetched_at":"2026-09-24T08:12:00Z","empty":false,"totals":{"users":3184,"new_users":2410,"sessions":4352,` +
			`"views":11906,"engagement_rate":0.614,"avg_engagement_per_user_sec":74},` +
			`"daily":[{"date":"2026-09-17","users":452,"sessions":610,"views":1702}],` +
			`"top_pages":[{"page":"/","views":3412,"users":1980},{"page":"/pricing","views":1207,"users":846}],` +
			`"sources":[{"source":"google","medium":"organic","sessions":2118,"users":1562}]}`,
	},
	"gsc_performance": {
		AR: "بيانات Search Console لموقع مفعَّل على مدى 7 أو 28 أو 90 يومًا تنتهي بأحدث يوم تتوفر له بيانات (تتأخر يومين إلى ثلاثة): " +
			"النقرات ومرات الظهور ونسبة النقر (CTR) ومتوسط الترتيب، مع مقارنة اختيارية بالفترة السابقة، وأبرز طلبات البحث والصفحات. " +
			"عوامل التصفية: query وpage (تحتوي على)، وcountry (رمز alpha-3، مثل egy)، وdevice (DESKTOP أو MOBILE أو TABLET).",
		Example: map[string]any{"resource": "a3f90d12-6c4e-4b87-9e15-7d2b8c0f3e41", "days": 28, "compare": true, "country": "egy", "device": "MOBILE"},
		Result: `{"site":"sc-domain:acme.example","nothing_yet":false,"start_date":"2026-08-24","end_date":"2026-09-20",` +
			`"imported_days":28,"basis":"position","missing_days":0,"empty":false,` +
			`"totals":{"clicks":1846,"impressions":72310,"ctr":0.0255,"position":14.2},` +
			`"previous":{"start_date":"2026-07-27","end_date":"2026-08-23","missing_days":0,"clicks":1512,"impressions":64980,"ctr":0.0233,"position":15.8},` +
			`"top_queries":[{"query":"acme trading","clicks":412,"impressions":1930,"position":1.3}],` +
			`"top_pages":[{"page":"https://acme.example/","clicks":688,"impressions":9840,"position":4.1}]}`,
	},
	"gsc_inspections": {
		AR: "أحدث عمليات فحص عناوين URL لموقع مفعَّل، وميزانية الفحص لليوم " +
			"(2,000 فحص يوميًا لكل موقع، تتقاسمها كل مساحات العمل التي تقرأه).",
		Example: map[string]any{"resource": "a3f90d12-6c4e-4b87-9e15-7d2b8c0f3e41"},
		Result: `{"site":"sc-domain:acme.example","quota_used_today":14,"quota_limit":2000,"count":2,"items":[` +
			`{"url":"https://acme.example/pricing","verdict":"PASS","coverage":"Submitted and indexed","inspected_at":"2026-09-24T07:41:00Z"},` +
			`{"url":"https://acme.example/blog/new-warehouse","verdict":"NEUTRAL","coverage":"Discovered - currently not indexed","inspected_at":"2026-09-23T16:05:00Z"}]}`,
	},
	"gsc_inspect_url": {
		AR: "يسأل أداة فحص عنوان URL من Google عن رؤيتها لصفحة واحدة من موقع مفعَّل: هل هي مفهرسة أم لا، والتغطية، وآخر زحف، والعنوان الأساسي (canonical). " +
			"يستهلك فحصًا واحدًا من الفحوص اليومية للموقع البالغة 2,000.",
		Example: map[string]any{"resource": "a3f90d12-6c4e-4b87-9e15-7d2b8c0f3e41", "url": "https://acme.example/pricing"},
		Result: `{"inspection":{"url":"https://acme.example/pricing","verdict":"PASS","coverage":"Submitted and indexed",` +
			`"last_crawled_at":"2026-09-22T03:18:00Z","fetch":"SUCCESSFUL","robots_txt":"ALLOWED",` +
			`"google_canonical":"https://acme.example/pricing","declared_canonical":"https://acme.example/pricing",` +
			`"inspect_url":"https://search.google.com/search-console/inspect?resource_id=sc-domain:acme.example&id=Qm9vZ2xl"}}`,
	},
	"gsc_sitemaps": {
		AR:      "خرائط الموقع الخاصة بموقع مفعَّل كما تعرضها Google الآن.",
		Example: map[string]any{"resource": "a3f90d12-6c4e-4b87-9e15-7d2b8c0f3e41"},
		Result: `{"site":"sc-domain:acme.example","count":2,"items":[` +
			`{"path":"https://acme.example/sitemap.xml","status":"read","urls":214,"errors":0},` +
			`{"path":"https://acme.example/blog/sitemap.xml","status":"read","urls":58,"errors":2}]}`,
	},
	"gsc_submit_sitemap": {
		AR:      "يرسل خريطة موقع لموقع مفعَّل إلى Google، أو يطلب من Google قراءتها من جديد، ثم يعرض قائمة خرائط الموقع بعد الإرسال.",
		Example: map[string]any{"resource": "a3f90d12-6c4e-4b87-9e15-7d2b8c0f3e41", "url": "https://acme.example/sitemap.xml"},
		Result: `{"ok":true,"site":"sc-domain:acme.example","count":1,"items":[` +
			`{"path":"https://acme.example/sitemap.xml","status":"pending","urls":214,"errors":0}]}`,
	},
	"youtube_channel": {
		AR: "قناة يوتيوب مفعَّلة كما كانت في آخر مزامنة: المشتركون، وإجمالي المشاهدات، والفيديوهات المقروءة، وحالة كل خطوة من خطوات المزامنة، " +
			"وحصة YouTube المستهلكة اليوم.",
		Example: map[string]any{"resource": "c7d24e89-1a5f-4e30-b6c9-0f8e3d2a7b15"},
		Result: `{"synced":true,"channel":{"title":"Acme Trading","handle":"@acmetrading","external_id":"UCx7Qe2Lm9Rt4Vb1Nk8Ps3Hz",` +
			`"url":"https://www.youtube.com/@acmetrading","subscribers":12480,"lifetime_views":1906332,"uploads_read":142,` +
			`"uploads_public":131,"synced_at":"2026-09-23T22:10:00Z"},` +
			`"quota":{"day":"2026-09-23","workspace_used":318,"workspace_limit":2000,"global_used":1204,"global_limit":9000}}`,
	},
	"youtube_videos": {
		AR: "الفيديوهات المرفوعة على قناة مفعَّلة، من الأحدث إلى الأقدم، صفحة تلو الأخرى: total وcounts (لكل حالة خصوصية). " +
			"اختياريًا: q (في العنوان والوصف والوسوم)، وprivacy (public أو unlisted أو private أو scheduled)، وlimit (بحد أقصى 50)، " +
			"وcursor (من الصفحة السابقة).",
		Example: map[string]any{"resource": "c7d24e89-1a5f-4e30-b6c9-0f8e3d2a7b15", "q": "warehouse", "privacy": "public", "limit": 2},
		Result: `{"total":7,"counts":{"public":7,"unlisted":0,"private":0,"scheduled":0},"count":2,"items":[` +
			`{"id":"dQ3kP9xLm2A","title":"Inside our new Cairo warehouse","privacy":"public","published_at":"2026-09-18T00:00:00Z",` +
			`"views":4812,"likes":236,"comments":41},` +
			`{"id":"Vt7nR2cYb8E","title":"Warehouse safety in 90 seconds","privacy":"public","published_at":"2026-08-30T00:00:00Z",` +
			`"views":2105,"likes":null,"comments":null}],"next_cursor":"eyJwIjoiMjAyNi0wOC0zMCJ9"}`,
	},
	"youtube_video": {
		AR:      "فيديو واحد مرفوع على قناة مفعَّلة مع أبرز تعليقاته (يُعاد جلبها من يوتيوب إذا مضى عليها أكثر من ساعة وسمحت الحصة بذلك).",
		Example: map[string]any{"resource": "c7d24e89-1a5f-4e30-b6c9-0f8e3d2a7b15", "id": "dQ3kP9xLm2A"},
		Result: `{"video":{"id":"dQ3kP9xLm2A","title":"Inside our new Cairo warehouse","privacy":"public","views":4812,` +
			`"url":"https://www.youtube.com/watch?v=dQ3kP9xLm2A","comments":[` +
			`{"author":"Omar Hassan","answered":true,"likes":18,"reply_count":2,"text":"Do you deliver to Alexandria?",` +
			`"url":"https://www.youtube.com/watch?v=dQ3kP9xLm2A&lc=UgzK4r8Tn2Qm"},` +
			`{"author":"Layla N.","answered":false,"likes":9,"reply_count":0,"text":"Great tour, thanks!",` +
			`"url":"https://www.youtube.com/watch?v=dQ3kP9xLm2A&lc=UgwP7s1Vx9Bd"}]}}`,
	},
	"youtube_reports": {
		AR: "تقارير YouTube Reporting لقناة مفعَّلة: مهام التقارير اليومية والتقارير التي نُسخت حتى الآن (من الأحدث إلى الأقدم). " +
			"يتوفر ملف CSV لأي تقرير على GET /api/w/youtube/reports/{report_id}/csv.",
		Example: map[string]any{"resource": "c7d24e89-1a5f-4e30-b6c9-0f8e3d2a7b15", "limit": 2},
		Result: `{"checked_at":"2026-09-24T07:00:00Z","jobs":[` +
			`{"job":"channel_basic_a2","reports":21,"newest":"2026-09-22"},` +
			`{"job":"channel_device_os_a2","reports":0,"newest":null}],"count":2,"items":[` +
			`{"report_id":"8812345670123","job":"channel_basic_a2","date":"2026-09-22","rows":142},` +
			`{"report_id":"8812345670456","job":"channel_traffic_source_a2","date":"2026-09-22","rows":37}]}`,
	},
	"youtube_analytics": {
		AR: "بيانات YouTube Analytics لقناة مفعَّلة أو لأحد فيديوهاتها (video)، على مدى 7 أو 28 أو 90 يومًا تنتهي أمس (بتوقيت المحيط الهادئ): " +
			"المشاهدات، ووقت المشاهدة، والمشتركون، والإعجابات، والتعليقات، وأبرز الفيديوهات، ومصادر الزيارات. تُخزَّن النتيجة مؤقتًا لست ساعات.",
		Example: map[string]any{"resource": "c7d24e89-1a5f-4e30-b6c9-0f8e3d2a7b15", "days": 7},
		Result: `{"scope":"channel","start_date":"2026-09-17","end_date":"2026-09-23","stale":false,"note":"",` +
			`"fetched_at":"2026-09-24T07:55:00Z","empty":false,"totals":{"views":9420,"watch_minutes":36744,` +
			`"avg_view_duration_sec":234,"subscribers_gained":188,"subscribers_lost":21,"likes":704,"comments":96},` +
			`"daily":[{"date":"2026-09-17","views":1302}],` +
			`"top_videos":[{"id":"dQ3kP9xLm2A","title":"Inside our new Cairo warehouse","views":3120}],` +
			`"traffic":[{"source":"YT_SEARCH","views":3904},{"source":"SUBSCRIBER","views":2210}]}`,
	},
}

var docsAudit = map[string]ToolDoc{
	"audit_log": {
		AR: "سجل التدقيق لمساحة العمل، من الأحدث إلى الأقدم: كل عملية كتابة أجراها عضو أو رمز MCP أو عميل OAuth أو وكيل — " +
			"من أجراها (actor_kind وactor_id وactor_name)، وماذا تغيّر (action وentity_type وentity_id)، ومتى، ومن أين (ip وuser_agent)، " +
			"مع مقارنة قبل/بعد للحقول المتغيّرة تُحجب فيها الأسرار. يمكن التصفية حسب المنفّذ أو السجل أو الإجراء ونطاق زمني، " +
			"واتّباع next_cursor للوصول إلى الإدخالات الأقدم.",
		Example: map[string]any{"actor_kind": "token", "entity_type": "deals", "from": "2026-09-01", "to": "2026-09-24", "limit": 2},
		Result: `{"entries":[` +
			`{"id":"e1b7c3d9-4f28-4a60-9d15-6c8e2a0f7b43","occurred_at":"2026-09-23T14:02:11.482Z","kind":"change",` +
			`"request_id":"req_7f3a9c2e1b","actor_kind":"token","actor_id":"4d8e2f71-0c9b-4a36-b5e1-7a2c9f3d6e80","actor_name":"Sales agent",` +
			`"user_id":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47","via":"mcp","operation":"mcp:move_deal","action":"update",` +
			`"entity_type":"deals","entity_id":"9c4a1e6b-3d7f-4b28-8e05-2f1b7c9d4a36",` +
			`"changes":{"stage_id":{"before":"1f6b3d8a-5c29-4e70-9a14-8d2e7b0c5f36","after":"6e9a2c4f-0b71-4d58-b3e6-1c8f5a9d2b07"}},"status":0,"ip":"203.0.113.24","user_agent":"Claude-User"},` +
			`{"id":"b2f8d4a1-7e36-4c95-a0b3-5d9e1c7f2a68","occurred_at":"2026-09-22T09:47:30.105Z","kind":"change",` +
			`"request_id":"req_2c8e5a1f9d","actor_kind":"token","actor_id":"4d8e2f71-0c9b-4a36-b5e1-7a2c9f3d6e80","actor_name":"Sales agent",` +
			`"user_id":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47","via":"mcp","operation":"mcp:create_deal","action":"create",` +
			`"entity_type":"deals","entity_id":"3a7d9f2c-8b14-4e60-a5c1-0e6f4b2d8c97",` +
			`"changes":{"title":{"after":"Acme — annual supply"},"value_minor":{"after":4800000},"currency":{"after":"USD"}},"status":0,"ip":"203.0.113.24","user_agent":"Claude-User"}],` +
			`"next_cursor":"MjAyNi0wOS0yMlQwOTo0NzozMC4xMDVafGIyZjhkNGEx","total":17,"limit":2}`,
	},
}

var argsARTools = map[string]string{
	// automation
	"From list_browsers": "من list_browsers",
	`e.g. [{"command":"goto","args":{"url":"https://example.com"}},{"command":"extract","args":{"selector":"h1"}}]`: `مثال: [{"command":"goto","args":{"url":"https://example.com"}},{"command":"extract","args":{"selector":"h1"}}]`,
	// browser tools (MH-478)
	"browser name or id; omitted = the only paired one": "اسم المتصفّح أو معرّفه؛ إن تُرك فارغًا فيُقصد المتصفّح الوحيد المقترن",
	"the whole new list; overrides add/remove":          "القائمة الجديدة كاملة؛ تتجاوز add وremove",
	"slug or id":                             "المعرّف المختصر (slug) أو المعرّف",
	"input name -> value":                    "اسم المدخل ← قيمته",
	"required for a workflow that publishes": "مطلوب لسير عمل ينشر",
	"lowercase-with-dashes; the identity":    "حروف صغيرة وشرطات؛ هو الهوية",
	"what it does, its inputs, and the date its selectors were verified": "ما يؤديه، ومدخلاته، وتاريخ التحقق من مُحدِّداته",
	"it publishes, sends or submits; runs then need confirm":             "ينشر أو يرسل أو يقدّم؛ فتحتاج عمليات تشغيله إلى confirm",
	"lucide icon name":                       "اسم أيقونة من مكتبة Lucide",
	"From list_workflows":                    "من list_workflows",
	"Values for the workflow's placeholders": "قيم العناصر النائبة في سير العمل",

	// research
	"How many to return. Default 10.":                                       "عدد النتائج المطلوب إعادتها، والافتراضي 10.",
	"Include the fetched page text of each source. Large — off by default.": "تضمين نص الصفحة المجلوب لكل مصدر. حجمه كبير، لذا فهو معطَّل افتراضيًا.",
	"The id returned by start_research.":                                    "المعرّف الذي أعادته start_research.",

	// google
	"Window in days (default 28)":                                        "مدة النافذة بالأيام (الافتراضي 28)",
	"Resource id (from google_resources); default the first enabled one": "معرّف المورد (من google_resources)؛ ويُستخدم أول مورد مفعَّل افتراضيًا",
	"Also the same length of time just before":                           "والمدة المماثلة التي تسبقها مباشرةً أيضًا، للمقارنة",
	"ISO 3166-1 alpha-3, lower case":                                     "رمز ISO 3166-1 alpha-3 بأحرف صغيرة",
	"Page URL contains":                                                  "عنوان URL للصفحة يحتوي على",
	"Query contains":                                                     "طلب البحث يحتوي على",
	"An absolute URL under the site":                                     "عنوان URL كامل ضمن الموقع",
	"The sitemap's absolute URL, under the site":                         "عنوان URL الكامل لخريطة الموقع، ضمن الموقع",
	"next_cursor of the previous page":                                   "قيمة next_cursor من الصفحة السابقة",
	"At most this many (default 24, max 50)":                             "بحد أقصى هذا العدد (الافتراضي 24، والحد الأقصى 50)",
	"Search":                                                             "بحث",
	"The video id":                                                       "معرّف الفيديو",
	"At most this many reports (default 30, max 200)":                    "بحد أقصى هذا العدد من التقارير (الافتراضي 30، والحد الأقصى 200)",
	"A video id; empty for the channel":                                  "معرّف فيديو؛ اتركه فارغًا لبيانات القناة كلها",

	// audit
	"An actor id: a user id, token id, OAuth grant id or browser id":    "معرّف المنفّذ: معرّف مستخدم أو رمز أو تفويض OAuth أو متصفّح",
	"A table, e.g. contacts, deals, campaigns":                          "جدول، مثل contacts أو deals أو campaigns",
	"The id of one record":                                              "معرّف سجل واحد",
	"create, update, delete, or an operation such as mcp:send_campaign": "create أو update أو delete، أو عملية مثل mcp:send_campaign",
	"On or after this time (RFC 3339 or YYYY-MM-DD)":                    "في هذا الوقت أو بعده (بصيغة RFC 3339 أو YYYY-MM-DD)",
	"Before this time (RFC 3339 or YYYY-MM-DD)":                         "قبل هذا الوقت (بصيغة RFC 3339 أو YYYY-MM-DD)",
	"Page size (default 50, at most 200)":                               "حجم الصفحة (الافتراضي 50، وبحد أقصى 200)",
}
