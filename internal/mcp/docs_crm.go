package mcp

// Docs for the CRM and lead tools (user_tools_crm.go, user_tools_leads.go,
// user_tools_reports.go, user_tools_icp.go, list_mail_lists). See docs.go.

var docsCRM = map[string]ToolDoc{
	// ---- contacts -----------------------------------------------------------
	"list_contacts": {
		AR: "جهات الاتصال في إدارة العملاء، الأحدث أولًا (أو حسب درجة الملاءمة لملف العميل المثالي مع sort=score)، صفحةً بعد صفحة. " +
			"لكل جهة معرّفها وبريدها واسمها وهاتفها ومسمّاها الوظيفي وشركتها (المعرّف والاسم)، ومرحلتها في دورة الحياة، ودرجتها (0-100) " +
			"مع أبرز أسبابها، وموافقتها على واتساب (موافِقة أو منسحبة)، وحالتها في كل قائمة بريدية. يبيّن total عدد النتائج المطابقة كلها، " +
			"ولجلب الصفحة التالية مرِّر next_cursor في cursor. للتصفية استخدم q (الاسم أو البريد أو الهاتف أو الشركة)، وlist (الاسم الدقيق " +
			"لقائمة بريدية) ومعها status، وlifecycle، وmin_score.",
		Example: map[string]any{"sort": "score", "lifecycle": "qualified", "min_score": 60, "limit": 2},
		Result: `{"contacts":[` +
			`{"company":"Delta Freight","company_id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","created_at":"2026-08-14T09:12:00Z",` +
			`"email":"layla.nasser@mail.example","id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","lifecycle":"qualified","locale":"en",` +
			`"name":"Layla Nasser","phone":"+20 101 234 5678","score":86,"score_reasons":[` +
			`{"contribution":4,"dimension":"industry","text":"The company's industry, Logistics, is in the profile."},` +
			`{"contribution":3,"dimension":"open_deal","text":"Has an open deal (theirs or their company's)."}],` +
			`"source":"lead","subscriptions":[{"list":"Newsletter","list_id":"4d6f8a0c-2e4a-4c6e-9a0c-4e6a8c0e2a4c","status":"subscribed"}],` +
			`"title":"Operations Director","whatsapp_opted_in":true,"whatsapp_opted_out":false},` +
			`{"company":"Cairo Retail Group","company_id":"e4f5a6b7-c8d9-4e0f-a1b2-c3d4e5f6a7b8","created_at":"2026-07-30T15:02:00Z",` +
			`"email":"karim.adel@mail.example","id":"8d2e4f60-1a3b-4c5d-9e7f-0a1b2c3d4e5f","lifecycle":"qualified","locale":"ar",` +
			`"name":"Karim Adel","phone":"","score":71,"score_reasons":[` +
			`{"contribution":4,"dimension":"industry","text":"The company's industry, Retail, is in the profile."}],` +
			`"source":"crm","subscriptions":[],"title":"Procurement Manager","whatsapp_opted_in":false,"whatsapp_opted_out":false}],` +
			`"count":2,"next_cursor":"eyJrIjpbIjcxIiwiOGQyZTRmNjAiXX0","total":5}`,
	},
	"search_contacts": {
		AR: "البحث عن جهات اتصال في إدارة العملاء بالاسم أو البريد أو الهاتف أو اسم الشركة (أي جزء منه، دون تمييز لحالة الأحرف). " +
			"الإجابة بنفس شكل list_contacts: المعرّفات والشركة وموافقة واتساب وحالات القوائم، مع total وnext_cursor لجلب المزيد.",
		Example: map[string]any{"q": "cairo retail"},
		Result: `{"contacts":[` +
			`{"company":"Cairo Retail Group","company_id":"e4f5a6b7-c8d9-4e0f-a1b2-c3d4e5f6a7b8","created_at":"2026-07-30T15:02:00Z",` +
			`"email":"karim.adel@mail.example","id":"8d2e4f60-1a3b-4c5d-9e7f-0a1b2c3d4e5f","lifecycle":"qualified","locale":"ar",` +
			`"name":"Karim Adel","phone":"","score":71,"score_reasons":[` +
			`{"contribution":4,"dimension":"industry","text":"The company's industry, Retail, is in the profile."}],` +
			`"source":"crm","subscriptions":[],"title":"Procurement Manager","whatsapp_opted_in":false,"whatsapp_opted_out":false}],` +
			`"count":1,"next_cursor":"","total":1}`,
	},
	"get_contact": {
		AR: "جهة اتصال واحدة بكامل تفاصيلها: حقول إدارة العملاء (الاسم والبريد والهاتف والمسمّى الوظيفي والوسوم والمالك)، وشركتها، " +
			"واشتراكاتها في القوائم البريدية، وصفقاتها (يبيّن deals_total عددها الكلي)، والصفحة الأولى من سجلها الزمني: أنشطتها وأنشطة " +
			"صفقاتها، ومعها محادثات صندوق الوارد المرتبطة بها إن كان الرمز يملك صلاحية قراءة الوارد. لمتابعة السجل مرِّر " +
			"activities_next_cursor إلى list_activities.",
		Example: map[string]any{"id": "3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23"},
		Result: `{"contact":{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser",` +
			`"locale":"en","source":"lead","phone":"+20 101 234 5678","title":"Operations Director","tags":["vip"],` +
			`"company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight"},"owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30",` +
			`"created_at":"2026-08-14T09:12:00Z","subscriptions":[],"lifecycle":"qualified","lifecycle_at":"2026-09-02T11:30:00Z",` +
			`"lifecycle_history":[{"id":"2a4c6e8a-0c2e-4a4c-9e6a-8c0e2a4c6e8a","from":"lead","to":"qualified","source":"manual",` +
			`"actor_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","at":"2026-09-02T11:30:00Z"}],"lifecycle_history_total":2,` +
			`"score":86,"score_reasons":[{"dimension":"open_deal","weight":3,"value":1,"contribution":3,"matched":[],"detail":"",` +
			`"text":"Has an open deal (theirs or their company's)."}]},` +
			`"deals":[{"id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e","title":"Delta Freight — fleet tracking rollout","value_minor":1800000,` +
			`"currency":"USD","stage_id":"6e8f0a2c-4d6e-4a8b-9c0d-2e4f6a8b0c1d","stage_name":"Proposal","stage_kind":"open","position":1,` +
			`"contact":{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser","title":"Operations Director"},` +
			`"company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight"},"owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30",` +
			`"expected_close":"2026-11-30","closed_at":null,"created_at":"2026-09-02T11:25:00Z"}],"deals_total":1,` +
			`"activities":[],"activities_total":0,"activities_next_cursor":"","conversations":[]}`,
	},
	"create_contact": {
		AR: "إضافة جهة اتصال عن طريق بريدها الإلكتروني؛ وإن وُجدت جهة بالبريد نفسه أُعيد استخدامها بدل تكرارها. الحقول الاختيارية: " +
			"name وlocale (en أو ar) وphone وtitle وcompany_id، وlist (الاسم الدقيق لقائمة بريدية) لاشتراكها فيها، مع إرسال رسالة " +
			"تأكيد الاشتراك المزدوج إن كانت القائمة تطلبه. تُعيد جهة الاتصال بمعرّفها.",
		Example: map[string]any{"email": "nour.salem@mail.example", "name": "Nour Salem", "locale": "ar", "list": "Newsletter"},
		Result: `{"confirmation":"sent","contact":{"id":"6c8e0a2c-4e6a-4c8e-a0c2-4e6a8c0e2a4d","email":"nour.salem@mail.example",` +
			`"name":"Nour Salem","locale":"ar","source":"manual","phone":"","title":"","company_id":null,"company_name":"",` +
			`"created_at":"2026-09-24T10:05:00Z","subscriptions":[{"id":"d8f0a2c4-6e8a-4c0e-b2d4-6f8a0c2e4a6c",` +
			`"list_id":"4d6f8a0c-2e4a-4c6e-9a0c-4e6a8c0e2a4c","list_name":"Newsletter","status":"pending","confirmed_at":null,` +
			`"unsubscribed_at":null,"unsubscribed_by_workspace":false}],"whatsapp_opt_in_at":null,"whatsapp_opt_out_at":null,` +
			`"lifecycle":"","score":null,"score_reasons":[]},"list_status":"pending"}`,
	},
	"update_contact": {
		AR: "تعديل حقول جهة اتصال في إدارة العملاء، مع إرسال ما يتغيّر فقط: name وphone وtitle وcompany_id (النص الفارغ يفكّ الربط بالشركة)، " +
			"وowner (معرّف مستخدم العضو أو بريده؛ الفارغ يلغي الإسناد)، وtags (تحلّ محل الوسوم كلها)، وlifecycle (subscriber أو lead أو " +
			"qualified أو customer أو فارغ). تُسجَّل المرحلة المضبوطة يدويًا باسمك، بينما تدفعها الأحداث إلى الأمام تلقائيًا.",
		Example: map[string]any{
			"id":    "3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23",
			"title": "Head of Operations",
			"owner": "omar@mail.example",
			"tags":  []string{"vip", "renewal-q4"},
		},
		Result: `{"contact":{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser",` +
			`"locale":"en","source":"lead","phone":"+20 101 234 5678","title":"Head of Operations","tags":["vip","renewal-q4"],` +
			`"company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight"},"owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30",` +
			`"created_at":"2026-08-14T09:12:00Z","subscriptions":[{"id":"d8f0a2c4-6e8a-4c0e-b2d4-6f8a0c2e4a6c",` +
			`"list_id":"4d6f8a0c-2e4a-4c6e-9a0c-4e6a8c0e2a4c","list_name":"Newsletter","status":"subscribed",` +
			`"confirmed_at":"2026-08-14T09:20:00Z","unsubscribed_at":null,"unsubscribed_by_workspace":false}],` +
			`"lifecycle":"qualified","lifecycle_at":"2026-09-02T11:30:00Z","lifecycle_history":[{"id":"2a4c6e8a-0c2e-4a4c-9e6a-8c0e2a4c6e8a",` +
			`"from":"lead","to":"qualified","source":"manual","actor_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","at":"2026-09-02T11:30:00Z"}],` +
			`"lifecycle_history_total":2,"score":90,"score_reasons":[{"dimension":"keywords","weight":2,"value":0.5,"contribution":1,` +
			`"matched":["operations"],"detail":"","text":"Profile keywords found: operations (in title)."}]}}`,
	},

	// ---- companies ----------------------------------------------------------
	"list_companies": {
		AR: "الشركات في إدارة العملاء مرتّبة أبجديًا (أو حسب درجة الملاءمة لملف العميل المثالي مع sort=score)، صفحةً بعد صفحة: لكل شركة " +
			"معرّفها ونطاقها وقطاعها وحجمها وبلدها وعدد جهات الاتصال فيها وعدد صفقاتها المفتوحة ودرجتها (0-100) مع أسبابها. يبيّن total " +
			"عدد النتائج المطابقة، ولجلب الصفحة التالية مرِّر next_cursor في cursor. يبحث q في الاسم والنطاق، ويُبقي min_score الشركات " +
			"التي تبلغ تلك الدرجة على الأقل.",
		Example: map[string]any{"sort": "score", "min_score": 50, "limit": 25},
		Result: `{"companies":[{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight","domain":"deltafreight.example",` +
			`"industry":"Logistics","size":"51-200","country":"Egypt","notes":"Runs 120 trucks between Cairo and Alexandria.",` +
			`"owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","contact_count":3,"open_deals":1,"created_at":"2026-08-14T09:15:00Z",` +
			`"score":88,"score_reasons":[{"dimension":"industry","weight":4,"value":1,"contribution":4,"matched":["Logistics"],` +
			`"detail":"Logistics","text":"The company's industry, Logistics, is in the profile."},` +
			`{"dimension":"region","weight":2,"value":1,"contribution":2,"matched":["Egypt"],"detail":"Egypt",` +
			`"text":"The company's country, Egypt, is in the profile."}]}],"count":1,"next_cursor":"","total":1}`,
	},
	"search_companies": {
		AR: "البحث عن الشركات التي يحتوي اسمها أو نطاقها على q، دون تمييز لحالة الأحرف. الإجابة بنفس شكل list_companies: المعرّفات " +
			"والأعداد وtotal وnext_cursor.",
		Example: map[string]any{"q": "freight"},
		Result: `{"companies":[{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight","domain":"deltafreight.example",` +
			`"industry":"Logistics","size":"51-200","country":"Egypt","notes":"","owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30",` +
			`"contact_count":3,"open_deals":1,"created_at":"2026-08-14T09:15:00Z","score":88,"score_reasons":[` +
			`{"dimension":"industry","weight":4,"value":1,"contribution":4,"matched":["Logistics"],"detail":"Logistics",` +
			`"text":"The company's industry, Logistics, is in the profile."}]}],"count":1,"next_cursor":"","total":1}`,
	},
	"get_company": {
		AR: "شركة واحدة: حقولها (النطاق والقطاع والحجم والملاحظات والمالك)، والأشخاص التابعون لها (معرّفات جهات الاتصال وبريدها " +
			"ومسمّياتها)، وصفقاتها بمرحلتها وقيمتها (يبيّن deals_total عددها الكلي)، وعدد الأنشطة في سجلها الزمني. أما السجل نفسه " +
			"فتعرضه company_timeline.",
		Example: map[string]any{"id": "5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4"},
		Result: `{"activities_total":5,"company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight",` +
			`"domain":"deltafreight.example","industry":"Logistics","size":"51-200","country":"Egypt","notes":"",` +
			`"owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","contact_count":1,"open_deals":1,"created_at":"2026-08-14T09:15:00Z",` +
			`"score":88,"score_reasons":[]},` +
			`"contacts":[{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser","title":"Operations Director"}],` +
			`"deals":[{"closed_at":null,"company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight"},` +
			`"contact":{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser","title":"Operations Director"},` +
			`"created_at":"2026-09-02T11:25:00Z","currency":"USD","expected_close":"2026-11-30","id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e",` +
			`"owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","stage":{"id":"6e8f0a2c-4d6e-4a8b-9c0d-2e4f6a8b0c1d","kind":"open","name":"Proposal"},` +
			`"title":"Delta Freight — fleet tracking rollout","value":18000,"value_minor":1800000}],"deals_total":1}`,
	},
	"create_company": {
		AR: "إضافة شركة، بحدّ شركة واحدة لكل نطاق في مساحة العمل. الحقل name إلزامي، أما domain وindustry وsize وcountry وnotes " +
			"فاختيارية. تُعيد الشركة بمعرّفها، ويكون منشئها مالكها.",
		Example: map[string]any{
			"name": "Nile Couriers", "domain": "nilecouriers.example", "industry": "Logistics", "size": "11-50", "country": "Egypt",
		},
		Result: `{"company":{"id":"e0a2c4e6-8a0c-4e2a-b4c6-e8a0c2e4a6c8","name":"Nile Couriers","domain":"nilecouriers.example",` +
			`"industry":"Logistics","size":"11-50","country":"Egypt","notes":"","owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30",` +
			`"contact_count":0,"open_deals":0,"created_at":"2026-09-24T10:08:00Z","score":null,"score_reasons":[]}}`,
	},
	"update_company": {
		AR: "تعديل شركة، مع إرسال ما يتغيّر فقط: name وdomain وindustry وsize وcountry وnotes، وowner (معرّف مستخدم العضو أو بريده؛ " +
			"النص الفارغ يلغي الإسناد).",
		Example: map[string]any{"id": "5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4", "size": "201-1000", "owner": "sara@mail.example"},
		Result: `{"company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight","domain":"deltafreight.example",` +
			`"industry":"Logistics","size":"201-1000","country":"Egypt","notes":"","owner_user_id":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47",` +
			`"contact_count":3,"open_deals":1,"created_at":"2026-08-14T09:15:00Z","score":88,"score_reasons":[` +
			`{"dimension":"industry","weight":4,"value":1,"contribution":4,"matched":["Logistics"],"detail":"Logistics",` +
			`"text":"The company's industry, Logistics, is in the profile."}]}}`,
	},
	"company_timeline": {
		AR: "كل ما جرى مع شركة واحدة، الأحدث أولًا، صفحةً بعد صفحة: الأنشطة (الملاحظات والمكالمات والاجتماعات والمهام على الشركة أو " +
			"أشخاصها أو صفقاتها)، ومحادثات صندوق الوارد المرتبطة بها (إن كان الرمز يملك صلاحية قراءة الوارد)، وفتح صفقاتها وإغلاقها " +
			"(فوزًا أو خسارة). لكل عنصر نوعه ومعرّفه، فيمكن فتحه بـ get_conversation أو get_deal. لجلب العناصر الأقدم مرِّر next_cursor في cursor.",
		Example: map[string]any{"id": "5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4", "limit": 20},
		Result: `{"company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight"},"entries":[` +
			`{"at":"2026-09-18T13:40:00Z","type":"activity","id":"7a9c1e3b-5d7f-4a9b-8c1d-3e5f7a9b1c3d","kind":"call",` +
			`"summary":"Walked through the pilot scope; they want a proposal by Friday."},` +
			`{"at":"2026-09-12T08:05:00Z","type":"conversation","id":"b2d4f6a8-0c2e-4a6c-8e0a-2c4e6a8c0e2b","kind":"email",` +
			`"summary":"Fleet tracking pricing — Layla Nasser (open, 7 messages)"},` +
			`{"at":"2026-09-02T11:25:00Z","type":"deal","id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e","kind":"opened",` +
			`"summary":"Delta Freight — fleet tracking rollout — 18000.00 USD"}],` +
			`"activities_total":1,"deals_total":1,"conversations_included":true,"next_cursor":""}`,
	},
	"list_activities": {
		AR: "السجل الزمني لسجل واحد، الأحدث أولًا، صفحةً بعد صفحة؛ حدِّد واحدًا فقط من contact_id أو company_id أو deal_id. لكل نشاط " +
			"معرّفه ونوعه ونصّه وموعد استحقاقه ووقت إنجازه وما يرتبط به، ويَعُدّ total الأنشطة كلها. وتأتي معه محادثات صندوق الوارد " +
			"المرتبطة التي تقع في المدة الزمنية للصفحة، إن كان الرمز يملك صلاحية قراءة الوارد.",
		Example: map[string]any{"deal_id": "a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e", "limit": 20},
		Result: `{"activities":[{"id":"1b3d5f7a-9c1e-4b3d-8f5a-7c9e1b3d5f7a","kind":"task","body":"Send the revised proposal with the pilot pricing",` +
			`"due_at":"2026-10-01T00:00:00Z","done_at":null,` +
			`"contact":{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser","title":"Operations Director"},` +
			`"company":null,"deal":{"id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e","name":"Delta Freight — fleet tracking rollout"},` +
			`"created_by":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","created_at":"2026-09-24T10:22:00Z"},` +
			`{"id":"7a9c1e3b-5d7f-4a9b-8c1d-3e5f7a9b1c3d","kind":"call","body":"Walked through the pilot scope; they want a proposal by Friday.",` +
			`"due_at":null,"done_at":null,"contact":null,"company":null,` +
			`"deal":{"id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e","name":"Delta Freight — fleet tracking rollout"},` +
			`"created_by":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","created_at":"2026-09-18T13:40:00Z"}],` +
			`"total":2,"limit":20,"next_cursor":"","conversations":[]}`,
	},

	// ---- deals --------------------------------------------------------------
	"list_deals": {
		AR: "مسار المبيعات بترتيب اللوحة (المرحلة ثم الموضع داخلها)، صفحةً بعد صفحة: لكل صفقة معرّفها ومرحلتها (المعرّف والاسم والنوع " +
			"open أو won أو lost) وقيمتها بالوحدة الرئيسية للعملة وجهة الاتصال والشركة، ومعها المراحل كلها بأعدادها الكاملة. يَعُدّ total " +
			"كل الصفقات، ولجلب الصفحة التالية مرِّر next_cursor في cursor. ويقصر stage_id النتائج على مرحلة واحدة.",
		Example: map[string]any{"stage_id": "6e8f0a2c-4d6e-4a8b-9c0d-2e4f6a8b0c1d"},
		Result: `{"count":1,"currency":"USD","deals":[{"closed_at":null,"company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight"},` +
			`"contact":{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser","title":"Operations Director"},` +
			`"created_at":"2026-09-02T11:25:00Z","currency":"USD","expected_close":"2026-11-30","id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e",` +
			`"owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","stage":{"id":"6e8f0a2c-4d6e-4a8b-9c0d-2e4f6a8b0c1d","kind":"open","name":"Proposal"},` +
			`"title":"Delta Freight — fleet tracking rollout","value":18000,"value_minor":1800000}],"next_cursor":"",` +
			`"stages":[{"deal_count":4,"id":"2c4e6a8b-0d1f-4a3b-8c5d-7e9f1a3b5c7d","kind":"open","name":"Qualified","value_minor":3200000},` +
			`{"deal_count":1,"id":"6e8f0a2c-4d6e-4a8b-9c0d-2e4f6a8b0c1d","kind":"open","name":"Proposal","value_minor":1800000},` +
			`{"deal_count":7,"id":"9b1d3f5a-7c9e-4b0d-a2c4-6e8a0b2d4f6a","kind":"won","name":"Won","value_minor":9650000}],"total":1}`,
	},
	"get_deal": {
		AR: "صفقة واحدة: مرحلتها وقيمتها وجهة الاتصال والشركة والمالك وتاريخ الإغلاق المتوقَّع، والصفحة الأولى من سجلها الزمني (الأنشطة، " +
			"ومحادثة صندوق الوارد التي فُتحت منها إن كان الرمز يملك صلاحية قراءة الوارد). لمتابعة السجل مرِّر activities_next_cursor " +
			"إلى list_activities.",
		Example: map[string]any{"id": "a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e"},
		Result: `{"activities":[{"id":"7a9c1e3b-5d7f-4a9b-8c1d-3e5f7a9b1c3d","kind":"call","body":"Walked through the pilot scope; they want a proposal by Friday.",` +
			`"due_at":null,"done_at":null,"contact":null,"company":null,` +
			`"deal":{"id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e","name":"Delta Freight — fleet tracking rollout"},` +
			`"created_by":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","created_at":"2026-09-18T13:40:00Z"}],` +
			`"activities_next_cursor":"","activities_total":1,` +
			`"conversations":[{"id":"b2d4f6a8-0c2e-4a6c-8e0a-2c4e6a8c0e2b","channel":"email","subject":"Fleet tracking pricing","status":"open",` +
			`"customer_name":"Layla Nasser","preview":"Thanks — Friday works for the proposal.","message_count":7,"contact":null,` +
			`"deal_id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e","last_message_at":"2026-09-12T08:05:00Z","created_at":"2026-09-01T16:20:00Z"}],` +
			`"deal":{"closed_at":null,"company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight"},` +
			`"contact":{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser","title":"Operations Director"},` +
			`"created_at":"2026-09-02T11:25:00Z","currency":"USD","expected_close":"2026-11-30","id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e",` +
			`"owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","stage":{"id":"6e8f0a2c-4d6e-4a8b-9c0d-2e4f6a8b0c1d","kind":"open","name":"Proposal"},` +
			`"title":"Delta Freight — fleet tracking rollout","value":18000,"value_minor":1800000}}`,
	},
	"create_deal": {
		AR: "فتح صفقة. الحقل title إلزامي، وvalue بالوحدة الرئيسية للعملة (مثل 1500.50). تبدأ الصفقة في أول مرحلة مفتوحة ما لم تحدِّد " +
			"مرحلة (بمعرّفها أو اسمها)، وبعملة مساحة العمل ما لم تحدِّد غيرها، ويكون مالكها أنت. ويربطها contact_id وcompany_id " +
			"الاختياريان بجهة اتصال وشركة.",
		Example: map[string]any{
			"title": "Delta Freight — fleet tracking rollout", "value": 18000.0, "currency": "USD", "stage": "Proposal",
			"contact_id": "3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23", "company_id": "5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4",
			"expected_close": "2026-11-30",
		},
		Result: `{"deal":{"closed_at":null,"company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight"},` +
			`"contact":{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser","title":"Operations Director"},` +
			`"created_at":"2026-09-24T10:12:00Z","currency":"USD","expected_close":"2026-11-30","id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e",` +
			`"owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","stage":{"id":"6e8f0a2c-4d6e-4a8b-9c0d-2e4f6a8b0c1d","kind":"open","name":"Proposal"},` +
			`"title":"Delta Freight — fleet tracking rollout","value":18000,"value_minor":1800000}}`,
	},
	"update_deal": {
		AR: "تعديل صفقة، مع إرسال ما يتغيّر فقط: title وvalue (بالوحدة الرئيسية) وcurrency وstage (بالمعرّف أو الاسم؛ نقلها إلى مرحلة " +
			"فوز أو خسارة يغلقها، وإعادتها إلى مرحلة مفتوحة يعيد فتحها)، وcontact_id وcompany_id (النص الفارغ يفكّ الربط)، وowner " +
			"(معرّف المستخدم أو بريده)، وexpected_close (بصيغة YYYY-MM-DD، والفارغ يمسحه).",
		Example: map[string]any{"id": "a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e", "value": 21500.5, "expected_close": "2026-12-15"},
		Result: `{"deal":{"closed_at":null,"company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight"},` +
			`"contact":{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser","title":"Operations Director"},` +
			`"created_at":"2026-09-02T11:25:00Z","currency":"USD","expected_close":"2026-12-15","id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e",` +
			`"owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","stage":{"id":"6e8f0a2c-4d6e-4a8b-9c0d-2e4f6a8b0c1d","kind":"open","name":"Proposal"},` +
			`"title":"Delta Freight — fleet tracking rollout","value":21500.5,"value_minor":2150050}}`,
	},
	"move_deal": {
		AR: "نقل صفقة إلى مرحلة أخرى. حدِّد الصفقة بمعرّفها (أو بعنوانها الدقيق) والمرحلة بمعرّفها أو اسمها. النقل إلى مرحلة فوز أو " +
			"خسارة يغلق الصفقة.",
		Example: map[string]any{"title": "Delta Freight — fleet tracking rollout", "stage": "Won"},
		Result: `{"deal":{"closed_at":"2026-09-24T10:20:00Z","company":{"id":"5b7c9d1e-2f3a-4b5c-8d6e-7f8091a2b3c4","name":"Delta Freight"},` +
			`"contact":{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser","title":"Operations Director"},` +
			`"created_at":"2026-09-02T11:25:00Z","currency":"USD","expected_close":"2026-12-15","id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e",` +
			`"owner_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","stage":{"id":"9b1d3f5a-7c9e-4b0d-a2c4-6e8a0b2d4f6a","kind":"won","name":"Won"},` +
			`"title":"Delta Freight — fleet tracking rollout","value":21500.5,"value_minor":2150050}}`,
	},

	// ---- timeline -----------------------------------------------------------
	"log_activity": {
		AR: "تسجيل ملاحظة أو مكالمة أو اجتماع، أو إضافة مهمة، على جهة اتصال أو شركة أو صفقة أو أكثر من واحدة منها. حدِّد واحدًا على " +
			"الأقل بمعرّفه، أو ببريد جهة الاتصال، أو بالاسم الدقيق للشركة، أو بالعنوان الدقيق للصفقة. يمكن أن تحمل المهمة موعد " +
			"استحقاق. تُعيد النشاط بمعرّفه.",
		Example: map[string]any{
			"kind": "task", "body": "Send the revised proposal with the pilot pricing",
			"contact": "layla.nasser@mail.example", "deal_id": "a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e", "due": "2026-10-01",
		},
		Result: `{"activity":{"id":"1b3d5f7a-9c1e-4b3d-8f5a-7c9e1b3d5f7a","kind":"task","body":"Send the revised proposal with the pilot pricing",` +
			`"due_at":"2026-10-01T00:00:00Z","done_at":null,` +
			`"contact":{"id":"3f9a2c1e-7b4d-4e8a-9c21-5d6e8f0a1b23","email":"layla.nasser@mail.example","name":"Layla Nasser","title":"Operations Director"},` +
			`"company":null,"deal":{"id":"a1c3e5f7-0b2d-4f6a-8c9e-1d3f5a7b9c0e","name":"Delta Freight — fleet tracking rollout"},` +
			`"created_by":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","created_at":"2026-09-24T10:22:00Z"}}`,
	},

	// ---- reports ------------------------------------------------------------
	"crm_report": {
		AR: "تقرير إدارة العملاء لمدة من الأيام (بتوقيت مساحة العمل؛ آخر 90 يومًا افتراضيًا، و731 يومًا كحدّ أقصى): قيمة الصفقات المفتوحة " +
			"لكل مرحلة (الآن)، والصفقات الرابحة والخاسرة مع نسبة الفوز ومتوسط الصفقة الرابحة، والتحويل بين المراحل للصفقات المفتوحة خلال " +
			"المدة (حسب أبعد مرحلة بلغتها كل صفقة)، والفوز والخسارة لكل يوم أو أسبوع أو شهر، ومصادر العملاء المحتملين (تتطلب leads:read " +
			"على الرمز، وإلا كانت null)، وزمن الرد الأول في صندوق الوارد (يتطلب inbox:read، وإلا كان null). المبالغ بالوحدة الفرعية لعملة " +
			"مساحة العمل، والصفقات بعملات أخرى تُعَدّ في حقول other_currency_* ولا تُحوَّل أبدًا. النسب بين 0 و1، وتكون null حين لا يوجد ما " +
			"يُقسَم عليه.",
		Example: map[string]any{"days": 30},
		Result: `{"from":"2026-08-26","to":"2026-09-24","days":30,"timezone":"Africa/Cairo","currency":"USD","bucket":"day",` +
			`"generated_at":"2026-09-24T10:15:00Z",` +
			`"pipeline":{"stages":[{"id":"2c4e6a8b-0d1f-4a3b-8c5d-7e9f1a3b5c7d","name":"Qualified","deal_count":4,"value_minor":3200000},` +
			`{"id":"6e8f0a2c-4d6e-4a8b-9c0d-2e4f6a8b0c1d","name":"Proposal","deal_count":1,"value_minor":1800000}],` +
			`"open_deals":5,"open_value_minor":5000000,"other_currency_deals":1},` +
			`"deals":{"opened":8,"won":3,"lost":1,"win_rate":0.75,"won_value_minor":4200000,"lost_value_minor":600000,` +
			`"avg_won_value_minor":1400000,"other_currency_won":0,"other_currency_lost":0},` +
			`"conversion":{"opened":8,"steps":[{"stage_id":"2c4e6a8b-0d1f-4a3b-8c5d-7e9f1a3b5c7d","name":"Qualified","kind":"open","reached":8,` +
			`"from_previous":null,"from_start":1},{"stage_id":"6e8f0a2c-4d6e-4a8b-9c0d-2e4f6a8b0c1d","name":"Proposal","kind":"open","reached":5,` +
			`"from_previous":0.625,"from_start":0.625}],"lost":1},` +
			`"timeline":[{"start":"2026-09-23","won":1,"lost":0,"won_value_minor":1500000,"lost_value_minor":0},` +
			`{"start":"2026-09-24","won":0,"lost":0,"won_value_minor":0,"lost_value_minor":0}],` +
			`"lead_sources":{"leads":12,"spam":2,"converted":5,"sources":[{"source":"google","leads":7,"converted":3,"won_deals":2,` +
			`"won_value_minor":2700000}]},"inbox":null}`,
	},

	// ---- ideal-customer profile and lifecycle --------------------------------
	"get_icp_profile": {
		AR: "ملف العميل المثالي لمساحة العمل: القطاعات وأحجام الشركات والمناطق (بلد الشركة) والكلمات المفتاحية، لكلٍّ منها وزن، ووزن لكل " +
			"إشارة (open_deal وwon_deal وlost_deal وinbound_lead وconversation وsubscribed وwhatsapp_opt_in وrecent_activity). تُقيَّم " +
			"كل جهة اتصال وكل شركة على أساسه من 0 إلى 100: الدرجة هي مجموع حاصل ضرب كل وزن في مدى تحقّق معياره، مقسومًا على مجموع " +
			"الأوزان الموجبة. تكون configured بقيمة false ما دامت القيم الافتراضية مستخدمة، ويبيّن scored عدد جهات الاتصال التي لها درجة.",
		Example: map[string]any{},
		Result: `{"profile":{"industries":{"values":["Logistics","Retail"],"weight":4},"sizes":{"values":["51-200","201-1000"],"weight":2},` +
			`"regions":{"values":["Egypt","Saudi Arabia"],"weight":2},"keywords":{"values":["operations","fleet"],"weight":2},` +
			`"signals":{"conversation":1,"inbound_lead":2,"lost_deal":-2,"open_deal":3,"recent_activity":2,"subscribed":1,"whatsapp_opt_in":1,"won_deal":3}},` +
			`"configured":true,"updated_at":"2026-09-10T08:00:00Z","updated_by":"7f3e9a12-6b4c-4d1e-8f2a-1b5c9d0e3f47",` +
			`"signals":["open_deal","won_deal","lost_deal","inbound_lead","conversation","subscribed","whatsapp_opt_in","recent_activity"],` +
			`"sizes":["1-10","11-50","51-200","201-1000","1000+"],"can_edit":true,"scored":412,"contacts":430}`,
	},
	"set_icp_profile": {
		AR: "تغيير ملف العميل المثالي وإعادة تقييم كل جهات الاتصال والشركات على أساسه دفعة واحدة. أرسل ما يتغيّر فقط: كل معيار يُرسَل " +
			"تحلّ قيمه ووزنه محل السابقة، أما signals فتُدمَج مع الأوزان الحالية. تُعيد الملف المحفوظ، وبعدها تعرض list_contacts مع " +
			"sort=score الترتيب الجديد وأسباب درجة كل جهة.",
		Example: map[string]any{
			"industries": map[string]any{"values": []string{"Logistics", "Retail"}, "weight": 4},
			"sizes":      map[string]any{"values": []string{"51-200", "201-1000"}, "weight": 2},
			"signals":    map[string]any{"open_deal": 3, "lost_deal": -2},
		},
		Result: `{"profile":{"industries":{"values":["Logistics","Retail"],"weight":4},"sizes":{"values":["51-200","201-1000"],"weight":2},` +
			`"regions":{"values":["Egypt"],"weight":2},"keywords":{"values":[],"weight":2},` +
			`"signals":{"conversation":1,"inbound_lead":2,"lost_deal":-2,"open_deal":3,"recent_activity":2,"subscribed":1,"whatsapp_opt_in":1,"won_deal":3}},` +
			`"configured":true,"updated_at":"2026-09-24T10:30:00Z","updated_by":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30",` +
			`"signals":["open_deal","won_deal","lost_deal","inbound_lead","conversation","subscribed","whatsapp_opt_in","recent_activity"],` +
			`"sizes":["1-10","11-50","51-200","201-1000","1000+"],"can_edit":true,"scored":430,"contacts":430}`,
	},
	"set_contact_lifecycle": {
		AR: "ضبط مرحلة جهة الاتصال في دورة الحياة يدويًا: subscriber أو lead أو qualified أو customer أو فارغ. يُسجَّل التغيير في سجلها " +
			"باسم العضو صاحب الرمز. (تدفع الأحداث المرحلة إلى الأمام تلقائيًا أيضًا: تأكيد اشتراك، أو تحويل عميل محتمل، أو فتح صفقة، " +
			"أو الفوز بها.) تُعيد جهة الاتصال مع سجل مراحلها ودرجتها.",
		Example: map[string]any{"id": "8d2e4f60-1a3b-4c5d-9e7f-0a1b2c3d4e5f", "lifecycle": "customer"},
		Result: `{"contact":{"id":"8d2e4f60-1a3b-4c5d-9e7f-0a1b2c3d4e5f","email":"karim.adel@mail.example","name":"Karim Adel",` +
			`"locale":"ar","source":"crm","phone":"","title":"Procurement Manager","tags":[],` +
			`"company":{"id":"e4f5a6b7-c8d9-4e0f-a1b2-c3d4e5f6a7b8","name":"Cairo Retail Group"},"owner_user_id":null,` +
			`"created_at":"2026-07-30T15:02:00Z","subscriptions":[],"lifecycle":"customer","lifecycle_at":"2026-09-24T10:35:00Z",` +
			`"lifecycle_history":[{"id":"3b5d7f9a-1c3e-4b5d-9f7a-1c3e5b7d9f1a","from":"qualified","to":"customer","source":"manual",` +
			`"actor_user_id":"c21d4b8e-0a9f-4e3c-b7d6-5f8a2e1c9b30","at":"2026-09-24T10:35:00Z"}],"lifecycle_history_total":3,` +
			`"score":71,"score_reasons":[{"dimension":"industry","weight":4,"value":1,"contribution":4,"matched":["Retail"],` +
			`"detail":"Retail","text":"The company's industry, Retail, is in the profile."}]}}`,
	},

	// ---- mailing lists ------------------------------------------------------
	"list_mail_lists": {
		AR:      "القوائم البريدية لمساحة العمل مرتّبة أبجديًا، مع عدد المشتركين وعدد من ينتظرون التأكيد في كل قائمة، وتنبيه على القوائم التي تطلب الاشتراك المزدوج.",
		Example: map[string]any{},
		Result: "2 list(s):\n" +
			"Customers — 214 subscribed, 0 pending\n" +
			"Newsletter — 1380 subscribed, 42 pending (double opt-in)\n",
	},
}

