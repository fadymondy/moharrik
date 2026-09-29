// Command api is the moharrik HTTP entrypoint. It boots the shared togo stack
// (Huma REST + OpenAPI and gqlgen GraphQL on the kernel) and serves it.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/fadymondy/moharrik/internal/envfile"
	"github.com/fadymondy/moharrik/internal/server"
	"github.com/fadymondy/moharrik/internal/telemetry"
)

func main() {
	envfile.Load(".env") // before togo.New(): the kernel reads only os.Getenv
	if len(os.Args) > 1 && os.Args[1] == "openapi" {
		b, err := server.OpenAPI()
		if err != nil {
			panic(err)
		}
		os.Stdout.Write(b)
		return
	}

	// Before Boot, so the global tracer is in place when routes are built.
	stopTraces := telemetry.Init(slog.Default())
	defer stopTraces(context.Background())

	a := server.Boot()
	defer a.Kernel.Close()
	k := a.Kernel
	fmt.Printf("→ moharrik listening on %s  (GraphQL %s · REST %s · docs %s)\n",
		k.Config.Addr, k.Config.GraphQLPath, k.Config.RESTPath, k.Config.DocsPath)
	if err := k.Serve(context.Background()); err != nil {
		panic(err)
	}
}
