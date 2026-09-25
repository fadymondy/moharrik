package channels

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
)

// slackAPIBaseOverride is set by SetSlackAPIBase (tests). SLACK_API_BASE
// itself is read lazily by slackAPIBase() — see telegramAPIBase's comment.
var slackAPIBaseOverride string

func slackAPIBase() string {
	if slackAPIBaseOverride != "" {
		return slackAPIBaseOverride
	}
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("SLACK_API_BASE")), "/"); v != "" {
		return v
	}
	return "https://slack.com/api"
}

// ErrSlackNotFound: no such connection in this workspace.
var ErrSlackNotFound = errors.New("this Slack connection does not exist")

// SlackChannel is one Slack app connection.
type SlackChannel struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	HasBotToken       bool       `json:"has_bot_token"`
	HasSigningSecret  bool       `json:"has_signing_secret"`
	TeamID            string     `json:"team_id"`
	TeamName          string     `json:"team_name"`
	BotUserID         string     `json:"bot_user_id"`
	AllowedChannelIDs string     `json:"allowed_channel_ids"`
	Enabled           bool       `json:"enabled"`
	LastDeliveryAt    *time.Time `json:"last_delivery_at"`
	LastError         string     `json:"last_error"`
	LastErrorAt       *time.Time `json:"last_error_at"`
	LastTestAt        *time.Time `json:"last_test_at"`
	LastTestOK        *bool      `json:"last_test_ok"`
	WebhookURL        string     `json:"webhook_url" doc:"Paste this into the Slack app's Event Subscriptions Request URL."`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func slackWebhookURL(webhookID string) string { return publicBase() + "/api/slack/hook/" + webhookID }

const slackSelect = `
	SELECT id, name, bot_token_sealed, signing_secret_sealed, webhook_id, team_id, team_name, bot_user_id,
	       allowed_channel_ids, enabled, last_delivery_at, last_error, last_error_at, last_test_at, last_test_ok,
	       created_at, updated_at
	FROM slack_channels WHERE workspace_id = $1`

func scanSlack(row interface{ Scan(...any) error }) (SlackChannel, string, string, string, error) {
	var c SlackChannel
	var botSealed, signingSealed, webhookID string
	var lastDelivery, lastErrorAt, lastTestAt sql.NullTime
	var lastTestOK sql.NullBool
	if err := row.Scan(&c.ID, &c.Name, &botSealed, &signingSealed, &webhookID, &c.TeamID, &c.TeamName, &c.BotUserID,
		&c.AllowedChannelIDs, &c.Enabled, &lastDelivery, &c.LastError, &lastErrorAt, &lastTestAt, &lastTestOK,
		&c.CreatedAt, &c.UpdatedAt); err != nil {
		return c, "", "", "", err
	}
	c.HasBotToken = botSealed != ""
	c.HasSigningSecret = signingSealed != ""
	c.WebhookURL = slackWebhookURL(webhookID)
	if lastDelivery.Valid {
		c.LastDeliveryAt = &lastDelivery.Time
	}
	if lastErrorAt.Valid {
		c.LastErrorAt = &lastErrorAt.Time
	}
	if lastTestAt.Valid {
		c.LastTestAt = &lastTestAt.Time
	}
	if lastTestOK.Valid {
		c.LastTestOK = &lastTestOK.Bool
	}
	return c, botSealed, signingSealed, webhookID, nil
}

