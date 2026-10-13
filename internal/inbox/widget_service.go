package inbox

import (
	"context"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

// ListWidgets is GET /api/w/chat_widgets and the MCP list_chat_widgets: every
// chat widget in the caller's workspace, by name.
func ListWidgets(ctx context.Context, a *app.App) ([]WidgetOut, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT `+widgetColumns+`
		FROM chat_widgets WHERE workspace_id = $1 ORDER BY lower(name)`, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list chat widgets")
	}
	defer rows.Close()
	list := []WidgetOut{}
	for rows.Next() {
		w, err := scanWidget(rows)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list chat widgets")
		}
		list = append(list, w)
	}
	if err := rows.Err(); err != nil {
		return nil, huma.Error500InternalServerError("could not list chat widgets")
	}
	return list, nil
}

// WidgetUpdate is a partial change to a widget: nil = unchanged.
type WidgetUpdate struct {
	Name           *string   `json:"name,omitempty" maxLength:"80"`
	AllowedOrigins *[]string `json:"allowed_origins,omitempty"`
	Enabled        *bool     `json:"enabled,omitempty"`
	WidgetPatch
}

// UpdateWidget is PATCH /api/w/chat_widgets/{id} and the MCP
// update_chat_widget, so a change made by an agent is the one the settings
// sheet would have made.
func UpdateWidget(ctx context.Context, a *app.App, id string, p WidgetUpdate) (*WidgetOut, error) {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return nil, err
	}
	db := a.SQLDB
	u := &update{}
	if p.Name != nil {
		n := strings.TrimSpace(*p.Name)
		if n == "" {
			return nil, huma.Error422UnprocessableEntity("a name is required")
		}
		u.set("name", n)
	}
	if p.AllowedOrigins != nil {
		if bad := badOrigin(*p.AllowedOrigins); bad != "" {
			return nil, originError(bad)
		}
		u.set("allowed_origins", strings.Join(splitOrigins(strings.Join(*p.AllowedOrigins, ",")), ","))
	}
	// The settings are read, patched and validated as a whole, so a patch
	// can never leave a widget in a state validate() would refuse.
	current, err := readWidget(ctx, db, s.WorkspaceID, id)
	if notFound(err) {
		return nil, huma.Error404NotFound("chat widget not found")
	} else if err != nil {
		return nil, huma.Error500InternalServerError("could not read the chat widget")
	}
	settings := current.WidgetSettings
	if err := p.WidgetPatch.apply(&settings); err != nil {
		return nil, err
	}
	for i, v := range settingsArgs(settings) {
		u.set([]string{"greeting", "greeting_ar", "title", "title_ar", "accent", "theme", "position",
			"invite", "invite_after_seconds", "quick_actions", "lead_event", "require_contact",
			"display_name", "display_avatar_url"}[i], v)
	}
	for i, set := range u.sets {
		if strings.HasPrefix(set, "quick_actions = ") {
			u.sets[i] = set + "::jsonb"
		}
	}
	if p.Enabled != nil {
		u.set("enabled", *p.Enabled)
	}
	res, err := db.ExecContext(ctx, `UPDATE chat_widgets SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		append([]any{s.WorkspaceID, id}, u.args...)...)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not update the chat widget")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, huma.Error404NotFound("chat widget not found")
	}
	w, err := readWidget(ctx, db, s.WorkspaceID, id)
	if err != nil {
		if notFound(err) {
			return nil, huma.Error404NotFound("chat widget not found")
		}
		return nil, huma.Error500InternalServerError("could not read the chat widget")
	}
	return &w, nil
}
