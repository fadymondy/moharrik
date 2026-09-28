package mcp

import (
	"context"
	"encoding/json"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/inbox"
)

/*
EmailTemplateTools (MH-661): the reply-email counterpart to canned replies —
list/get/create/update/delete/render, all thin wrappers over internal/inbox's
exported EmailTemplate functions, the same ones the REST routes call, so an
agent and a member get identical behaviour.
*/

func emailTemplateSummary(t inbox.EmailTemplateOut) map[string]any {
	return map[string]any{
		"id": t.ID, "name": t.Name, "subject_en": t.SubjectEn, "subject_ar": t.SubjectAr,
		"body_en": t.BodyEn, "body_ar": t.BodyAr, "position": t.Position,
		"created_at": t.CreatedAt, "updated_at": t.UpdatedAt,
	}
}

func EmailTemplateTools() []Tool {
	return []Tool{
		{
			Name: "list_email_templates",
			Description: "The workspace's email templates, in picker order: each with its id, name, English and Arabic subject and body. " +
				"Bodies may contain placeholders — {{contact.first_name}}, {{contact.name}}, {{contact.email}}, {{workspace.name}}, " +
				"{{agent.name}} — filled by render_email_template. Any other {{...}} in a template is not a known placeholder and stays " +
				"as written when rendered.",
			Scope:       "inbox:read",
			InputSchema: schema(nil, map[string]any{}),
			Run: func(ctx context.Context, a *app.App, _ json.RawMessage) (string, error) {
				list, err := inbox.ListEmailTemplates(ctx, a)
				if err != nil {
					return "", err
				}
				out := make([]map[string]any, len(list))
				for i, t := range list {
					out[i] = emailTemplateSummary(t)
				}
				return jsonResult(map[string]any{"count": len(list), "templates": out})
			},
		},
		{
			Name:        "get_email_template",
			Description: "One email template by id.",
			Scope:       "inbox:read",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id": prop("string", "The template id (list_email_templates)"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				id, err := required("id", str(decodeArgs(raw), "id"))
				if err != nil {
					return "", err
				}
				t, err := inbox.GetEmailTemplate(ctx, a, id)
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"template": emailTemplateSummary(*t)})
			},
		},
		{
			Name: "create_email_template",
			Description: "Add an email template: a name (for the picker, never sent), and a subject and body in English, Arabic or both " +
				"(subject_en/subject_ar/body_en/body_ar — at least one body is required). Bodies are Markdown and may use the placeholders " +
				"list_email_templates describes.",
			Scope: "inbox:manage",
			InputSchema: schema([]string{"name"}, map[string]any{
				"name":       prop("string", "What the picker shows; never sent"),
				"subject_en": prop("string", "The English subject line"),
				"subject_ar": prop("string", "The Arabic subject line"),
				"body_en":    prop("string", "The English Markdown body. One of body_en and body_ar is required"),
				"body_ar":    prop("string", "The Arabic Markdown body"),
				"position":   prop("integer", "Lower comes first in the picker"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					Name      string  `json:"name"`
					SubjectEn *string `json:"subject_en"`
					SubjectAr *string `json:"subject_ar"`
					BodyEn    *string `json:"body_en"`
					BodyAr    *string `json:"body_ar"`
					Position  *int64  `json:"position"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				name, err := required("name", in.Name)
				if err != nil {
					return "", err
				}
				t, err := inbox.CreateEmailTemplate(ctx, a, inbox.EmailTemplateFields{
					Name: &name, SubjectEn: in.SubjectEn, SubjectAr: in.SubjectAr, BodyEn: in.BodyEn, BodyAr: in.BodyAr, Position: in.Position,
				})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"template": emailTemplateSummary(*t)})
			},
		},
		{
			Name: "update_email_template",
			Description: "Change an email template. Give only what changes; body_en/body_ar left both empty after the change is refused " +
				"(at least one must stay).",
			Scope: "inbox:manage",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":         prop("string", "The template id (list_email_templates)"),
				"name":       prop("string", "What the picker shows; never sent"),
				"subject_en": prop("string", "The English subject line"),
				"subject_ar": prop("string", "The Arabic subject line"),
				"body_en":    prop("string", "The English Markdown body"),
				"body_ar":    prop("string", "The Arabic Markdown body"),
				"position":   prop("integer", "Lower comes first in the picker"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				var in struct {
					ID        string  `json:"id"`
					Name      *string `json:"name"`
					SubjectEn *string `json:"subject_en"`
					SubjectAr *string `json:"subject_ar"`
					BodyEn    *string `json:"body_en"`
					BodyAr    *string `json:"body_ar"`
					Position  *int64  `json:"position"`
				}
				if err := bind(raw, &in); err != nil {
					return "", err
				}
				id, err := required("id", in.ID)
				if err != nil {
					return "", err
				}
				t, err := inbox.UpdateEmailTemplate(ctx, a, id, inbox.EmailTemplateFields{
					Name: in.Name, SubjectEn: in.SubjectEn, SubjectAr: in.SubjectAr, BodyEn: in.BodyEn, BodyAr: in.BodyAr, Position: in.Position,
				})
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"template": emailTemplateSummary(*t)})
			},
		},
		{
			Name:        "delete_email_template",
			Description: "Delete an email template.",
			Scope:       "inbox:manage",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id": prop("string", "The template id (list_email_templates)"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				id, err := required("id", str(decodeArgs(raw), "id"))
				if err != nil {
					return "", err
				}
				if err := inbox.DeleteEmailTemplate(ctx, a, id); err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"deleted": id})
			},
		},
		{
			Name: "render_email_template",
			Description: "Fill an email template's placeholders — for a real conversation's contact (conversation_id) or, if omitted, " +
				"with sample data — and return the resulting subject and body. Any {{...}} that is not one of the five known placeholders " +
				"is left exactly as written, never blanked, so a mistake stays visible instead of quietly disappearing.",
			Scope: "inbox:read",
			InputSchema: schema([]string{"id"}, map[string]any{
				"id":              prop("string", "The template id (list_email_templates)"),
				"conversation_id": prop("string", "Conversation id"),
			}),
			Run: func(ctx context.Context, a *app.App, raw json.RawMessage) (string, error) {
				m := decodeArgs(raw)
				id, err := required("id", str(m, "id"))
				if err != nil {
					return "", err
				}
				subject, bodyText, err := inbox.RenderEmailTemplate(ctx, a, id, str(m, "conversation_id"))
				if err != nil {
					return "", err
				}
				return jsonResult(map[string]any{"subject": subject, "body": bodyText})
			},
		},
	}
}
