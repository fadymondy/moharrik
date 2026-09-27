package mcp

import "strings"

// Docs for OrganiseTools (MH-657, user_tools_organise.go). See docs.go.

var inboxExOrganised = strings.Replace(inboxExWhatsApp, `"unread":true}`,
	`"unread":true,"pinned":true,"archived":false,"muted_until":"2026-09-24T22:05:12Z","favourite":true,`+
		`"color":"teal","icon":"star","labels":[{"id":"5b1e7c93-2a4d-4f6e-8b0c-9d3a1e7f5c28","name":"VIP","color":"violet"}],"blocked":false}`, 1)

var docsOrganise = map[string]ToolDoc{
	"organise_conversation": {
		AR: "تنظيم محادثة كما تفعل قائمة السياق في صف الوارد. ما يخصّك وحدك: التثبيت pinned (أعلى قائمتك، 5 محادثات على الأكثر، " +
			"والتثبيت يلغي الأرشفة)، والأرشفة archived (تختفي من قائمتك حتى يكتب العميل مجددًا ما لم تكن مكتومة، والأرشفة تلغي التثبيت)، " +
			"والكتم mute (8h أو 1w أو always — بلا تنبيهات لك؛ والقيمة الفارغة تلغي الكتم)، والمفضلة favourite. وما يخصّ الفريق " +
			"(inbox:write): اللون color والأيقونة icon والتصنيفات label_ids — المجموعة الكاملة التي تحل محل الحالية " +
			"(تعرض list_conversation_labels المعرّفات). مرّر ما يتغيّر فقط. والحظر عبر block_conversation.",
		Example: map[string]any{"id": "3c9a7e21-4b6d-4f80-a2c5-9e1d7b3f6a04", "pinned": true, "mute": "8h", "color": "teal",
			"label_ids": []string{"5b1e7c93-2a4d-4f6e-8b0c-9d3a1e7f5c28"}},
		Result: `{"conversation":` + inboxExOrganised + `}`,
	},
	"block_conversation": {
		AR: "حظر عميل المحادثة أو إلغاء حظره. أثناء الحظر تُهمَل رسائله ولا يمكن الرد على المحادثة؛ وفي واتساب يُحظر الرقم أيضًا " +
			"على الهاتف المرتبط بالمساحة. مرّر blocked:false لإلغاء الحظر.",
		Example: map[string]any{"id": "3c9a7e21-4b6d-4f80-a2c5-9e1d7b3f6a04", "blocked": true},
		Result:  `{"conversation":` + strings.Replace(inboxExOrganised, `"blocked":false`, `"blocked":true`, 1) + `}`,
	},
	"clear_conversation": {
		AR:      "حذف كل رسائل المحادثة ومرفقاتها مع إبقاء المحادثة نفسها وعميلها وربطها بجهة الاتصال وتصنيفاتها. لا يمكن التراجع عن ذلك.",
		Example: map[string]any{"id": "3c9a7e21-4b6d-4f80-a2c5-9e1d7b3f6a04"},
		Result:  `{"cleared":true,"id":"3c9a7e21-4b6d-4f80-a2c5-9e1d7b3f6a04"}`,
	},
	"delete_conversation": {
		AR: "حذف محادثة بكل رسائلها ومرفقاتها. تبقى جهة اتصال العميل في إدارة العملاء. لا يمكن التراجع عن ذلك، " +
			"وإذا كتب العميل مجددًا تبدأ محادثة جديدة.",
		Example: map[string]any{"id": "3c9a7e21-4b6d-4f80-a2c5-9e1d7b3f6a04"},
		Result:  `{"deleted":true,"id":"3c9a7e21-4b6d-4f80-a2c5-9e1d7b3f6a04"}`,
	},
	"list_conversation_labels": {
		AR:      "تصنيفات المحادثات في المساحة، لكلٍّ منها معرّفها واسمها ولونها وعدد المحادثات التي تحمله. صفِّ list_conversations بأحدها عبر label_id.",
		Example: map[string]any{},
		Result: `{"count":2,"labels":[{"color":"violet","count":4,"created_at":"2026-09-20T10:00:00Z","id":"5b1e7c93-2a4d-4f6e-8b0c-9d3a1e7f5c28","name":"VIP"},` +
			`{"color":"amber","count":11,"created_at":"2026-09-21T08:30:00Z","id":"9e2c4a61-7b3d-4f5e-a1c8-3d6f9b2e4a70","name":"Wholesale"}]}`,
	},
	"create_conversation_label": {
		AR:      "إنشاء تصنيف للمحادثات في المساحة. الأسماء فريدة بلا تفريق بين حروف كبيرة وصغيرة، واللون الافتراضي gray.",
		Example: map[string]any{"name": "VIP", "color": "violet"},
		Result:  `{"label":{"color":"violet","count":0,"created_at":"2026-09-20T10:00:00Z","id":"5b1e7c93-2a4d-4f6e-8b0c-9d3a1e7f5c28","name":"VIP"}}`,
	},
	"update_conversation_label": {
		AR:      "إعادة تسمية تصنيف أو تغيير لونه. تظهر التغييرات في كل محادثة تحمله.",
		Example: map[string]any{"id": "5b1e7c93-2a4d-4f6e-8b0c-9d3a1e7f5c28", "color": "pink"},
		Result:  `{"label":{"color":"pink","count":4,"created_at":"2026-09-20T10:00:00Z","id":"5b1e7c93-2a4d-4f6e-8b0c-9d3a1e7f5c28","name":"VIP"}}`,
	},
	"delete_conversation_label": {
		AR:      "حذف تصنيف. يُزال عن كل محادثة كانت تحمله، وتبقى المحادثات نفسها.",
		Example: map[string]any{"id": "5b1e7c93-2a4d-4f6e-8b0c-9d3a1e7f5c28"},
		Result:  `{"deleted":true,"id":"5b1e7c93-2a4d-4f6e-8b0c-9d3a1e7f5c28"}`,
	},
}

