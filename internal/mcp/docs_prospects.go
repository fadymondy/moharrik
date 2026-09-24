package mcp

// Docs for the research-schedule, evidence, suggestion and prospect tools
// (user_tools_research.go, user_tools_prospects.go; MH-388, MH-397). See docs.go.

var docsProspects = map[string]ToolDoc{
	"find_evidence": {
		Group: "research",
		AR: "البحث في كل الأدلة التي جمعتها عمليات البحث في مساحة العمل، عبر عمليات البحث كلها معًا: اقتباسات حرفية (وادعاءات من " +
			"سطر واحد حين يستخرجها نموذج) مع رابط مصدرها، والشركة المذكورة، والإشارة المُبلَّغ عنها، وتاريخها. يمكن التصفية بالنص q، " +
			"وبالشركة company (اسمًا أو نطاقًا)، وsignal_type، وdomain المصدر، وrun_id، وبمدى زمني عبر since وuntil. تُعاد النتيجة بصيغة JSON.",
		Example: map[string]any{"company": "acme.example", "signal_type": "funding", "limit": 2},
		Result: `{"count":1,"evidence":[{"id":"a4c8e0f2-1b3d-4e6a-9f7c-5d0e2a4c8e60","run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75",` +
			`"run_topic":"logistics companies raises funding Egypt","source_id":"b7e0f2c4-3d5f-4a80-9c1e-7b3d5f7a9c10","kind":"claim",` +
			`"quote":"Acme Freight raised a $4M seed round led by Delta Ventures to expand its Cairo warehouse network.",` +
			`"claim":"Acme Freight raised $4M in seed funding.","source_url":"https://techcrunch.example/acme-freight-seed",` +
			`"source_title":"Acme Freight raises $4M seed round","source_domain":"techcrunch.example","company_name":"Acme Freight",` +
			`"company_domain":"acme.example","company_key":"acme freight|acme.example","signal_type":"funding","confidence":0.9,` +
			`"published_at":"2026-09-10T00:00:00Z","captured_at":"2026-09-20T11:05:00Z","extractor":"model:anthropic"}],` +
			`"limit":2,"offset":0,"total":1}`,
	},
	"list_company_suggestions": {
		Group: "research",
		AR: "الشركات التي عثر عليها البحث في أدلته، بانتظار مراجعة شخص لها — لا تُضاف إلى إدارة العملاء من تلقاء نفسها أبدًا. " +
			"تحمل كل شركة أقوى أدلتها. الحالات status: suggested (الافتراضي) وaccepted وdismissed وlinked (موجودة في إدارة العملاء أصلًا).",
		Example: map[string]any{"status": "suggested", "limit": 2},
		Result: `{"suggestions":[{"id":"c1d3e5f7-9b1d-4a3c-8e5f-7b9d1c3e5f70","company_key":"acme freight|acme.example",` +
			`"name":"Acme Freight","domain":"acme.example","status":"suggested","crm_company_id":"","evidence_count":3,"strongest":0.9,` +
			`"signal_types":["expansion","funding"],"first_run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75",` +
			`"last_run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75","first_seen_at":"2026-09-18T09:00:00Z","last_seen_at":"2026-09-20T11:05:00Z",` +
			`"reviewed_at":null,"evidence":[{"id":"a4c8e0f2-1b3d-4e6a-9f7c-5d0e2a4c8e60","run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75",` +
			`"run_topic":"logistics companies raises funding Egypt","source_id":"b7e0f2c4-3d5f-4a80-9c1e-7b3d5f7a9c10","kind":"claim",` +
			`"quote":"Acme Freight raised a $4M seed round led by Delta Ventures.","claim":"Acme Freight raised $4M in seed funding.",` +
			`"source_url":"https://techcrunch.example/acme-freight-seed","source_title":"Acme Freight raises $4M seed round",` +
			`"source_domain":"techcrunch.example","company_name":"Acme Freight","company_domain":"acme.example",` +
			`"company_key":"acme freight|acme.example","signal_type":"funding","confidence":0.9,"published_at":"2026-09-10T00:00:00Z",` +
			`"captured_at":"2026-09-20T11:05:00Z","extractor":"model:anthropic"}]}],"total":1}`,
	},
	"accept_company_suggestion": {
		Group: "research",
		AR: "إضافة شركة اقترحها البحث إلى إدارة العملاء — أو ربطها بالشركة الموجودة أصلًا التي تملك نطاقها أو اسمها — ووضع أدلتها " +
			"في سجل الشركة الزمني كملاحظة. name وdomain يصحّحان ما قرأه البحث إن أخطأ.",
		Example: map[string]any{"id": "c1d3e5f7-9b1d-4a3c-8e5f-7b9d1c3e5f70", "domain": "acme.example"},
		Result: `{"suggestion":{"id":"c1d3e5f7-9b1d-4a3c-8e5f-7b9d1c3e5f70","company_key":"acme freight|acme.example",` +
			`"name":"Acme Freight","domain":"acme.example","status":"accepted","crm_company_id":"e0a2c4e6-8a0c-4e2a-b4c6-e8a0c2e4a6c8",` +
			`"evidence_count":3,"strongest":0.9,"signal_types":["expansion","funding"],` +
			`"first_run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75","last_run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75",` +
			`"first_seen_at":"2026-09-18T09:00:00Z","last_seen_at":"2026-09-20T11:05:00Z","reviewed_at":"2026-09-24T10:12:00Z",` +
			`"evidence":[]},"company_id":"e0a2c4e6-8a0c-4e2a-b4c6-e8a0c2e4a6c8","company_created":true}`,
	},
	"dismiss_company_suggestion": {
		Group:   "research",
		AR:      "رفض اقتراح شركة عرضه البحث. يُتذكَّر هذا القرار: أي عملية بحث لاحقة تعثر على الشركة نفسها تُبقيها مرفوضة.",
		Example: map[string]any{"id": "c1d3e5f7-9b1d-4a3c-8e5f-7b9d1c3e5f70"},
		Result: `{"id":"c1d3e5f7-9b1d-4a3c-8e5f-7b9d1c3e5f70","company_key":"acme freight|acme.example","name":"Acme Freight",` +
			`"domain":"acme.example","status":"dismissed","crm_company_id":"","evidence_count":3,"strongest":0.9,` +
			`"signal_types":["expansion","funding"],"first_run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75",` +
			`"last_run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75","first_seen_at":"2026-09-18T09:00:00Z","last_seen_at":"2026-09-20T11:05:00Z",` +
			`"reviewed_at":"2026-09-24T10:14:00Z","evidence":[]}`,
	},
	"list_research_schedules": {
		Group:   "research",
		AR:      "عمليات البحث التي تعمل من تلقاء نفسها (يوميًا أو أسبوعيًا)، مع نتيجة كل جدولة الأخيرة، وميزانية عمليات البحث التلقائية لليوم.",
		Example: map[string]any{},
		Result: `{"budget":{"daily_run_budget":3,"runs_today":1,"day":"2026-09-24"},"schedules":[{"id":"f1a3c5e7-9b1d-4f3a-8c5e-7a9b1d3f5a7c",` +
			`"name":"Competitor funding watch","topic":"logistics startups raises funding Egypt","mode":"search","urls":[],` +
			`"cadence":"weekly","enabled":true,"next_run_at":"2026-09-27T00:00:00Z","last_run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75",` +
			`"last_run_at":"2026-09-20T00:05:00Z","last_status":"started","last_note":"","created_at":"2026-09-10T08:00:00Z"}]}`,
	},
	"save_research_schedule": {
		Group: "research",
		AR: "إنشاء جدولة بحث (بلا id) أو تغيير جدولة قائمة (مع id): سؤال (mode = search) أو قائمة صفحات (mode = urls)، تعمل يوميًا " +
			"أو أسبوعيًا. كل عملية بحث مجدولة تستهلك واحدة من ميزانية مساحة العمل اليومية للبحث.",
		Example: map[string]any{"name": "Competitor funding watch", "topic": "logistics startups raises funding Egypt", "cadence": "weekly"},
		Result: `{"id":"f1a3c5e7-9b1d-4f3a-8c5e-7a9b1d3f5a7c","name":"Competitor funding watch",` +
			`"topic":"logistics startups raises funding Egypt","mode":"search","urls":[],"cadence":"weekly","enabled":true,` +
			`"next_run_at":"2026-09-24T10:16:00Z","last_run_id":"","last_run_at":null,"last_status":"","last_note":"",` +
			`"created_at":"2026-09-24T10:16:00Z"}`,
	},
	"delete_research_schedule": {
		Group:   "research",
		AR:      "حذف جدولة بحث. تبقى عمليات بحثها السابقة وأدلتها كما هي.",
		Example: map[string]any{"id": "f1a3c5e7-9b1d-4f3a-8c5e-7a9b1d3f5a7c"},
		Result:  `{"deleted":"f1a3c5e7-9b1d-4f3a-8c5e-7a9b1d3f5a7c","ok":true}`,
	},
	"set_research_budget": {
		Group: "research",
		AR: "عدد عمليات البحث التلقائية (الجدولات، واستكشاف العملاء المرتقبين) التي يمكن لمساحة العمل بدؤها في اليوم الواحد " +
			"بتوقيت UTC، من 0 إلى 50. عمليات البحث التي يبدؤها شخص بنفسه لا تُحتسب.",
		Example: map[string]any{"daily_run_budget": 5},
		Result:  `{"daily_run_budget":5,"runs_today":1,"day":"2026-09-24"}`,
	},
	"capability_profile": {
		Group: "research",
		AR: "العميل المثالي لمساحة العمل، كما يبحث عنه ويقيّمه استكشاف العملاء المرتقبين: القطاعات والكلمات المفتاحية والمناطق " +
			"والكلمات المستبعدة، وأوزان الإشارات، والعرض الذي تصوغه مسودة التواصل، والاستعلامات التي سيبحث عنها الاستكشاف تباعًا، " +
			"وهل الاستكشاف المجدوَل مفعَّل. مرِّر أي حقل لتغييره (يُعاد عندها تقييم العملاء المرتقبين المنتظرين).",
		Example: map[string]any{
			"offer": "Fleet tracking software for logistics companies", "industries": []string{"Logistics", "Retail"},
			"keywords": []string{"fleet", "warehouse"}, "regions": []string{"Egypt"}, "enabled": true, "cadence": "daily",
		},
		Result: `{"can_search":true,"profile":{"offer":"Fleet tracking software for logistics companies",` +
			`"industries":["Logistics","Retail"],"keywords":["fleet","warehouse"],"regions":["Egypt"],"exclude_keywords":[],` +
			`"signal_weights":{"expansion":0.8,"funding":1,"hiring":1,"launch":0.6,"leadership":0.4,"partnership":0.5,` +
			`"regulatory":0.3,"technology":0.6},"queries":[],"enabled":true,"cadence":"daily","next_run_at":"2026-09-25T00:00:00Z",` +
			`"last_run_id":"","last_run_at":null,"last_status":"","last_note":"","configured":true,` +
			`"effective_queries":["Logistics companies raises funding Egypt","Logistics companies hiring Egypt"]}}`,
	},
	"run_discovery": {
		Group: "research",
		AR: "تشغيل جولة استكشاف عملاء مرتقبين واحدة الآن: الاستعلام التالي في الملف، كعملية بحث. تُعيد معرّف التشغيل؛ وحين تستقر " +
			"النتيجة تصبح الشركات التي عثرت عليها عملاء مرتقبين (find_opportunities). لا تستهلك الميزانية اليومية.",
		Example: map[string]any{},
		Result: `{"note":"read it with get_research; prospects appear when it settles","query":"Logistics companies raises funding Egypt",` +
			`"run_id":"9c1e3b5d-7f9a-4c1e-8b3d-5f7a9c1e3b50"}`,
	},
	"find_opportunities": {
		Group: "research",
		AR: "العملاء المرتقبون الذين عثر عليهم الاستكشاف، الأفضل أولًا: درجة من 0 إلى 100 مع تفسير كل نقطة فيها (الإشارات، " +
			"والملاءمة، والمنطقة، والأدلة، والحداثة، والاستبعادات، وتغذية راجعة من أحكامك السابقة)، والشخص الذي سمّته الصفحات إن " +
			"وُجد، ومعرّفاته في إدارة العملاء بعد القبول.",
		Example: map[string]any{"status": "new", "min_score": 50, "limit": 2},
		Result: `{"count":1,"counts":{"accepted":2,"new":5,"rejected":1},"opportunities":[{"id":"d2e4f6a8-0c2e-4a6c-8e0a-2c4e6a8c0e21",` +
			`"suggestion_id":"c1d3e5f7-9b1d-4a3c-8e5f-7b9d1c3e5f70","name":"Acme Freight","domain":"acme.example",` +
			`"company_key":"acme freight|acme.example","status":"new","score":78,"reasons":[{"dimension":"signals","points":22.5,` +
			`"items":["funding","expansion"],"count":0,"text":"funding, expansion in the evidence"},{"dimension":"fit","points":18.8,` +
			`"items":["Logistics","fleet"],"count":0,"text":"mentions Logistics, fleet"}],"signal_types":["expansion","funding"],` +
			`"evidence_count":3,"contact":{"name":"Layla Nasser","title":"Operations Director","email":"layla.nasser@mail.example",` +
			`"source_url":"https://acme.example/about"},"crm_company_id":"","crm_contact_id":"","crm_deal_id":"","reject_reason":"",` +
			`"first_run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75","decided_at":null,"scored_at":"2026-09-20T11:10:00Z",` +
			`"created_at":"2026-09-20T11:10:00Z"}]}`,
	},
	"get_opportunity": {
		Group: "research",
		AR: "عميل مرتقب واحد بكامل تفاصيله: درجته وأسبابها، والشخص الذي سمّته الصفحات، وسجله الزمني من الإشارات (كل دليل، من " +
			"الأقدم إلى الأحدث، مع رابط مصدره)، ومسودات التواصل التي صيغت له.",
		Example: map[string]any{"id": "d2e4f6a8-0c2e-4a6c-8e0a-2c4e6a8c0e21"},
		Result: `{"prospect":{"id":"d2e4f6a8-0c2e-4a6c-8e0a-2c4e6a8c0e21","suggestion_id":"c1d3e5f7-9b1d-4a3c-8e5f-7b9d1c3e5f70",` +
			`"name":"Acme Freight","domain":"acme.example","company_key":"acme freight|acme.example","status":"new","score":78,` +
			`"reasons":[{"dimension":"signals","points":22.5,"items":["funding","expansion"],"count":0,` +
			`"text":"funding, expansion in the evidence"}],"signal_types":["expansion","funding"],"evidence_count":3,` +
			`"contact":{"name":"Layla Nasser","title":"Operations Director","email":"layla.nasser@mail.example",` +
			`"source_url":"https://acme.example/about"},"crm_company_id":"","crm_contact_id":"","crm_deal_id":"","reject_reason":"",` +
			`"first_run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75","decided_at":null,"scored_at":"2026-09-20T11:10:00Z",` +
			`"created_at":"2026-09-20T11:10:00Z"},"timeline":[{"id":"a4c8e0f2-1b3d-4e6a-9f7c-5d0e2a4c8e60",` +
			`"run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75","run_topic":"logistics companies raises funding Egypt",` +
			`"source_id":"b7e0f2c4-3d5f-4a80-9c1e-7b3d5f7a9c10","kind":"claim",` +
			`"quote":"Acme Freight raised a $4M seed round led by Delta Ventures.","claim":"Acme Freight raised $4M in seed funding.",` +
			`"source_url":"https://techcrunch.example/acme-freight-seed","source_title":"Acme Freight raises $4M seed round",` +
			`"source_domain":"techcrunch.example","company_name":"Acme Freight","company_domain":"acme.example",` +
			`"company_key":"acme freight|acme.example","signal_type":"funding","confidence":0.9,"published_at":"2026-09-10T00:00:00Z",` +
			`"captured_at":"2026-09-20T11:05:00Z","extractor":"model:anthropic"}],"drafts":[]}`,
	},
	"mark_opportunity": {
		Group: "research",
		AR: "اتخاذ قرار بشأن عميل مرتقب. accept يضيف الشركة، وجهة الاتصال (إن أُعطي بريد إلكتروني — فجهة الاتصال هي عنوان) وصفقة " +
			"إلى إدارة العملاء مع الأدلة في السجل الزمني؛ وreject يرفضه ويخفض ترتيب العملاء المرتقبين المشابهين من الآن فصاعدًا.",
		Example: map[string]any{
			"id": "d2e4f6a8-0c2e-4a6c-8e0a-2c4e6a8c0e21", "mark": "accept",
			"contact_name": "Layla Nasser", "contact_email": "layla.nasser@mail.example", "contact_title": "Operations Director",
		},
		Result: `{"prospect":{"id":"d2e4f6a8-0c2e-4a6c-8e0a-2c4e6a8c0e21","suggestion_id":"c1d3e5f7-9b1d-4a3c-8e5f-7b9d1c3e5f70",` +
			`"name":"Acme Freight","domain":"acme.example","company_key":"acme freight|acme.example","status":"accepted","score":78,` +
			`"reasons":[{"dimension":"signals","points":22.5,"items":["funding","expansion"],"count":0,` +
			`"text":"funding, expansion in the evidence"}],"signal_types":["expansion","funding"],"evidence_count":3,` +
			`"contact":{"name":"Layla Nasser","title":"Operations Director","email":"layla.nasser@mail.example",` +
			`"source_url":"https://acme.example/about"},"crm_company_id":"e0a2c4e6-8a0c-4e2a-b4c6-e8a0c2e4a6c8",` +
			`"crm_contact_id":"6c8e0a2c-4e6a-4c8e-a0c2-4e6a8c0e2a4d","crm_deal_id":"c4e6a8c0-2e4a-4c6e-8a0c-2e4a6c8e0a2c",` +
			`"reject_reason":"","first_run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75","decided_at":"2026-09-24T10:22:00Z",` +
			`"scored_at":"2026-09-20T11:10:00Z","created_at":"2026-09-20T11:10:00Z"},` +
			`"company_id":"e0a2c4e6-8a0c-4e2a-b4c6-e8a0c2e4a6c8","company_created":true,` +
			`"contact_id":"6c8e0a2c-4e6a-4c8e-a0c2-4e6a8c0e2a4d","deal_id":"c4e6a8c0-2e4a-4c6e-8a0c-2e4a6c8e0a2c"}`,
	},
	"draft_message": {
		Group: "research",
		AR: "صياغة أول رسالة تواصل لعميل مرتقب، بالإنجليزية en أو العربية ar، من أدلته فقط. مسودة فقط: تُحفظ وتُعاد، ولا يرسلها " +
			"محرّك أبدًا — يتولّى شخص نسخها. عند عدم ضبط نموذج تكون قالبًا جاهزًا (generator=template).",
		Example: map[string]any{"id": "d2e4f6a8-0c2e-4a6c-8e0a-2c4e6a8c0e21", "locale": "en"},
		Result: `{"draft":{"id":"7a9c1e3b-5d7f-4a9b-8c1d-3e5f7a9b1c30","locale":"en","subject":"Acme Freight — recently raised funding",` +
			`"body":"Hi Layla,\n\nI read that Acme Freight raised a $4M seed round led by Delta Ventures (techcrunch.example).\n\n` +
			`We build fleet tracking software for logistics companies.\n\nWould a short call next week be useful?\n\nBest,\n[Your name]",` +
			`"generator":"model:anthropic","evidence_ids":["a4c8e0f2-1b3d-4e6a-9f7c-5d0e2a4c8e60"],"created_at":"2026-09-24T10:24:00Z"},` +
			`"note":"a draft only — nothing was sent","sent":false}`,
	},
	"prospect_digest": {
		Group: "research",
		AR: "العملاء المرتقبون الذين عُثر عليهم منذ آخر موجز، الأفضل أولًا. mark=true ينقل علامة الموجز إلى الآن، فيبدأ الموجز " +
			"التالي بعد هؤلاء.",
		Example: map[string]any{"mark": false},
		Result: `{"since":"2026-09-20T00:00:00Z","prospects":[{"id":"d2e4f6a8-0c2e-4a6c-8e0a-2c4e6a8c0e21",` +
			`"suggestion_id":"c1d3e5f7-9b1d-4a3c-8e5f-7b9d1c3e5f70","name":"Acme Freight","domain":"acme.example",` +
			`"company_key":"acme freight|acme.example","status":"new","score":78,"reasons":[{"dimension":"signals","points":22.5,` +
			`"items":["funding","expansion"],"count":0,"text":"funding, expansion in the evidence"}],` +
			`"signal_types":["expansion","funding"],"evidence_count":3,"contact":{"name":"Layla Nasser","title":"Operations Director",` +
			`"email":"layla.nasser@mail.example","source_url":"https://acme.example/about"},"crm_company_id":"","crm_contact_id":"",` +
			`"crm_deal_id":"","reject_reason":"","first_run_id":"2c7e9a14-5b3f-4d80-a6e1-9f0b4c8d2e75","decided_at":null,` +
			`"scored_at":"2026-09-20T11:10:00Z","created_at":"2026-09-20T11:10:00Z"}]}`,
	},
}

