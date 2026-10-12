package mcp

// Docs for the bring-your-own-agent tools (user_tools_agent.go): the
// Opportunity Finder run by the calling agent. See docs.go.

var docsAgentProvider = map[string]ToolDoc{
	"opportunity_tasks": {
		Group: "research",
		AR: "ابدأ من هنا لتشغيل مُكتشف الفرص (أو البحث المنتظر في الطابور) بنموذجك وأدوات الويب الخاصة بك — لا تحتاج مساحة العمل " +
			"إلى أي مفتاح API. تُعيد مهامًّا محجوزة لك لمدة 30 دقيقة، كلٌّ منها بكل ما يلزم لإنجازها دون سؤال: ملف العميل المثالي " +
			"(العرض والقطاعات والأحجام والمناطق والكلمات والاستبعادات وأوزان الإشارات)، واستعلامات البحث، ومعنى كل نوع إشارة، " +
			"وتلميحات منع التكرار (شركات إدارة العملاء وما عُثر عليه سابقًا)، وصيغة المخرجات بالضبط. إن لم يكن هناك ما ينتظر، " +
			"يُضاف استعلام الاستكشاف التالي لك (create_if_empty افتراضيًا true). ثم: ابحث، وأرسل عبر submit_opportunities أو submit_research، ثم complete_task.",
		Example: map[string]any{"agent": map[string]any{"name": "Claude", "model": "claude-opus-5-5"}, "limit": 1},
		Result: `{"count":1,"lease_minutes":30,"agent":{"name":"Claude","model":"claude-opus-5-5"},"note":"",` +
			`"tasks":[{"task":{"id":"5b7d9f1a-3c5e-4a7c-9e1a-3c5e7a9c1e30","kind":"discovery","status":"claimed",` +
			`"topic":"fintech raises funding Egypt","mode":"search","urls":[],"origin":"assistant","run_id":"","agent_name":"Claude",` +
			`"agent_model":"claude-opus-5-5","lease_until":"2026-10-11T10:30:00Z","attempts":1,"received":0,"stored":0,"note":"",` +
			`"error":"","created_at":"2026-10-11T10:00:00Z","finished_at":null},` +
			`"goal":"Find companies that match the ICP and show a buying signal now.",` +
			`"search_queries":["fintech raises funding Egypt","fintech \"raises funding\" Egypt"],` +
			`"icp":{"offer":"Payment integrations for fintechs.","industries":["fintech"],"sizes":[],"regions":["Egypt"],"keywords":[],` +
			`"exclude_keywords":["crypto"],"signal_weights":{"funding":1,"hiring":1},"configured":true},` +
			`"signals":{"funding":"raised money: a seed/Series round, a grant, debt financing, an acquisition of the company"},` +
			`"dedupe":{"crm_companies":[{"name":"Acme Pay","domain":"acmepay.com"}],"known":[],"note":"Moharrik dedupes again on submit."},` +
			`"submit":{"tool":"submit_opportunities","then":"complete_task with this task_id when you are done.",` +
			`"limits":{"candidates_per_call":25},"rules":["Use only public pages you actually read; copy quotes exactly."],"output_schema":{"type":"object"}}}]}`,
	},
	"agent_tasks": {
		Group: "research",
		AR: "ابدأ من هنا حين يكون لدى محرّك عمل لنموذجك — لا تملك مساحة العمل مفتاح نموذج، فأنت ذكاؤها. تُعيد مهامًّا محجوزة لك " +
			"لمدة 30 دقيقة بكل ما يلزم لإنجازها. التلخيص: جمع محرّك الصفحات لعملية بحث، وتحمل المهمة تلك الصفحات (مصادر مرقّمة) " +
			"والأدلة المستخرجة منها — أجب عن السؤال منها عبر submit_research ثم complete_task. البحث: أجب عن سؤال من الويب بنفسك. " +
			"الاستكشاف: اعثر على شركات تطابق ملف العميل المثالي (submit_opportunities).",
		Example: map[string]any{"agent": map[string]any{"name": "Claude", "model": "claude-opus-5-5"}, "kind": "summary"},
		Result: `{"count":1,"lease_minutes":30,"agent":{"name":"Claude","model":"claude-opus-5-5"},"note":"",` +
			`"tasks":[{"task":{"id":"6c8e0a2c-4e6a-4c8e-a0c2-4e6a8c0e2a40","kind":"summary","status":"claimed",` +
			`"topic":"Saudi logistics companies hiring CTOs","mode":"search","urls":[],"origin":"manual","run_id":"8d0f2b4c-6e8a-4c0e-a2b4-6c8e0a2c4e60",` +
			`"agent_name":"Claude","agent_model":"claude-opus-5-5","lease_until":"2026-10-11T10:30:00Z","attempts":1,"received":0,"stored":0,` +
			`"note":"","error":"","created_at":"2026-10-11T10:00:00Z","finished_at":null},` +
			`"goal":"Moharrik already searched and read the web for this question; it has no model to think with. Read the pages below and answer.",` +
			`"search_queries":[],"pages":[{"source":1,"url":"https://example.com/news/cto","title":"Naqel names a new CTO","read":true,` +
			`"content":"Naqel Express appointed a new chief technology officer to lead its digital transformation."}],` +
			`"evidence":[{"source":1,"quote":"Naqel Express appointed a new chief technology officer.","company":"Naqel Express","signal_type":"leadership"}],` +
			`"submit":{"tool":"submit_research","then":"complete_task with this task_id once the answer is in.","limits":{"insights":10},` +
			`"rules":["Answer from these pages only; cite each claim as [source]."],"output_schema":{"type":"object"}}}]}`,
	},
	"summarise_research": {
		Group: "research",
		AR: "لخّص عملية بحث بنفسك: لعملية انتهت جمع محرّك صفحاتها ولم يستطع تلخيصها (لا مفتاح نموذج). تُضيف مهمة تلخيص وتُعيدها " +
			"بملخّصها: الصفحات (مصادر مرقّمة) والأدلة المستخرجة منها. أجب عبر submit_research ثم complete_task.",
		Example: map[string]any{"run_id": "8d0f2b4c-6e8a-4c0e-a2b4-6c8e0a2c4e60", "agent": map[string]any{"name": "Claude", "model": "claude-opus-5-5"}},
		Result: `{"lease_minutes":30,"task":{"task":{"id":"6c8e0a2c-4e6a-4c8e-a0c2-4e6a8c0e2a40","kind":"summary","status":"claimed",` +
			`"topic":"Saudi logistics companies hiring CTOs","run_id":"8d0f2b4c-6e8a-4c0e-a2b4-6c8e0a2c4e60"},"pages":[],"evidence":[],` +
			`"submit":{"tool":"submit_research"}}}`,
	},
	"set_research_provider": {
		Group: "research",
		AR: "من يتولّى تفكير البحث في مساحة العمل: auto (الخادم حين يملك البحث ومفتاح نموذج، وإلا مساعدك الذكي عبر MCP)، " +
			"أو server، أو agent (مساعدك دائمًا: يجمع الخادم الصفحات حين يستطيع وتلخّصها أنت). تُعيد المزوّد والمهام المنتظرة.",
		Example: map[string]any{"mode": "agent"},
		Result: `{"provider":{"mode":"agent","effective":"agent","can_server":true,"can_summarise":false,"pending_tasks":1,"claimed_tasks":0},` +
			`"prompt":"You are the AI for my Moharrik workspace …"}`,
	},
	"submit_prospects": {
		Group: "research",
		AR:    "مثل submit_opportunities تمامًا: إرجاع الشركات التي عثرت عليها لمهمة مع الأدلة؛ تُحفظ كعملاء مرتقبين مُقيَّمين، والرسالة الافتتاحية مسودة لا تُرسل.",
		Example: map[string]any{"task_id": "5b7d9f1a-3c5e-4a7c-9e1a-3c5e7a9c1e30", "candidates": []any{map[string]any{"company": "Zeal", "domain": "zeal.com.eg",
			"signal_type": "funding", "evidence": []any{map[string]any{"url": "https://techcrunch.com/2026/09/10/zeal-raises-10m",
				"quote": "Egyptian fintech startup Zeal has raised US$10 million in funding to grow its payments business."}}}}},
		Result: `{"task_id":"5b7d9f1a-3c5e-4a7c-9e1a-3c5e7a9c1e30","received":1,"stored":1,"rejected":[],"sent":false}`,
	},
	"submit_opportunities": {
		Group: "research",
		AR: "إرجاع الشركات التي عثرت عليها لمهمة: الشركة ونطاقها، والشخص المناسب للتواصل (الاسم والدور وقنوات التواصل فقط إن نشرتها " +
			"صفحة عامة)، ونوع الإشارة، ولماذا الآن، ورسالة افتتاحية مقترحة، والأدلة — رابط عام واحد على الأقل لكل مرشح مع جملة منسوخة " +
			"حرفيًا منه. ينظّف محرّك الوسوم، ويرفض الروابط غير العامة، ويمنع التكرار مقابل إدارة العملاء وجهات الاتصال والعملاء المحتملين، " +
			"ويقيّم كل مرشح مقابل ملف العميل المثالي، ويحفظه كعميل مرتقب مع مصدره (author_kind=agent). تُحفظ الرسالة الافتتاحية كمسودة ولا تُرسل أبدًا.",
		Example: map[string]any{"task_id": "5b7d9f1a-3c5e-4a7c-9e1a-3c5e7a9c1e30", "agent": map[string]any{"name": "Claude", "model": "claude-opus-5-5"},
			"candidates": []any{map[string]any{"company": "Zeal", "domain": "zeal.com.eg", "signal_type": "funding",
				"person":           map[string]any{"name": "Omar Ebeid", "role": "Co-founder & CEO", "linkedin": "https://www.linkedin.com/in/omar-ebeid"},
				"why_now":          "Raised $10M to expand payments across the region.",
				"suggested_opener": "Congrats on the $10M round — we help fintechs ship payment integrations fast.",
				"evidence": []any{map[string]any{"url": "https://techcrunch.com/2026/09/10/zeal-raises-10m",
					"quote": "Egyptian fintech startup Zeal has raised US$10 million in funding to grow its payments business.", "published_at": "2026-09-10"}}}}},
		Result: `{"task_id":"5b7d9f1a-3c5e-4a7c-9e1a-3c5e7a9c1e30","run_id":"8d0f2b4c-6e8a-4c0e-a2b4-6c8e0a2c4e60","received":1,"stored":1,` +
			`"results":[{"index":0,"company":"Zeal","outcome":"created","prospect_id":"1f3b5d7e-9a1c-4e3a-b5c7-9e1a3c5e7a90","score":74,` +
			`"draft_id":"3a5c7e9b-1d3f-4b5d-8f9b-1d3f5b7d9f10"}],"rejected":[],"sent":false,` +
			`"next":"Send more with submit_opportunities, then complete_task(task_id) to finish."}`,
	},
	"submit_research": {
		Group: "research",
		AR: "إرجاع نتيجة بحث لمهمة: إجابتك المختصرة (مع الإشارة إلى المصادر بـ[1] و[2] بترتيب النتائج) والنتائج التي تستند إليها — " +
			"كلٌّ منها رابط عام قرأته وجملة منسوخة حرفيًا منه وادعاء من سطر واحد والشركة المعنية والإشارة التي يذكرها. النتيجة بلا اقتباس " +
			"تسجّل الصفحة كمصدر فقط. تُحفظ كعملية بحث للمهمة (author_kind=agent)، وتصبح الشركات المذكورة اقتراحات، وفي مهمة الاستكشاف " +
			"تتحول إلى عملاء مرتقبين مُقيَّمين عند complete_task.",
		Example: map[string]any{"task_id": "7c9e1b3d-5f7a-4c9e-b1d3-5f7a9c1e3b50", "answer": "Two Egyptian fintechs raised rounds this month [1][2].",
			"findings": []any{map[string]any{"url": "https://techcrunch.com/2026/09/10/zeal-raises-10m", "quote": "Egyptian fintech startup Zeal has raised US$10 million in funding.",
				"claim": "Zeal raised $10M.", "company": "Zeal", "domain": "zeal.com.eg", "signal_type": "funding"}}},
		Result: `{"task_id":"7c9e1b3d-5f7a-4c9e-b1d3-5f7a9c1e3b50","run_id":"9e1a3c5e-7a9c-4e1a-b3c5-7e9a1c3e5a70","received":1,"stored":1,` +
			`"new_companies":["Zeal"],"rejected":[],"next":"complete_task(task_id) when done."}`,
	},
	"complete_task": {
		Group: "research",
		AR: "إنهاء مهمة تحجزها حتى لا تُسلَّم لغيرك مجددًا. تستقر عملية البحث الخاصة بها كما تستقر عملية الخادم: يُعلَن عمّا وُجد، " +
			"وتصبح شركات مهمة الاستكشاف عملاء مرتقبين مُقيَّمين (find_opportunities). لا يُرسل أي شيء لأي أحد.",
		Example: map[string]any{"task_id": "5b7d9f1a-3c5e-4a7c-9e1a-3c5e7a9c1e30", "note": "8 queries, 22 pages, 6 candidates"},
		Result: `{"task":{"id":"5b7d9f1a-3c5e-4a7c-9e1a-3c5e7a9c1e30","kind":"discovery","status":"done","topic":"fintech raises funding Egypt",` +
			`"mode":"search","urls":[],"origin":"assistant","run_id":"8d0f2b4c-6e8a-4c0e-a2b4-6c8e0a2c4e60","agent_name":"Claude",` +
			`"agent_model":"claude-opus-5-5","lease_until":null,"attempts":1,"received":6,"stored":6,"note":"8 queries, 22 pages, 6 candidates",` +
			`"error":"","created_at":"2026-10-11T10:00:00Z","finished_at":"2026-10-11T10:18:00Z"},` +
			`"next":"Read the results with find_opportunities (prospects) or get_research(run_id)."}`,
	},
	"fail_task": {
		Group:   "research",
		AR:      "التخلّي عن مهمة تحجزها مع ذكر السبب (لا وصول للويب، لم يُعثر على شيء، محجوب). لن تُسلَّم مجددًا، ويُحتفظ بكل ما أُرسل لها من قبل.",
		Example: map[string]any{"task_id": "5b7d9f1a-3c5e-4a7c-9e1a-3c5e7a9c1e30", "error": "No web search tool is available in this session."},
		Result: `{"task":{"id":"5b7d9f1a-3c5e-4a7c-9e1a-3c5e7a9c1e30","kind":"discovery","status":"failed","topic":"fintech raises funding Egypt",` +
			`"mode":"search","urls":[],"origin":"assistant","run_id":"","agent_name":"Claude","agent_model":"claude-opus-5-5","lease_until":null,` +
			`"attempts":1,"received":0,"stored":0,"note":"","error":"No web search tool is available in this session.",` +
			`"created_at":"2026-10-11T10:00:00Z","finished_at":"2026-10-11T10:02:00Z"},"next":"The task is closed. Ask for another with opportunity_tasks."}`,
	},
}

