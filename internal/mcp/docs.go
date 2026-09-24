package mcp

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/fadymondy/moharrik/internal/authz"
)

/*
The MCP tool reference (MH-428), generated from the registry.

What a tool is called, what it does in English, the scope it needs and its
arguments come from UserTools() itself: the docs page cannot list a tool that
does not exist or miss one that does. The parts code cannot know — the Arabic
text, an example call and its result — live in the docs_*.go maps next to
this file, keyed by tool name. docs_test.go fails when a tool has none, when
an example passes an argument the tool does not take or leaves out a required
one, when an argument's English description has no Arabic, or when a doc
names a tool that is gone. Adding a tool without its docs therefore fails CI.

GET /api/mcp/docs serves it, anonymously: the docs are public, and nothing in
them is a secret (the same names and schemas tools/list hands any token).
*/

// ToolDoc is the hand-written half of one tool's reference.
type ToolDoc struct {
	// AR is the tool's description in Arabic. The English one is Tool.Description.
	AR string
	// Example is a realistic arguments object for tools/call; empty for a tool
	// that takes none. Every key must be one of the tool's arguments.
	Example map[string]any
	// Result is what that call answers, shortened with "…" where a list goes
	// on. JSON when the tool answers in JSON.
	Result string
	// Group puts the tool in another section than its scope's, by section id:
	// accept_company_suggestion needs contacts:write but belongs with research.
	Group string
}

// docGroup is one section of the reference: the tools whose scope is listed.
type docGroup struct {
	ID     string
	Title  [2]string // en, ar
	Intro  [2]string
	Scopes []string
}

var docGroups = []docGroup{
	{"workspace", [2]string{"Workspace", "مساحة العمل"},
		[2]string{"Who the token acts as, and the members an agent can assign work to.",
			"من يمثّله الرمز، والأعضاء الذين يمكن للوكيل إسناد العمل إليهم."},
		[]string{"members:read"}},
	{"crm", [2]string{"CRM", "إدارة العملاء"},
		[2]string{"Contacts, companies, deals, the timeline, mailing lists, the CRM report and the ideal-customer profile every record is scored against.",
			"جهات الاتصال والشركات والصفقات والسجل الزمني والقوائم البريدية وتقرير إدارة العملاء، وملف العميل المثالي الذي يُقيَّم كل سجل على أساسه."},
		[]string{"contacts:read", "contacts:write", "crm:manage"}},
	{"leads", [2]string{"Leads", "العملاء المحتملون"},
		[2]string{"Inquiries from your website forms, with their attribution, and turning one into a contact, a company and a deal.",
			"الاستفسارات الواردة من نماذج موقعك مع مصدر كل منها، وتحويل الاستفسار إلى جهة اتصال وشركة وصفقة."},
		[]string{"leads:read", "leads:write"}},
	{"inbox", [2]string{"Inbox and WhatsApp", "الوارد وواتساب"},
		[2]string{"One inbox for the chat widget, email and WhatsApp: read, reply, assign, snooze, link to the CRM, and message opted-in contacts on WhatsApp.",
			"صندوق وارد واحد لنافذة المحادثة والبريد وواتساب: القراءة والرد والإسناد والتأجيل والربط بإدارة العملاء، ومراسلة جهات الاتصال الموافِقة على واتساب."},
		[]string{"inbox:read", "inbox:write", "inbox:manage", "whatsapp:send"}},
	{"campaigns", [2]string{"Campaigns", "الحملات"},
		[2]string{"Email and WhatsApp campaigns to your mailing lists: draft, preview, send and follow their delivery.",
			"حملات البريد وواتساب إلى قوائمك البريدية: الصياغة والمعاينة والإرسال ومتابعة التسليم."},
		[]string{"campaigns:read", "campaigns:write"}},
	{"notifications", [2]string{"Notification center", "مركز الإشعارات"},
		[2]string{"The feed of everything collected for the workspace, the webhook sources that post into it, and the sources it polls.",
			"موجز كل ما جُمع لمساحة العمل، ومصادر Webhook التي ترسل إليه، والمصادر التي يستطلعها دوريًا."},
		[]string{"notifications:read", "notifications:write", "notifications:manage"}},
	{"trends", [2]string{"Trends", "الاتجاهات"},
		[2]string{"The news and community sources the workspace watches, what they collected, and the stories trending across them.",
			"مصادر الأخبار والمجتمعات التي تتابعها مساحة العمل، وما جمعته، والقصص الرائجة عبرها."},
		[]string{"trends:read", "trends:write", "trends:manage"}},
	{"automation", [2]string{"Browser automation", "أتمتة المتصفّح"},
		[2]string{"Drive a browser paired through the Chrome extension, step by step or with a saved workflow, and read or change which sites it may drive.",
			"قيادة متصفّح مقترن عبر إضافة كروم، خطوة بخطوة أو بسير عمل محفوظ، وقراءة المواقع التي يحق له قيادتها أو تغييرها."},
		[]string{"automation:read", "automation:run", "automation:manage"}},
	{"research", [2]string{"Research and prospects", "البحث والعملاء المرتقبون"},
		[2]string{"Ask a question; Moharrik searches the web, reads the results in a real browser and answers with its sources. Discovery turns research into scored prospects with their evidence, and drafts outreach that is never sent on its own.",
			"اطرح سؤالًا، فيبحث محرّك في الويب ويقرأ النتائج في متصفّح حقيقي ويجيب مع مصادره. ويحوّل الاستكشاف نتائج البحث إلى عملاء مرتقبين مقيَّمين مع أدلتهم، ويصوغ رسائل تواصل لا تُرسل من تلقاء نفسها."},
		[]string{"research:read", "research:run"}},
	{"google", [2]string{"Google Analytics, Search Console and YouTube", "Google Analytics وSearch Console ويوتيوب"},
		[2]string{"The GA4 properties, Search Console sites and YouTube channels connected to the workspace.",
			"خصائص GA4 ومواقع Search Console وقنوات يوتيوب المتصلة بمساحة العمل."},
		[]string{"analytics:read", "search_console:write"}},
	{"audit", [2]string{"Audit log", "سجل التدقيق"},
		[2]string{"Every change in the workspace: who made it — a member, a token, a connected app or a browser — what changed and from where.",
			"كل تغيير في مساحة العمل: من أجراه — عضو أو رمز أو تطبيق متصل أو متصفّح — وما الذي تغيّر ومن أين."},
		[]string{"audit:read"}},
}

