package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/tenant"
)

/*
Every message the workspace's number sends goes through SendAs (MH-409): the
inbox's replies, broadcasts, and messages an agent starts over MCP. Two things
follow from having one door.

  - PACING. Sends from one number are spaced at least Pace apart, whoever
    asked for them. A broadcast already sleeps Pace between messages; this
    makes a reply, or an agent's message, landing in the middle of one wait its
    turn instead of making a burst. Bursts from one number are what WhatsApp's
    abuse detection looks for, and the cost of being wrong is the workspace's
    only number.
  - A LOG. Each attempt is a whatsapp_sends row, so "how many did we send
    today" (whatsapp_status) is counted, not guessed.

The pacing is in-process: Moharrik runs as one container (the same assumption
internal/trends' collector documents). The log would let a second replica pace
against the first if that ever changes.
*/

// Pace is the least time between two messages from one number —
// the same gap a broadcast keeps (internal/marketing).
const Pace = 1200 * time.Millisecond

// Send kinds, as stored in whatsapp_sends.kind.
const (
	KindReply     = "reply"
	KindBroadcast = "broadcast"
	KindDirect    = "direct"
	KindReaction  = "reaction"
)

var pacer = struct {
	sync.Mutex
	lanes map[string]*lane
}{lanes: map[string]*lane{}}

type lane struct {
	sync.Mutex
	last time.Time
}

// laneFor is one number's lane. Lanes are per number, not per workspace: a
// workspace with several numbers paces each on its own, so a broadcast from
// one never holds up a reply from another.
func laneFor(linkID string) *lane {
	pacer.Lock()
	defer pacer.Unlock()
	l, ok := pacer.lanes[linkID]
	if !ok {
		l = &lane{}
		pacer.lanes[linkID] = l
	}
	return l
}

// ErrDailyCap is a send refused because the number already sent its daily
// cap today (in the workspace's time zone).
var ErrDailyCap = errors.New("this WhatsApp number reached its daily send limit")

// Send delivers one reply as this workspace's linked number and returns
// WhatsApp's id for it.
func Send(ctx context.Context, a *app.App, workspaceID, phone, text string) (string, error) {
	return SendAs(ctx, a, workspaceID, phone, text, KindReply)
}

// SendAs is Send with the kind recorded in the log. It waits its turn in the
// workspace's lane (see Pace) before sending.
func SendAs(ctx context.Context, a *app.App, workspaceID, phone, text, kind string) (string, error) {
	return sendVia(ctx, a, workspaceID, phone, kind, func(c *Client) (string, error) {
		return c.SendText(ctx, phone, text)
	})
}

// SendMediaAs sends an image, video or document as this workspace's number
// (MH-659: the inbox composer's attach button) — same pacing and logging lane
// as an ordinary text reply, since it is still one message from one number.
func SendMediaAs(ctx context.Context, a *app.App, workspaceID, phone, mediaType, mimeType string, data []byte, fileName, caption, kind string) (string, error) {
	return sendVia(ctx, a, workspaceID, phone, kind, func(c *Client) (string, error) {
		return c.SendMedia(ctx, phone, mediaType, mimeType, data, fileName, caption)
	})
}

// SendAudioAs sends a voice note (MH-659: the composer's mic button).
func SendAudioAs(ctx context.Context, a *app.App, workspaceID, phone string, data []byte, kind string) (string, error) {
	return sendVia(ctx, a, workspaceID, phone, kind, func(c *Client) (string, error) {
		return c.SendWhatsAppAudio(ctx, phone, data)
	})
}

// SendLocationAs sends a location pin (MH-659: the composer's "send my
// location" and manual lat/lng entry).
func SendLocationAs(ctx context.Context, a *app.App, workspaceID, phone string, lat, lng float64, name, address, kind string) (string, error) {
	return sendVia(ctx, a, workspaceID, phone, kind, func(c *Client) (string, error) {
		return c.SendLocation(ctx, phone, lat, lng, name, address)
	})
}