var docsLeads = map[string]ToolDoc{
	"list_leads": {
		AR: "العملاء المحتملون الواردون من نماذج الاستفسار على موقع مساحة العمل، الأحدث أولًا، مع عددهم لكل حالة. للتصفية استخدم status " +
			"(new أو contacted أو qualified أو converted أو spam) وq (يطابق الاسم والبريد والشركة والرسالة).",
		Example: map[string]any{"status": "new", "limit": 10},
		Result: "Counts: new 2, contacted 5, qualified 3, converted 11, spam 4\n" +
			"2 lead(s):\n" +
			"- [new] Youssef Kamal <youssef.kamal@mail.example> — Nile Couriers · google/cpc · 2026-09-23 16:42 · id=0e2a4c6e-8a0c-4e2a-9c4e-6a8c0e2a4c6e\n" +
			"  We need live tracking for 40 trucks across Cairo and Alexandria.\n" +
			"- [new] Hana Mostafa <hana.mostafa@mail.example> · newsletter/email · 2026-09-22 09:15 · id=f1a3c5e7-9b1d-4f3a-8c5e-7a9b1d3f5a7c\n" +
			"  Do you offer the dashboard in Arabic?\n",
	},
	"get_lead": {
		AR: "عميل محتمل واحد بكامل تفاصيله: الرسالة والهاتف والشركة كما كتبها الزائر، والنموذج الذي أرسله، ومصدر الزيارة (UTM والمُحيل " +
			"وgclid وصفحة الهبوط)، وما تحوّل إليه.",
		Example: map[string]any{"id": "0e2a4c6e-8a0c-4e2a-9c4e-6a8c0e2a4c6e"},
		Result: "Lead 0e2a4c6e-8a0c-4e2a-9c4e-6a8c0e2a4c6e — new\n" +
			"name: Youssef Kamal\n" +
			"email: youssef.kamal@mail.example\n" +
			"phone: +20 122 345 6789\n" +
			"company: Nile Couriers\n" +
			"form: Request a demo\n" +
			"site: https://acme.example\n" +
			"received: 2026-09-23 16:42 UTC\n" +
			"utm_source: google\n" +
			"utm_medium: cpc\n" +
			"utm_campaign: fleet-tracking-q3\n" +
			"gclid: Cj0KCQjwfake0gclid\n" +
			"referrer: https://search.example/\n" +
			"landing_page: https://acme.example/fleet-tracking\n" +
			"\nmessage:\n" +
			"We need live tracking for 40 trucks across Cairo and Alexandria.\n" +
			"Can you send pricing for a three-month pilot?\n",
	},
	"update_lead": {
		AR: "ضبط حالة عميل محتمل: new أو contacted أو qualified أو spam. أما الحالة converted فلا تُبلَغ إلا عبر convert_lead، " +
			"والعميل المحتمل المحوَّل يحتفظ بحالته.",
		Example: map[string]any{"id": "f1a3c5e7-9b1d-4f3a-8c5e-7a9b1d3f5a7c", "status": "contacted"},
		Result:  "Lead f1a3c5e7-9b1d-4f3a-8c5e-7a9b1d3f5a7c is now contacted.",
	},
	"convert_lead": {
		AR: "تحويل عميل محتمل في خطوة واحدة: جهة الاتصال (يُعاد استخدامها إن كان البريد مسجّلًا من قبل)، والشركة من نطاق البريد (ولا " +
			"شركة لعناوين البريد المجاني مثل gmail أو outlook)، وصفقة في أول مرحلة مفتوحة من مسار المبيعات، والرسالة كأول نشاط في سجلها " +
			"الزمني. يتطلب contacts:write إلى جانب leads:write.",
		Example: map[string]any{"id": "0e2a4c6e-8a0c-4e2a-9c4e-6a8c0e2a4c6e"},
		Result: "Converted lead 0e2a4c6e-8a0c-4e2a-9c4e-6a8c0e2a4c6e.\n" +
			"contact: youssef.kamal@mail.example (created) id=5e7a9c1e-3b5d-4f7a-9c1e-3b5d7f9a1c3e\n" +
			"company: Nile Couriers (created) id=e0a2c4e6-8a0c-4e2a-b4c6-e8a0c2e4a6c8\n" +
			"deal: Nile Couriers — website inquiry id=c4e6a8c0-2e4a-4c6e-8a0c-2e4a6c8e0a2c (the message is its first activity)\n",
	},
}

