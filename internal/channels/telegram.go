package channels

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/app"
)

// telegramAPIBaseOverride is set by SetTelegramAPIBase (tests). TELEGRAM_API_BASE
// itself is read lazily by telegramAPIBase(), never at package init: envfile.Load
// (.env) runs inside main(), after every package's init() has already run, so a
// var set from os.Getenv in init() would always see it empty (cmd/api/main.go).
// internal/google's GOOGLE_API_BASE reads the same way for the same reason.
var telegramAPIBaseOverride string

func telegramAPIBase() string {
	if telegramAPIBaseOverride != "" {
		return telegramAPIBaseOverride
	}
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("TELEGRAM_API_BASE")), "/"); v != "" {
		return v
	}
	return "https://api.telegram.org"
}

// ErrTelegramNotFound: no such connection in this workspace.
var ErrTelegramNotFound = errors.New("this Telegram connection does not exist")

// TelegramChannel is one bot connection.
type TelegramChannel struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	BotUsername    string     `json:"bot_username"`
	HasToken       bool       `json:"has_token"`
	AllowedChatIDs string     `json:"allowed_chat_ids"`
	Enabled        bool       `json:"enabled"`
	LastDeliveryAt *time.Time `json:"last_delivery_at"`
	LastError      string     `json:"last_error"`
	LastErrorAt    *time.Time `json:"last_error_at"`
	LastTestAt     *time.Time `json:"last_test_at"`
	LastTestOK     *bool      `json:"last_test_ok"`
	WebhookURL     string     `json:"webhook_url" doc:"Informational only — Telegram is told this URL automatically."`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func telegramWebhookURL(webhookID string) string {
	return publicBase() + "/api/telegram/hook/" + webhookID
}

func scanTelegram(row interface{ Scan(...any) error }) (TelegramChannel, string, string, string, error) {
	var c TelegramChannel
	var botSealed, webhookID, secretSealed string
	var lastDelivery, lastErrorAt, lastTestAt sql.NullTime
	var lastTestOK sql.NullBool
	if err := row.Scan(&c.ID, &c.Name, &botSealed, &c.BotUsername, &webhookID, &secretSealed,
		&c.AllowedChatIDs, &c.Enabled, &lastDelivery, &c.LastError, &lastErrorAt,
		&lastTestAt, &lastTestOK, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return c, "", "", "", err
	}
	c.HasToken = botSealed != ""
	c.WebhookURL = telegramWebhookURL(webhookID)
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
	return c, botSealed, webhookID, secretSealed, nil
}

const telegramSelect = `
	SELECT id, name, bot_token_sealed, bot_username, webhook_id, secret_token_sealed,
	       allowed_chat_ids, enabled, last_delivery_at, last_error, last_error_at,
	       last_test_at, last_test_ok, created_at, updated_at
	FROM telegram_channels WHERE workspace_id = $1`

