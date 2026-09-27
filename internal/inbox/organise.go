package inbox

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/realtime"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/whatsapp"
)

/*
Organising the inbox (MH-657): WhatsApp's chat-row menu.

Pin, archive, mute and favourite belong to the member who chose them
(conversation_prefs, one row per member per chat): pinning a chat must not
rearrange a teammate's inbox, and muting one must not silence it for the
member it is assigned to. Colour, icon, labels and block belong to the team —
they describe the customer, not one member's view of them.

Blocking drops the customer's messages at the door (every inbound path asks
isBlocked before filing) and closes the composer; on WhatsApp the number is
also blocked on the linked phone. Clearing deletes the messages but keeps the
chat listed; deleting removes the chat and its messages.
*/

// LabelRef is a label as a conversation carries it.
type LabelRef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color" enum:"gray,red,orange,amber,green,teal,blue,violet,pink"`
}

// Label is a workspace label with how many chats carry it.
type Label struct {
	LabelRef
	Count     int64     `json:"count" doc:"Chats carrying this label."`
	CreatedAt time.Time `json:"created_at"`
}

// tagColors is the palette labels and chats are coloured from. The names are
// the web app's --tag-* tokens, so a colour follows the theme.
var tagColors = map[string]bool{"gray": true, "red": true, "orange": true, "amber": true, "green": true,
	"teal": true, "blue": true, "violet": true, "pink": true}

var iconName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// maxPinned is how many chats one member can pin, the way WhatsApp caps it:
// a pin is only worth something while there are few of them.
const maxPinned = 5

// mutedForever is what "always" stores: a time far enough away to be never,
// that still compares like any other.
var mutedForever = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// memberOf is the signed-in member, or "" for a context with none (a webhook,
// the waker): their prefs read as none.
func memberOf(ctx context.Context) string {
	if s, ok := tenant.From(ctx); ok {
		return s.UserID
	}
	return ""
}

// OrganisePatch changes how a chat is organised. nil = unchanged.
type OrganisePatch struct {
	Pinned    *bool     `json:"pinned,omitempty" doc:"Pin to the top of your inbox (at most 5). Pinning unarchives it."`
	Archived  *bool     `json:"archived,omitempty" doc:"Archive in your inbox. Archiving unpins it."`
	Mute      *string   `json:"mute,omitempty" enum:",8h,1w,always" doc:"Mute alerts for you: 8h, 1w or always; empty string unmutes."`
	Favourite *bool     `json:"favourite,omitempty" doc:"Add to (or remove from) your favourites."`
	Color     *string   `json:"color,omitempty" enum:",gray,red,orange,amber,green,teal,blue,violet,pink" doc:"The chat's colour for the whole team; empty string clears it."`
	Icon      *string   `json:"icon,omitempty" maxLength:"40" doc:"The chat's icon (a lowercase icon name) for the whole team; empty string clears it."`
	LabelIDs  *[]string `json:"label_ids,omitempty" maxItems:"20" doc:"The full set of labels the chat carries (replaces the current set)."`
	Blocked   *bool     `json:"blocked,omitempty" doc:"Block the customer: their messages are dropped and the chat cannot be answered. On WhatsApp the number is also blocked on the linked phone. Needs inbox:manage."`
}