// sendVia is every send's one door (MH-409, extended MH-659 for kinds other
// than plain text): the pacing, the client lookup, and the whatsapp_sends log
// row all happen exactly once here, regardless of what "action" actually
// calls on the client.
func sendVia(ctx context.Context, a *app.App, workspaceID, phone, kind string, action func(c *Client) (string, error)) (string, error) {
	cfg := LoadConfig()
	if !cfg.Configured() {
		return "", ErrNotConfigured
	}
	l, err := Load(ctx, a, workspaceID)
	if err != nil {
		return "", err
	}

	ln := laneFor(l.ID)
	ln.Lock()
	defer ln.Unlock()
	// The cap is checked inside the lane, so two sends racing for the last
	// slot cannot both pass. A reaction is not a message and never counts.
	if l.DailyCap > 0 && kind != KindReaction {
		n, err := capped(ctx, a, l)
		if err != nil {
			return "", err
		}
		if n >= int64(l.DailyCap) {
			return "", fmt.Errorf("%w (%d)", ErrDailyCap, l.DailyCap)
		}
	}
	if wait := Pace - time.Since(ln.last); wait > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
	id, sendErr := action(ClientFor(cfg, l))
	ln.last = time.Now()

	errText := ""
	if sendErr != nil {
		errText = clip(sendErr.Error(), 300)
	}
	sctx := tenant.WithWorkspace(context.WithoutCancel(ctx), workspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `
		INSERT INTO whatsapp_sends (workspace_id, link_id, kind, phone, ok, external_id, error)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		workspaceID, l.ID, kind, Number(phone), sendErr == nil, id, errText)
	return id, sendErr
}

// SendPresenceFor tells the contact the workspace's number is composing,
// recording, or paused (MH-660). Deliberately lighter than SendAs: presence
// is high-frequency and ephemeral by nature, so it skips both the pacing lane
// (the caller already throttles "composing") and the whatsapp_sends log — a
// failed presence call is not a message that needs a retry or a count.
func SendPresenceFor(ctx context.Context, a *app.App, workspaceID, phone, presence string) error {
	cfg := LoadConfig()
	if !cfg.Configured() {
		return ErrNotConfigured
	}
	l, err := Load(ctx, a, workspaceID)
	if err != nil {
		return err
	}
	return ClientFor(cfg, l).SendPresence(ctx, phone, presence)
}

// SendCounts is what one workspace's number sent since a moment.
type SendCounts struct {
	Sent   int64 `json:"sent"`
	Failed int64 `json:"failed"`
}

// CountSince counts the log since t.
func CountSince(ctx context.Context, a *app.App, workspaceID string, t time.Time) (SendCounts, error) {
	var c SendCounts
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT count(*) FILTER (WHERE ok), count(*) FILTER (WHERE NOT ok)
		FROM whatsapp_sends WHERE workspace_id = $1 AND created_at >= $2`, workspaceID, t).Scan(&c.Sent, &c.Failed)
	return c, err
}

// CountSinceFor counts one number's log since t.
func CountSinceFor(ctx context.Context, a *app.App, l *Link, t time.Time) (SendCounts, error) {
	var c SendCounts
	sctx := tenant.WithWorkspace(ctx, l.WorkspaceID)
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT count(*) FILTER (WHERE ok), count(*) FILTER (WHERE NOT ok)
		FROM whatsapp_sends WHERE workspace_id = $1 AND link_id = $2 AND created_at >= $3`,
		l.WorkspaceID, l.ID, t).Scan(&c.Sent, &c.Failed)
	return c, err
}

// capped is what counts against a number's daily cap: today's delivered
// messages, reactions aside.
func capped(ctx context.Context, a *app.App, l *Link) (int64, error) {
	var n int64
	sctx := tenant.WithWorkspace(ctx, l.WorkspaceID)
	err := a.SQLDB.QueryRowContext(sctx, `
		SELECT count(*) FROM whatsapp_sends
		WHERE workspace_id = $1 AND link_id = $2 AND ok AND kind <> $3 AND created_at >= $4`,
		l.WorkspaceID, l.ID, KindReaction, startOfDay(ctx, a, l.WorkspaceID)).Scan(&n)
	return n, err
}

// SetBlocked blocks (or unblocks) a customer's number on the workspace's
// linked phone (MH-657: the inbox's "Block").
func SetBlocked(ctx context.Context, a *app.App, workspaceID, phone string, block bool) error {
	cfg := LoadConfig()
	if !cfg.Configured() {
		return ErrNotConfigured
	}
	l, err := Load(ctx, a, workspaceID)
	if err != nil {
		return err
	}
	return ClientFor(cfg, l).UpdateBlockStatus(ctx, phone, block)
}