// scopeAR is each user scope's description in Arabic (UserScopes has the English).
var scopeAR = map[string]string{
	"members:read":         "رؤية مساحة العمل ودورك وأعضائها (whoami وlist_members)",
	"contacts:read":        "قراءة جهات الاتصال والشركات والصفقات والسجل الزمني في إدارة العملاء",
	"contacts:write":       "إنشاء جهات الاتصال والشركات والصفقات وتعديلها والإضافة إلى السجل الزمني",
	"crm:manage":           "تغيير ملف العميل المثالي الذي تُقيَّم على أساسه كل جهة اتصال وشركة",
	"leads:read":           "قراءة العملاء المحتملين الواردين من نماذج الاستفسار على موقعك، مع رسائلهم ومصادرهم",
	"leads:write":          "تغيير حالة العميل المحتمل وتحويله (التحويل يحتاج contacts:write أيضًا)",
	"inbox:read":           "قراءة المحادثات عبر نافذة المحادثة والبريد وواتساب، وحالة رقم واتساب",
	"inbox:write":          "الرد على المحادثات وإسنادها وإغلاقها",
	"inbox:manage":         "ربط رقم واتساب الخاص بمساحة العمل",
	"whatsapp:send":        "مراسلة جهة اتصال وافقت على واتساب (المحادثة الجديدة تحتاج رمز تأكيد)",
	"campaigns:read":       "قراءة حملات البريد وواتساب ونتائجها",
	"campaigns:write":      "صياغة الحملات وتعديلها وإرسالها",
	"automation:read":      "قراءة الأتمتة ووكلاء المتصفّح وعمليات تشغيلها",
	"automation:run":       "بدء عمليات الأتمتة وإلغاؤها",
	"automation:manage":    "تغيير المواقع التي يحق لمتصفّح قيادتها",
	"notifications:read":   "قراءة مركز الإشعارات ومصادر Webhook والمصادر المجمَّعة",
	"notifications:write":  "تعليم الإشعارات مقروءة أو غير مقروءة، وحفظها لوقت لاحق",
	"notifications:manage": "إنشاء مصادر Webhook وتدويرها وتفعيلها وتعطيلها، وإضافة المصادر المجمَّعة وتغييرها وجمعها",
	"research:read":        "قراءة عمليات البحث والأدلة التي جمعتها",
	"research:run":         "بدء عمليات البحث",
	"trends:read":          "قراءة المصادر والفهرس والميزانية والعناصر المجمَّعة والمواضيع الرائجة",
	"trends:write":         "حفظ العناصر، ومراجعة المواضيع الرائجة أو استبعادها أو حفظها",
	"trends:manage":        "إضافة المصادر وتغييرها، والجمع الآن (يستهلك الميزانية اليومية)",
	"analytics:read":       "قراءة بيانات Google Analytics وSearch Console ويوتيوب الخاصة بمساحة العمل",
	"search_console:write": "فحص عناوين URL وإرسال خرائط الموقع في Search Console",
	"audit:read":           "قراءة سجل التدقيق: من غيّر ماذا، ومتى، ومن أين",
}