var argsARCRM = map[string]string{
	// contacts
	"Search name, email, phone and company":                                          "البحث في الاسم والبريد والهاتف والشركة",
	"Only contacts on this mailing list (its exact name)":                            "جهات الاتصال في هذه القائمة البريدية فقط (باسمها الدقيق)",
	"With list: only this status on that list":                                       "مع list: هذه الحالة فقط في تلك القائمة",
	"Only contacts at this lifecycle stage (none: without one)":                      "جهات الاتصال في مرحلة دورة الحياة هذه فقط (none: بلا مرحلة)",
	"Only contacts scoring at least this (0-100) against the ideal-customer profile": "جهات الاتصال التي تبلغ درجتها هذا الحدّ على الأقل (0-100) وفق ملف العميل المثالي",
	"newest (default) or score (highest first)":                                      "newest (الافتراضي) أو score (الأعلى درجة أولًا)",
	"Page size, default 100, at most 200":                                            "حجم الصفحة، 100 افتراضيًا و200 كحدّ أقصى",
	"Page size, default 25, at most 200":                                             "حجم الصفحة، 25 افتراضيًا و200 كحدّ أقصى",
	"Text to find in name, email, phone or company":                                  "النص المطلوب في الاسم أو البريد أو الهاتف أو الشركة",
	"Contact id":                                     "معرّف جهة الاتصال",
	"A company id (search_companies)":                "معرّف شركة (من search_companies)",
	"A mailing list's exact name, to subscribe them": "الاسم الدقيق لقائمة بريدية لاشتراكها فيها",
	"A mailing list's exact name, to subscribe them (needs an email)":                   "الاسم الدقيق لقائمة بريدية لاشتراكها فيها (يتطلب بريدًا إلكترونيًا)",
	"A messaging handle/username on a channel with neither a phone nor an email":        "معرّف أو اسم مستخدم على قناة مراسلة لا تعطي هاتفًا ولا بريدًا",
	"A messaging handle/username. Empty string clears it, if an email or phone remains": "معرّف أو اسم مستخدم للمراسلة. النص الفارغ يمسحه ما دام هناك بريد أو هاتف",
	"Empty string clears it, if a phone or handle remains":                              "النص الفارغ يمسحه ما دام هناك هاتف أو معرّف",
	"Empty string clears it, if an email or handle remains":                             "النص الفارغ يمسحه ما دام هناك بريد أو معرّف",
	"Required if phone is omitted":                                                      "مطلوب إذا لم يُعطَ هاتف",
	"Required if email is omitted":                                                      "مطلوب إذا لم يُعطَ بريد إلكتروني",
	"With country code, e.g. +20 101 234 5678":       "مع رمز الدولة، مثل +20 101 234 5678",
	"Job title":                          "المسمّى الوظيفي",
	"A company id; empty string unlinks": "معرّف شركة؛ النص الفارغ يفكّ الربط",
	"A member's user id or email; empty string unassigns": "معرّف مستخدم العضو أو بريده؛ النص الفارغ يلغي الإسناد",
	"Replaces the tags (at most 20)":                      "تحلّ محل الوسوم الحالية (20 وسمًا كحدّ أقصى)",
	"Set the lifecycle stage by hand (recorded as yours)": "ضبط مرحلة دورة الحياة يدويًا (يُسجَّل باسمك)",

	// companies
	"Search name and domain":                                "البحث في الاسم والنطاق",
	"name (A-Z, default) or score (highest first)":          "name (أبجديًا، الافتراضي) أو score (الأعلى درجة أولًا)",
	"Only companies scoring at least this (0-100)":          "الشركات التي تبلغ درجتها هذا الحدّ على الأقل (0-100)",
	"Page size, default 50, at most 200":                    "حجم الصفحة، 50 افتراضيًا و200 كحدّ أقصى",
	"Text to find in the name or domain":                    "النص المطلوب في الاسم أو النطاق",
	"Company id":                                            "معرّف الشركة",
	"Company name":                                          "اسم الشركة",
	"e.g. acme.com — one company per domain in a workspace": "مثل acme.com — شركة واحدة لكل نطاق في مساحة العمل",
	"Country or region, e.g. Egypt — the ideal-customer profile's regions match it": "البلد أو المنطقة، مثل Egypt — تُطابَق عليه مناطق ملف العميل المثالي",
	"Country or region": "البلد أو المنطقة",
	"Activities per page, default 50, at most 200 (conversations and deal events in the same stretch of time come with them)": "عدد الأنشطة في الصفحة، 50 افتراضيًا و200 كحدّ أقصى (وتأتي معها المحادثات وأحداث الصفقات الواقعة في المدة الزمنية نفسها)",

	// deals
	"Only this stage's deals":                    "صفقات هذه المرحلة فقط",
	"Page size, default 50, at most 500":         "حجم الصفحة، 50 افتراضيًا و500 كحدّ أقصى",
	"Deal id":                                    "معرّف الصفقة",
	"Major units, e.g. 1500.00":                  "بالوحدة الرئيسية للعملة، مثل 1500.00",
	"3-letter code, e.g. USD":                    "رمز من 3 أحرف، مثل USD",
	"A stage id or name (list_deals shows them)": "معرّف مرحلة أو اسمها (تعرضها list_deals)",
	"YYYY-MM-DD":                                 "بصيغة YYYY-MM-DD",
	"Major units":                                "بالوحدة الرئيسية للعملة",
	"3-letter code":                              "رمز من 3 أحرف",
	"A stage id or name":                         "معرّف مرحلة أو اسمها",
	"Empty string unlinks":                       "النص الفارغ يفكّ الربط",
	"YYYY-MM-DD; empty string clears":            "بصيغة YYYY-MM-DD؛ النص الفارغ يمسحه",
	"The deal's exact title, if you have no id":  "العنوان الدقيق للصفقة، إن لم يكن لديك معرّفها",
	"The destination stage's id or name":         "معرّف المرحلة المقصودة أو اسمها",

	// timeline
	"A contact's email, instead of contact_id":      "بريد جهة الاتصال، بدلًا من contact_id",
	"A company's exact name, instead of company_id": "الاسم الدقيق للشركة، بدلًا من company_id",
	"A deal's exact title, instead of deal_id":      "العنوان الدقيق للصفقة، بدلًا من deal_id",
	"Tasks only: YYYY-MM-DD or RFC 3339":            "للمهام فقط: بصيغة YYYY-MM-DD أو RFC 3339",

	// crm_report
	"First day, YYYY-MM-DD (default: 89 days before to)":                            "اليوم الأول بصيغة YYYY-MM-DD (افتراضيًا: قبل to بـ 89 يومًا)",
	"Last day, inclusive, YYYY-MM-DD (default: today in the workspace's time zone)": "اليوم الأخير شاملًا، بصيغة YYYY-MM-DD (افتراضيًا: اليوم بتوقيت مساحة العمل)",
	"Without from: the window of this many days ending on to, e.g. 30 (default 90)": "دون from: نافذة بهذا العدد من الأيام تنتهي في to، مثل 30 (الافتراضي 90)",

	// ideal-customer profile
	"Company industries that fit":                                                                 "قطاعات الشركات الملائمة",
	"Company sizes that fit: 1-10, 11-50, 51-200, 201-1000, 1000+":                                "أحجام الشركات الملائمة: 1-10 و11-50 و51-200 و201-1000 و1000+",
	"Company countries or regions that fit":                                                       "بلدان الشركات أو مناطقها الملائمة",
	"Words that mark a fit in a contact's title and tags or a company's name, industry and notes": "كلمات تدلّ على الملاءمة في المسمّى الوظيفي لجهة الاتصال ووسومها، أو في اسم الشركة وقطاعها وملاحظاتها",
	`signal name -> weight (-10 to 10), e.g. {"open_deal": 3, "lost_deal": -2}`:                   `اسم الإشارة -> الوزن (من -10 إلى 10)، مثل {"open_deal": 3, "lost_deal": -2}`,
	"-10 to 10; negative is a penalty, 0 turns it off":                                            "من -10 إلى 10؛ القيمة السالبة عقوبة، والصفر يعطّل المعيار",

	// leads
	"Search name, email, company and message": "البحث في الاسم والبريد والشركة والرسالة",
	"At most this many (default 50, max 500)": "هذا العدد على الأكثر (50 افتراضيًا و500 كحدّ أقصى)",
	"Lead id": "معرّف العميل المحتمل",
}
