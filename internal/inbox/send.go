package inbox

import (
	"net/http"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/marketing"
	"github.com/fadymondy/moharrik/internal/tenant"
)

// SendEmailReply sends the team's answer out through the conversation's own
// mailbox — or the workspace's default SMTP service, for a conversation that
// predates MH-555 or was never tied to one — and returns the Message-ID it was
// sent with. text is Markdown (MH-556): it goes out multipart, the rendered
// HTML alongside the plain text, the same shape marketing.Send already builds
// for a campaign.
func SendEmailReply(ctx context.Context, a *app.App, workspaceID, convID, mailboxID, to, subject, text string) (string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return "", errors.New("this conversation has no email address to answer")
	}
	svc, err := resolveSMTP(ctx, a.SQLDB, workspaceID, mailboxID)
	if err != nil {
		return "", err
	}

	// Thread it under the customer's own last message, so their client files
	// the answer in the conversation they started rather than as a new mail.
	inReplyTo, references, err := threadHeadersFor(ctx, a, workspaceID, convID)
	if err != nil {
		return "", err
	}
	headers := ReplyHeaders(inReplyTo, references)
	if headers == nil {
		headers = map[string]string{}
	}

	msgID := newMessageID(svc.FromEmail)
	headers["Message-Id"] = "<" + msgID + ">"

	if err := marketing.Send(ctx, svc, marketing.Message{
		To:      to,
		Subject: replySubject(subject),
		Text:    text,
		HTML:    RenderMarkdown(text),
		Headers: headers,
	}); err != nil {
		return "", err
	}
	return msgID, nil
}

// threadHeadersFor gathers what the reply must answer: the newest message on
// this conversation that has an upstream id, plus the ids already in the
// thread so the References chain stays intact.
func threadHeadersFor(ctx context.Context, a *app.App, workspaceID, convID string) (string, []string, error) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	rows, err := a.SQLDB.QueryContext(sctx, `
		SELECT external_id FROM messages
		WHERE workspace_id = $1 AND conversation_id = $2 AND external_id <> ''
		ORDER BY created_at LIMIT 50`, workspaceID, convID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", nil, err
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		// Nothing to answer (a thread that started here). The reply is a new
		// mail, and its own Message-ID becomes the thread root.
		return "", nil, nil
	}
	return ids[len(ids)-1], ids, nil
}

// replySubject prefixes Re: once, the way a mail client would.
func replySubject(subject string) string {
	s := strings.TrimSpace(subject)
	if s == "" {
		return "Re: (no subject)"
	}
	if strings.HasPrefix(strings.ToLower(s), "re:") {
		return s
	}
	return "Re: " + s
}

// newMessageID mints the id this reply is sent with. The domain is taken from
// the From address so the id looks like it belongs to the sender, which some
// spam filters check.
func newMessageID(from string) string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		panic("inbox: no entropy for a message id: " + err.Error())
	}
	domain := "moharrik.local"
	if at := strings.LastIndex(from, "@"); at >= 0 && at+1 < len(from) {
		domain = from[at+1:]
	}
	return fmt.Sprintf("%s.%s", hex.EncodeToString(raw), domain)
}

// mailError turns a send failure into a status the UI can explain. A mail
// server's own refusal is the part that says what to change, so it is passed
// through rather than replaced with "could not send".
func mailError(err error) error {
	if errors.Is(err, marketing.ErrNoService) {
		return huma.Error409Conflict("this workspace has no SMTP service configured, so email replies cannot be sent yet")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return huma.Error404NotFound("conversation not found")
	}
	if marketing.IsHardBounce(err) {
		return huma.Error422UnprocessableEntity("the mail server rejected that address: " + err.Error())
	}
	return huma.NewError(http.StatusFailedDependency, "the reply could not be sent: " + err.Error())
}
