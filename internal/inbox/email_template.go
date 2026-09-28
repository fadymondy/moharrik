package inbox

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

/*
Email templates (MH-661): the reply-email counterpart to canned replies — a
name, a subject line and a Markdown body, in English and Arabic, kept once per
workspace (internal/db/schema/email_template.sql). Picked from the email
composer, they fill both the subject and the body before the member edits and
sends.

Placeholders resolve a small, fixed set of fields — {{contact.first_name}},
{{contact.name}}, {{contact.email}}, {{workspace.name}}, {{agent.name}} — with
a DEDICATED resolver (renderPlaceholders below), not internal/rules.Render:
that renderer blanks any placeholder it does not recognise, which is right for
a customer-facing rule action but wrong here, where an unknown placeholder
(mistyped, or one that will exist later) must stay visible verbatim so the
member notices and fixes it rather than silently sending a gap.

The exported Xxx functions below are the shared body: routes.go's REST
handlers and internal/mcp's email_templates tools both call them, so an agent
and a member get exactly the same behaviour, same as inbox.Reply.
*/

type EmailTemplateOut struct {
	ID        string    `json:"id"`
	Name      string    `json:"name" doc:"What the picker shows; never sent."`
	SubjectEn string    `json:"subject_en"`
	SubjectAr string    `json:"subject_ar"`
	BodyEn    string    `json:"body_en"`
	BodyAr    string    `json:"body_ar"`
	Position  int64     `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const emailTemplateSelect = `SELECT id, name, subject_en, subject_ar, body_en, body_ar, position, created_at, updated_at FROM email_templates WHERE workspace_id = $1`

func scanEmailTemplate(r interface{ Scan(...any) error }) (EmailTemplateOut, error) {
	var t EmailTemplateOut
	err := r.Scan(&t.ID, &t.Name, &t.SubjectEn, &t.SubjectAr, &t.BodyEn, &t.BodyAr, &t.Position, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func readEmailTemplate(ctx context.Context, db *sql.DB, wsID, id string) (EmailTemplateOut, error) {
	return scanEmailTemplate(db.QueryRowContext(ctx, emailTemplateSelect+` AND id = $2`, wsID, id))
}

// EmailTemplateFields is a create/update payload. Optional fields (nil) are
// left unchanged on update; on create, an omitted name/body pair is rejected
// the same way registerEmailTemplates's REST validation does.
type EmailTemplateFields struct {
	Name      *string
	SubjectEn *string
	SubjectAr *string
	BodyEn    *string
	BodyAr    *string
	Position  *int64
}

type emailTemplateBody struct {
	Name      *string `json:"name,omitempty" maxLength:"80"`
	SubjectEn *string `json:"subject_en,omitempty" maxLength:"500"`
	SubjectAr *string `json:"subject_ar,omitempty" maxLength:"500"`
	BodyEn    *string `json:"body_en,omitempty" maxLength:"8000" doc:"The English Markdown body. One of body_en and body_ar is required."`
	BodyAr    *string `json:"body_ar,omitempty" maxLength:"8000" doc:"The Arabic Markdown body."`
	Position  *int64  `json:"position,omitempty" minimum:"0" maximum:"10000" doc:"Lower comes first in the picker."`
}

func (b emailTemplateBody) fields() EmailTemplateFields {
	return EmailTemplateFields{Name: b.Name, SubjectEn: b.SubjectEn, SubjectAr: b.SubjectAr, BodyEn: b.BodyEn, BodyAr: b.BodyAr, Position: b.Position}
}

// ---- placeholders -----------------------------------------------------

// placeholderPattern matches {{ dotted.key }} — deliberately looser than
// rules.placeholder (which only allows [a-z0-9_], no dot) so a mistyped or
// future key still matches the pattern and is left in place rather than
// falling through untouched-but-unnoticed as plain text that merely LOOKS
// like a placeholder.
var placeholderPattern = regexp.MustCompile(`\{\{\s*([a-z_.]+)\s*\}\}`)

// PlaceholderData is what renderPlaceholders can fill in.
type PlaceholderData struct {
	ContactName   string
	ContactEmail  string
	WorkspaceName string
	AgentName     string
}

// renderPlaceholders fills the five known {{...}} placeholders and leaves
// every other one untouched, verbatim — the opposite of rules.Render, which
// blanks anything it does not recognise. Shipping a template picker whose
// unknown placeholders silently vanished would send a customer half a
// sentence with no way for the member to notice before hitting send.
func renderPlaceholders(tpl string, d PlaceholderData) string {
	return placeholderPattern.ReplaceAllStringFunc(tpl, func(m string) string {
		key := placeholderPattern.FindStringSubmatch(m)[1]
		switch key {
		case "contact.first_name":
			return firstToken(d.ContactName)
		case "contact.name":
			return d.ContactName
		case "contact.email":
			return d.ContactEmail
		case "workspace.name":
			return d.WorkspaceName
		case "agent.name":
			return d.AgentName
		default:
			return m
		}
	})
}

// firstToken is a contact's first name: the first whitespace-delimited token
// of their full name, falling back to the full name when it has none (a
// one-word name, or none at all).
func firstToken(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if i := strings.IndexAny(name, " \t\n"); i > 0 {
		return name[:i]
	}
	return name
}

// ---- shared service functions (REST + MCP) --------------------------------

// ListEmailTemplates is the workspace's email templates, in picker order.
func ListEmailTemplates(ctx context.Context, a *app.App) ([]EmailTemplateOut, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	rows, err := a.SQLDB.QueryContext(ctx, emailTemplateSelect+` ORDER BY position, lower(name), created_at LIMIT 500`, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list the email templates")
	}
	defer rows.Close()
	list := []EmailTemplateOut{}
	for rows.Next() {
		t, err := scanEmailTemplate(rows)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list the email templates")
		}
		list = append(list, t)
	}
	return list, nil
}

// GetEmailTemplate reads one email template.
func GetEmailTemplate(ctx context.Context, a *app.App, id string) (*EmailTemplateOut, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	t, err := readEmailTemplate(ctx, a.SQLDB, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("email template not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the email template")
	}
	return &t, nil
}

// CreateEmailTemplate adds a template (English, Arabic or both).
func CreateEmailTemplate(ctx context.Context, a *app.App, f EmailTemplateFields) (*EmailTemplateOut, error) {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return nil, err
	}
	name, subEn, subAr, en, ar := deref(f.Name), deref(f.SubjectEn), deref(f.SubjectAr), deref(f.BodyEn), deref(f.BodyAr)
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("a name is required")
	}
	if en == "" && ar == "" {
		return nil, huma.Error422UnprocessableEntity("write the template in English, Arabic or both")
	}
	var pos int64
	if f.Position != nil {
		pos = *f.Position
	} else if err := a.SQLDB.QueryRowContext(ctx, `SELECT coalesce(max(position), -1) + 1 FROM email_templates WHERE workspace_id = $1`, s.WorkspaceID).Scan(&pos); err != nil {
		return nil, huma.Error500InternalServerError("could not add the email template")
	}
	var id string
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO email_templates (workspace_id, name, subject_en, subject_ar, body_en, body_ar, position, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`, s.WorkspaceID, name, subEn, subAr, en, ar, pos, s.UserID).Scan(&id); err != nil {
		return nil, huma.Error500InternalServerError("could not add the email template")
	}
	t, err := readEmailTemplate(ctx, a.SQLDB, s.WorkspaceID, id)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the email template")
	}
	return &t, nil
}