// Organise applies p to one conversation and returns it as the caller now sees it.
func Organise(ctx context.Context, a *app.App, id string, p OrganisePatch) (*ConversationOut, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	shared := p.Color != nil || p.Icon != nil || p.LabelIDs != nil
	if shared && !authz.Can(s.Role, authz.InboxWrite) {
		return nil, huma.Error403Forbidden("your role cannot change a chat's colour, icon or labels")
	}
	if p.Blocked != nil && !authz.Can(s.Role, authz.InboxManage) {
		return nil, huma.Error403Forbidden("your role cannot block or unblock a customer")
	}
	own := p.Pinned != nil || p.Archived != nil || p.Mute != nil || p.Favourite != nil
	if own && s.UserID == "" {
		return nil, huma.Error422UnprocessableEntity("pin, archive, mute and favourite belong to a member; this caller is none")
	}

	var channel, phone string
	var blocked bool
	if err := a.SQLDB.QueryRowContext(ctx, `
		SELECT channel, coalesce(customer_phone, ''), blocked_at IS NOT NULL FROM conversations WHERE workspace_id = $1 AND id = $2`,
		s.WorkspaceID, id).Scan(&channel, &phone, &blocked); err != nil {
		if notFound(err) {
			return nil, huma.Error404NotFound("conversation not found")
		}
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}

	if own {
		u := &update{}
		if p.Pinned != nil {
			if *p.Pinned {
				var n int
				if err := a.SQLDB.QueryRowContext(ctx, `
					SELECT count(*) FROM conversation_prefs
					WHERE workspace_id = $1 AND user_id = $2 AND pinned_at IS NOT NULL AND conversation_id <> $3`,
					s.WorkspaceID, s.UserID, id).Scan(&n); err != nil {
					return nil, huma.Error500InternalServerError("could not read your pinned chats")
				}
				if n >= maxPinned {
					return nil, huma.Error422UnprocessableEntity("you can pin up to 5 chats; unpin one first")
				}
				u.sets = append(u.sets, "pinned_at = coalesce(pinned_at, now())", "archived_at = NULL")
			} else {
				u.sets = append(u.sets, "pinned_at = NULL")
			}
		}
		if p.Archived != nil {
			if *p.Archived {
				u.sets = append(u.sets, "archived_at = coalesce(archived_at, now())", "pinned_at = NULL")
			} else {
				u.sets = append(u.sets, "archived_at = NULL")
			}
		}
		if p.Mute != nil {
			switch *p.Mute {
			case "":
				u.sets = append(u.sets, "muted_until = NULL")
			case "8h":
				u.set("muted_until", time.Now().Add(8*time.Hour).UTC())
			case "1w":
				u.set("muted_until", time.Now().Add(7*24*time.Hour).UTC())
			case "always":
				u.set("muted_until", mutedForever)
			default:
				return nil, huma.Error422UnprocessableEntity("mute must be 8h, 1w, always, or empty to unmute")
			}
		}
		if p.Favourite != nil {
			u.set("favourite", *p.Favourite)
		}
		// $1 workspace, $2 conversation, then update's own args, then the member.
		// The row is made first, so the SETs below (which read the row's own
		// pinned_at/archived_at) apply the same way to a first change and a later one.
		args := append([]any{s.WorkspaceID, id}, u.args...)
		args = append(args, s.UserID)
		userArg := "$" + strconv.Itoa(len(args))
		tx, err := a.SQLDB.BeginTx(ctx, nil)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not save your chat settings")
		}
		defer tx.Rollback() //nolint:errcheck
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO conversation_prefs (workspace_id, conversation_id, user_id) VALUES ($1, $2, $3)
			ON CONFLICT (workspace_id, conversation_id, user_id) DO NOTHING`, s.WorkspaceID, id, s.UserID); err == nil {
			_, err = tx.ExecContext(ctx, `
				UPDATE conversation_prefs SET `+strings.Join(u.sets, ", ")+`, updated_at = now()
				WHERE workspace_id = $1 AND conversation_id = $2 AND user_id = `+userArg,
				args...)
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not save your chat settings")
		}
	}

	if p.Color != nil || p.Icon != nil || p.Blocked != nil {
		u := &update{}
		if p.Color != nil {
			c := strings.TrimSpace(*p.Color)
			if c != "" && !tagColors[c] {
				return nil, huma.Error422UnprocessableEntity("color must be one of gray, red, orange, amber, green, teal, blue, violet, pink")
			}
			u.set("color", c)
		}
		if p.Icon != nil {
			ic := strings.TrimSpace(*p.Icon)
			if ic != "" && !iconName.MatchString(ic) {
				return nil, huma.Error422UnprocessableEntity("icon must be a lowercase icon name")
			}
			u.set("icon", ic)
		}
		if p.Blocked != nil && *p.Blocked != blocked {
			if *p.Blocked {
				u.sets = append(u.sets, "blocked_at = now()")
			} else {
				u.sets = append(u.sets, "blocked_at = NULL")
			}
		}
		if len(u.sets) > 0 {
			if _, err := a.SQLDB.ExecContext(ctx, `UPDATE conversations SET `+strings.Join(u.sets, ", ")+`, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
				append([]any{s.WorkspaceID, id}, u.args...)...); err != nil {
				return nil, huma.Error500InternalServerError("could not update the conversation")
			}
		}
		// The phone blocks the number too, so the customer cannot tell the
		// difference between this and blocking them in WhatsApp itself. Best
		// effort: a disconnected link still gets the inbox-side block.
		if p.Blocked != nil && *p.Blocked != blocked && channel == "whatsapp" && phone != "" {
			if err := blockOnWhatsApp(ctx, a, s.WorkspaceID, phone, *p.Blocked); err != nil && a.Log != nil {
				a.Log.Warn("could not change the block on the linked WhatsApp phone", "workspace", s.WorkspaceID, "err", err)
			}
		}
	}

	if p.LabelIDs != nil {
		if err := setLabels(ctx, a.SQLDB, s.WorkspaceID, id, *p.LabelIDs); err != nil {
			return nil, err
		}
	}

	c, err := readConversation(ctx, a.SQLDB, s.WorkspaceID, id)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	changed(ctx, a, s.WorkspaceID, realtime.Event{Kind: "conversation", ConversationID: id})
	return &c, nil
}

