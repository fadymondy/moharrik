/*
Package imports turns a workspace's connections — WhatsApp (MH-562), an IMAP
mailbox, and later Telegram/Slack/Discord/workflow channels (MH-563/564) —
into contacts and inbox threads, on one interface every source implements the
same way.

The shape every provider gives the same treatment to:

  - Connections: which instance of this source the workspace has (a linked
    WhatsApp number, a mailbox) — usually one, sometimes several.
  - Preview: the people/chats/threads a connection has, each with its last
    message, filterable before anything is written.
  - Import: turn the selected ones into contacts (UpsertContactIdentity,
    MH-561's dedup rule) and inbox conversations with their history, dedup'd
    on (workspace, channel, external_thread_id) so a second import of the
    same chat finds the same conversation rather than duplicating it
    (internal/db/post/156_import.sql). Runs in the background; StartImport
    returns a job id immediately and JobStatus polls it (MH-562: "background
    import with progress").
  - ChatSummary: the analytics an AI client can ask for about one imported
    conversation — the same shape whatever source it came from.

Register a Provider with Register(); ListSources/Preview/StartImport/
ChatSummary dispatch to whichever one a caller names by its Kind(). No opt-in
is implied anywhere in this package (MH-410 stands): importing a chat's
history never sets a contact's whatsapp_opt_in_at or any other broadcast
consent — a consent is a decision a person made, not a fact an import found.
*/
package imports

