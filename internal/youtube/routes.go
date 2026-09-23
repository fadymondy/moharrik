package youtube

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/tenant"
)

type body[T any] struct{ Body T }

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

var tags = []string{"YouTube"}

// ChannelOut is the YouTube page's header.
type ChannelOut struct {
	// The channel resource; null when the workspace has none enabled.
	Resource *google.Resource `json:"resource"`
	// Null until the first sync has read it.
	Channel *YTChannel `json:"channel"`
	Quota   YTQuota    `json:"quota"`
}

func resolve(ctx context.Context, a *app.App, ws, id string) (*google.Resource, error) {
	r, err := google.ResolveEnabled(ctx, a, ws, google.KindYT, id)
	if errors.Is(err, google.ErrResourceNotFound) {
		return nil, huma.Error404NotFound("that YouTube channel is not enabled in this workspace")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the YouTube channels")
	}
	if r != nil {
		r.ConnectionEmail = ""
	}
	return r, nil
}

// AnalyticsOut is a channel's or a video's YouTube Analytics.
type AnalyticsOut struct {
	Resource  *google.Resource `json:"resource"`
	Analytics *YTAnalytics     `json:"analytics"`
	Error     string           `json:"error,omitempty"`
}

// Register mounts the YouTube routes. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-youtube-channel", Method: http.MethodGet, Path: "/api/w/youtube/channel",
		Summary: "An enabled YouTube channel as last synced, and today's YouTube quota", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Resource string `query:"resource" doc:"An enabled channel's resource id; default the first enabled one."`
	}) (*body[ChannelOut], error) {
		s, _, err := google.ReadScope(ctx)
		if err != nil {
			return nil, err
		}
		r, err := resolve(ctx, a, s.WorkspaceID, in.Resource)
		if err != nil {
			return nil, err
		}
		out := ChannelOut{Resource: r}
		if out.Quota, err = QuotaFor(ctx, a.SysDB, s.WorkspaceID); err != nil {
			return nil, huma.Error500InternalServerError("could not read the YouTube quota")
		}
		if r == nil {
			return &body[ChannelOut]{out}, nil
		}
		if out.Channel, err = GetChannel(tenant.WithWorkspace(ctx, s.WorkspaceID), a.SQLDB, s.WorkspaceID, r.ID); err != nil {
			return nil, huma.Error500InternalServerError("could not read the channel")
		}
		return &body[ChannelOut]{out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-youtube-videos", Method: http.MethodGet, Path: "/api/w/youtube/videos",
		Summary: "Every upload of an enabled channel, newest first, from the last sync", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Resource string `query:"resource"`
		Q        string `query:"q" maxLength:"200"`
		Privacy  string `query:"privacy" enum:",all,public,unlisted,private,scheduled"`
		Cursor   string `query:"cursor"`
		Limit    int    `query:"limit" minimum:"0" maximum:"50"`
	}) (*body[YTVideoPage], error) {
		s, _, err := google.ReadScope(ctx)
		if err != nil {
			return nil, err
		}
		r, err := resolve(ctx, a, s.WorkspaceID, in.Resource)
		if err != nil {
			return nil, err
		}
		if r == nil {
			return nil, huma.Error404NotFound("no YouTube channel is enabled in this workspace")
		}
		page, err := ListVideos(tenant.WithWorkspace(ctx, s.WorkspaceID), a.SQLDB, s.WorkspaceID, r.ID,
			YTVideoFilter{Query: in.Q, Privacy: in.Privacy, Cursor: in.Cursor, Limit: in.Limit})
		var inv ErrInvalid
		if errors.As(err, &inv) {
			return nil, huma.Error422UnprocessableEntity(inv.Msg)
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list the videos")
		}
		return &body[YTVideoPage]{*page}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-youtube-analytics", Method: http.MethodGet, Path: "/api/w/youtube/analytics",
		Summary: "YouTube Analytics for an enabled channel, or one of its videos, over 7, 28 or 90 days",
		Description: "Cached for 6 hours; when Google fails the last answer is served with stale=true and the reason; " +
			"with nothing cached, analytics is null and error says why.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Resource string `query:"resource"`
		Video    string `query:"video" maxLength:"64" doc:"A video id; empty for the whole channel."`
		Days     int    `query:"days" default:"28" doc:"7, 28 or 90."`
		Refresh  bool   `query:"refresh"`
	}) (*body[AnalyticsOut], error) {
		s, _, err := google.ReadScope(ctx)
		if err != nil {
			return nil, err
		}
		if !ValidDays(in.Days) {
			return nil, huma.Error422UnprocessableEntity("days must be 7, 28 or 90")
		}
		r, err := resolve(ctx, a, s.WorkspaceID, in.Resource)
		if err != nil {
			return nil, err
		}
		out := AnalyticsOut{Resource: r}
		if r == nil {
			return &body[AnalyticsOut]{out}, nil
		}
		res, err := GetAnalytics(ctx, a, s.WorkspaceID, *r, in.Video, in.Days, in.Refresh)
		var none *ErrNoAnalytics
		switch {
		case errors.Is(err, ErrNotFound):
			return nil, huma.Error404NotFound(err.Error())
		case errors.As(err, &none):
			out.Error = none.Error()
		case err != nil:
			return nil, huma.Error500InternalServerError("could not read YouTube Analytics")
		default:
			out.Analytics = res
		}
		return &body[AnalyticsOut]{out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-youtube-video", Method: http.MethodGet, Path: "/api/w/youtube/videos/{id}",
		Summary:     "One upload with its top comments",
		Description: "Top comments are re-read from YouTube when older than an hour and the quota allows; comment_note says why they may be older.",
		Tags:        tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID       string `path:"id" maxLength:"64"`
		Resource string `query:"resource"`
	}) (*body[YTVideoDetail], error) {
		s, _, err := google.ReadScope(ctx)
		if err != nil {
			return nil, err
		}
		r, err := resolve(ctx, a, s.WorkspaceID, in.Resource)
		if err != nil {
			return nil, err
		}
		if r == nil {
			return nil, huma.Error404NotFound("no YouTube channel is enabled in this workspace")
		}
		d, err := GetVideo(tenant.WithWorkspace(ctx, s.WorkspaceID), a, s.WorkspaceID, *r, in.ID)
		if errors.Is(err, ErrNotFound) {
			return nil, huma.Error404NotFound(err.Error())
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not read the video")
		}
		return &body[YTVideoDetail]{*d}, nil
	})
}