// blockOnWhatsApp is swapped by the tests: they never reach a phone.
var blockOnWhatsApp = whatsapp.SetBlocked

func setLabels(ctx context.Context, db *sql.DB, wsID, convID string, ids []string) error {
	seen := map[string]bool{}
	clean := []string{}
	for _, l := range ids {
		if l = strings.TrimSpace(l); l != "" && !seen[l] {
			seen[l] = true
			clean = append(clean, l)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return huma.Error500InternalServerError("could not change the labels")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversation_label_links WHERE workspace_id = $1 AND conversation_id = $2`, wsID, convID); err != nil {
		return huma.Error500InternalServerError("could not change the labels")
	}
	for _, l := range clean {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO conversation_label_links (workspace_id, conversation_id, label_id) VALUES ($1, $2, $3)`,
			wsID, convID, l); err != nil {
			if pgCode(err) == "23503" {
				return huma.Error422UnprocessableEntity("that label is not in this workspace")
			}
			return huma.Error500InternalServerError("could not change the labels")
		}
	}
	if err := tx.Commit(); err != nil {
		return huma.Error500InternalServerError("could not change the labels")
	}
	return nil
}

// ClearConversation deletes a chat's messages and keeps the chat.
func ClearConversation(ctx context.Context, a *app.App, id string) error {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return err
	}
	tx, err := a.SQLDB.BeginTx(ctx, nil)
	if err != nil {
		return huma.Error500InternalServerError("could not clear the chat")
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE conversations SET cleared_at = now(), unread = false, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		s.WorkspaceID, id)
	if err != nil {
		return huma.Error500InternalServerError("could not clear the chat")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return huma.Error404NotFound("conversation not found")
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM message_attachments WHERE workspace_id = $1
		  AND message_id IN (SELECT id FROM messages WHERE workspace_id = $1 AND conversation_id = $2)`, s.WorkspaceID, id); err != nil {
		return huma.Error500InternalServerError("could not clear the chat")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE workspace_id = $1 AND conversation_id = $2`, s.WorkspaceID, id); err != nil {
		return huma.Error500InternalServerError("could not clear the chat")
	}
	if err := tx.Commit(); err != nil {
		return huma.Error500InternalServerError("could not clear the chat")
	}
	changed(ctx, a, s.WorkspaceID, realtime.Event{Kind: "conversation", ConversationID: id})
	return nil
}

