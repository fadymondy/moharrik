package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Workflow channels (MH-564): an inbox channel fed by a browser workflow's
"Push to inbox" step on a schedule, for a network with no usable API —
LinkedIn messages, a Facebook Page inbox, Instagram DMs, X DMs.

The step runtime (internal/automation) calls PushMessages with what one run
of the workflow read; this file owns the channel's settings and the dedupe
that makes a re-run safe. Contact linking is kept behind one small function,
linkContactByHandle, so it can be switched to batch V's find-or-create-by-
handle helper the moment MH-561 lands on origin/main without touching the
push path itself.
*/

var networks = map[string]bool{"linkedin": true, "facebook": true, "instagram": true, "x": true}

// ErrWorkflowChannelNotFound: no such connection in this workspace.
var ErrWorkflowChannelNotFound = errors.New("this workflow channel does not exist")

// WorkflowChannel is one browser-workflow-fed connection.
type WorkflowChannel struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Network         string     `json:"network" enum:"linkedin,facebook,instagram,x"`
	WorkflowID      *string    `json:"workflow_id"`
	AllowReplies    bool       `json:"allow_replies"`
	Enabled         bool       `json:"enabled"`
	LastRunAt       *time.Time `json:"last_run_at"`
	LastError       string     `json:"last_error"`
	LastErrorAt     *time.Time `json:"last_error_at"`
	LastPushedCount int        `json:"last_pushed_count"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

const workflowChannelSelect = `
	SELECT id, name, network, workflow_id, allow_replies, enabled, last_run_at, last_error, last_error_at, last_pushed_count, created_at, updated_at
	FROM workflow_channels WHERE workspace_id = $1`

func scanWorkflowChannel(row interface{ Scan(...any) error }) (WorkflowChannel, error) {
	var c WorkflowChannel
	var workflowID sql.NullString
	var lastRun, lastErrorAt sql.NullTime
	if err := row.Scan(&c.ID, &c.Name, &c.Network, &workflowID, &c.AllowReplies, &c.Enabled,
		&lastRun, &c.LastError, &lastErrorAt, &c.LastPushedCount, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return c, err
	}
	if workflowID.Valid {
		c.WorkflowID = &workflowID.String
	}
	if lastRun.Valid {
		c.LastRunAt = &lastRun.Time
	}
	if lastErrorAt.Valid {
		c.LastErrorAt = &lastErrorAt.Time
	}
	return c, nil
}

// ListWorkflowChannels returns a workspace's workflow channels, newest first.
func ListWorkflowChannels(ctx context.Context, db *sql.DB, workspaceID string) ([]WorkflowChannel, error) {
	rows, err := db.QueryContext(ctx, workflowChannelSelect+` ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []WorkflowChannel{}
	for rows.Next() {
		c, err := scanWorkflowChannel(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func getWorkflowChannel(ctx context.Context, db *sql.DB, workspaceID, id string) (WorkflowChannel, error) {
	c, err := scanWorkflowChannel(db.QueryRowContext(ctx, workflowChannelSelect+` AND id = $2`, workspaceID, id))
	if notFound(err) {
		return c, ErrWorkflowChannelNotFound
	}
	return c, err
}

// WorkflowChannelInput is what the setup page writes.
type WorkflowChannelInput struct {
	Name         string
	Network      string
	WorkflowID   *string // nil keeps the current one; empty string clears it
	AllowReplies *bool
	Enabled      *bool
}

// CreateWorkflowChannel adds a connection. It can be created before a
// workflow names it — the same order a chat widget allows.
func CreateWorkflowChannel(ctx context.Context, a *app.App, workspaceID, userID string, in WorkflowChannelInput) (*WorkflowChannel, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("a name is required")
	}
	network := strings.TrimSpace(in.Network)
	if !networks[network] {
		return nil, fmt.Errorf("network must be one of linkedin, facebook, instagram, x")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	allowReplies := false
	if in.AllowReplies != nil {
		allowReplies = *in.AllowReplies
	}
	var workflowID *string
	if in.WorkflowID != nil && strings.TrimSpace(*in.WorkflowID) != "" {
		v := strings.TrimSpace(*in.WorkflowID)
		workflowID = &v
	}
	var id string
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO workflow_channels (workspace_id, name, network, workflow_id, allow_replies, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		workspaceID, name, network, workflowID, allowReplies, enabled, userID,
	).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("a workflow channel named %q already exists in this workspace", name)
		}
		return nil, err
	}
	c, err := getWorkflowChannel(ctx, a.SQLDB, workspaceID, id)
	return &c, err
}