var argsARAgent = map[string]string{
	"Who you are: your product/agent name and model, recorded on everything you submit.": "من أنت: اسم منتجك أو وكيلك والنموذج، ويُسجَّل على كل ما ترسله.",
	"Only this kind of task. Default any.":                                               "هذا النوع من المهام فقط. الافتراضي: أي نوع.",
	"How many tasks to take at once, 1 to 5. Default 1.":                                 "عدد المهام التي تُؤخذ دفعة واحدة، من 1 إلى 5. الافتراضي 1.",
	"Queue the ICP's next discovery query when nothing is waiting. Default true.":        "أضف استعلام الاستكشاف التالي من ملف العميل المثالي حين لا ينتظر شيء. الافتراضي true.",
	"The task id from opportunity_tasks.":                                                "معرّف المهمة من opportunity_tasks.",
	"The companies you found, best first.":                                               "الشركات التي عثرت عليها، الأفضل أولًا.",
	"Your answer to the task's question, citing the findings as [1], [2].":               "إجابتك عن سؤال المهمة، مع الإشارة إلى النتائج بـ[1] و[2].",
	"The pages you read and what each says.":                                             "الصفحات التي قرأتها وما تقوله كلٌّ منها.",
	"Optional: what you did, in a sentence (e.g. 12 queries, 30 pages, 6 candidates).":   "اختياري: ما فعلته في جملة (مثلًا 12 استعلامًا و30 صفحة و6 مرشحين).",
	"Why, in a sentence.": "السبب في جملة.",
	"Queue the ICP's next discovery query when nothing is waiting.":                                    "أضف استعلام الاستكشاف التالي من ملف العميل المثالي حين لا ينتظر شيء.",
	"The task id from agent_tasks / opportunity_tasks.":                                                "معرّف المهمة من agent_tasks أو opportunity_tasks.",
	"Optional: up to 10 one-line takeaways, each citing a source; stored under the answer.":            "اختياري: حتى 10 خلاصات من سطر واحد، كلٌّ منها يشير إلى مصدر؛ تُحفظ تحت الإجابة.",
	"Your answer to the task's question, citing the sources as [1], [2]. Required for a summary task.": "إجابتك عن سؤال المهمة، مع الإشارة إلى المصادر بـ[1] و[2]. مطلوبة في مهمة التلخيص.",
	"The run, from list_research or get_research.":                                                     "عملية البحث، من list_research أو get_research.",
	"Leave out to only read it.":                                                                       "اتركه فارغًا للقراءة فقط.",
}
