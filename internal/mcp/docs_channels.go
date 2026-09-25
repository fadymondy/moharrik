package mcp

// Docs for the Telegram/Slack/Discord/workflow-channel tools
// (user_tools_channels.go, MH-563/MH-564). See docs.go.

const telegramChannelEx = `{"allowed_chat_ids":"","bot_username":"acme_support_bot","enabled":true,"has_token":true,` +
	`"id":"6c1f2e3a-8b4d-4e5f-9a0b-1c2d3e4f5a6b","last_delivery_at":"2026-09-24T12:30:05Z","last_error":"",` +
	`"last_test_ok":true,"name":"Sales","webhook_url":"https://app.moharrik.com/api/telegram/hook/9f8e7d6c-…"}`

const slackChannelEx = `{"allowed_channel_ids":"","bot_user_id":"U0ACME123","enabled":true,"has_bot_token":true,` +
	`"has_signing_secret":true,"id":"7d2e3f4b-9c5e-4f6a-8b1c-2d3e4f5a6b7c","last_delivery_at":"2026-09-24T12:30:05Z",` +
	`"last_error":"","last_test_ok":true,"name":"Support","team_name":"Acme Inc",` +
	`"webhook_url":"https://app.moharrik.com/api/slack/hook/8e7d6c5b-…"}`

const discordChannelEx = `{"allowed_channel_ids":"","allowed_guild_ids":"","bot_username":"AcmeBot#0001","enabled":true,` +
	`"gateway_state":"connected","has_token":true,"id":"8e3f4a5c-0d6f-4a7b-9c2d-3e4f5a6b7c8d",` +
	`"interactions_url":"https://app.moharrik.com/api/discord/hook/7d6c5b4a-…","last_delivery_at":"2026-09-24T12:30:05Z",` +
	`"last_error":"","last_test_ok":true,"name":"Community"}`

const workflowChannelEx = `{"allow_replies":false,"enabled":true,"id":"9f4a5b6d-1e7a-4b8c-0d3e-4f5a6b7c8d9e",` +
	`"last_error":"","last_pushed_count":12,"last_run_at":"2026-09-24T12:00:00Z","name":"LinkedIn inbox",` +
	`"network":"linkedin","workflow_id":"3a4b5c6d-7e8f-4a9b-0c1d-2e3f4a5b6c7d"}`

const channelConnectionEx = `{"channels":[` +
	`{"conversations_30d":4,"created_at":"2026-08-01T09:00:00Z","enabled":true,"id":"6c1f2e3a-8b4d-4e5f-9a0b-1c2d3e4f5a6b",` +
	`"last_error":"delivery refused","last_error_at":"2026-09-24T11:02:19Z","last_message_at":"2026-09-24T11:00:00Z",` +
	`"name":"Sales","status":"error","type":"telegram","unread":1},` +
	`{"conversations_30d":19,"created_at":"2026-07-10T09:00:00Z","enabled":true,"id":"7d2e3f4b-9c5e-4f6a-8b1c-2d3e4f5a6b7c",` +
	`"last_message_at":"2026-09-24T12:30:05Z","name":"Support","status":"connected","type":"email","unread":3}],` +
	`"count":2,"next_cursor":""}`