// DeleteConversation removes a chat and everything in it.
func DeleteConversation(ctx context.Context, a *app.App, id string) error {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return err
	}
	tx, err := a.SQLDB.BeginTx(ctx, nil)
	if err != nil {
		return huma.Error500InternalServerError("could not delete the chat")
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM message_attachments WHERE workspace_id = $1
		  AND message_id IN (SELECT id FROM messages WHERE workspace_id = $1 AND conversation_id = $2)`, s.WorkspaceID, id); err != nil {
		return huma.Error500InternalServerError("could not delete the chat")
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM conversations WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id)
	if err != nil {
		return huma.Error500InternalServerError("could not delete the chat")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return huma.Error404NotFound("conversation not found")
	}
	if err := tx.Commit(); err != nil {
		return huma.Error500InternalServerError("could not delete the chat")
	}
	changed(ctx, a, s.WorkspaceID, realtime.Event{Kind: "conversation", ConversationID: id})
	return nil
}

// ---- labels ----------------------------------------------------------------

func ListLabels(ctx context.Context, a *app.App) ([]Label, error) {
	s, err := scope(ctx, authz.InboxRead)
	if err != nil {
		return nil, err
	}
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT l.id, l.name, l.color, l.created_at,
		       (SELECT count(*) FROM conversation_label_links ll WHERE ll.workspace_id = l.workspace_id AND ll.label_id = l.id)
		FROM conversation_labels l WHERE l.workspace_id = $1 ORDER BY lower(l.name)`, s.WorkspaceID)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list the labels")
	}
	defer rows.Close()
	out := []Label{}
	for rows.Next() {
		var l Label
		if err := rows.Scan(&l.ID, &l.Name, &l.Color, &l.CreatedAt, &l.Count); err != nil {
			return nil, huma.Error500InternalServerError("could not list the labels")
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LabelInput creates or renames/recolours a label.
type LabelInput struct {
	Name  *string `json:"name,omitempty" minLength:"1" maxLength:"40"`
	Color *string `json:"color,omitempty" enum:"gray,red,orange,amber,green,teal,blue,violet,pink"`
}

func labelErr(err error) error {
	if pgCode(err) == "23505" {
		return huma.Error409Conflict("a label with that name already exists")
	}
	return huma.Error500InternalServerError("could not save the label")
}

func CreateLabel(ctx context.Context, a *app.App, in LabelInput) (*Label, error) {
	s, err := scope(ctx, authz.InboxWrite)
	if err != nil {
		return nil, err
	}
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if name == "" {
		return nil, huma.Error422UnprocessableEntity("a label needs a name")
	}
	color := "gray"
	if in.Color != nil {
		color = *in.Color
	}
	if !tagColors[color] {
		return nil, huma.Error422UnprocessableEntity("color must be one of gray, red, orange, amber, green, teal, blue, violet, pink")
	}
	l := &Label{}
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO conversation_labels (workspace_id, name, color) VALUES ($1, $2, $3)
		RETURNING id, name, color, created_at`, s.WorkspaceID, trim(name, 40), color).
		Scan(&l.ID, &l.Name, &l.Color, &l.CreatedAt); err != nil {
		return nil, labelErr(err)
	}
	changed(ctx, a, s.WorkspaceID, realtime.Event{Kind: "conversation"})
	return l, nil
}

func UpdateLabel(ctx context.Context, a *app.App, id string, in LabelInput) (*Label, error) {
	s, err := scope(ctx, authz.InboxWrite)
	if err != nil {
		return nil, err
	}
	u := &update{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, huma.Error422UnprocessableEntity("a label needs a name")
		}
		u.set("name", trim(name, 40))
	}
	if in.Color != nil {
		if !tagColors[*in.Color] {
			return nil, huma.Error422UnprocessableEntity("color must be one of gray, red, orange, amber, green, teal, blue, violet, pink")
		}
		u.set("color", *in.Color)
	}
	if len(u.sets) == 0 {
		return nil, huma.Error422UnprocessableEntity("nothing to change")
	}
	l := &Label{}
	err = a.SQLDB.QueryRowContext(ctx, `UPDATE conversation_labels SET `+strings.Join(u.sets, ", ")+`
		WHERE workspace_id = $1 AND id = $2 RETURNING id, name, color, created_at`,
		append([]any{s.WorkspaceID, id}, u.args...)...).Scan(&l.ID, &l.Name, &l.Color, &l.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("label not found")
	}
	if err != nil {
		return nil, labelErr(err)
	}
	changed(ctx, a, s.WorkspaceID, realtime.Event{Kind: "conversation"})
	return l, nil
}

// DeleteLabel removes a label from the workspace and from every chat.
func DeleteLabel(ctx context.Context, a *app.App, id string) error {
	s, err := scope(ctx, authz.InboxManage)
	if err != nil {
		return err
	}
	res, err := a.SQLDB.ExecContext(ctx, `DELETE FROM conversation_labels WHERE workspace_id = $1 AND id = $2`, s.WorkspaceID, id)
	if err != nil {
		return huma.Error500InternalServerError("could not delete the label")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return huma.Error404NotFound("label not found")
	}
	changed(ctx, a, s.WorkspaceID, realtime.Event{Kind: "conversation"})
	return nil
}

// ---- the inbound side --------------------------------------------------------

// isBlocked reports whether a conversation's customer is blocked; every
// inbound path asks before filing. An error reads as not blocked: a message
// must not be lost to a failed read.
func isBlocked(ctx context.Context, db *sql.DB, wsID, convID string) bool {
	var b bool
	_ = db.QueryRowContext(ctx, `SELECT blocked_at IS NOT NULL FROM conversations WHERE workspace_id = $1 AND id = $2`, wsID, convID).Scan(&b)
	return b
}

// inboundPrefs runs when a customer writes: a chat a member archived comes
// back to their inbox (unless they also muted it, the way WhatsApp keeps a
// muted archived chat archived), and the members who muted it are returned so
// the alert skips them.
func inboundPrefs(ctx context.Context, db *sql.DB, wsID, convID string) []string {
	_, _ = db.ExecContext(ctx, `
		UPDATE conversation_prefs SET archived_at = NULL, updated_at = now()
		WHERE workspace_id = $1 AND conversation_id = $2 AND archived_at IS NOT NULL
		  AND (muted_until IS NULL OR muted_until <= now())`, wsID, convID)
	rows, err := db.QueryContext(ctx, `
		SELECT user_id FROM conversation_prefs
		WHERE workspace_id = $1 AND conversation_id = $2 AND muted_until > now()`, wsID, convID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil {
			out = append(out, u)
		}
	}
	return out
}

// ---- routes ------------------------------------------------------------------

func registerOrganise(api huma.API, a *app.App) {
	type convBody = body[struct {
		Conversation ConversationOut `json:"conversation"`
	}]
	huma.Register(api, huma.Operation{
		OperationID: "w-organise-conversation", Method: http.MethodPatch, Path: "/api/w/conversations/{id}/organise",
		Summary: "Pin, archive, mute, favourite, colour, icon, label or block a chat (MH-657)", Tags: tags,
		Description: "pinned, archived, mute and favourite are the signed-in member's own; color, icon and label_ids (inbox:write) " +
			"and blocked (inbox:manage) are the team's.",
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body OrganisePatch
	}) (*convBody, error) {
		c, err := Organise(ctx, a, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		res := &convBody{}
		res.Body.Conversation = *c
		return res, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-clear-conversation", Method: http.MethodPost, Path: "/api/w/conversations/{id}/clear",
		DefaultStatus: http.StatusNoContent, Summary: "Delete every message in a chat and keep the chat (inbox:manage)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		return nil, ClearConversation(ctx, a, in.ID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-conversation", Method: http.MethodDelete, Path: "/api/w/conversations/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete a chat and its messages (inbox:manage)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		return nil, DeleteConversation(ctx, a, in.ID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-conversation-labels", Method: http.MethodGet, Path: "/api/w/conversation_labels",
		Summary: "The workspace's chat labels, with how many chats carry each", Tags: tags,
	}, func(ctx context.Context, in *wsHeader) (*body[struct {
		Labels []Label `json:"labels"`
	}], error) {
		l, err := ListLabels(ctx, a)
		if err != nil {
			return nil, err
		}
		return out(struct {
			Labels []Label `json:"labels"`
		}{l}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-conversation-label", Method: http.MethodPost, Path: "/api/w/conversation_labels",
		DefaultStatus: http.StatusCreated, Summary: "Create a chat label", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body LabelInput
	}) (*body[Label], error) {
		l, err := CreateLabel(ctx, a, in.Body)
		if err != nil {
			return nil, err
		}
		return out(*l), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-conversation-label", Method: http.MethodPatch, Path: "/api/w/conversation_labels/{id}",
		Summary: "Rename or recolour a chat label", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body LabelInput
	}) (*body[Label], error) {
		l, err := UpdateLabel(ctx, a, in.ID, in.Body)
		if err != nil {
			return nil, err
		}
		return out(*l), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-conversation-label", Method: http.MethodDelete, Path: "/api/w/conversation_labels/{id}",
		DefaultStatus: http.StatusNoContent, Summary: "Delete a chat label (from every chat; inbox:manage)", Tags: tags,
	}, func(ctx context.Context, in *idPath) (*struct{}, error) {
		return nil, DeleteLabel(ctx, a, in.ID)
	})
}