// ListSlack returns a workspace's Slack connections, newest first.
func ListSlack(ctx context.Context, db *sql.DB, workspaceID string) ([]SlackChannel, error) {
	rows, err := db.QueryContext(ctx, slackSelect+` ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []SlackChannel{}
	for rows.Next() {
		c, _, _, _, err := scanSlack(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func getSlack(ctx context.Context, db *sql.DB, workspaceID, id string) (SlackChannel, string, string, string, error) {
	c, bot, signing, webhookID, err := scanSlack(db.QueryRowContext(ctx, slackSelect+` AND id = $2`, workspaceID, id))
	if notFound(err) {
		return c, "", "", "", ErrSlackNotFound
	}
	return c, bot, signing, webhookID, err
}

// SlackInput is what the setup page writes.
type SlackInput struct {
	Name              string
	BotToken          string // "" keeps the stored one on update
	SigningSecret     string // "" keeps the stored one on update
	AllowedChannelIDs string
	Enabled           *bool
}

// CreateSlack adds a connection. Slack's Event Subscriptions URL must be
// pasted into the Slack app by hand (Slack itself verifies it with a
// challenge on save; there is no register-webhook call the way Telegram has).
func CreateSlack(ctx context.Context, a *app.App, workspaceID, userID string, in SlackInput) (*SlackChannel, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("a name is required")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	webhookID := newWebhookID()
	sealedBot, sealedSigning := "", ""
	var err error
	if strings.TrimSpace(in.BotToken) != "" {
		if sealedBot, err = slackBotBox.Seal(strings.TrimSpace(in.BotToken)); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(in.SigningSecret) != "" {
		if sealedSigning, err = slackSigningBox.Seal(strings.TrimSpace(in.SigningSecret)); err != nil {
			return nil, err
		}
	}
	var id string
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO slack_channels (workspace_id, name, bot_token_sealed, signing_secret_sealed, webhook_id, allowed_channel_ids, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		workspaceID, name, sealedBot, sealedSigning, webhookID, strings.TrimSpace(in.AllowedChannelIDs), enabled, userID,
	).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("a Slack connection named %q already exists in this workspace", name)
		}
		return nil, err
	}
	c, _, _, _, err := getSlack(ctx, a.SQLDB, workspaceID, id)
	return &c, err
}

// UpdateSlack edits a connection. Empty BotToken/SigningSecret keep the
// stored ones.
func UpdateSlack(ctx context.Context, a *app.App, workspaceID, id string, in SlackInput) (*SlackChannel, error) {
	_, prevBot, prevSigning, _, err := getSlack(ctx, a.SQLDB, workspaceID, id)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("a name is required")
	}
	sealedBot, sealedSigning := prevBot, prevSigning
	if strings.TrimSpace(in.BotToken) != "" {
		if sealedBot, err = slackBotBox.Seal(strings.TrimSpace(in.BotToken)); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(in.SigningSecret) != "" {
		if sealedSigning, err = slackSigningBox.Seal(strings.TrimSpace(in.SigningSecret)); err != nil {
			return nil, err
		}
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if _, err := a.SQLDB.ExecContext(ctx, `
		UPDATE slack_channels SET name = $3, bot_token_sealed = $4, signing_secret_sealed = $5, allowed_channel_ids = $6, enabled = $7, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id, name, sealedBot, sealedSigning, strings.TrimSpace(in.AllowedChannelIDs), enabled); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("a Slack connection named %q already exists in this workspace", name)
		}
		return nil, err
	}
	c, _, _, _, err := getSlack(ctx, a.SQLDB, workspaceID, id)
	return &c, err
}

