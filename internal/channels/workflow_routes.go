package channels

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
)

type workflowChannelList struct {
	Channels []WorkflowChannel `json:"channels"`
}
type workflowChannelOne struct {
	Channel WorkflowChannel `json:"channel"`
}
type workflowChannelBody struct {
	Name         string  `json:"name" minLength:"1" maxLength:"80"`
	Network      string  `json:"network" enum:"linkedin,facebook,instagram,x"`
	WorkflowID   *string `json:"workflow_id,omitempty" doc:"The workflow that reads this network's inbox; can be set later."`
	AllowReplies *bool   `json:"allow_replies,omitempty" doc:"Reply through the network's reply preset, with confirmation (off by default)."`
	Enabled      *bool   `json:"enabled,omitempty"`
}

func (b workflowChannelBody) input() WorkflowChannelInput {
	return WorkflowChannelInput{Name: b.Name, Network: b.Network, WorkflowID: b.WorkflowID, AllowReplies: b.AllowReplies, Enabled: b.Enabled}
}

func workflowChannelErr(err error) error {
	if errors.Is(err, ErrWorkflowChannelNotFound) {
		return huma.Error404NotFound(err.Error())
	}
	return huma.Error422UnprocessableEntity(err.Error())
}

// RegisterWorkflowChannels mounts workflow channel settings under
// inbox:manage, and the push endpoint the step runtime calls under
// automation:run — a workflow run pushing into the inbox is an automation
// act, not a settings change.
func RegisterWorkflowChannels(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-workflow-channels", Method: http.MethodGet, Path: "/api/w/workflow-channels",
		Summary: "This workspace's browser-workflow inbox channels (LinkedIn, Facebook, Instagram, X)", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[workflowChannelList], error) {
		s, err := scope(ctx, authz.InboxRead)
		if err != nil {
			return nil, err
		}
		list, err := ListWorkflowChannels(ctx, a.SQLDB, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list workflow channels")
		}
		return out(workflowChannelList{list}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-create-workflow-channel", Method: http.MethodPost, Path: "/api/w/workflow-channels",
		DefaultStatus: http.StatusCreated,
		Summary:       "Add a browser-workflow inbox channel for a network with no usable API", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body workflowChannelBody
	}) (*body[workflowChannelOne], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		c, err := CreateWorkflowChannel(ctx, a, s.WorkspaceID, s.UserID, in.Body.input())
		if err != nil {
			return nil, workflowChannelErr(err)
		}
		return out(workflowChannelOne{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-update-workflow-channel", Method: http.MethodPut, Path: "/api/w/workflow-channels/{id}",
		Summary: "Edit a workflow channel", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body workflowChannelBody
	}) (*body[workflowChannelOne], error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		c, err := UpdateWorkflowChannel(ctx, a, s.WorkspaceID, in.ID, in.Body.input())
		if err != nil {
			return nil, workflowChannelErr(err)
		}
		return out(workflowChannelOne{*c}), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-delete-workflow-channel", Method: http.MethodDelete, Path: "/api/w/workflow-channels/{id}",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Remove a workflow channel. Conversations already collected stay.", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*struct{}, error) {
		s, err := scope(ctx, authz.InboxManage)
		if err != nil {
			return nil, err
		}
		if err := DeleteWorkflowChannel(ctx, a, s.WorkspaceID, in.ID); err != nil {
			return nil, workflowChannelErr(err)
		}
		return nil, nil
	})

	type pushItemBody struct {
		ThreadID   string `json:"thread_id" minLength:"1"`
		MessageID  string `json:"message_id" minLength:"1"`
		ProfileURL string `json:"profile_url,omitempty"`
		Name       string `json:"name,omitempty"`
		Text       string `json:"text" minLength:"1" maxLength:"8000"`
		SentAt     string `json:"sent_at,omitempty" doc:"RFC 3339; defaults to now"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "w-push-workflow-channel", Method: http.MethodPost, Path: "/api/w/workflow-channels/{id}/push",
		Summary: "Push to inbox: file the messages a browser workflow's read step collected. Idempotent by (thread_id, message_id).", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID   string `path:"id"`
		Body struct {
			Items []pushItemBody `json:"items"`
		}
	}) (*body[PushResult], error) {
		s, err := scope(ctx, authz.AutomationRun)
		if err != nil {
			return nil, err
		}
		items := make([]PushItem, 0, len(in.Body.Items))
		for _, it := range in.Body.Items {
			sentAt := time.Now()
			if it.SentAt != "" {
				if t, err := time.Parse(time.RFC3339, it.SentAt); err == nil {
					sentAt = t
				}
			}
			items = append(items, PushItem{ThreadID: it.ThreadID, MessageID: it.MessageID, ProfileURL: it.ProfileURL,
				Name: it.Name, Text: it.Text, SentAt: sentAt})
		}
		res, err := PushMessages(ctx, a, s.WorkspaceID, in.ID, items)
		if err != nil {
			RecordPushError(ctx, a, s.WorkspaceID, in.ID, err)
			return nil, workflowChannelErr(err)
		}
		return out(res), nil
	})
}
