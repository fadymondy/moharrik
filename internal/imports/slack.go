package imports

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/channels"
)

/*
The Slack import source (MH-562's "even emails" scope, i.e. every connection
kind, not just WhatsApp/email — MH-563 added the connection itself): a
workspace's Slack app connections, read through
internal/channels.ListSlackConversations/SlackConversationHistory (Slack's
conversations.list/conversations.history) and turned into inbox threads.

No Slack Web API call and no token handling lives here — both stay inside
internal/channels, exactly as that package's own doc comment requires; this
file only shapes what those two calls returned into Provider's generic
Items/Import, the same division whatsapp.go keeps with internal/whatsapp.

Threads are keyed exactly like a live Slack delivery
(internal/channels/slack.go's RegisterSlackHook -> deliver(...) ->
inbox.FileChannelMessage): channel="slack", source_channel_id=the Slack
connection's own row id, external_thread_id=the Slack channel/DM id. A
message's external_id is "slack:"+ts — the same derivation
RegisterSlackHook already uses for a live delivery — so a message imported
here and one delivered live (before or after) collide into one row rather
than duplicating.
*/

const KindSlack = "slack"

// SlackProvider implements Provider against internal/channels' Slack API
// client.
type SlackProvider struct{}

func (SlackProvider) Kind() string { return KindSlack }

func (SlackProvider) Connections(ctx context.Context, a *app.App, wsID string) ([]ImportConnection, error) {
	list, err := channels.ListSlack(ctx, a.SQLDB, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]ImportConnection, 0, len(list))
	for _, c := range list {
		ready := c.Enabled && c.HasBotToken
		note := ""
		switch {
		case !c.HasBotToken:
			note = "this Slack connection has no bot token yet"
		case !c.Enabled:
			note = "this Slack connection is disabled"
		}
		label := c.TeamName
		if label == "" {
			label = c.Name
		}
		out = append(out, ImportConnection{ID: c.ID, Label: label, Ready: ready, Note: note})
	}
	return out, nil
}

// slackConn resolves one connection by id, the way whatsapp.go's client()
// resolves the workspace's Evolution link — an error here is the message a
// preview/import surfaces.
func slackConn(ctx context.Context, a *app.App, wsID, connID string) (channels.SlackChannel, error) {
	list, err := channels.ListSlack(ctx, a.SQLDB, wsID)
	if err != nil {
		return channels.SlackChannel{}, err
	}
	for _, c := range list {
		if c.ID == connID {
			if !c.HasBotToken {
				return c, errors.New("this Slack connection has no bot token configured")
			}
			if !c.Enabled {
				return c, errors.New("this Slack connection is disabled")
			}
			return c, nil
		}
	}
	return channels.SlackChannel{}, errors.New("no such Slack connection")
}

// slackTs parses Slack's own message identifier/timestamp ("1234567890.123456")
// into a time.Time.
func slackTs(ts string) (time.Time, bool) {
	sec, _, _ := strings.Cut(ts, ".")
	n, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(n, 0).UTC(), true
}

func (SlackProvider) Preview(ctx context.Context, a *app.App, wsID, connID string, f Filters) ([]Item, error) {
	if _, err := slackConn(ctx, a, wsID, connID); err != nil {
		return nil, err
	}
	convos, err := channels.ListSlackConversations(ctx, a, wsID, connID)
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, sc := range convos {
		kind := "group"
		if sc.IsIM {
			kind = "direct"
		}
		if f.Kind != "" && f.Kind != kind {
			continue
		}
		it := Item{ID: sc.ID, Kind: kind, Name: sc.Name, Identity: sc.ID, MessageCount: sc.NumMembers}
		if hist, herr := channels.SlackConversationHistory(ctx, a, wsID, connID, sc.ID, 1); herr == nil && len(hist) > 0 {
			last := hist[0]
			it.LastMessage = last.Text
			if t, ok := slackTs(last.Ts); ok {
				it.LastMessageAt = &t
			}
		}
		items = append(items, it)
	}
	items = applyTextFilter(items, f.Q)
	if f.Since != nil {
		filtered := items[:0]
		for _, it := range items {
			if it.LastMessageAt == nil || !it.LastMessageAt.Before(*f.Since) {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	if items == nil {
		items = []Item{}
	}
	return items, nil
}

func (SlackProvider) Import(ctx context.Context, a *app.App, wsID string, job *Job, req ImportRequest, progress func(int)) error {
	c, err := slackConn(ctx, a, wsID, req.ConnectionID)
	if err != nil {
		return err
	}
	botUserID := c.BotUserID
	if botUserID == "" {
		// A connection that has never been through its "Test connection"
		// button has no bot_user_id on file yet; auth.test is the same call
		// TestSlack makes, and — like TestSlack — persists what it learns, so
		// this only has to happen once per connection. Without a bot user id
		// there is nothing to filter the bot's own replies out of the
		// customer history a channel's messages become.
		if tested, terr := channels.TestSlack(ctx, a, wsID, req.ConnectionID); terr == nil && tested != nil {
			botUserID = tested.BotUserID
		}
	}
	convos, err := channels.ListSlackConversations(ctx, a, wsID, req.ConnectionID)
	if err != nil {
		return err
	}
	byID := map[string]channels.SlackConversationInfo{}
	for _, sc := range convos {
		byID[sc.ID] = sc
	}
	depth := req.HistoryDepth
	if depth <= 0 {
		depth = 50
	}
	done := 0
	for _, itemID := range req.Items {
		sc, ok := byID[itemID]
		if !ok {
			a.Log.Warn("slack import: channel not visible to this connection", "workspace", wsID, "channel", itemID)
			job.Skipped++
			done++
			progress(done)
			continue
		}
		if err := importSlackChannel(ctx, a, wsID, job, req.ConnectionID, botUserID, sc, depth); err != nil {
			a.Log.Warn("slack import: channel failed", "workspace", wsID, "channel", itemID, "err", err)
			job.Skipped++
		}
		done++
		progress(done)
	}
	return nil
}

func importSlackChannel(ctx context.Context, a *app.App, wsID string, job *Job, connID, botUserID string, sc channels.SlackConversationInfo, depth int) error {
	isGroup := !sc.IsIM
	convID, created, err := channelConversation(ctx, a, wsID, KindSlack, connID, sc.ID, isGroup, sc.Name, sc.ID)
	if err != nil {
		return err
	}
	if created {
		job.ConversationsCreated++
	} else {
		job.ConversationsReused++
	}

	hist, err := channels.SlackConversationHistory(ctx, a, wsID, connID, sc.ID, depth)
	if err != nil {
		return err
	}
	msgs := make([]channelHistoryMsg, 0, len(hist))
	for _, m := range hist {
		if botUserID != "" && m.User == botUserID {
			continue // this workspace's own reply, not customer history
		}
		t, ok := slackTs(m.Ts)
		if !ok {
			continue
		}
		msgs = append(msgs, channelHistoryMsg{ExternalID: "slack:" + m.Ts, Text: m.Text, At: t})
	}
	n, err := insertChannelHistory(ctx, a, wsID, convID, msgs)
	if err != nil {
		return err
	}
	job.MessagesImported += n
	return nil
}

func (SlackProvider) Summary(ctx context.Context, a *app.App, wsID, conversationID string) (*Summary, error) {
	return genericSummary(ctx, a, wsID, conversationID, KindSlack)
}

var _ Provider = SlackProvider{}