// toolDocs is every tool's hand-written half, merged from the docs_*.go files.
func toolDocs() map[string]ToolDoc {
	out := map[string]ToolDoc{}
	for _, set := range []map[string]ToolDoc{
		docsWorkspace, docsCRM, docsLeads, docsInbox, docsCampaigns,
		docsNotifications, docsTrends, docsAutomation, docsRules, docsBrowserRun, docsResearch, docsProspects, docsGoogle, docsAudit,
		docsMailbox, docsChannels,
	} {
		for k, v := range set {
			out[k] = v
		}
	}
	return out
}

// argAR is the Arabic for an argument's English description, keyed by that
// English text: when the English changes, the Arabic is missing again and
// docs_test.go says so.
func argAR() map[string]string {
	out := map[string]string{}
	for _, set := range []map[string]string{
		argsARCommon, argsARCRM, argsARInbox, argsARNotifications, argsARTools, argsARProspects,
		argsARRules, argsARBrowserRun, argsARMailbox, argsARChannels,
	} {
		for k, v := range set {
			out[k] = v
		}
	}
	return out
}

// argsARCommon covers the arguments many tools share.
var argsARCommon = map[string]string{
	"next_cursor from the previous answer": "قيمة next_cursor من الإجابة السابقة",
	"next_cursor from the previous page":   "قيمة next_cursor من الصفحة السابقة",
}

// ---- the catalog --------------------------------------------------------

// DocArg is one argument of a tool, flattened from its JSON schema.
type DocArg struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
	Enum        []any  `json:"enum,omitempty"`
}

// DocTool is one tool's entry in the reference.
type DocTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Scope       string         `json:"scope"`
	MinRole     authz.Role     `json:"min_role"`
	Writes      bool           `json:"writes"`
	Arguments   []DocArg       `json:"arguments"`
	InputSchema map[string]any `json:"input_schema"`
	// ExampleCall is the whole JSON-RPC request an MCP client sends.
	ExampleCall   map[string]any `json:"example_call"`
	ExampleResult string         `json:"example_result"`
}

// DocGroup is one section of the reference.
type DocGroup struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	Intro string    `json:"intro"`
	Tools []DocTool `json:"tools"`
}

// DocScope is one grantable scope, the lowest role that may hold it, and the
// tools it opens.
type DocScope struct {
	Scope       string     `json:"scope"`
	Description string     `json:"description"`
	MinRole     authz.Role `json:"min_role"`
	Tools       []string   `json:"tools"`
}

// Catalog is the whole reference in one language.
type Catalog struct {
	Lang string `json:"lang"`
	// ServerURL is MCPServerURL() (oauth.go): the dedicated mcp.<domain> host
	// once MCP_PUBLIC_URL names one (MH-516), else the app's own /api/mcp.
	// The single source Settings' server_url field also reads — the docs and
	// the settings page never say different things about where to connect.
	ServerURL string `json:"server_url"`
	// LegacyURL is the app's /api/mcp when ServerURL is somewhere else: it
	// keeps working for connections made before the move.
	LegacyURL        string     `json:"legacy_url,omitempty"`
	ProtocolVersions []string   `json:"protocol_versions"`
	ToolCount        int        `json:"tool_count"`
	Roles            []string   `json:"roles"`
	Scopes           []DocScope `json:"scopes"`
	Groups           []DocGroup `json:"groups"`
}