// DeleteSlack removes a connection. Conversations already collected stay.
func DeleteSlack(ctx context.Context, a *app.App, workspaceID, id string) error {
	if _, _, _, _, err := getSlack(ctx, a.SQLDB, workspaceID, id); err != nil {
		return err
	}
	_, err := a.SQLDB.ExecContext(ctx, `DELETE FROM slack_channels WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
	return err
}

// TestSlack calls auth.test to prove the bot token works and records the
// team and bot identity.
func TestSlack(ctx context.Context, a *app.App, workspaceID, id string) (*SlackChannel, error) {
	_, botSealed, _, _, err := getSlack(ctx, a.SQLDB, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if botSealed == "" {
		return nil, fmt.Errorf("no bot token is saved for this connection yet")
	}
	token, err := slackBotBox.Open(botSealed)
	if err != nil {
		return nil, err
	}
	raw, testErr := slackCall(ctx, token, "auth.test", nil)
	var teamID, teamName, botUserID string
	if testErr == nil {
		var r struct {
			Team   string `json:"team"`
			TeamID string `json:"team_id"`
			UserID string `json:"user_id"`
		}
		_ = json.Unmarshal(raw, &r)
		teamID, teamName, botUserID = r.TeamID, r.Team, r.UserID
	}
	ok := testErr == nil
	msg := ""
	if testErr != nil {
		msg = testErr.Error()
	}
	if _, err := a.SQLDB.ExecContext(ctx, `
		UPDATE slack_channels SET team_id = coalesce(nullif($3,''), team_id), team_name = coalesce(nullif($4,''), team_name),
		    bot_user_id = coalesce(nullif($5,''), bot_user_id), last_test_at = now(), last_test_ok = $6,
		    last_error = $7, last_error_at = CASE WHEN $6 THEN last_error_at ELSE now() END, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id, teamID, teamName, botUserID, ok, msg); err != nil {
		return nil, err
	}
	c, _, _, _, err := getSlack(ctx, a.SQLDB, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if testErr != nil {
		return &c, fmt.Errorf("could not verify the Slack connection: %w", testErr)
	}
	return &c, nil
}

// ---- webhook delivery --------------------------------------------------

// SlackEnvelope is the shape of one Events API delivery.
type SlackEnvelope struct {
	Type      string `json:"type"` // "url_verification" | "event_callback"
	Challenge string `json:"challenge"`
	TeamID    string `json:"team_id"`
	Event     struct {
		Type     string `json:"type"` // "message"
		Subtype  string `json:"subtype"`
		User     string `json:"user"`
		BotID    string `json:"bot_id"`
		Text     string `json:"text"`
		Channel  string `json:"channel"`
		Ts       string `json:"ts"`
		ThreadTs string `json:"thread_ts"`
	} `json:"event"`
}

// SlackInbound is what an event flattens to for the inbox.
type SlackInbound struct {
	ChannelID string
	UserID    string
	Text      string
	Ts        string
	// IsGroup (MH-604) is true for a channel or private group id, false for
	// a DM — Slack's own convention: a DM's id always starts with "D", a
	// public channel's with "C" and a private group's with "G".
	IsGroup bool
}

// Read flattens an event; ok is false for a bot's own message, a message
// subtype (edits, joins) or empty text.
func (e SlackEnvelope) Read() (SlackInbound, bool) {
	var in SlackInbound
	if e.Type != "event_callback" || e.Event.Type != "message" {
		return in, false
	}
	if e.Event.BotID != "" || e.Event.Subtype != "" {
		return in, false
	}
	in.ChannelID = e.Event.Channel
	in.UserID = e.Event.User
	in.Text = strings.TrimSpace(e.Event.Text)
	in.Ts = e.Event.Ts
	in.IsGroup = !strings.HasPrefix(in.ChannelID, "D")
	if in.Text == "" || in.ChannelID == "" {
		return in, false
	}
	return in, true
}

// VerifySlackSignature checks Slack's v0 HMAC over "v0:{timestamp}:{body}",
// and refuses a timestamp older than five minutes (replay protection) — both
// required by Slack's own signature verification spec.
func VerifySlackSignature(signingSecret, timestamp, signature string, rawBody []byte) bool {
	if signingSecret == "" || timestamp == "" || signature == "" {
		return false
	}
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if age := time.Since(time.Unix(ts, 0)); age > 5*time.Minute || age < -5*time.Minute {
		return false
	}
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(rawBody)
	want := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(signature), []byte(want)) == 1
}

func slackAllowed(allowList, channelID string) bool {
	allowList = strings.TrimSpace(allowList)
	if allowList == "" {
		return true
	}
	for _, id := range strings.Split(allowList, ",") {
		if strings.TrimSpace(id) == channelID {
			return true
		}
	}
	return false
}

// SendSlackReply posts a reply on one connection's channel and returns
// Slack's own ts, for redelivery dedupe from our own side.
func SendSlackReply(ctx context.Context, a *app.App, workspaceID, connID, channelID, text string) (string, error) {
	_, botSealed, _, _, err := getSlack(ctx, a.SQLDB, workspaceID, connID)
	if err != nil {
		return "", err
	}
	if botSealed == "" {
		return "", fmt.Errorf("this Slack connection has no bot token configured")
	}
	token, err := slackBotBox.Open(botSealed)
	if err != nil {
		return "", err
	}
	raw, err := slackCall(ctx, token, "chat.postMessage", map[string]any{"channel": channelID, "text": text})
	if err != nil {
		return "", err
	}
	var r struct {
		Ts string `json:"ts"`
	}
	_ = json.Unmarshal(raw, &r)
	return "slack:" + r.Ts, nil
}

// ---- history, for internal/imports (MH-562) -----------------------------
//
// internal/imports never opens a sealed token itself (internal/secret stays
// inside this package, per this package's own doc comment); it calls these
// two exported functions instead, which mirror SendSlackReply/slackCall's
// plumbing but read instead of write.