// ListTelegram returns a workspace's Telegram connections, newest first.
func ListTelegram(ctx context.Context, db *sql.DB, workspaceID string) ([]TelegramChannel, error) {
	rows, err := db.QueryContext(ctx, telegramSelect+` ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []TelegramChannel{}
	for rows.Next() {
		c, _, _, _, err := scanTelegram(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func getTelegram(ctx context.Context, db *sql.DB, workspaceID, id string) (TelegramChannel, string, string, string, error) {
	c, bot, webhookID, secretTok, err := scanTelegram(db.QueryRowContext(ctx, telegramSelect+` AND id = $2`, workspaceID, id))
	if notFound(err) {
		return c, "", "", "", ErrTelegramNotFound
	}
	return c, bot, webhookID, secretTok, err
}

// TelegramInput is what the setup page writes.
type TelegramInput struct {
	Name           string
	BotToken       string // "" keeps the stored one on update
	AllowedChatIDs string
	Enabled        *bool
}

// CreateTelegram adds a connection and, given a token, registers the webhook
// with Telegram at once — the setup page's "paste your token" step ends with
// a working connection, not a second button to press.
func CreateTelegram(ctx context.Context, a *app.App, workspaceID, userID string, in TelegramInput) (*TelegramChannel, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("a name is required")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	webhookID := newWebhookID()
	secretToken := newWebhookID()
	sealedSecret, err := telegramSecretBox.Seal(secretToken)
	if err != nil {
		return nil, err
	}
	sealedBot := ""
	if strings.TrimSpace(in.BotToken) != "" {
		if sealedBot, err = telegramBotBox.Seal(strings.TrimSpace(in.BotToken)); err != nil {
			return nil, err
		}
	}
	var id string
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO telegram_channels (workspace_id, name, bot_token_sealed, webhook_id, secret_token_sealed, allowed_chat_ids, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		workspaceID, name, sealedBot, webhookID, sealedSecret, strings.TrimSpace(in.AllowedChatIDs), enabled, userID,
	).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("a Telegram connection named %q already exists in this workspace", name)
		}
		return nil, err
	}
	if sealedBot != "" && enabled {
		if err := telegramRegisterWebhook(ctx, strings.TrimSpace(in.BotToken), telegramWebhookURL(webhookID), secretToken); err != nil {
			recordTelegramError(ctx, a, workspaceID, id, err)
		}
	}
	c, _, _, _, err := getTelegram(ctx, a.SQLDB, workspaceID, id)
	return &c, err
}

// UpdateTelegram edits a connection. An empty BotToken keeps the stored one.
func UpdateTelegram(ctx context.Context, a *app.App, workspaceID, id string, in TelegramInput) (*TelegramChannel, error) {
	_, prevBotSealed, webhookID, secretSealed, err := getTelegram(ctx, a.SQLDB, workspaceID, id)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("a name is required")
	}
	sealedBot := prevBotSealed
	tokenChanged := false
	if strings.TrimSpace(in.BotToken) != "" {
		if sealedBot, err = telegramBotBox.Seal(strings.TrimSpace(in.BotToken)); err != nil {
			return nil, err
		}
		tokenChanged = true
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if _, err := a.SQLDB.ExecContext(ctx, `
		UPDATE telegram_channels SET name = $3, bot_token_sealed = $4, allowed_chat_ids = $5, enabled = $6, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id, name, sealedBot, strings.TrimSpace(in.AllowedChatIDs), enabled); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("a Telegram connection named %q already exists in this workspace", name)
		}
		return nil, err
	}

	token := ""
	if tokenChanged {
		token = strings.TrimSpace(in.BotToken)
	} else if sealedBot != "" {
		token, _ = telegramBotBox.Open(sealedBot)
	}
	secretToken, _ := telegramSecretBox.Open(secretSealed)
	if token != "" {
		if enabled {
			if err := telegramRegisterWebhook(ctx, token, telegramWebhookURL(webhookID), secretToken); err != nil {
				recordTelegramError(ctx, a, workspaceID, id, err)
			}
		} else {
			_ = telegramDeleteWebhook(ctx, token)
		}
	}
	c, _, _, _, err := getTelegram(ctx, a.SQLDB, workspaceID, id)
	return &c, err
}

