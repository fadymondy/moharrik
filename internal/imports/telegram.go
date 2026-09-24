package imports

import (
	"context"
	"errors"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/channels"
)

/*
The Telegram import source is registered so every connection kind shows up
in the "Import from a connection" list (MH-562), but it is honestly
not-ready: Telegram's Bot API has no endpoint that lists a chat's history.
getUpdates only drains a short-lived server-side queue of updates the bot
has not yet acknowledged, not a chat's messages — and internal/channels'
own CreateTelegram/UpdateTelegram/TestTelegram all call
telegramRegisterWebhook (setWebhook), which Telegram documents as mutually
exclusive with getUpdates: registering a webhook empties and disables that
queue. There is no way to ask Telegram "what did this chat say last month,"
so there is nothing here to import — same gap MH-563's spec for this
provider calls out, not an oversight.

Connections() reflects that by always reporting Ready: false with a Note
explaining why, and Preview/Import fail cleanly with that same message
rather than returning an empty list that would look like "this chat has no
history." Summary() still works, through genericSummary — a Telegram
conversation can already exist from a bot reply filed live (SendTelegramReply
-> inbox.FileChannelMessage), and reading that conversation back doesn't
need a history API, only the messages already on file.
*/

const KindTelegram = "telegram"

const telegramNotReadyNote = "Telegram's Bot API has no endpoint that lists a chat's history, and registering the bot's webhook (required to receive messages live) disables getUpdates, its only other source of past updates — so there is nothing to import here. Conversations already start themselves the moment a chat messages the bot."

var errTelegramNoHistory = errors.New(telegramNotReadyNote)

// TelegramProvider is a deliberately not-ready Provider — see the package
// doc comment above for why.
type TelegramProvider struct{}

func (TelegramProvider) Kind() string { return KindTelegram }

func (TelegramProvider) Connections(ctx context.Context, a *app.App, wsID string) ([]ImportConnection, error) {
	list, err := channels.ListTelegram(ctx, a.SQLDB, wsID)
	if err != nil {
		return nil, err
	}
	out := make([]ImportConnection, 0, len(list))
	for _, c := range list {
		out = append(out, ImportConnection{ID: c.ID, Label: c.Name, Ready: false, Note: telegramNotReadyNote})
	}
	return out, nil
}

func (TelegramProvider) Preview(ctx context.Context, a *app.App, wsID, connID string, f Filters) ([]Item, error) {
	return nil, errTelegramNoHistory
}

func (TelegramProvider) Import(ctx context.Context, a *app.App, wsID string, job *Job, req ImportRequest, progress func(int)) error {
	return errTelegramNoHistory
}

func (TelegramProvider) Summary(ctx context.Context, a *app.App, wsID, conversationID string) (*Summary, error) {
	return genericSummary(ctx, a, wsID, conversationID, KindTelegram)
}

var _ Provider = TelegramProvider{}
