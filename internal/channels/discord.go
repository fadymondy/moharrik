package channels

import (
	"bytes"
	"context"
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

// discordAPIBaseOverride is set by SetDiscordAPIBase (tests). DISCORD_API_BASE
// itself is read lazily by discordAPIBase() — see telegramAPIBase's comment.
var discordAPIBaseOverride string

func discordAPIBase() string {
	if discordAPIBaseOverride != "" {
		return discordAPIBaseOverride
	}
	if v := strings.TrimRight(strings.TrimSpace(os.Getenv("DISCORD_API_BASE")), "/"); v != "" {
		return v
	}
	return "https://discord.com/api/v10"
}

// ErrDiscordNotFound: no such connection in this workspace.
var ErrDiscordNotFound = errors.New("this Discord connection does not exist")

// DiscordChannel is one bot connection.
type DiscordChannel struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	HasToken          bool       `json:"has_token"`
	ApplicationID     string     `json:"application_id"`
	PublicKey         string     `json:"public_key"`
	BotUsername       string     `json:"bot_username"`
	AllowedGuildIDs   string     `json:"allowed_guild_ids"`
	AllowedChannelIDs string     `json:"allowed_channel_ids"`
	Enabled           bool       `json:"enabled"`
	GatewayState      string     `json:"gateway_state" enum:"disconnected,connecting,connected"`
	LastDeliveryAt    *time.Time `json:"last_delivery_at"`
	LastError         string     `json:"last_error"`
	LastErrorAt       *time.Time `json:"last_error_at"`
	LastTestAt        *time.Time `json:"last_test_at"`
	LastTestOK        *bool      `json:"last_test_ok"`
	InteractionsURL   string     `json:"interactions_url" doc:"Optional: an Interactions Endpoint URL, for slash commands/components."`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func discordInteractionsURL(webhookID string) string {
	return publicBase() + "/api/discord/hook/" + webhookID
}

const discordSelect = `
	SELECT id, name, bot_token_sealed, application_id, public_key, bot_username, webhook_id,
	       allowed_guild_ids, allowed_channel_ids, enabled, gateway_state,
	       last_delivery_at, last_error, last_error_at, last_test_at, last_test_ok, created_at, updated_at
	FROM discord_channels WHERE workspace_id = $1`

func scanDiscord(row interface{ Scan(...any) error }) (DiscordChannel, string, string, error) {
	var c DiscordChannel
	var botSealed, webhookID string
	var lastDelivery, lastErrorAt, lastTestAt sql.NullTime
	var lastTestOK sql.NullBool
	if err := row.Scan(&c.ID, &c.Name, &botSealed, &c.ApplicationID, &c.PublicKey, &c.BotUsername, &webhookID,
		&c.AllowedGuildIDs, &c.AllowedChannelIDs, &c.Enabled, &c.GatewayState,
		&lastDelivery, &c.LastError, &lastErrorAt, &lastTestAt, &lastTestOK, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return c, "", "", err
	}
	c.HasToken = botSealed != ""
	c.InteractionsURL = discordInteractionsURL(webhookID)
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
	return c, botSealed, webhookID, nil
}

// ListDiscord returns a workspace's Discord connections, newest first.
func ListDiscord(ctx context.Context, db *sql.DB, workspaceID string) ([]DiscordChannel, error) {
	rows, err := db.QueryContext(ctx, discordSelect+` ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []DiscordChannel{}
	for rows.Next() {
		c, _, _, err := scanDiscord(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

func getDiscord(ctx context.Context, db *sql.DB, workspaceID, id string) (DiscordChannel, string, string, error) {
	c, bot, webhookID, err := scanDiscord(db.QueryRowContext(ctx, discordSelect+` AND id = $2`, workspaceID, id))
	if notFound(err) {
		return c, "", "", ErrDiscordNotFound
	}
	return c, bot, webhookID, err
}

// DiscordInput is what the setup page writes.
type DiscordInput struct {
	Name              string
	BotToken          string // "" keeps the stored one on update
	ApplicationID     string
	PublicKey         string
	AllowedGuildIDs   string
	AllowedChannelIDs string
	Enabled           *bool
}

// CreateDiscord adds a connection and, given a token and enabled, starts its
// gateway session at once.
func CreateDiscord(ctx context.Context, a *app.App, workspaceID, userID string, in DiscordInput) (*DiscordChannel, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("a name is required")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	webhookID := newWebhookID()
	sealedBot := ""
	var err error
	if strings.TrimSpace(in.BotToken) != "" {
		if sealedBot, err = discordBotBox.Seal(strings.TrimSpace(in.BotToken)); err != nil {
			return nil, err
		}
	}
	var id string
	if err := a.SQLDB.QueryRowContext(ctx, `
		INSERT INTO discord_channels (workspace_id, name, bot_token_sealed, application_id, public_key, webhook_id,
		    allowed_guild_ids, allowed_channel_ids, enabled, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		workspaceID, name, sealedBot, strings.TrimSpace(in.ApplicationID), strings.TrimSpace(in.PublicKey), webhookID,
		strings.TrimSpace(in.AllowedGuildIDs), strings.TrimSpace(in.AllowedChannelIDs), enabled, userID,
	).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("a Discord connection named %q already exists in this workspace", name)
		}
		return nil, err
	}
	if sealedBot != "" && enabled {
		startGateway(a, workspaceID, id)
	}
	c, _, _, err := getDiscord(ctx, a.SQLDB, workspaceID, id)
	return &c, err
}

