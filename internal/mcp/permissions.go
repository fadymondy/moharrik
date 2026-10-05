package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/fadymondy/moharrik/internal/authz"
)

/*
The permission explainer (MH-1069): what a token holding each scope can read,
can change, can change only after a confirmation, and can never do.

It is derived from the tool registry and nothing else. A tool is "can read" when
its scope ends in ":read" or the tool is marked ReadOnly, "can change" when
ChangesData(t), and "needs confirmation" when it also carries a Confirm rule.
The one hand-written part is the "never" list, and it is self-checking: an entry
names the tools that WOULD do that thing, and is shown only while the registry
holds none of them. Add such a tool and the entry disappears (and
TestPermissionsNeverIsTrue fails first), so the page cannot promise a limit the
registry no longer has.

Served at GET /api/mcp/permissions?lang=en|ar, public like /api/mcp/docs: it is
the same names and scopes tools/list hands any token, and no workspace data.
*/

// PermTool is one tool in a scope's lists.
type PermTool struct {
	Name string `json:"name"`
	// Summary is the tool's description cut to its first sentence, in the
	// requested language: the per-tool annotation.
	Summary string `json:"summary"`
	// Confirm is the confirmation the tool itself demands before it acts; empty
	// when it needs none. Only change tools carry one.
	Confirm string `json:"confirm,omitempty"`
}

// PermScope is one scope's answer.
type PermScope struct {
	Scope       string     `json:"scope"`
	Description string     `json:"description"`
	MinRole     authz.Role `json:"min_role"`
	// CanRead and CanChange partition the scope's tools. NeedsConfirm is the
	// subset of CanChange that demands a confirmation; CanChange holds the rest
	// that act at once, so a tool is in exactly one of the three change/read lists.
	CanRead      []PermTool `json:"can_read"`
	CanChange    []PermTool `json:"can_change"`
	NeedsConfirm []PermTool `json:"needs_confirm"`
}

// PermNever is a limit that holds because no tool does it.
type PermNever struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// PermissionsDoc is the whole explainer in one language.
type PermissionsDoc struct {
	Lang      string      `json:"lang"`
	ToolCount int         `json:"tool_count"`
	Scopes    []PermScope `json:"scopes"`
	Never     []PermNever `json:"never"`
}

// neverEntry is one curated limit. Forbidden lists the tool names that would
// break it; the entry is shown only when none is registered.
type neverEntry struct {
	ID        string
	Label     [2]string // en, ar
	Forbidden []string
}

var neverEntries = []neverEntry{
	{"delete_workspace", [2]string{"Delete or close the workspace", "حذف مساحة العمل أو إغلاقها"},
		[]string{"delete_workspace", "close_workspace", "delete_account", "remove_workspace"}},
	{"delete_records", [2]string{"Delete contacts, companies, deals or leads", "حذف جهات الاتصال أو الشركات أو الصفقات أو العملاء المحتملين"},
		[]string{"delete_contact", "delete_company", "delete_deal", "delete_lead", "remove_contact", "remove_company", "remove_deal", "remove_lead"}},
	{"delete_campaigns", [2]string{"Delete campaigns or notifications", "حذف الحملات أو الإشعارات"},
		[]string{"delete_campaign", "remove_campaign", "delete_notification", "remove_notification", "delete_mail_list"}},
	{"tokens", [2]string{"Create, list or revoke access tokens — a person does that in Settings", "إنشاء رموز الوصول أو عرضها أو إلغاؤها — يفعل ذلك شخص من الإعدادات"},
		[]string{"create_token", "create_mcp_token", "list_tokens", "list_mcp_tokens", "revoke_token", "create_api_token"}},
	{"read_secrets", [2]string{"Read a saved password or signing secret back — it is shown once, when it is made", "قراءة كلمة مرور أو سرّ توقيع محفوظ — يُعرض مرة واحدة فقط عند إنشائه"},
		[]string{"reveal_secret", "get_secret", "get_smtp_password", "reveal_smtp_password", "get_webhook_secret"}},
}

// firstSentence is a tool description cut to one plain line for the page.
func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	const max = 170
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)[:max]
		cut := string(r)
		if i := strings.LastIndex(cut, " "); i > 60 {
			cut = cut[:i]
		}
		s = strings.TrimRight(cut, " ,;:.") + "…"
	}
	return s
}

// Permissions builds the explainer for tools in lang ("ar" or English).
func Permissions(tools []Tool, lang string) PermissionsDoc {
	ar := lang == "ar"
	if !ar {
		lang = "en"
	}
	docs := toolDocs()
	doc := PermissionsDoc{Lang: lang, ToolCount: len(tools), Scopes: []PermScope{}, Never: []PermNever{}}

	byScope := map[string][]Tool{}
	have := map[string]bool{}
	for _, t := range tools {
		byScope[t.Scope] = append(byScope[t.Scope], t)
		have[t.Name] = true
	}
	for _, s := range UserScopes {
		d := s.Description
		if ar && scopeAR[s.Scope] != "" {
			d = scopeAR[s.Scope]
		}
		ps := PermScope{Scope: s.Scope, Description: d, MinRole: s.MinRole,
			CanRead: []PermTool{}, CanChange: []PermTool{}, NeedsConfirm: []PermTool{}}
		for _, t := range byScope[s.Scope] {
			text := t.Description
			if ar && docs[t.Name].AR != "" {
				text = docs[t.Name].AR
			}
			pt := PermTool{Name: t.Name, Summary: firstSentence(text)}
			switch {
			case !ChangesData(t):
				ps.CanRead = append(ps.CanRead, pt)
			case t.Confirm != "":
				pt.Confirm = t.Confirm
				if ar && confirmAR[t.Confirm] != "" {
					pt.Confirm = confirmAR[t.Confirm]
				}
				ps.NeedsConfirm = append(ps.NeedsConfirm, pt)
			default:
				ps.CanChange = append(ps.CanChange, pt)
			}
		}
		doc.Scopes = append(doc.Scopes, ps)
	}
	for _, n := range neverEntries {
		held := true
		for _, name := range n.Forbidden {
			if have[name] {
				held = false
				break
			}
		}
		if !held {
			continue
		}
		label := n.Label[0]
		if ar {
			label = n.Label[1]
		}
		doc.Never = append(doc.Never, PermNever{ID: n.ID, Label: label})
	}
	return doc
}

// confirmAR is each Confirm rule's Arabic text, keyed by the English one. A rule
// without an entry here fails TestPermissionsEveryConfirmHasArabic.
var confirmAR = map[string]string{
	"A workflow that publishes, sends or submits only runs when the call carries confirm: true; without it nothing runs.": "سير العمل الذي ينشر أو يرسل أو يُسلِّم لا يعمل إلا إذا حمل الطلب confirm: true؛ وبدونه لا يُنفَّذ شيء.",
	"The first message to a contact with no WhatsApp thread yet needs a 6-digit code from a first call (valid 10 minutes), repeated with the same text.": "أول رسالة إلى جهة اتصال ليس لها محادثة واتساب بعد تحتاج رمزًا من 6 أرقام يُعاد في طلب ثانٍ بالنص نفسه (صالح 10 دقائق).",
}

func permissionsHandler(tools []Tool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		doc := Permissions(tools, r.URL.Query().Get("lang"))
		w.Header().Set("Content-Type", "application/json")
		// Built from the in-memory registry on every call, like /api/mcp/docs: a
		// deploy that adds a tool must not leave a cached explainer behind.
		w.Header().Set("Cache-Control", "no-store")
		allowAnyOrigin(w)
		_ = json.NewEncoder(w).Encode(doc)
	}
}
