package google

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/togo-framework/auth"

	"github.com/fadymondy/moharrik/internal/app"
	"github.com/fadymondy/moharrik/internal/authz"
	"github.com/fadymondy/moharrik/internal/tenant"
)

var tags = []string{"Integrations"}

type wsHeader struct {
	WorkspaceID string `header:"X-Workspace-ID" doc:"Workspace id or slug. Non-members get 404."`
}

type body[T any] struct{ Body T }

func scope(ctx context.Context) (*tenant.Scope, error) {
	s, ok := tenant.From(ctx)
	if !ok {
		return nil, huma.Error404NotFound("workspace not found")
	}
	if !authz.Can(s.Role, authz.IntegrationsManage) {
		return nil, huma.Error403Forbidden("connecting Google accounts needs the admin role in this workspace")
	}
	return s, nil
}

// ConnectionsOut is the integrations page.
type ConnectionsOut struct {
	// Configured is false when this server has no Google OAuth client: the
	// page says so instead of offering buttons that cannot work.
	Configured bool `json:"configured"`
	// RedirectURL is the callback that must be registered on the OAuth
	// client in the Google Cloud console.
	RedirectURL string       `json:"redirect_url"`
	Connections []Connection `json:"connections"`
}

// ConnectOut is where to send the browser.
type ConnectOut struct {
	URL     string   `json:"url"`
	Product Product  `json:"product"`
	Scopes  []string `json:"scopes"`
}

func stateCookie(value string, maxAge int) http.Cookie {
	return http.Cookie{
		Name: cookieName, Value: value, Path: cookiePath, MaxAge: maxAge, HttpOnly: true,
		Secure:   strings.HasPrefix(LoadConfig().RedirectURL, "https://"),
		SameSite: http.SameSiteLaxMode,
	}
}

// Register mounts the integration routes. `a` may be nil (OpenAPI export).
//
// The three /api/w/google routes are in the guard's workspace tier. The
// callback is in the SIGNED-IN tier (internal/server/guard.go): it carries no
// X-Workspace-ID, so it re-derives the workspace from the state, but it must
// come back to the same signed-in user, so a session is required.
func Register(api huma.API, a *app.App) {
	huma.Register(api, huma.Operation{
		OperationID: "w-google-connections", Method: http.MethodGet, Path: "/api/w/google/connections",
		Summary: "The Google accounts this workspace has connected (no token is ever returned)", Tags: tags,
	}, func(ctx context.Context, in *struct{ wsHeader }) (*body[ConnectionsOut], error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		list, err := List(ctx, a, s.WorkspaceID)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not list the Google connections")
		}
		cfg := LoadConfig()
		return &body[ConnectionsOut]{ConnectionsOut{Configured: cfg.Ready(), RedirectURL: cfg.RedirectURL, Connections: list}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-google-disconnect", Method: http.MethodDelete, Path: "/api/w/google/connections/{id}",
		Summary: "Revoke a Google account's grant at Google and remove it from this workspace", Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		ID string `path:"id"`
	}) (*body[Disconnected], error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		out, err := Disconnect(ctx, a, s.WorkspaceID, in.ID)
		if errors.Is(err, ErrNotConnected) {
			return nil, huma.Error404NotFound(ErrNotConnected.Error())
		}
		if err != nil {
			return nil, huma.Error500InternalServerError("could not disconnect the Google account")
		}
		return &body[Disconnected]{*out}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "w-google-connect", Method: http.MethodGet, Path: "/api/w/google/connect",
		Summary: "Start connecting a Google account for one product",
		Description: "Answers the Google consent URL rather than redirecting: this is a workspace route, and a " +
			"browser navigation cannot carry X-Workspace-ID. The page sends the browser to `url`. " +
			"The state cookie set here is what the callback checks.",
		Tags: tags,
	}, func(ctx context.Context, in *struct {
		wsHeader
		Product  string `query:"product" enum:"analytics,search_console,youtube" required:"true"`
		Monetary bool   `query:"monetary" doc:"YouTube only: also ask for revenue reporting (yt-analytics-monetary.readonly)."`
		Locale   string `query:"locale" enum:",en,ar" doc:"The page to come back to."`
		// A connection being repaired: its other products are asked for again,
		// and its account is pre-selected.
		Connection string `query:"connection" doc:"Reconnect: the id of a connection in this workspace."`
	}) (*struct {
		SetCookie    http.Cookie `header:"Set-Cookie"`
		CacheControl string      `header:"Cache-Control"`
		Body         ConnectOut
	}, error) {
		s, err := scope(ctx)
		if err != nil {
			return nil, err
		}
		product, ok := ParseProduct(in.Product)
		if !ok {
			return nil, huma.Error422UnprocessableEntity("unknown product")
		}
		started, err := StartConnect(ctx, a, s, product, in.Monetary, in.Locale, in.Connection)
		if errors.Is(err, ErrNotConfigured) {
			return nil, huma.Error503ServiceUnavailable(ErrNotConfigured.Error())
		}
		if errors.Is(err, ErrNotConnected) {
			return nil, huma.Error404NotFound(ErrNotConnected.Error())
		}
		if err != nil {
			logWarn(a, "google connect: start failed", err)
			return nil, huma.Error500InternalServerError("could not start the Google connect")
		}
		return &struct {
			SetCookie    http.Cookie `header:"Set-Cookie"`
			CacheControl string      `header:"Cache-Control"`
			Body         ConnectOut
		}{
			SetCookie:    stateCookie(started.State, int(stateTTL.Seconds())),
			CacheControl: "no-store",
			Body:         ConnectOut{URL: started.URL, Product: product, Scopes: started.Scopes},
		}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "google-oauth-callback", Method: http.MethodGet, Path: CallbackPath,
		Summary:       "Google's redirect back after consent (browser only)",
		Description:   "Always answers 302 to the workspace's integrations page with ?google=<result>. Never returns a token.",
		DefaultStatus: http.StatusFound,
		Tags:          tags,
	}, func(ctx context.Context, in *struct {
		State  string `query:"state"`
		Code   string `query:"code"`
		Error  string `query:"error"`
		Cookie string `cookie:"moh_google_state"`
	}) (*struct {
		Status       int
		Location     string      `header:"Location"`
		SetCookie    http.Cookie `header:"Set-Cookie"`
		CacheControl string      `header:"Cache-Control"`
	}, error) {
		cb := CallbackInput{State: in.State, Code: in.Code, Error: in.Error, CookieState: in.Cookie}
		if id, ok := auth.IdentityFrom(ctx); ok && id != nil {
			cb.UserID = id.ID
		}
		outcome := Callback(ctx, a, cb)
		return &struct {
			Status       int
			Location     string      `header:"Location"`
			SetCookie    http.Cookie `header:"Set-Cookie"`
			CacheControl string      `header:"Cache-Control"`
		}{
			Status:   http.StatusFound,
			Location: outcome.Location(),
			// Whatever happened, the cookie is spent.
			SetCookie:    stateCookie("", -1),
			CacheControl: "no-store",
		}, nil
	})
}