import (
	"net/http"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/pgtext"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// ---- the provider interface -----------------------------------------------

// ImportConnection is one instance of a source a workspace has: a linked WhatsApp
// number, a mailbox. Most sources have zero or one; nothing stops several.
type ImportConnection struct {
	ID    string `json:"id"`
	Label string `json:"label" doc:"The phone number, mailbox address, or similar — whatever identifies this connection to a person."`
	Ready bool   `json:"ready" doc:"False when the connection exists but is not usable yet (e.g. WhatsApp not linked)."`
	Note  string `json:"note,omitempty" doc:"Why Ready is false, when it is."`
}

// Participant is one member of a group/thread an Item represents.
type Participant struct {
	Identity string `json:"identity" doc:"Phone, email address, or handle, whatever the source uses."`
	Name     string `json:"name,omitempty"`
}

// Item is one importable chat/sender with its last message — what Preview
// lists and Import acts on. ID is stable within (workspace, source,
// connection), so the same chat previewed twice has the same ID.
type Item struct {
	ID              string        `json:"id"`
	Kind            string        `json:"kind" enum:"direct,group" doc:"direct = one person; group = a group chat or a thread with several participants."`
	Name            string        `json:"name"`
	Identity        string        `json:"identity" doc:"Phone/email/handle — what a contact would be created with."`
	LastMessage     string        `json:"last_message"`
	LastMessageAt   *time.Time    `json:"last_message_at"`
	MessageCount    int           `json:"message_count,omitempty" doc:"When the source can say how many; 0 = unknown."`
	Participants    []Participant `json:"participants,omitempty" doc:"Group/thread items only."`
	Flags           []string      `json:"flags,omitempty" doc:"Source-specific warnings, e.g. email's mailing_list/no_reply."`
	AlreadyImported bool          `json:"already_imported" doc:"A conversation with this external_thread_id already exists."`
}

// Filters narrow Preview.
type Filters struct {
	Q              string     `json:"q,omitempty"`
	Kind           string     `json:"kind,omitempty" enum:",direct,group"`
	Since          *time.Time `json:"since,omitempty"`
	IncludeFlagged bool       `json:"include_flagged,omitempty" doc:"Include items with a flag (e.g. email's mailing lists/no-reply senders) — off by default."`
}

// ImportRequest is one import run.
type ImportRequest struct {
	ConnectionID       string   `json:"connection_id"`
	Items              []string `json:"items" doc:"Item ids from Preview to import."`
	HistoryDepth       int      `json:"history_depth,omitempty" doc:"Messages per conversation, newest first; 0 = the source's default."`
	ImportParticipants bool     `json:"import_participants,omitempty" doc:"Group items only: also create contacts for participants."`
}

// Job is one background import run's state (import_jobs).
type Job struct {
	ID                   string    `json:"id"`
	Source               string    `json:"source"`
	ConnectionID         string    `json:"connection_id"`
	Status               string    `json:"status" enum:"running,done,failed"`
	Total                int       `json:"total"`
	Done                 int       `json:"done"`
	ContactsCreated      int       `json:"contacts_created"`
	ContactsReused       int       `json:"contacts_reused"`
	ConversationsCreated int       `json:"conversations_created"`
	ConversationsReused  int       `json:"conversations_reused"`
	MessagesImported     int       `json:"messages_imported"`
	Skipped              int       `json:"skipped"`
	LastError            string    `json:"last_error,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// Summary is one imported (or any) conversation's analytics — what
// inbox_chat_summary/whatsapp_chat_summary hands an AI client.
type Summary struct {
	ConversationID   string     `json:"conversation_id"`
	Channel          string     `json:"channel"`
	IsGroup          bool       `json:"is_group"`
	MessageCount     int        `json:"message_count"`
	InboundCount     int        `json:"inbound_count"`
	OutboundCount    int        `json:"outbound_count"`
	ParticipantCount int        `json:"participant_count"`
	FirstMessageAt   *time.Time `json:"first_message_at"`
	LastMessageAt    *time.Time `json:"last_message_at"`
}

// Provider is what a source (WhatsApp, email, and MH-563/564's Telegram/
// Slack/Discord/workflow channels) implements. Register it once at startup;
// everything else in this package dispatches to it by Kind().
type Provider interface {
	// Kind is the source's key: "whatsapp", "email", and so on — what every
	// API in this package and the MCP tools call "source".
	Kind() string
	// Connections lists this workspace's instances of the source.
	Connections(ctx context.Context, a *app.App, wsID string) ([]ImportConnection, error)
	// Preview lists a connection's importable items.
	Preview(ctx context.Context, a *app.App, wsID, connectionID string, f Filters) ([]Item, error)
	// Import performs one import run against an already-created Job row,
	// updating it as it goes (via UpdateProgress) and returning the final
	// tallies. Runs on a background goroutine started by StartImport.
	Import(ctx context.Context, a *app.App, wsID string, job *Job, req ImportRequest, progress func(done int)) error
	// Summary reports one conversation's analytics, when this provider owns
	// the conversation's channel.
	Summary(ctx context.Context, a *app.App, wsID, conversationID string) (*Summary, error)
}

// ---- registry ---------------------------------------------------------------

var (
	mu        sync.RWMutex
	providers = map[string]Provider{}
	order     []string
)

// RegisterProvider adds a provider. Call once at startup (internal/server
// wiring); registering the same Kind twice replaces it, which is what a
// test's fake provider needs. Named apart from the REST Register(api, a)
// every other package in this app uses, since this package needs both.
func RegisterProvider(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := providers[p.Kind()]; !exists {
		order = append(order, p.Kind())
	}
	providers[p.Kind()] = p
}

func get(kind string) (Provider, error) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := providers[kind]
	if !ok {
		// Never echo the caller's raw path segment back: internal/server's
		// cross-workspace isolation sweep (isolation_test.go) substitutes
		// another workspace's id into every {param}, source included, and
		// flags a leak if the response merely contains it — even here, where
		// it is the caller's own (invalid) input, not anyone's data.
		return nil, huma.Error404NotFound("no import source with that name — call inbox_import_sources / GET /api/w/import/sources")
	}
	return p, nil
}

// Kinds lists every registered source key, in registration order.
func Kinds() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, len(order))
	copy(out, order)
	return out
}

// ---- dispatch -----------------------------------------------------------------

// SourceInfo is one entry in ListSources: a source kind and the workspace's
// connections for it.
type SourceInfo struct {
	Source      string             `json:"source"`
	Connections []ImportConnection `json:"connections"`
}

func scope(ctx context.Context, action string) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, action) {
		return nil, huma.Error403Forbidden("your role cannot do this in this workspace")
	}
	return s, nil
}

// ListSources reports every registered source and the workspace's
// connections for each (inbox_import_sources / whatsapp_chats's sibling).
func ListSources(ctx context.Context, a *app.App, wsID string) ([]SourceInfo, error) {
	if _, err := scope(ctx, authz.InboxRead); err != nil {
		return nil, err
	}
	out := []SourceInfo{}
	for _, kind := range Kinds() {
		p, err := get(kind)
		if err != nil {
			continue
		}
		conns, err := p.Connections(ctx, a, wsID)
		if err != nil {
			return nil, err
		}
		out = append(out, SourceInfo{Source: kind, Connections: conns})
	}
	return out, nil
}

// Preview lists one connection's importable items (inbox_import_preview).
func Preview(ctx context.Context, a *app.App, wsID, source, connectionID string, f Filters) ([]Item, error) {
	if _, err := scope(ctx, authz.InboxRead); err != nil {
		return nil, err
	}
	p, err := get(source)
	if err != nil {
		return nil, err
	}
	items, err := p.Preview(ctx, a, wsID, connectionID, f)
	if err != nil {
		// A source's own server failing (unreachable, an answer we can't read)
		// is a 502 that says what went wrong — a bare 500 hid MH-562's cause
		// from both the page and the log.
		var se huma.StatusError
		if errors.As(err, &se) {
			return nil, err
		}
		slog.Warn("import preview failed", "source", source, "workspace", wsID, "err", err)
		return nil, huma.NewError(http.StatusFailedDependency, "could not read this " + source + " connection: " + err.Error())
	}
	if err := markImported(ctx, a, wsID, source, items); err != nil {
		return nil, err
	}
	return items, nil
}

// markImported sets AlreadyImported on items whose external_thread_id already
// has a conversation, so a preview screen can show "already imported" instead
// of a person finding out by re-importing.
func markImported(ctx context.Context, a *app.App, wsID, source string, items []Item) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	rows, err := a.SQLDB.QueryContext(ctx, `
		SELECT external_thread_id FROM conversations
		WHERE workspace_id = $1 AND channel = $2 AND external_thread_id = ANY($3::text[])`, wsID, source, pgtext.Literal(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		have[id] = true
	}
	for i := range items {
		items[i].AlreadyImported = have[items[i].ID]
	}
	return rows.Err()
}

// StartImport creates the job row and runs the import on a background
// goroutine, returning the job immediately (MH-562: "background import with
// progress").
func StartImport(ctx context.Context, a *app.App, wsID, source string, req ImportRequest) (*Job, error) {
	s, err := scope(ctx, authz.InboxWrite)
	if err != nil {
		return nil, err
	}
	p, err := get(source)
	if err != nil {
		return nil, err
	}
	if len(req.Items) == 0 {
		return nil, huma.Error422UnprocessableEntity("choose at least one item to import")
	}
	job := &Job{Source: source, ConnectionID: req.ConnectionID, Status: "running", Total: len(req.Items)}
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO import_jobs (workspace_id, source, connection_id, status, total, created_by)
		VALUES ($1, $2, $3, 'running', $4, $5) RETURNING id, created_at, updated_at`,
		wsID, source, req.ConnectionID, job.Total, s.UserID,
	).Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return nil, huma.Error500InternalServerError("could not start the import")
	}

	bg := tenant.WithWorkspace(context.Background(), wsID)
	jobID := job.ID
	go func() {
		done := 0
		progress := func(n int) {
			done = n
			_, _ = a.SQLDB.ExecContext(bg, `UPDATE import_jobs SET done = $2, updated_at = now() WHERE id = $1`, jobID, done)
		}
		runErr := p.Import(bg, a, wsID, job, req, progress)
		status := "done"
		lastErr := ""
		if runErr != nil {
			status = "failed"
			lastErr = runErr.Error()
			a.Log.Warn("import failed", "workspace", wsID, "source", source, "job", jobID, "err", runErr)
		}
		_, _ = a.SQLDB.ExecContext(bg, `
			UPDATE import_jobs SET status = $2, done = $3, contacts_created = $4, contacts_reused = $5,
			  conversations_created = $6, conversations_reused = $7, messages_imported = $8, skipped = $9,
			  last_error = $10, updated_at = now()
			WHERE id = $1`, jobID, status, job.Total, job.ContactsCreated, job.ContactsReused,
			job.ConversationsCreated, job.ConversationsReused, job.MessagesImported, job.Skipped, lastErr)
	}()
	return job, nil
}