// UpdateEmailTemplate changes a template. Fields left nil are unchanged.
func UpdateEmailTemplate(ctx context.Context, a *app.App, id string, f EmailTemplateFields) (*EmailTemplateOut, error) {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return nil, err
	}
	u := &update{}
	if f.Name != nil {
		n := deref(f.Name)
		if n == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		u.set("name", n)
	}
	if f.SubjectEn != nil {
		u.set("subject_en", deref(f.SubjectEn))
	}
	if f.SubjectAr != nil {
		u.set("subject_ar", deref(f.SubjectAr))
	}
	if f.BodyEn != nil {
		u.set("body_en", deref(f.BodyEn))
	}
	if f.BodyAr != nil {
		u.set("body_ar", deref(f.BodyAr))
	}
	if f.Position != nil {
		u.set("position", *f.Position)
	}
	if len(u.sets) > 0 {
		res, err := a.SQLDB.ExecContext(ctx, `UPDATE email_templates SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
			append([]any{s.WorkspaceID, id}, u.args...)...)
		if pgCode(err) == "23514" {
			return nil, huma.Error422UnprocessableEntity("write the template in English, Arabic or both")
		} else if err != nil {
			return nil, huma.Error500InternalServerError("could not change the email template")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, huma.Error404NotFound("email template not found")
		}
	}
	t, err := readEmailTemplate(ctx, a.SQLDB, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("email template not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the email template")
	}
	return &t, nil
}

// DeleteEmailTemplate removes a template.
func DeleteEmailTemplate(ctx context.Context, a *app.App, id string) error {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return err
	}
	res, err := a.SQLDB.ExecContext(ctx, `DELETE FROM email_templates WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id)
	if err != nil {
		return huma.Error500InternalServerError("could not delete the email template")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return huma.Error404NotFound("email template not found")
	}
	return nil
}