// argsARProspects covers the arguments of the tools above (MH-397, MH-388),
// plus start_research's new mode/urls/topic (docsResearch, docs_tools.go).
var argsARProspects = map[string]string{
	// start_research (mode=urls, MH-397)
	"The question, in plain words. In URL mode, what to look for in the pages.": "السؤال بكلمات بسيطة؛ وفي وضع URL يحدّد ما يُبحث عنه داخل الصفحات.",
	"search (default) or urls.":         "search (الافتراضي) أو urls.",
	"URL mode: up to 10 pages to read.": "وضع URL: حتى 10 صفحات لقراءتها.",

	// find_evidence
	"Words in the quote, claim, page title or company name.": "كلمات في الاقتباس أو الادعاء أو عنوان الصفحة أو اسم الشركة.",
	"A company's name or domain.":                            "اسم شركة أو نطاقها.",
	"The source site, e.g. techcrunch.com.":                  "موقع المصدر، مثل techcrunch.com.",
	"Only this run's evidence.":                              "أدلة عملية البحث هذه فقط.",
	"YYYY-MM-DD — published or captured on or after.":        "بصيغة YYYY-MM-DD — نُشر أو جُمع في هذا التاريخ أو بعده.",
	"YYYY-MM-DD — captured on or before.":                    "بصيغة YYYY-MM-DD — جُمع في هذا التاريخ أو قبله.",
	"Default 25, at most 100.":                               "الافتراضي 25، وبحد أقصى 100.",
	"For the next page.":                                     "للصفحة التالية.",

	// list_company_suggestions / find_opportunities
	"Default 25.":                           "الافتراضي 25.",
	"Default 50.":                           "الافتراضي 50.",
	"Default new.":                          "الافتراضي new.",
	"Name or domain contains.":              "الاسم أو النطاق يحتوي على.",
	"Only prospects scoring at least this.": "العملاء المرتقبون الذين تبلغ درجتهم هذا الحدّ على الأقل.",

	// accept/dismiss_company_suggestion
	"The suggestion id (list_company_suggestions).":            "معرّف الاقتراح (من list_company_suggestions).",
	"Optional: the company's name, if research read it wrong.": "اختياري: اسم الشركة، إن كان البحث قد قرأه خطأً.",
	"Optional: its website domain.":                            "اختياري: نطاق موقعها الإلكتروني.",
	"The suggestion id.":                                       "معرّف الاقتراح.",

	// research schedules and budget
	"Omit to create.":                      "اتركه فارغًا للإنشاء.",
	"What to call it.":                     "الاسم الذي يُطلق عليها.",
	"The question.":                        "السؤال.",
	"false pauses it.":                     "القيمة false توقفها مؤقتًا.",
	"The schedule id.":                     "معرّف الجدولة.",
	"0 to 50. 0 stops automatic research.": "من 0 إلى 50؛ الصفر يوقف البحث التلقائي.",

	// capability_profile
	"What the workspace sells, in a sentence or two.":                                                                  "ما تبيعه مساحة العمل، في جملة أو جملتين.",
	`Signal type -> weight 0..3: hiring, funding, launch, expansion, partnership, leadership, technology, regulatory.`: `نوع الإشارة -> الوزن من 0 إلى 3: hiring وfunding وlaunch وexpansion وpartnership وleadership وtechnology وregulatory.`,
	"What discovery searches, in turn. Empty: built from the profile.":                                                 "ما يبحث عنه الاستكشاف، الواحد تلو الآخر. اتركه فارغًا ليُبنى من الملف.",
	"Scheduled discovery on or off (spends the daily research budget).":                                                "تفعيل الاستكشاف المجدوَل أو تعطيله (يستهلك ميزانية البحث اليومية).",

	// get_opportunity / mark_opportunity / draft_message
	"The prospect id (find_opportunities).":                  "معرّف العميل المرتقب (من find_opportunities).",
	"The prospect id.":                                       "معرّف العميل المرتقب.",
	"reject: why, in a sentence.":                            "reject: السبب في جملة واحدة.",
	"accept: the person, if not the one the pages named.":    "accept: الشخص المعني، إن لم يكن من سمّته الصفحات.",
	"accept: their address. Without it no contact is added.": "accept: بريده الإلكتروني؛ من دونه لا تُضاف جهة اتصال.",
	"accept: their role.":                                    "accept: مسمّاه الوظيفي.",
	"accept: defaults to '<company> — prospect'.":            "accept: الافتراضي '<company> — prospect'.",
	"accept: the deal's value in minor units.":               "accept: قيمة الصفقة بالوحدة الفرعية للعملة.",

	// prospect_digest
	"Move the digest mark to now. Default false.": "نقل علامة الموجز إلى الآن. الافتراضي false.",
}