var docsChannels = map[string]ToolDoc{
	"list_channels": {
		AR: "صفحة القنوات الواحدة: كل اتصالات قنوات الوارد في مساحة العمل — البريد ونافذة المحادثة وواتساب وتيليجرام وSlack وديسكورد وقنوات سير العمل " +
			"(لينكدإن/فيسبوك/إنستغرام/إكس) — في قائمة واحدة، بحالة كل قناة ونشاطها الأخير. القناة التي بها خطأ تظهر أولًا ثم البقية أبجديًا. " +
			"صفِّ بالنوع (يمكن تكراره) أو الحالة (connected أو error أو paused) أو q (مطابقة جزئية غير حساسة لحالة الأحرف على اسم القناة، وعلى رقم الهاتف في حالة واتساب). " +
			"لا يُعاد أي رمز أو سرّ مهما كان نوع القناة.",
		Result: channelConnectionEx,
	},
	"list_telegram_channels": {
		AR: "بوتات تيليجرام المتصلة بمساحة العمل، بحالتها وصحة الرابط Webhook. رمز البوت لا يُعاد أبدًا، بل has_token فقط. " +
			"الرسائل الواردة تظهر في الوارد المشترك موسومة بهذه القناة، والرد عليها يمر عبر واجهة تيليجرام نفسها.",
		Result: `{"channels":[` + telegramChannelEx + `],"count":1}`,
	},
	"connect_telegram_channel": {
		AR: "إضافة اتصال بوت تيليجرام (من BotFather@). بمجرد إرسال الرمز يُسجَّل رابط الاستقبال Webhook لدى تيليجرام فورًا. " +
			"allowed_chat_ids قائمة معرّفات محادثات مفصولة بفواصل؛ تركها فارغة يسمح بأي محادثة تصل إلى البوت.",
		Example: map[string]any{"name": "Sales", "bot_token": "123456:ABC-DEF..."},
		Result:  `{"channel":` + telegramChannelEx + `}`,
	},
	"test_telegram_channel": {
		AR:      "التحقق من رمز بوت تيليجرام (getMe) وإعادة تسجيل رابط الاستقبال Webhook الخاص به.",
		Example: map[string]any{"id": "6c1f2e3a-8b4d-4e5f-9a0b-1c2d3e4f5a6b"},
		Result:  `{"channel":` + telegramChannelEx + `,"ok":true}`,
	},
	"list_slack_channels": {
		AR: "تطبيقات Slack المتصلة بمساحة العمل، بحالتها وصحة الرابط Webhook. رمز البوت وسرّ التوقيع لا يُعادان أبدًا. " +
			"الرسائل الواردة تظهر في الوارد المشترك موسومة بهذه القناة، والرد عليها يمر عبر واجهة Slack نفسها.",
		Result: `{"channels":[` + slackChannelEx + `],"count":1}`,
	},
	"connect_slack_channel": {
		AR: "إضافة اتصال تطبيق Slack (رمز بوت وسرّ توقيع من صفحات تطبيق Slack نفسه). تعيد webhook_url — " +
			"الصِقها في حقل Request URL ضمن Event Subscriptions بتطبيق Slack. allowed_channel_ids قائمة معرّفات قنوات مفصولة بفواصل؛ فارغة تعني كل قناة أو محادثة خاصة يشارك فيها البوت.",
		Example: map[string]any{"name": "Support", "bot_token": "xoxb-…", "signing_secret": "8f6e4d2c…"},
		Result:  `{"channel":` + slackChannelEx + `}`,
	},
	"test_slack_channel": {
		AR:      "التحقق من رمز بوت Slack (auth.test) وتسجيل هوية الفريق والبوت.",
		Example: map[string]any{"id": "7d2e3f4b-9c5e-4f6a-8b1c-2d3e4f5a6b7c"},
		Result:  `{"channel":` + slackChannelEx + `,"ok":true}`,
	},
	"list_discord_channels": {
		AR: "بوتات ديسكورد المتصلة بمساحة العمل، بحالتها وحالة اتصال البوابة Gateway. رمز البوت لا يُعاد أبدًا. " +
			"لأن ديسكورد لا توفر رابط Webhook للرسائل العادية، تصل هذه الرسائل عبر اتصال بوابة دائم يبدأ فور نجاح الاتصال.",
		Result: `{"channels":[` + discordChannelEx + `],"count":1}`,
	},
	"connect_discord_channel": {
		AR: "إضافة اتصال بوت ديسكورد (رمز من صفحة Bot في بوابة المطورين). فور إرسال الرمز تبدأ جلسة بوابة Gateway على الفور — " +
			"وهي الطريقة الوحيدة لاستقبال الرسائل العادية والمحادثات الخاصة. allowed_guild_ids وallowed_channel_ids قوائم معرّفات مفصولة بفواصل؛ فارغة تعني كل خادم أو قناة.",
		Example: map[string]any{"name": "Community", "bot_token": "MTIzNDU2Nzg5MDEyMzQ1Njc4.…"},
		Result:  `{"channel":` + discordChannelEx + `}`,
	},
	"test_discord_channel": {
		AR:      "التحقق من رمز بوت ديسكورد (GET /users/@me) وإعادة تشغيل جلسة البوابة Gateway الخاصة به عند الحاجة.",
		Example: map[string]any{"id": "8e3f4a5c-0d6f-4a7b-9c2d-3e4f5a6b7c8d"},
		Result:  `{"channel":` + discordChannelEx + `,"ok":true}`,
	},
	"list_workflow_channels": {
		AR: "قنوات الوارد التي يغذّيها سير عمل متصفّح مجدوَل (MH-564): لينكدإن وفيسبوك وإنستغرام وإكس، لكل شبكة لا تملك واجهة برمجية صالحة. " +
			"تُملأ كل قناة بخطوة \"دفع إلى الوارد\" داخل سير العمل المرتبط بها.",
		Result: `{"channels":[` + workflowChannelEx + `],"count":1}`,
	},
	"connect_workflow_channel": {
		AR: "إضافة قناة وارد بسير عمل متصفّح لشبكة لا تملك واجهة برمجية صالحة: linkedin أو facebook أو instagram أو x. " +
			"اربطها بسير عمل (list_workflows) يقرأ وارد تلك الشبكة بخطوة \"دفع إلى الوارد\"؛ يمكن ضبط سير العمل لاحقًا.",
		Example: map[string]any{"name": "LinkedIn inbox", "network": "linkedin"},
		Result:  `{"channel":` + workflowChannelEx + `}`,
	},
}

