package whatsapp

import (
	"context"
	"net/http"

	"github.com/fadymondy/moharrik/internal/app"
)

/*
MH-664: emoji reactions and reply quotes, outbound.

A reaction is its own Evolution call, keyed to the message it reacts to:

	POST /message/sendReaction/{instance}
	{"key": {"remoteJid": "2012…@s.whatsapp.net", "fromMe": false, "id": "3EB0…"},
	 "reaction": "👍"}          // "" takes it back

A reply quote rides on the ordinary send (sendText, sendMedia,
sendWhatsAppAudio, sendLocation) as

	"quoted": {"key": {"id": "3EB0…"}, "message": {"conversation": "the original text"}}

The quote travels in the context (WithOutboundQuote) rather than as another
parameter, so every existing SendAs/SendMediaAs/… caller keeps its signature
and a send without a quote is byte-for-byte what it was before.
*/

// OutboundQuote is the message an outgoing send replies to.
type OutboundQuote struct {
	// ExternalID is WhatsApp's id for the quoted message.
	ExternalID string
	// Text is what it said, which WhatsApp shows in the quote bubble.
	Text string
}

type outQuoteKey struct{}

// WithOutboundQuote makes the next send in ctx quote q. A quote with no
// WhatsApp id (a message that never went through WhatsApp) is ignored:
// WhatsApp could not resolve it anyway.
func WithOutboundQuote(ctx context.Context, q OutboundQuote) context.Context {
	if q.ExternalID == "" {
		return ctx
	}
	return context.WithValue(ctx, outQuoteKey{}, q)
}

// withQuoted adds the "quoted" field to a send body when ctx carries a quote.
func withQuoted(ctx context.Context, body map[string]any) map[string]any {
	q, ok := ctx.Value(outQuoteKey{}).(OutboundQuote)
	if !ok || q.ExternalID == "" {
		return body
	}
	body["quoted"] = map[string]any{
		"key":     map[string]any{"id": q.ExternalID},
		"message": map[string]any{"conversation": q.Text},
	}
	return body
}

// SendReaction reacts to (or, with emoji "", un-reacts to) one message in a
// chat. fromMe is whether the TARGET was sent by this number — WhatsApp keys
// a message by chat + sender + id, so it has to be right. Returns the
// reaction's own message id.
func (c *Client) SendReaction(ctx context.Context, number, targetID string, targetFromMe bool, emoji string) (string, error) {
	var out struct {
		Key struct {
			ID string `json:"id"`
		} `json:"key"`
	}
	body := map[string]any{
		"key": map[string]any{
			"remoteJid": Number(number) + "@s.whatsapp.net",
			"fromMe":    targetFromMe,
			"id":        targetID,
		},
		"reaction": emoji,
	}
	if err := c.do(ctx, http.MethodPost, "/message/sendReaction/"+c.inst(), body, &out); err != nil {
		return "", err
	}
	return out.Key.ID, nil
}

// SendReactionAs sends a reaction from the workspace's number, through the
// same pacing lane and send log as every other send (a reaction is still the
// number doing something WhatsApp can see).
func SendReactionAs(ctx context.Context, a *app.App, workspaceID, phone, targetID string, targetFromMe bool, emoji string) error {
	_, err := sendVia(ctx, a, workspaceID, phone, "reaction", func(c *Client) (string, error) {
		return c.SendReaction(ctx, phone, targetID, targetFromMe, emoji)
	})
	return err
}