// UpdateWorkflowChannel edits a connection.
func UpdateWorkflowChannel(ctx context.Context, a *app.App, workspaceID, id string, in WorkflowChannelInput) (*WorkflowChannel, error) {
	cur, err := getWorkflowChannel(ctx, a.SQLDB, workspaceID, id)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("a name is required")
	}
	enabled := cur.Enabled
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	allowReplies := cur.AllowReplies
	if in.AllowReplies != nil {
		allowReplies = *in.AllowReplies
	}
	workflowID := cur.WorkflowID
	if in.WorkflowID != nil {
		if strings.TrimSpace(*in.WorkflowID) == "" {
			workflowID = nil
		} else {
			v := strings.TrimSpace(*in.WorkflowID)
			workflowID = &v
		}
	}
	if _, err := a.SQLDB.ExecContext(ctx, `
		UPDATE workflow_channels SET name = $3, workflow_id = $4, allow_replies = $5, enabled = $6, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id, name, workflowID, allowReplies, enabled); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("a workflow channel named %q already exists in this workspace", name)
		}
		return nil, err
	}
	c, err := getWorkflowChannel(ctx, a.SQLDB, workspaceID, id)
	return &c, err
}

// DeleteWorkflowChannel removes a connection. Conversations already
// collected stay.
func DeleteWorkflowChannel(ctx context.Context, a *app.App, workspaceID, id string) error {
	if _, err := getWorkflowChannel(ctx, a.SQLDB, workspaceID, id); err != nil {
		return err
	}
	_, err := a.SQLDB.ExecContext(ctx, `DELETE FROM workflow_channels WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
	return err
}

// PushItem is one message the "Push to inbox" step read from a network's
// inbox preset.
type PushItem struct {
	ThreadID   string // stable per conversation on the network
	MessageID  string // stable per message; dedupe key together with ThreadID
	ProfileURL string // the contact's profile URL or handle
	Name       string
	Text       string
	SentAt     time.Time
}

// PushResult is what one push (one workflow run) did.
type PushResult struct {
	Pushed  int `json:"pushed"`
	Deduped int `json:"deduped"`
}

// PushMessages files each item that has not already been pushed on this
// channel, keyed by (thread id, message id) — a re-run of the same workflow
// pushes nothing twice, whether or not the network still has the message.
func PushMessages(ctx context.Context, a *app.App, workspaceID, channelID string, items []PushItem) (PushResult, error) {
	var res PushResult
	c, err := getWorkflowChannel(ctx, a.SQLDB, workspaceID, channelID)
	if err != nil {
		return res, err
	}
	if !c.Enabled {
		return res, fmt.Errorf("this workflow channel is off")
	}
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	for _, it := range items {
		threadID := strings.TrimSpace(it.ThreadID)
		msgID := strings.TrimSpace(it.MessageID)
		text := strings.TrimSpace(it.Text)
		if threadID == "" || msgID == "" || text == "" {
			continue
		}
		var pushID string
		err := a.SQLDB.QueryRowContext(sctx, `
			INSERT INTO workflow_channel_pushes (workspace_id, workflow_channel_id, thread_id, message_id)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (workspace_id, workflow_channel_id, thread_id, message_id) DO NOTHING
			RETURNING id`, workspaceID, channelID, threadID, msgID).Scan(&pushID)
		if errors.Is(err, sql.ErrNoRows) {
			res.Deduped++
			continue
		}
		if err != nil {
			return res, err
		}
		handle := linkContactByHandle(ctx, a, workspaceID, it.ProfileURL)
		if err := deliver(sctx, workspaceID, c.Network, channelID, threadID, handle, it.Name, text, c.Network+":"+msgID); err != nil {
			return res, err
		}
		res.Pushed++
	}
	if _, err := a.SQLDB.ExecContext(sctx, `
		UPDATE workflow_channels SET last_run_at = now(), last_pushed_count = $3, last_error = '', last_error_at = NULL, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, channelID, res.Pushed); err != nil {
		return res, err
	}
	return res, nil
}

// RecordPushError keeps the workflow channel's own status honest when a run's
// read step failed before any items reached PushMessages.
func RecordPushError(ctx context.Context, a *app.App, workspaceID, channelID string, pushErr error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `
		UPDATE workflow_channels SET last_run_at = now(), last_error = $3, last_error_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, channelID, trimTo(pushErr.Error(), 300))
}

// linkContactByHandle is the one small function MH-562's coordination note
// asks for: contact linking behind a seam that can be switched the moment
// batch V's find-or-create-by-phone/handle (MH-561) lands on origin/main.
// Until then it only returns the handle for display — no contact is
// created or matched.
func linkContactByHandle(_ context.Context, _ *app.App, _ string, profileURL string) string {
	return strings.TrimSpace(profileURL)
}