var argsARChannels = map[string]string{
	"A label for this connection":                                           "تسمية لهذا الاتصال",
	"From @BotFather":                                                       "من BotFather@",
	"Comma-separated chat/group ids; empty allows every chat":               "معرّفات محادثات/مجموعات مفصولة بفواصل؛ فارغة تعني كل محادثة",
	"The connection's id":                                                   "معرّف هذا الاتصال",
	"xoxb-… from OAuth & Permissions":                                       "xoxb-… من OAuth & Permissions",
	"From Basic Information":                                                "من Basic Information",
	"Comma-separated Slack channel ids; empty allows every channel/DM":      "معرّفات قنوات Slack مفصولة بفواصل؛ فارغة تعني كل قناة أو محادثة خاصة",
	"From the developer portal's Bot page":                                  "من صفحة Bot في بوابة المطورين",
	"For verifying interactions":                                            "للتحقق من تفاعلات Interactions",
	"Comma-separated guild ids; empty allows every guild":                   "معرّفات خوادم Discord مفصولة بفواصل؛ فارغة تعني كل خادم",
	"Comma-separated channel ids; empty allows every channel in an allowed guild, plus DMs": "معرّفات قنوات مفصولة بفواصل؛ فارغة تعني كل قناة ضمن خادم مسموح به، إضافةً إلى المحادثات الخاصة",
	"The workflow that reads this network's inbox; can be set later":        "سير العمل الذي يقرأ وارد هذه الشبكة؛ يمكن ضبطه لاحقًا",
	"Reply through the network's reply preset, with confirmation (off by default)": "الرد عبر قالب الرد الجاهز لهذه الشبكة، مع تأكيد (معطّل افتراضيًا)",
	"Only these types; empty means every type":                                     "هذه الأنواع فقط؛ تركها فارغة يعني كل الأنواع",
	"Case-insensitive substring match on the connection's name (and, for WhatsApp, its phone)": "مطابقة جزئية غير حساسة لحالة الأحرف على اسم الاتصال (وعلى رقم الهاتف في حالة واتساب)",
}
