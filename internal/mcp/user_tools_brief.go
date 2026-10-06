package mcp

import (
	"context"
	"encoding/json"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/contactcontext"
)

/*
BriefTools (MH-1169): the AI context brief on a contact, company or lead — the
team's markdown on who the record is and how to speak to them, which every AI
prompt about the record reads first. Contacts and companies share
contacts:read/contacts:write; a lead's brief takes the leads scopes, as the
lead itself does. Writing replaces the whole brief; an empty markdown clears it.
*/

func BriefTools() []Tool {
	recordProps := func(write bool) map[string]any {
		p := map[string]any{
			"kind": prop("string", "\"contact\" or \"company\""),
			"id":   prop("string", "The contact's or company's id"),
		}
		if write {
			p["markdown"] = prop("string", "The whole brief, in markdown (an empty string clears it)")
		}
		return p
	}
	leadProps := func(write bool) map[string]any {
		p := map[string]any{"lead_id": prop("string", "The lead's id")}
		if write {
			p["markdown"] = prop("string", "The whole brief, in markdown (an empty string clears it)")
		}
		return p
	}
	recordKind := func(m map[string]any) (contactcontext.BriefKind, string, error) {
		kind := contactcontext.BriefKind(str(m, "kind"))
		if kind != contactcontext.BriefContact && kind != contactcontext.BriefCompany {
			return "", "", &toolError{`kind must be "contact" or "company"`}
		}
		id, err := required("id", str(m, "id"))
		return kind, id, err
	}
	get := func(kindOf func(map[string]any) (contactcontext.BriefKind, string, error)) func(context.Context, *app.App, json.RawMessage) (string, error) {
		return func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
			s, err := need(ctx)
			if err != nil {
				return "", err
			}
			kind, id, err := kindOf(decodeArgs(raw))
			if err != nil {
				return "", err
			}
			b, err := contactcontext.GetBrief(ctx, a, s.WorkspaceID, kind, id)
			if err != nil {
				return "", err
			}
			return jsonResult(b)
		}
	}
	set := func(kindOf func(map[string]any) (contactcontext.BriefKind, string, error)) func(context.Context, *app.App, json.RawMessage) (string, error) {
		return func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
			s, err := need(ctx)
			if err != nil {
				return "", err
			}
			m := decodeArgs(raw)
			kind, id, err := kindOf(m)
			if err != nil {
				return "", err
			}
			b, err := contactcontext.SaveBrief(ctx, a, s.WorkspaceID, kind, id, str(m, "markdown"), s.UserID)
			if err != nil {
				return "", err
			}
			return jsonResult(b)
		}
	}
	leadKind := func(m map[string]any) (contactcontext.BriefKind, string, error) {
		id, err := required("lead_id", str(m, "lead_id"))
		return contactcontext.BriefLead, id, err
	}

	return []Tool{
		{
			Name: "get_ai_context",
			Description: "Read the AI context brief on a contact or company: the team's markdown on who they are and how we speak to them. " +
				"Every AI answer, report and draft about the record follows it. Read it before writing to them.",
			Scope:       "contacts:read",
			InputSchema: schema([]string{"kind", "id"}, recordProps(false)),
			Run:         get(recordKind),
		},
		{
			Name: "set_ai_context",
			Description: "Replace the AI context brief on a contact or company with new markdown (an empty string clears it). " +
				"Read it first with get_ai_context and keep what the team wrote: this overwrites the whole brief.",
			Scope:       "contacts:write",
			InputSchema: schema([]string{"kind", "id", "markdown"}, recordProps(true)),
			Run:         set(recordKind),
		},
		{
			Name:        "get_lead_ai_context",
			Description: "Read the AI context brief on a lead: the team's markdown on who they are and how we speak to them.",
			Scope:       "leads:read",
			InputSchema: schema([]string{"lead_id"}, leadProps(false)),
			Run:         get(leadKind),
		},
		{
			Name: "set_lead_ai_context",
			Description: "Replace the AI context brief on a lead with new markdown (an empty string clears it). " +
				"Read it first with get_lead_ai_context: this overwrites the whole brief.",
			Scope:       "leads:write",
			InputSchema: schema([]string{"lead_id", "markdown"}, leadProps(true)),
			Run:         set(leadKind),
		},
	}
}

var argsARBrief = map[string]string{
	"\"contact\" or \"company\"":                               "\"contact\" أو \"company\"",
	"The contact's or company's id":                            "معرّف جهة الاتصال أو الشركة",
	"The whole brief, in markdown (an empty string clears it)": "الملخّص كاملًا بصيغة markdown (النص الفارغ يمسحه)",
	"The lead's id":                                            "معرّف العميل المحتمل",
}

const briefExResult = `{"markdown":"## Who they are\nSara runs procurement at Acme...\n\n## How we speak to them\nArabic, warm, short messages.","updated_at":"2026-10-05T10:00:00Z","updated_by":"u_1"}`

var docsBrief = map[string]ToolDoc{
	"get_ai_context": {
		AR:      "قراءة ملخّص السياق لجهة اتصال أو شركة: ما كتبه الفريق بصيغة markdown عن هويتهم وكيف نخاطبهم. كل إجابة وتقرير ومسودة بالذكاء الاصطناعي عن السجل تتبعه.",
		Example: map[string]any{"kind": "contact", "id": ccExContactID},
		Group:   "crm",
		Result:  briefExResult,
	},
	"set_ai_context": {
		AR:      "استبدال ملخّص السياق لجهة اتصال أو شركة بنص markdown جديد (النص الفارغ يمسحه). اقرأه أولًا: هذا يستبدل الملخّص كاملًا.",
		Example: map[string]any{"kind": "contact", "id": ccExContactID, "markdown": "## How we speak to them\nArabic, warm, short messages."},
		Group:   "crm",
		Result:  briefExResult,
	},
	"get_lead_ai_context": {
		AR:      "قراءة ملخّص السياق لعميل محتمل: ما كتبه الفريق عن هويته وكيف نخاطبه.",
		Example: map[string]any{"lead_id": "l_7c2e..."},
		Result:  briefExResult,
	},
	"set_lead_ai_context": {
		AR:      "استبدال ملخّص السياق لعميل محتمل بنص markdown جديد (النص الفارغ يمسحه).",
		Example: map[string]any{"lead_id": "l_7c2e...", "markdown": "## How we speak to them\nEnglish, formal."},
		Result:  briefExResult,
	},
}