// UpdateDiscord edits a connection. An empty BotToken keeps the stored one.
func UpdateDiscord(ctx context.Context, a *app.App, workspaceID, id string, in DiscordInput) (*DiscordChannel, error) {
	_, prevBotSealed, _, err := getDiscord(ctx, a.SQLDB, workspaceID, id)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, fmt.Errorf("a name is required")
	}
	sealedBot := prevBotSealed
	if strings.TrimSpace(in.BotToken) != "" {
		if sealedBot, err = discordBotBox.Seal(strings.TrimSpace(in.BotToken)); err != nil {
			return nil, err
		}
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	if _, err := a.SQLDB.ExecContext(ctx, `
		UPDATE discord_channels SET name = $3, bot_token_sealed = $4, application_id = $5, public_key = $6,
		    allowed_guild_ids = $7, allowed_channel_ids = $8, enabled = $9, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id, name, sealedBot, strings.TrimSpace(in.ApplicationID), strings.TrimSpace(in.PublicKey),
		strings.TrimSpace(in.AllowedGuildIDs), strings.TrimSpace(in.AllowedChannelIDs), enabled); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("a Discord connection named %q already exists in this workspace", name)
		}
		return nil, err
	}
	if enabled && sealedBot != "" {
		startGateway(a, workspaceID, id)
	} else {
		stopGateway(workspaceID, id)
	}
	c, _, _, err := getDiscord(ctx, a.SQLDB, workspaceID, id)
	return &c, err
}

// DeleteDiscord removes a connection. Conversations already collected stay.
func DeleteDiscord(ctx context.Context, a *app.App, workspaceID, id string) error {
	if _, _, _, err := getDiscord(ctx, a.SQLDB, workspaceID, id); err != nil {
		return err
	}
	stopGateway(workspaceID, id)
	_, err := a.SQLDB.ExecContext(ctx, `DELETE FROM discord_channels WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
	return err
}

// TestDiscord calls GET /users/@me to prove the token works, records the
// bot's own username, and (re)starts the gateway session.
func TestDiscord(ctx context.Context, a *app.App, workspaceID, id string) (*DiscordChannel, error) {
	_, botSealed, _, err := getDiscord(ctx, a.SQLDB, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if botSealed == "" {
		return nil, fmt.Errorf("no bot token is saved for this connection yet")
	}
	token, err := discordBotBox.Open(botSealed)
	if err != nil {
		return nil, err
	}
	raw, testErr := discordCall(ctx, token, http.MethodGet, "/users/@me", nil)
	username := ""
	if testErr == nil {
		var r struct {
			Username      string `json:"username"`
			Discriminator string `json:"discriminator"`
		}
		if e := json.Unmarshal(raw, &r); e == nil {
			username = r.Username
		}
	}
	ok := testErr == nil
	msg := ""
	if testErr != nil {
		msg = testErr.Error()
	}
	if _, err := a.SQLDB.ExecContext(ctx, `
		UPDATE discord_channels SET bot_username = coalesce(nullif($3,''), bot_username), last_test_at = now(), last_test_ok = $4,
		    last_error = $5, last_error_at = CASE WHEN $4 THEN last_error_at ELSE now() END, updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id, username, ok, msg); err != nil {
		return nil, err
	}
	if ok {
		startGateway(a, workspaceID, id)
	}
	c, _, _, err := getDiscord(ctx, a.SQLDB, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if testErr != nil {
		return &c, fmt.Errorf("could not verify the Discord connection: %w", testErr)
	}
	return &c, nil
}

func discordAllowed(allowList, id string) bool {
	allowList = strings.TrimSpace(allowList)
	if allowList == "" {
		return true
	}
	for _, v := range strings.Split(allowList, ",") {
		if strings.TrimSpace(v) == id {
			return true
		}
	}
	return false
}

// SendDiscordReply posts a reply into one connection's channel over the REST
// API — this needs no gateway session, since posting a message is a plain
// HTTP call — and returns Discord's own message id.
func SendDiscordReply(ctx context.Context, a *app.App, workspaceID, connID, channelID, text string) (string, error) {
	_, botSealed, _, err := getDiscord(ctx, a.SQLDB, workspaceID, connID)
	if err != nil {
		return "", err
	}
	if botSealed == "" {
		return "", fmt.Errorf("this Discord connection has no bot token configured")
	}
	token, err := discordBotBox.Open(botSealed)
	if err != nil {
		return "", err
	}
	raw, err := discordCall(ctx, token, http.MethodPost, "/channels/"+channelID+"/messages", map[string]any{"content": text})
	if err != nil {
		return "", err
	}
	var r struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &r)
	return "discord:" + r.ID, nil
}

func recordDiscordError(workspaceID, id, msg string) {
	if globalApp == nil {
		return
	}
	sctx := discordCtx(workspaceID)
	_, _ = globalApp.SQLDB.ExecContext(sctx, `
		UPDATE discord_channels SET last_error = $3, last_error_at = now(), updated_at = now()
		WHERE workspace_id = $1 AND id = $2`, workspaceID, id, trimTo(msg, 300))
}

func setGatewayState(workspaceID, id, state string) {
	if globalApp == nil {
		return
	}
	sctx := discordCtx(workspaceID)
	_, _ = globalApp.SQLDB.ExecContext(sctx, `
		UPDATE discord_channels SET gateway_state = $3, updated_at = now() WHERE workspace_id = $1 AND id = $2`,
		workspaceID, id, state)
}

func markDiscordDelivery(workspaceID, id string) {
	if globalApp == nil {
		return
	}
	sctx := discordCtx(workspaceID)
	_, _ = globalApp.SQLDB.ExecContext(sctx, `UPDATE discord_channels SET last_delivery_at = now() WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
}

// ---- the Discord REST API client ----------------------------------------

func discordCall(ctx context.Context, token, method, path string, params map[string]any) ([]byte, error) {
	var body io.Reader
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, discordAPIBase()+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bot "+token)
	if params != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Message
		if msg == "" {
			msg = fmt.Sprintf("Discord returned HTTP %d", res.StatusCode)
		}
		return nil, fmt.Errorf("discord: %s", msg)
	}
	return raw, nil
}