// DocsCatalog builds the reference for tools in lang ("ar" or English).
func DocsCatalog(tools []Tool, lang string) Catalog {
	ar := lang == "ar"
	pick := func(v [2]string) string {
		if ar {
			return v[1]
		}
		return v[0]
	}
	if !ar {
		lang = "en"
	}
	docs, args := toolDocs(), argAR()

	versions := make([]string, 0, len(supportedVersions))
	for v := range supportedVersions {
		versions = append(versions, v)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))

	byScope := map[string][]string{}
	for _, t := range tools {
		byScope[t.Scope] = append(byScope[t.Scope], t.Name)
	}
	cat := Catalog{
		Lang: lang, ProtocolVersions: versions,
		ToolCount: len(tools), Roles: []string{"owner", "admin", "manager", "member", "client"},
	}
	cat.ServerURL = MCPServerURL()
	if OAuthIssuer() != "" && cat.ServerURL != MCPResourceURL() {
		cat.LegacyURL = MCPResourceURL()
	}
	for _, s := range UserScopes {
		d := s.Description
		if ar && scopeAR[s.Scope] != "" {
			d = scopeAR[s.Scope]
		}
		names := byScope[s.Scope]
		if names == nil {
			names = []string{}
		}
		cat.Scopes = append(cat.Scopes, DocScope{Scope: s.Scope, Description: d, MinRole: s.MinRole, Tools: names})
	}

	for _, g := range docGroups {
		group := DocGroup{ID: g.ID, Title: pick(g.Title), Intro: pick(g.Intro), Tools: []DocTool{}}
		for _, t := range tools {
			doc := docs[t.Name]
			if doc.Group != "" {
				if doc.Group != g.ID {
					continue
				}
			} else if !contains(g.Scopes, t.Scope) {
				continue
			}
			desc := t.Description
			if ar && doc.AR != "" {
				desc = doc.AR
			}
			example := doc.Example
			if example == nil {
				example = map[string]any{}
			}
			minRole := authz.None
			if def, ok := userScope(t.Scope); ok {
				minRole = def.MinRole
			}
			group.Tools = append(group.Tools, DocTool{
				Name: t.Name, Description: desc, Scope: t.Scope, MinRole: minRole, Writes: IsWriteTool(t),
				Arguments: docArgs(t.InputSchema, ar, args), InputSchema: t.InputSchema,
				ExampleCall: map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
					"params": map[string]any{"name": t.Name, "arguments": example}},
				ExampleResult: doc.Result,
			})
		}
		cat.Groups = append(cat.Groups, group)
	}
	return cat
}

// docArgs flattens a tool's input schema: required arguments first, then A-Z.
func docArgs(schema map[string]any, ar bool, argsAR map[string]string) []DocArg {
	props, _ := schema["properties"].(map[string]any)
	req := map[string]bool{}
	for _, r := range requiredOf(schema) {
		req[r] = true
	}
	out := make([]DocArg, 0, len(props))
	for name, raw := range props {
		p, _ := raw.(map[string]any)
		desc, _ := p["description"].(string)
		if ar && argsAR[desc] != "" {
			desc = argsAR[desc]
		}
		out = append(out, DocArg{Name: name, Type: argType(p), Required: req[name], Description: desc, Enum: enumOf(p)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Required != out[j].Required {
			return out[i].Required
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func requiredOf(schema map[string]any) []string {
	switch r := schema["required"].(type) {
	case []string:
		return r
	case []any:
		out := make([]string, 0, len(r))
		for _, v := range r {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func argType(p map[string]any) string {
	t, _ := p["type"].(string)
	if t == "array" {
		if items, ok := p["items"].(map[string]any); ok {
			if it, _ := items["type"].(string); it != "" {
				return it + "[]"
			}
		}
	}
	if t == "" {
		return "any"
	}
	return t
}

func enumOf(p map[string]any) []any {
	switch e := p["enum"].(type) {
	case []any:
		return e
	case []string:
		out := make([]any, len(e))
		for i, v := range e {
			out[i] = v
		}
		return out
	case []int:
		out := make([]any, len(e))
		for i, v := range e {
			out[i] = v
		}
		return out
	}
	return nil
}

// docsHandler serves GET /api/mcp/docs?lang=en|ar.
func docsHandler(tools []Tool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cat := DocsCatalog(tools, r.URL.Query().Get("lang"))
		w.Header().Set("Content-Type", "application/json")
		// The catalog is built from the in-memory registry on every call — no DB, no
		// external fetch — so there is no real cost to recomputing it. A cached
		// answer risks the opposite: a deploy that adds a tool leaves visitors
		// reading a stale tool_count for as long as the cache lives. Seen live in
		// testing (MH-428): a fresh browser context still showed the pre-deploy
		// count because "public, max-age=300" let Chromium's HTTP cache serve it.
		w.Header().Set("Cache-Control", "no-store")
		allowAnyOrigin(w)
		_ = json.NewEncoder(w).Encode(cat)
	}
}
