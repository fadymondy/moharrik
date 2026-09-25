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

type slackList struct {
	Channels []SlackChannel `json:"channels"`
}
type slackOne struct {
	Channel SlackChannel `json:"channel"`
}
type slackBody struct {
	Name              string `json:"name" minLength:"1" maxLength:"80"`
	BotToken          string `json:"bot_token,omitempty" maxLength:"200" doc:"xoxb-… from the Slack app's OAuth & Permissions page. Omit to keep the stored one."`
	SigningSecret     string `json:"signing_secret,omitempty" maxLength:"200" doc:"From the Slack app's Basic Information page. Omit to keep the stored one."`
	AllowedChannelIDs string `json:"allowed_channel_ids,omitempty" maxLength:"2000" doc:"Comma-separated Slack channel ids; empty allows every channel/DM the bot is in."`
	Enabled           *bool  `json:"enabled,omitempty"`
}

func (b slackBody) input() SlackInput {
	return SlackInput{Name: b.Name, BotToken: b.BotToken, SigningSecret: b.SigningSecret, AllowedChannelIDs: b.AllowedChannelIDs, Enabled: b.Enabled}
}

func slackErr(err error) error {
	if errors.Is(err, ErrSlackNotFound) {
		return huma.Error404NotFound(err.Error())
	}
	return huma.Error422UnprocessableEntity(err.Error())
}

// RegisterSlack mounts the Slack channel's settings under inbox:manage.
func RegisterSlack(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-slack-channels", Method: http.MethodGet, Path: "/api/w/slack-channels",
		Summary: "This workspace's Slack app connections", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[slackList], error) {
		s, err := scope(ctx, authz.InboxRead)
		if err != nil {
			return nil, err
		}
		list, err := ListSlack(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list Slack connections")
		}
		return out(slackList{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-slack-channel", Method: http.MethodPost, Path: "/api/w/slack-channels",
		DefaultStatus: http.StatusCreated,
		Summary:       "Add a Slack app connection. The Event Subscriptions Request URL is returned for pasting into the Slack app.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body slackBody
	}) (*body[slackOne], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		c, err := CreateSlack(ctx, a, s.WorkspaceID, s.UserID, in.Body.input())
		if err != nil {
			return nil, slackErr(err)
		}
		return out(slackOne{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-slack-channel", Method: http.MethodPut, Path: "/api/w/slack-channels/{id}",
		Summary: "Edit a Slack connection (omit bot_token/signing_secret to keep them)", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body slackBody
	}) (*body[slackOne], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		c, err := UpdateSlack(ctx, a, s.WorkspaceID, in.ID, in.Body.input())
		if err != nil {
			return nil, slackErr(err)
		}
		return out(slackOne{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-slack-channel", Method: http.MethodDelete, Path: "/api/w/slack-channels/{id}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Remove a Slack connection. Conversations already collected stay.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		if err := DeleteSlack(ctx, a, s.WorkspaceID, in.ID); err != nil {
			return nil, slackErr(err)
		}
		return nil, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-test-slack-channel", Method: http.MethodPost, Path: "/api/w/slack-channels/{id}/test",
		Summary: "Verify the bot token (auth.test) and record the team and bot identity", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[slackOne], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		c, err := TestSlack(ctx, a, s.WorkspaceID, in.ID)
		if err != nil {
			if c == nil {
				return nil, slackErr(err)
			}
			return nil, huma.Error502BadGateway(err.Error())
		}
		return out(slackOne{*c}), nil
	})
}

// RegisterSlackHook mounts the public Events API endpoint. NOT a huma route:
// Slack's payload varies by event, the url_verification handshake answers
// plain text rather than JSON, and the unguessable webhook_id plus a verified
// signature are the whole authorization (MH-563: "Verify Slack signatures").
func RegisterSlackHook(r chi.Router, a *app.App) {
	r.Post("/api/slack/hook/{id}", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		wf, err := slackByWebhookID(ctx, a, chi.URLParam(r, "id"))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !VerifySlackSignature(wf.signingSecret, r.Header.Get("X-Slack-Request-Timestamp"), r.Header.Get("X-Slack-Signature"), raw) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		var e SlackEnvelope
		if json.Unmarshal(raw, &e) != nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		if e.Type == "url_verification" {
			// Slack's own handshake, answered in plain text — the one delivery
			// with no signature-bearing predecessor, since it happens the moment
			// the URL is saved.
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(e.Challenge))
			return
		}

		markSlackDelivery(ctx, a, wf.workspaceID, wf.id)
		in, ok := e.Read()
		if ok && slackAllowed(wf.allowedChannelIDs, in.ChannelID) {
			if err := deliver(ctx, wf.workspaceID, "slack", wf.id, in.ChannelID, in.UserID, "", in.Text, "slack:"+in.Ts, in.IsGroup); err != nil {
				http.Error(w, "could not file", http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	})
}

type slackWebhookLookup struct {
	id, workspaceID, signingSecret, allowedChannelIDs string
}

func slackByWebhookID(ctx context.Context, a *app.App, webhookID string) (slackWebhookLookup, error) {
	var wf slackWebhookLookup
	var signingSealed string
	err := a.SysDB.QueryRowContext(ctx, `
		SELECT id, workspace_id, signing_secret_sealed, allowed_channel_ids FROM slack_channels WHERE webhook_id = $1`,
		webhookID).Scan(&wf.id, &wf.workspaceID, &signingSealed, &wf.allowedChannelIDs)
	if err != nil {
		return wf, err
	}
	wf.signingSecret, _ = slackSigningBox.Open(signingSealed)
	return wf, nil
}

func markSlackDelivery(ctx context.Context, a *app.App, workspaceID, id string) {
	sctx := tenant.WithWorkspace(ctx, workspaceID)
	_, _ = a.SQLDB.ExecContext(sctx, `UPDATE slack_channels SET last_delivery_at = now() WHERE workspace_id = $1 AND id = $2`, workspaceID, id)
}