var argsAROrganise = map[string]string{
	"Pin it to the top of your inbox, or unpin it":                                                                "تثبيتها أعلى صندوق الوارد لديك، أو إلغاء تثبيتها",
	"Archive it in your inbox, or bring it back":                                                                  "أرشفتها في صندوق الوارد لديك، أو إعادتها",
	"Mute your alerts for 8h, 1w or always; empty string unmutes":                                                 "كتم تنبيهاتك لمدة 8h أو 1w أو always؛ القيمة الفارغة تلغي الكتم",
	"Add it to your favourites, or remove it":                                                                     "إضافتها إلى مفضلتك، أو إزالتها منها",
	"The chat's colour for the whole team; empty string clears it":                                                "لون المحادثة للفريق كله؛ القيمة الفارغة تزيله",
	"The chat's icon for the whole team, a lowercase icon name such as star or briefcase; empty string clears it": "أيقونة المحادثة للفريق كله، باسم أيقونة بحروف صغيرة مثل star أو briefcase؛ القيمة الفارغة تزيلها",
	"The full set of label ids the chat carries; an empty list removes them all":                                  "المجموعة الكاملة لمعرّفات التصنيفات التي تحملها المحادثة؛ القائمة الفارغة تزيلها كلها",
	"true blocks the customer, false unblocks them":                                                               "true تحظر العميل، وfalse تلغي حظره",
	"The label's name, at most 40 characters":                                                                     "اسم التصنيف، 40 حرفًا على الأكثر",
	"The label's colour, gray by default":                                                                         "لون التصنيف، gray افتراضيًا",
	"The label's new colour":                                                                                      "اللون الجديد للتصنيف",
	"The label's id (list_conversation_labels)":                                                                   "معرّف التصنيف (list_conversation_labels)",
	"MH-657: your archived conversations instead of the rest":                                                     "MH-657: محادثاتك المؤرشفة بدلًا من البقية",
	"MH-657: only your favourites":                                                                                "MH-657: مفضلتك فقط",
	"MH-657: only group chats":                                                                                    "MH-657: المحادثات الجماعية فقط",
	"MH-657: only conversations carrying this label (list_conversation_labels)":                                   "MH-657: المحادثات التي تحمل هذا التصنيف فقط (list_conversation_labels)",
}