// JobStatus reads back one import job's progress.
func JobStatus(ctx context.Context, a *app.App, wsID, jobID string) (*Job, error) {
	if _, err := scope(ctx, authz.InboxRead); err != nil {
		return nil, err
	}
	var j Job
	err := a.SQLDB.QueryRowContext(ctx, `
		SELECT id, source, connection_id, status, total, done, contacts_created, contacts_reused,
		       conversations_created, conversations_reused, messages_imported, skipped, last_error, created_at, updated_at
		FROM import_jobs WHERE workspace_id = $1 AND id = $2`, wsID, jobID,
	).Scan(&j.ID, &j.Source, &j.ConnectionID, &j.Status, &j.Total, &j.Done, &j.ContactsCreated, &j.ContactsReused,
		&j.ConversationsCreated, &j.ConversationsReused, &j.MessagesImported, &j.Skipped, &j.LastError, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, huma.Error404NotFound("import job not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the import job")
	}
	return &j, nil
}

// ChatSummary reports one conversation's analytics (inbox_chat_summary /
// whatsapp_chat_summary), dispatched by the conversation's own channel.
func ChatSummary(ctx context.Context, a *app.App, wsID, conversationID string) (*Summary, error) {
	if _, err := scope(ctx, authz.InboxRead); err != nil {
		return nil, err
	}
	var channel string
	if err := a.SQLDB.QueryRowContext(ctx, `SELECT channel FROM conversations WHERE workspace_id = $1 AND id = $2`, wsID, conversationID).Scan(&channel); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, huma.Error404NotFound("conversation not found")
		}
		return nil, huma.Error500InternalServerError("could not read the conversation")
	}
	p, err := get(channel)
	if err != nil {
		// No provider owns this channel (e.g. plain "chat"): a generic summary
		// straight off messages/conversation_participants still works.
		return genericSummary(ctx, a, wsID, conversationID, channel)
	}
	return p.Summary(ctx, a, wsID, conversationID)
}

func genericSummary(ctx context.Context, a *app.App, wsID, conversationID, channel string) (*Summary, error) {
	sum := &Summary{ConversationID: conversationID, Channel: channel}
	err := a.SQLDB.QueryRowContext(ctx, `
		SELECT count(*), count(*) FILTER (WHERE direction = 'in'), count(*) FILTER (WHERE direction = 'out'),
		       min(created_at), max(created_at)
		FROM messages WHERE workspace_id = $1 AND conversation_id = $2`, wsID, conversationID,
	).Scan(&sum.MessageCount, &sum.InboundCount, &sum.OutboundCount, &sum.FirstMessageAt, &sum.LastMessageAt)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not summarise the conversation")
	}
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT is_group FROM conversations WHERE workspace_id = $1 AND id = $2`, wsID, conversationID).Scan(&sum.IsGroup)
	_ = a.SQLDB.QueryRowContext(ctx, `SELECT count(*) FROM conversation_participants WHERE workspace_id = $1 AND conversation_id = $2`, wsID, conversationID).Scan(&sum.ParticipantCount)
	return sum, nil
}