// SlackConversationInfo is one channel/DM the bot can see
// (conversations.list), enough for MH-562's import preview.
type SlackConversationInfo struct {
	ID         string
	Name       string
	IsIM       bool
	IsPrivate  bool
	NumMembers int
}

// ListSlackConversations lists every public/private channel and DM the
// bot is a member of, across conversations.list's cursor pages.
func ListSlackConversations(ctx context.Context, a *app.App, workspaceID, connID string) ([]SlackConversationInfo, error) {
	token, err := slackToken(ctx, a, workspaceID, connID)
	if err != nil {
		return nil, err
	}
	var out []SlackConversationInfo
	cursor := ""
	for {
		params := map[string]any{"types": "public_channel,private_channel,im", "limit": 200}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := slackCall(ctx, token, "conversations.list", params)
		if err != nil {
			return nil, err
		}
		var r struct {
			Channels []struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				IsIM       bool   `json:"is_im"`
				IsPrivate  bool   `json:"is_private"`
				NumMembers int    `json:"num_members"`
				User       string `json:"user"` // IM only: the other person's user id
			} `json:"channels"`
			ResponseMetadata struct {
				NextCursor string `json:"next_cursor"`
			} `json:"response_metadata"`
		}
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		for _, c := range r.Channels {
			name := c.Name
			if c.IsIM && name == "" {
				name = c.User
			}
			out = append(out, SlackConversationInfo{ID: c.ID, Name: name, IsIM: c.IsIM, IsPrivate: c.IsPrivate, NumMembers: c.NumMembers})
		}
		cursor = r.ResponseMetadata.NextCursor
		if cursor == "" || len(r.Channels) == 0 {
			break
		}
	}
	return out, nil
}

// SlackHistoryMessage is one message read back by conversations.history.
type SlackHistoryMessage struct {
	Ts   string // Slack's own message identifier/timestamp, e.g. "1234567890.123456"
	User string
	Text string
}

// SlackConversationHistory reads a channel's recent messages
// (conversations.history), newest first, capped at limit (Slack's own
// per-call max is 999; MH-562 never needs more than a few hundred).
func SlackConversationHistory(ctx context.Context, a *app.App, workspaceID, connID, channelID string, limit int) ([]SlackHistoryMessage, error) {
	token, err := slackToken(ctx, a, workspaceID, connID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	raw, err := slackCall(ctx, token, "conversations.history", map[string]any{"channel": channelID, "limit": limit})
	if err != nil {
		return nil, err
	}
	var r struct {
		Messages []struct {
			Type string `json:"type"`
			User string `json:"user"`
			Text string `json:"text"`
			Ts   string `json:"ts"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	out := make([]SlackHistoryMessage, 0, len(r.Messages))
	for _, m := range r.Messages {
		if m.Type != "message" || strings.TrimSpace(m.Text) == "" {
			continue
		}
		out = append(out, SlackHistoryMessage{Ts: m.Ts, User: m.User, Text: m.Text})
	}
	return out, nil
}

// slackToken resolves and opens one connection's bot token — the only place
// outside CreateSlack/UpdateSlack/TestSlack/SendSlackReply that touches
// slackBotBox, so a decrypted token never crosses the package boundary.
func slackToken(ctx context.Context, a *app.App, workspaceID, connID string) (string, error) {
	_, botSealed, _, _, err := getSlack(ctx, a.SQLDB, workspaceID, connID)
	if err != nil {
		return "", err
	}
	if botSealed == "" {
		return "", fmt.Errorf("this Slack connection has no bot token configured")
	}
	return slackBotBox.Open(botSealed)
}

// ---- the Slack Web API client -------------------------------------------

func slackCall(ctx context.Context, token, method string, params map[string]any) ([]byte, error) {
	var req *http.Request
	var err error
	if params == nil {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, slackAPIBase()+"/"+method, nil)
	} else {
		form := url.Values{}
		for k, v := range params {
			form.Set(k, fmt.Sprintf("%v", v))
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, slackAPIBase()+"/"+method, bytes.NewReader([]byte(form.Encode())))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &envelope)
	if !envelope.OK {
		msg := envelope.Error
		if msg == "" {
			msg = fmt.Sprintf("Slack returned HTTP %d", res.StatusCode)
		}
		return nil, fmt.Errorf("slack: %s", msg)
	}
	return raw, nil
}
