package imports

import (
	"context"
	"errors"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/channels"
)

/*
The Discord import source (MH-562's "even emails" scope): a workspace's
Discord bot connections, read through
internal/channels.ListDiscordGuilds/ListDiscordChannels/DiscordChannelHistory
(Discord's GET /guilds/{id}/channels and GET /channels/{id}/messages) and
turned into inbox threads.

No Discord REST call and no token handling lives here — both stay inside
internal/channels; this file only shapes what those calls returned into
Provider's generic Items/Import.

Threads are keyed exactly like a live Discord delivery
(internal/channels/discord_gateway.go's handleDiscordMessage ->
deliver(...) -> inbox.FileChannelMessage): channel="discord",
source_channel_id=the Discord connection's own row id,
external_thread_id=the Discord channel id. A message's external_id is
"discord:"+id — the same derivation handleDiscordMessage already uses for a
live delivery — so a message imported here and one delivered live (before
or after) collide into one row rather than duplicating.

Discord DMs aren't reachable through a guild-channels listing — only guild
text channels are, which is why Preview only ever returns kind "group" and
a filter for kind "direct" comes back empty rather than silently wrong.
*/

const KindDiscord = "discord"

// DiscordProvider implements Provider against internal/channels' Discord API
// client.
type DiscordProvider struct{}

func (DiscordProvider) Kind() string { return KindDiscord }

func (DiscordProvider) Connections(ctx context.Context, a *app.App, wsID string) ([]ImportConnection, error) {
	list, err := channels.ListDiscord(ctx, a.SQLDB, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]ImportConnection, 0, len(list))
	for _, c := range list {
		ready := c.Enabled && c.HasToken
		note := ""
		switch {
		case !c.HasToken:
			note = "this Discord connection has no bot token yet"
		case !c.Enabled:
			note = "this Discord connection is disabled"
		}
		out = append(out, ImportConnection{ID: c.ID, Label: c.Name, Ready: ready, Note: note})
	}
	return out, nil
}

// discordConn resolves one connection by id — an error here is the message a
// preview/import surfaces.
func discordConn(ctx context.Context, a *app.App, wsID, connID string) (channels.DiscordChannel, error) {
	list, err := channels.ListDiscord(ctx, a.SQLDB, wsID)
	if err != nil {
		return channels.DiscordChannel{}, err
	}
	for _, c := range list {
		if c.ID == connID {
			if !c.HasToken {
				return c, errors.New("this Discord connection has no bot token configured")
			}
			if !c.Enabled {
				return c, errors.New("this Discord connection is disabled")
			}
			return c, nil
		}
	}
	return channels.DiscordChannel{}, errors.New("no such Discord connection")
}

// discordVisibleChannels enumerates every guild text channel this
// connection's bot can see, across every guild it belongs to (or its
// AllowedGuildIDs allow-list, when set).
func discordVisibleChannels(ctx context.Context, a *app.App, wsID, connID string) ([]channels.DiscordGuildChannel, error) {
	guilds, err := channels.ListDiscordGuilds(ctx, a, wsID, connID)
	if err != nil {
		return nil, err
	}
	var out []channels.DiscordGuildChannel
	for _, g := range guilds {
		chs, err := channels.ListDiscordChannels(ctx, a, wsID, connID, g)
		if err != nil {
			return nil, err
		}
		out = append(out, chs...)
	}
	return out, nil
}

func (DiscordProvider) Preview(ctx context.Context, a *app.App, wsID, connID string, f Filters) ([]Item, error) {
	if _, err := discordConn(ctx, a, wsID, connID); err != nil {
		return nil, err
	}
	if f.Kind == "direct" {
		return []Item{}, nil // Discord DMs aren't listable through guild channels
	}
	chs, err := discordVisibleChannels(ctx, a, wsID, connID)
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, c := range chs {
		it := Item{ID: c.ID, Kind: "group", Name: c.Name, Identity: c.ID}
		if hist, herr := channels.DiscordChannelHistory(ctx, a, wsID, connID, c.ID, 1); herr == nil && len(hist) > 0 {
			last := hist[0]
			it.LastMessage = last.Content
			t := last.Timestamp
			it.LastMessageAt = &t
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

func (DiscordProvider) Import(ctx context.Context, a *app.App, wsID string, job *Job, req ImportRequest, progress func(int)) error {
	if _, err := discordConn(ctx, a, wsID, req.ConnectionID); err != nil {
		return err
	}
	chs, err := discordVisibleChannels(ctx, a, wsID, req.ConnectionID)
	if err != nil {
		return err
	}
	byID := map[string]channels.DiscordGuildChannel{}
	for _, c := range chs {
		byID[c.ID] = c
	}
	depth := req.HistoryDepth
	if depth <= 0 {
		depth = 50
	}
	done := 0
	for _, itemID := range req.Items {
		c, ok := byID[itemID]
		if !ok {
			a.Log.Warn("discord import: channel not visible to this connection", "workspace", wsID, "channel", itemID)
			job.Skipped++
			done++
			progress(done)
			continue
		}
		if err := importDiscordChannel(ctx, a, wsID, job, req.ConnectionID, c, depth); err != nil {
			a.Log.Warn("discord import: channel failed", "workspace", wsID, "channel", itemID, "err", err)
			job.Skipped++
		}
		done++
		progress(done)
	}
	return nil
}

func importDiscordChannel(ctx context.Context, a *app.App, wsID string, job *Job, connID string, c channels.DiscordGuildChannel, depth int) error {
	convID, created, err := channelConversation(ctx, a, wsID, KindDiscord, connID, c.ID, true, c.Name, c.ID)
	if err != nil {
		return err
	}
	if created {
		job.ConversationsCreated++
	} else {
		job.ConversationsReused++
	}

	hist, err := channels.DiscordChannelHistory(ctx, a, wsID, connID, c.ID, depth)
	if err != nil {
		return err
	}
	msgs := make([]channelHistoryMsg, 0, len(hist))
	for _, m := range hist {
		msgs = append(msgs, channelHistoryMsg{ExternalID: "discord:" + m.ID, Text: m.Content, At: m.Timestamp})
	}
	n, err := insertChannelHistory(ctx, a, wsID, convID, msgs)
	if err != nil {
		return err
	}
	job.MessagesImported += n
	return nil
}

func (DiscordProvider) Summary(ctx context.Context, a *app.App, wsID, conversationID string) (*Summary, error) {
	return genericSummary(ctx, a, wsID, conversationID, KindDiscord)
}

var _ Provider = DiscordProvider{}