// DeleteTelegram removes a connection. The conversations already collected
// stay: they are the customer history, not part of the connection.
func DeleteTelegram(ctx context.Context, a *app.App, workspaceID, id string) error {
	_, botSealed, _, _, err := getTelegram(ctx, a.SQLDB, workspaceID, id)
	if err != nil {
		return err
	}
	if botSealed != "" {
		if token, err := telegramBotBox.Open(botSealed); err == nil {
			_ = telegramDeleteWebhook(ctx, token)
		}
	}
	_, err = a.SQLDB.ExecContext(ctx, `DELETE FROM telegram_channels WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
	return err
}

// TestTelegram calls getMe to prove the token works, records the bot's own
// username, and re-registers the webhook so a stale registration is repaired
// by the same button that tests the connection.
func TestTelegram(ctx context.Context, a *app.App, workspaceID, id string) (*TelegramChannel, error) {
	_, botSealed, webhookID, secretSealed, err := getTelegram(ctx, a.SQLDB, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if botSealed == "" {
		return nil, fmt.Errorf("no bot token is saved for this connection yet")
	}
	token, err := telegramBotBox.Open(botSealed)
	if err != nil {
		return nil, err
	}
	me, err := telegramCall(ctx, token, "getMe", nil)
	testErr := err
	username := ""
	if err == nil {
		var r struct {
			Result struct {
				Username string `json:"username"`
			} `json:"result"`
		}
		if e := json.Unmarshal(me, &r); e == nil {
			username = r.Result.Username
		}
	}
	if testErr == nil {
		secretToken, _ := telegramSecretBox.Open(secretSealed)
		if e := telegramRegisterWebhook(ctx, token, telegramWebhookURL(webhookID), secretToken); e != nil {
			testErr = e
		}
	}
	ok := testErr == nil
	msg := ""
	if testErr != nil {
		msg = testErr.Error()
	}
	if _, err := a.SQLDB.ExecContext(ctx, `
		UPDATE telegram_channels SET bot_username = coalesce(nullif($3, ''), bot_username),
		    last_test_at = now(), last_test_ok = $4, last_error = $5, last_error_at = CASE WHEN $4 THEN last_error_at ELSE now() END,
		    updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id, username, ok, msg); err != nil {
		return nil, err
	}
	c, _, _, _, err := getTelegram(ctx, a.SQLDB, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if testErr != nil {
		return &c, fmt.Errorf("could not verify the Telegram connection: %w", testErr)
	}
	return &c, nil
}

func recordTelegramError(ctx context.Context, a *app.App, workspaceID, id string, err error) {
	_, _ = a.SQLDB.ExecContext(ctx, `
		UPDATE telegram_channels SET last_error = $3, last_error_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id, trimTo(err.Error(), 300))
}

// ---- webhook delivery -------------------------------------------------

// TelegramUpdate is the shape of one Telegram webhook delivery. Only the
// fields the inbox uses are modelled; unused ones are dropped by
// json.Unmarshal rather than rejected, since Telegram adds fields freely.
type TelegramUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64  `json:"message_id"`
		Date      int64  `json:"date"`
		Text      string `json:"text"`
		Caption   string `json:"caption"`
		From      struct {
			ID        int64  `json:"id"`
			IsBot     bool   `json:"is_bot"`
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
			Username  string `json:"username"`
		} `json:"from"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
	} `json:"message"`
}

// TelegramInbound is what the update flattens to for the inbox.
type TelegramInbound struct {
	ChatID    string
	Name      string
	Handle    string
	Text      string
	MessageID string
	// IsGroup (MH-604) is true for anything but a private 1:1 chat with the
	// bot — a Telegram group, supergroup or channel, where Handle/Name still
	// identify THIS message's own sender, not the room.
	IsGroup bool
}

// Read flattens an update; ok is false for anything the inbox has nothing to
// do with (no message, a bot's own message, or no text).
func (u TelegramUpdate) Read() (TelegramInbound, bool) {
	var in TelegramInbound
	if u.Message == nil {
		return in, false
	}
	m := u.Message
	if m.From.IsBot {
		return in, false
	}
	in.ChatID = fmt.Sprintf("%d", m.Chat.ID)
	in.MessageID = fmt.Sprintf("%d", m.MessageID)
	in.IsGroup = m.Chat.Type != "" && m.Chat.Type != "private"
	in.Handle = strings.TrimSpace(m.From.Username)
	in.Name = strings.TrimSpace(strings.TrimSpace(m.From.FirstName + " " + m.From.LastName))
	in.Text = strings.TrimSpace(firstNonEmptyStr(m.Text, m.Caption))
	if in.Text == "" || in.ChatID == "" {
		return in, false
	}
	return in, true
}

func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// SameToken compares Telegram's X-Telegram-Bot-Api-Secret-Token header in
// constant time — the secret_token is the whole extra authorization beyond
// the unguessable path (MH-563: "Telegram secret webhook paths").
func SameToken(got, want string) bool {
	if want == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func telegramAllowed(allowList, chatID string) bool {
	allowList = strings.TrimSpace(allowList)
	if allowList == "" {
		return true
	}
	for _, id := range strings.Split(allowList, ",") {
		if strings.TrimSpace(id) == chatID {
			return true
		}
	}
	return false
}

// SendTelegramReply posts a reply on one connection's chat and returns
// Telegram's own message id, for redelivery dedupe from our own side.
func SendTelegramReply(ctx context.Context, a *app.App, workspaceID, connID, chatID, text string) (string, error) {
	_, botSealed, _, _, err := getTelegram(ctx, a.SQLDB, workspaceID, connID)
	if err != nil {
		return "", err
	}
	if botSealed == "" {
		return "", fmt.Errorf("this Telegram connection has no bot token configured")
	}
	token, err := telegramBotBox.Open(botSealed)
	if err != nil {
		return "", err
	}
	resp, err := telegramCall(ctx, token, "sendMessage", map[string]any{"chat_id": chatID, "text": text})
	if err != nil {
		return "", err
	}
	var r struct {
		Result struct {
			MessageID int64 `json:"message_id"`
		} `json:"result"`
	}
	_ = json.Unmarshal(resp, &r)
	return fmt.Sprintf("tg:%d", r.Result.MessageID), nil
}

// ---- the Telegram Bot API client ---------------------------------------

func telegramCall(ctx context.Context, token, method string, params map[string]any) ([]byte, error) {
	url := telegramAPIBase() + "/bot" + token + "/" + method
	var body io.Reader
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
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
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.Unmarshal(raw, &envelope)
	if !envelope.OK {
		msg := envelope.Description
		if msg == "" {
			msg = fmt.Sprintf("Telegram returned HTTP %d", res.StatusCode)
		}
		return nil, fmt.Errorf("telegram: %s", msg)
	}
	return raw, nil
}

func telegramRegisterWebhook(ctx context.Context, token, url, secretToken string) error {
	_, err := telegramCall(ctx, token, "setWebhook", map[string]any{"url": url, "secret_token": secretToken})
	return err
}

func telegramDeleteWebhook(ctx context.Context, token string) error {
	_, err := telegramCall(ctx, token, "deleteWebhook", nil)
	return err
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key value")
}
