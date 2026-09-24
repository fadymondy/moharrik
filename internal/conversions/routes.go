package conversions

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/google"
	"github.com/fadymondy/moharrik/internal/tenant"
)

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func out[T any](v T) *body[T] { return &body[T]{Body: v} }

// Setting up where a workspace's funnel is reported is an admin's call, like
// connecting the Google account itself (integrations:manage): the API secret
// writes into the workspace's GA4 property.
func scope(ctx context.Context) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, authz.IntegrationsManage) {
		return nil, huma.Error403Forbidden("only the workspace's owners and admins set up GA4 conversions")
	}
	return s, nil
}

// ConversionsPage is the settings page's answer.
type ConversionsPage struct {
	Settings ConversionSettings    `json:"settings"`
	Events   []ConversionEventInfo `json:"events" doc:"What is sent, and when."`
	Recent   []ConversionEvent     `json:"recent" doc:"The latest 20 events and what happened to each."`
}

// GA4Stream is one GA4 web data stream the workspace's connected Google accounts can see.
type GA4Stream struct {
	Property      string `json:"property" doc:"properties/123"`
	PropertyName  string `json:"property_name"`
	Name          string `json:"name"`
	MeasurementID string `json:"measurement_id"`
	URI           string `json:"uri"`
}

// GA4StreamsOut lists the web streams of the enabled GA4 properties.
type GA4StreamsOut struct {
	Streams []GA4Stream `json:"streams"`
	// Why a property's streams could not be read, one line per property.
	Errors []string `json:"errors"`
}

func page(ctx context.Context, a *app.App, ws string) (*body[ConversionsPage], error) {
	s, err := Load(ctx, a.SQLDB, ws)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the GA4 settings")
	}
	recent, err := Recent(ctx, a.SQLDB, ws, 20)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not read the GA4 events")
	}
	return out(ConversionsPage{Settings: s, Events: Events, Recent: recent}), nil
}

// Register mounts the routes. `a` may be nil (OpenAPI export).
func Register(api huma.API, a *app.App) {
	tags := []string{"GA4 conversions"}

	huma.Register(api, huma.Operation{
		OperationID: "w-ga4-conversions", Method: http.MethodGet, Path: "/api/w/ga4-conversions",
		Summary: "The workspace's GA4 conversions setup (never the API secret) and its latest events", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[ConversionsPage], error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		return page(ctx, a, s.WorkspaceID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-ga4-conversions-update", Method: http.MethodPatch, Path: "/api/w/ga4-conversions",
		Summary: "Set the measurement id and API secret, and switch the events and the embed tag on or off", Tags: tags,
		Description: "api_secret: omit to keep the stored one, \"\" to remove it. Events need both the measurement id and the " +
			"secret; the tag needs the measurement id. The secret is sealed at rest and never returned.",
	}, func(ctx context.Context, in *struct {
		wsHeader
		Body struct {
			MeasurementID *string `json:"measurement_id,omitempty" maxLength:"24"`
			APISecret     *string `json:"api_secret,omitempty" maxLength:"200"`
			EventsEnabled *bool   `json:"events_enabled,omitempty"`
			TagInEmbeds   *bool   `json:"tag_in_embeds,omitempty"`
			DebugMode     *bool   `json:"debug_mode,omitempty"`
		}
	}) (*body[ConversionsPage], error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		b := in.Body
		_, err = Save(ctx, a.SQLDB, s.WorkspaceID, s.UserID, Change{
			MeasurementID: b.MeasurementID, APISecret: b.APISecret,
			EventsEnabled: b.EventsEnabled, TagInEmbeds: b.TagInEmbeds, DebugMode: b.DebugMode,
		})
		switch {
		case errors.Is(err, ErrMeasurementID), errors.Is(err, ErrAPISecret), errors.Is(err, ErrNeedsBoth), errors.Is(err, ErrNeedsID):
			return nil, huma.Error422UnprocessableEntity(err.Error())
		case err != nil:
			return nil, huma.Error500InternalServerError("could not save the GA4 settings")
		}
		return page(ctx, a, s.WorkspaceID)
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-ga4-conversions-test", Method: http.MethodPost, Path: "/api/w/ga4-conversions/test",
		Summary: "Check the setup with GA4's validation server, then send a test event marked for DebugView", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[ConversionTestResult], error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		r, err := SendTest(ctx, a.SQLDB, s.WorkspaceID)
		if errors.Is(err, ErrNeedsBoth) {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not send the test event")
		}
		return out(r), nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-ga4-conversions-streams", Method: http.MethodGet, Path: "/api/w/ga4-conversions/streams",
		Summary: "The web data streams (and their measurement ids) of the GA4 properties this workspace reads", Tags: tags,
		Description: "Read through the connected Google account (ConversionSettings → Integrations). The API secret cannot be read " +
			"this way: GA4 shows it only in its own Admin, under the stream's Measurement Protocol API secrets.",
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[GA4StreamsOut], error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		return out(streams(ctx, a, s.WorkspaceID)), nil
	})
}

// streams asks Google, through each enabled GA4 property's connection, for the
// property's web data streams.
func streams(ctx context.Context, a *app.App, ws string) GA4StreamsOut {
	res := GA4StreamsOut{Streams: []GA4Stream{}, Errors: []string{}}
	props, err := google.ListResources(ctx, a, ws, google.Filter{Kind: google.KindGA4, EnabledOnly: true})
	if err != nil {
		res.Errors = append(res.Errors, "could not read the GA4 properties")
		return res
	}
	for _, p := range props {
		if !strings.HasPrefix(p.ExternalID, "properties/") {
			continue
		}
		src, err := google.SourceFor(ctx, a, p, ws)
		if err != nil {
			res.Errors = append(res.Errors, p.DisplayName+": "+google.Describe(err))
			continue
		}
		var list struct {
			DataStreams []struct {
				Name          string `json:"name"`
				Type          string `json:"type"`
				DisplayName   string `json:"displayName"`
				WebStreamData *struct {
					MeasurementID string `json:"measurementId"`
					DefaultURI    string `json:"defaultUri"`
				} `json:"webStreamData"`
			} `json:"dataStreams"`
		}
		q := url.Values{"pageSize": {"50"}}
		if err := src.Client(google.ScopeAnalytics).Get(ctx, google.API().AnalyticsAdmin+"/"+p.ExternalID+"/dataStreams?"+q.Encode(), &list); err != nil {
			res.Errors = append(res.Errors, p.DisplayName+": "+google.Describe(err))
			continue
		}
		for _, d := range list.DataStreams {
			if d.WebStreamData == nil || !ValidMeasurementID(d.WebStreamData.MeasurementID) {
				continue
			}
			res.Streams = append(res.Streams, GA4Stream{Property: p.ExternalID, PropertyName: p.DisplayName, Name: d.DisplayName,
				MeasurementID: d.WebStreamData.MeasurementID, URI: d.WebStreamData.DefaultURI})
		}
	}
	return res
}
