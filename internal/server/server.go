// Package server wires the togo kernel with both interfaces (Huma REST/OpenAPI
// and gqlgen GraphQL) and returns a ready App. It lives in its own package so
// both cmd/api (serve) and feature tests can boot the exact same stack.
package server

import (
	"context"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/app"
	graphgen "github.com/fadymondy/moharrik/internal/graph/gen"
	"github.com/fadymondy/moharrik/internal/graph/resolvers"
	"github.com/fadymondy/moharrik/internal/mcp"
	_ "github.com/fadymondy/moharrik/internal/plugins" // blank-imports installed plugins (togo install)
	"github.com/fadymondy/moharrik/internal/rest"
	"github.com/fadymondy/moharrik/internal/tenant"
	"github.com/fadymondy/moharrik/internal/workspaces"
)

// Boot builds the kernel + app and mounts REST, GraphQL, MCP and health on the
// kernel router. It does not start listening — the caller calls Kernel.Serve.
func Boot() *app.App {
	k := togo.New()
	ctx := context.Background()
	a := app.New(ctx, k)

	// Fail closed: without the RLS-enforced app pool, generated CRUD would run
	// on the owner pool with no tenant isolation at all.
	if a.SysDB == nil || a.SQLDB == nil {
		panic("moharrik: DATABASE_URL (owner) and APP_DATABASE_URL (app role) are both required — see README")
	}
	if err := tenant.VerifyAppRole(ctx, a.SQLDB); err != nil {
		panic(err)
	}

	installErrors()
	installProblemReports(k) // outermost: sees what the guard and handlers answer
	installGuard(k, a)

	api := humachi.New(k.Router, huma.DefaultConfig("Moharrik API", "0.1.0"))
	registerREST(api, a)

	mcp.Mount(k, a)

	gql := handler.NewDefaultServer(graphgen.NewExecutableSchema(graphgen.Config{
		Resolvers: &resolvers.Resolver{App: a},
	}))
	k.Router.Handle(k.Config.GraphQLPath, gql)
	k.Router.Handle("/graphql/play", playground.Handler("Moharrik GraphQL", k.Config.GraphQLPath))

	k.Router.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"ok","service":"moharrik","togo":%q}`, togoVersion())
	})

	// Deliberately NOT mounted from the scaffold:
	//   /events               — one global SSE stream; would broadcast every
	//                           tenant's rows to every subscriber (see app.Emit).
	//   /api/_meta/resources  — the dashboard plugin's schema dump; no dashboard.
	return a
}

func registerREST(api huma.API, a *app.App) {
	rest.RegisterRoutes(api, a) // generated CRUD — behind the guard's workspace tier
	workspaces.Register(api, a)
	mcp.RegisterTokenRoutes(api, a)
}

// togoVersion reports the togo framework version this app is built against.
func togoVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, d := range info.Deps {
			if d.Path == "github.com/togo-framework/togo" {
				return d.Version
			}
		}
	}
	return "dev"
}

// OpenAPI returns the generated OpenAPI 3.1 document (used by `togo generate`).
func OpenAPI() ([]byte, error) {
	router := chi.NewMux()
	installErrors()
	api := humachi.New(router, huma.DefaultConfig("Moharrik API", "0.1.0"))
	registerREST(api, nil)
	return api.OpenAPI().YAML()
}
