package channels

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

type discordList struct {
	Channels []DiscordChannel `json:"channels"`
}
type discordOne struct {
	Channel DiscordChannel `json:"channel"`
}
type discordBody struct {
	Name              string `json:"name" minLength:"1" maxLength:"80"`
	BotToken          string `json:"bot_token,omitempty" maxLength:"200" doc:"From the Discord developer portal's Bot page. Omit to keep the stored one."`
	ApplicationID     string `json:"application_id,omitempty" maxLength:"64"`
	PublicKey         string `json:"public_key,omitempty" maxLength:"128" doc:"The application's public key, for verifying interactions."`
	AllowedGuildIDs   string `json:"allowed_guild_ids,omitempty" maxLength:"2000" doc:"Comma-separated guild ids; empty allows every guild the bot is in."`
	AllowedChannelIDs string `json:"allowed_channel_ids,omitempty" maxLength:"2000" doc:"Comma-separated channel ids; empty allows every channel in an allowed guild, plus DMs."`
	Enabled           *bool  `json:"enabled,omitempty"`
}

func (b discordBody) input() DiscordInput {
	return DiscordInput{Name: b.Name, BotToken: b.BotToken, ApplicationID: b.ApplicationID, PublicKey: b.PublicKey,
		AllowedGuildIDs: b.AllowedGuildIDs, AllowedChannelIDs: b.AllowedChannelIDs, Enabled: b.Enabled}
}

func discordErr(err error) error {
	if errors.Is(err, ErrDiscordNotFound) {
		return huma.Error404NotFound(err.Error())
	}
	return huma.Error422UnprocessableEntity(err.Error())
}

// RegisterDiscord mounts the Discord channel's settings under inbox:manage.
func RegisterDiscord(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-discord-channels", Method: http.MethodGet, Path: "/api/w/discord-channels",
		Summary: "This workspace's Discord bot connections", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[discordList], error) {
		s, err := scope(ctx, authz.InboxRead)
		if err != nil {
			return nil, err
		}
		list, err := ListDiscord(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list Discord connections")
		}
		return out(discordList{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-discord-channel", Method: http.MethodPost, Path: "/api/w/discord-channels",
		DefaultStatus: http.StatusCreated,
		Summary:       "Add a Discord bot connection. Given a token, its gateway session starts at once.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body discordBody
	}) (*body[discordOne], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		c, err := CreateDiscord(ctx, a, s.WorkspaceID, s.UserID, in.Body.input())
		if err != nil {
			return nil, discordErr(err)
		}
		return out(discordOne{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-discord-channel", Method: http.MethodPut, Path: "/api/w/discord-channels/{id}",
		Summary: "Edit a Discord connection (omit bot_token to keep it)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body discordBody
	}) (*body[discordOne], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		c, err := UpdateDiscord(ctx, a, s.WorkspaceID, in.ID, in.Body.input())
		if err != nil {
			return nil, discordErr(err)
		}
		return out(discordOne{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-discord-channel", Method: http.MethodDelete, Path: "/api/w/discord-channels/{id}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Remove a Discord connection. Conversations already collected stay.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		if err := DeleteDiscord(ctx, a, s.WorkspaceID, in.ID); err != nil {
			return nil, discordErr(err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-test-discord-channel", Method: http.MethodPost, Path: "/api/w/discord-channels/{id}/test",
		Summary: "Verify the bot token (GET /users/@me) and (re)start the gateway session", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[discordOne], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		c, err := TestDiscord(ctx, a, s.WorkspaceID, in.ID)
		if err != nil {
			if c == nil {
				return nil, discordErr(err)
			}
			return nil, huma.NewError(http.StatusFailedDependency, err.Error())
		}
		return out(discordOne{*c}), nil
	})
}

// RegisterDiscordHook mounts the public Interactions Endpoint. NOT a huma
// route: Discord requires an unsigned-body Ed25519 verification before any
// JSON parsing, and a bare 401 (not our usual 404) on a bad signature — the
// exact shape Discord's own verification middleware expects.
func RegisterDiscordHook(r chi.Router, a *app.App) {
	r.Post("/api/discord/hook/{id}", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		wf, err := discordByWebhookID(ctx, a, chi.URLParam(r, "id"))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !verifyDiscordSignature(wf.publicKey, r.Header.Get("X-Signature-Ed25519"), r.Header.Get("X-Signature-Timestamp"), raw) {
			http.Error(w, "invalid request signature", http.StatusUnauthorized)
			return
		}

		var body struct {
			Type int `json:"type"`
		}
		if json.Unmarshal(raw, &body) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// type 1 is PING, Discord's own verification check when the URL is
		// saved; every other interaction type gets a minimal ack — this
		// endpoint exists to prove the connection, not to run slash commands.
		if body.Type == 1 {
			_, _ = w.Write([]byte(`{"type":1}`))
			return
		}
		_, _ = w.Write([]byte(`{"type":5}`)) // deferred channel message with source
	})
}

// verifyDiscordSignature checks Discord's Ed25519 signature over
// timestamp+body against the connection's public key.
func verifyDiscordSignature(publicKeyHex, sigHex, timestamp string, body []byte) bool {
	if publicKeyHex == "" || sigHex == "" || timestamp == "" {
		return false
	}
	pubKey, err := hex.DecodeString(publicKeyHex)
	if err != nil || len(pubKey) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	msg := append([]byte(timestamp), body...)
	return ed25519.Verify(pubKey, msg, sig)
}

type discordWebhookLookup struct {
	id, workspaceID, publicKey string
}

func discordByWebhookID(ctx context.Context, a *app.App, webhookID string) (discordWebhookLookup, error) {
	var wf discordWebhookLookup
	err := a.SysDB.QueryRowContext(ctx, `
		SELECT id, workspace_id, public_key FROM discord_channels WHERE webhook_id = $1`,
		webhookID).Scan(&wf.id, &wf.workspaceID, &wf.publicKey)
	return wf, err
}
