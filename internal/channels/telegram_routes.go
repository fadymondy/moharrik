package channels

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

type telegramList struct {
	Channels []TelegramChannel `json:"channels"`
}
type telegramOne struct {
	Channel TelegramChannel `json:"channel"`
}
type telegramBody struct {
	Name           string `json:"name" minLength:"1" maxLength:"80"`
	BotToken       string `json:"bot_token,omitempty" maxLength:"200" doc:"From @BotFather. Omit to keep the stored one."`
	AllowedChatIDs string `json:"allowed_chat_ids,omitempty" maxLength:"2000" doc:"Comma-separated chat/group ids; empty allows every chat that reaches the bot."`
	Enabled        *bool  `json:"enabled,omitempty"`
}

func (b telegramBody) input() TelegramInput {
	return TelegramInput{Name: b.Name, BotToken: b.BotToken, AllowedChatIDs: b.AllowedChatIDs, Enabled: b.Enabled}
}

func telegramErr(err error) error {
	switch {
	case errors.Is(err, ErrTelegramNotFound):
		return huma.Error404NotFound(err.Error())
	}
	return huma.Error422UnprocessableEntity(err.Error())
}

// RegisterTelegram mounts the Telegram channel's settings under inbox:manage
// — the same right as linking WhatsApp, because it decides what can write
// into the workspace's inbox.
func RegisterTelegram(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-telegram-channels", Method: http.MethodGet, Path: "/api/w/telegram-channels",
		Summary: "This workspace's Telegram bot connections", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[telegramList], error) {
		s, err := scope(ctx, authz.InboxRead)
		if err != nil {
			return nil, err
		}
		list, err := ListTelegram(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list Telegram connections")
		}
		return out(telegramList{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-telegram-channel", Method: http.MethodPost, Path: "/api/w/telegram-channels",
		DefaultStatus: http.StatusCreated,
		Summary:       "Add a Telegram bot connection. Given a token, its webhook is registered with Telegram at once.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body telegramBody
	}) (*body[telegramOne], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		c, err := CreateTelegram(ctx, a, s.WorkspaceID, s.UserID, in.Body.input())
		if err != nil {
			return nil, telegramErr(err)
		}
		return out(telegramOne{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-telegram-channel", Method: http.MethodPut, Path: "/api/w/telegram-channels/{id}",
		Summary: "Edit a Telegram connection (omit bot_token to keep it)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body telegramBody
	}) (*body[telegramOne], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		c, err := UpdateTelegram(ctx, a, s.WorkspaceID, in.ID, in.Body.input())
		if err != nil {
			return nil, telegramErr(err)
		}
		return out(telegramOne{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-telegram-channel", Method: http.MethodDelete, Path: "/api/w/telegram-channels/{id}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Remove a Telegram connection. Conversations already collected stay.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		if err := DeleteTelegram(ctx, a, s.WorkspaceID, in.ID); err != nil {
			return nil, telegramErr(err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-test-telegram-channel", Method: http.MethodPost, Path: "/api/w/telegram-channels/{id}/test",
		Summary: "Verify the bot token (getMe) and re-register the webhook", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[telegramOne], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		c, err := TestTelegram(ctx, a, s.WorkspaceID, in.ID)
		if err != nil {
			if c == nil {
				return nil, telegramErr(err)
			}
			return nil, huma.Error502BadGateway(err.Error())
		}
		return out(telegramOne{*c}), nil
	})
}

// RegisterTelegramHook mounts the public webhook. NOT a huma route, for the
// same reason internal/whatsapp's is not: Telegram's update shape varies, and
// the unguessable id in the path plus the secret_token header are the whole
// authorization for a public endpoint.
func RegisterTelegramHook(r chi.Router, a *app.App) {
	r.Post("/api/telegram/hook/{id}", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		wf, err := telegramByWebhookID(ctx, a, chi.URLParam(r, "id"))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if wf.secretToken != "" && !SameToken(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"), wf.secretToken) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		markTelegramDelivery(ctx, a, wf.workspaceID, wf.id)

		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var u TelegramUpdate
		if json.Unmarshal(raw, &u) != nil {
			w.WriteHeader(http.StatusOK) // malformed is accepted and dropped, like WhatsApp's own echo case
			return
		}
		in, ok := u.Read()
		if ok && telegramAllowed(wf.allowedChatIDs, in.ChatID) {
			if err := deliver(ctx, wf.workspaceID, "telegram", wf.id, in.ChatID, in.Handle, in.Name, in.Text, "tg:"+in.MessageID); err != nil {
				http.Error(w, "could not file", http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	})
}

type telegramWebhookLookup struct {
	id, workspaceID, secretToken, allowedChatIDs string
}

func telegramByWebhookID(ctx context.Context, a *app.App, webhookID string) (telegramWebhookLookup, error) {
	var wf telegramWebhookLookup
	var secretSealed string
	err := a.SysDB.QueryRowContext(ctx, `
		SELECT id, workspace_id, secret_token_sealed, allowed_chat_ids FROM telegram_channels WHERE webhook_id = $1`,
		webhookID).Scan(&wf.id, &wf.workspaceID, &secretSealed, &wf.allowedChatIDs)
	if err != nil {
		return wf, err
	}
	wf.secretToken, _ = telegramSecretBox.Open(secretSealed)
	return wf, nil
}

func markTelegramDelivery(ctx context.Context, a *app.App, workspaceID, id string) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `UPDATE telegram_channels SET last_delivery_at = now() WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
}