// RenderEmailTemplate fills a template's placeholders, either for a real
// conversation's contact/customer (conversationID set) or with sample data
// (conversationID empty) — the live preview the settings page and the
// composer's picker both call.
func RenderEmailTemplate(ctx context.Context, a *app.App, id, conversationID string) (subject, body string, err error) {
	s, serr := scope(ctx, authz.InboxRead)
	if serr != nil {
		return "", "", serr
	}
	t, terr := readEmailTemplate(ctx, a.SQLDB, s.WorkspaceID, id)
	if notFound(terr) {
		return "", "", huma.Error404NotFound("email template not found")
	} else if terr != nil {
		return "", "", huma.Error500InternalServerError("could not read the email template")
	}

	var wsName string
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT name FROM workspaces WHERE id = $1`, s.WorkspaceID).Scan(&wsName)

	data := PlaceholderData{
		ContactName:   "Jamie Sample",
		ContactEmail:  "jamie@example.com",
		WorkspaceName: wsName,
		AgentName:     s.Email,
	}
	if cid := strings.TrimSpace(conversationID); cid != "" {
		var custName, custEmail string
		var linkedName, linkedEmail sql.NullString
		qerr := a.SQLDB.QueryRowContext(ctx, `
			SELECT c.customer_name, c.customer_email, ct.name, ct.email
			FROM conversations c LEFT JOIN contacts ct ON ct.id = c.contact_id AND ct.workspace_id = c.workspace_id
			WHERE c.workspace_id = $1 AND c.id = $2`, s.WorkspaceID, cid).Scan(&custName, &custEmail, &linkedName, &linkedEmail)
		if notFound(qerr) {
			return "", "", huma.Error404NotFound("conversation not found")
		} else if qerr != nil {
			return "", "", huma.Error500InternalServerError("could not read the conversation")
		}
		data.ContactName = custName
		data.ContactEmail = custEmail
		if linkedName.Valid && linkedName.String != "" {
			data.ContactName = linkedName.String
		}
		if linkedEmail.Valid && linkedEmail.String != "" {
			data.ContactEmail = linkedEmail.String
		}
	}

	subj := t.SubjectEn
	bodyMD := t.BodyEn
	// Same bilingual fallback as canned replies: the empty variant falls back
	// to the other rather than sending blank.
	if subj == "" {
		subj = t.SubjectAr
	}
	if bodyMD == "" {
		bodyMD = t.BodyAr
	}
	return renderPlaceholders(subj, data), renderPlaceholders(bodyMD, data), nil
}

// ---- REST -------------------------------------------------------------

func registerEmailTemplates(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-email-templates", Method: http.MethodGet, Path: "/api/w/email_templates",
		Summary: "The workspace's email templates, in picker order", Tags: tags,
	}, func(ctx context.Context, _ *wsHeader) (*body[struct {
		Templates []EmailTemplateOut `json:"templates"`
	}], error) {
		list, err := ListEmailTemplates(ctx, a)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Templates []EmailTemplateOut `json:"templates"`
		}{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-email-template", Method: http.MethodGet, Path: "/api/w/email_templates/{id}",
		Summary: "One email template", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*body[struct {
		Template EmailTemplateOut `json:"template"`
	}], error) {
		t, err := GetEmailTemplate(ctx, a, in.ID)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Template EmailTemplateOut `json:"template"`
		}{*t}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-email-template", Method: http.MethodPost, Path: "/api/w/email_templates",
		DefaultStatus: http.StatusCreated, Summary: "Add an email template (English, Arabic or both)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body emailTemplateBody
	}) (*body[struct {
		Template EmailTemplateOut `json:"template"`
	}], error) {
		t, err := CreateEmailTemplate(ctx, a, in.Body.fields())
		if err != nil {
			return nil, err
		}
		return out(struct {
			Template EmailTemplateOut `json:"template"`
		}{*t}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-email-template", Method: http.MethodPatch, Path: "/api/w/email_templates/{id}",
		Summary: "Change an email template", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body emailTemplateBody
	}) (*body[struct {
		Template EmailTemplateOut `json:"template"`
	}], error) {
		t, err := UpdateEmailTemplate(ctx, a, in.ID, in.Body.fields())
		if err != nil {
			return nil, err
		}
		return out(struct {
			Template EmailTemplateOut `json:"template"`
		}{*t}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-email-template", Method: http.MethodDelete, Path: "/api/w/email_templates/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete an email template", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		if err := DeleteEmailTemplate(ctx, a, in.ID); err != nil {
			return nil, err
		}
		return &struct{}{}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-render-email-template", Method: http.MethodPost, Path: "/api/w/email_templates/{id}/render",
		Summary: "Fill an email template's placeholders for a conversation, or with sample data", Tags: tags,
		Description: "Unknown placeholders (anything other than contact.first_name, contact.name, contact.email, " +
			"workspace.name, agent.name) are left in the output verbatim, never blanked.",
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			ConversationID string `json:"conversation_id,omitempty" doc:"Render for this conversation's contact/customer. Omit for sample data."`
		}
	}) (*body[struct {
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}], error) {
		subject, bodyText, err := RenderEmailTemplate(ctx, a, in.ID, in.Body.ConversationID)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Subject string `json:"subject"`
			Body    string `json:"body"`
		}{subject, bodyText}), nil
	})
}
