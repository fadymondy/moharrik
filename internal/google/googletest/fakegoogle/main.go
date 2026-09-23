// Command fakegoogle runs the fake Google (internal/google/googletest) on a
// fixed address for a local browser check of the connect flow (MH-439):
//
//	go run ./internal/google/googletest/fakegoogle -addr 127.0.0.1:18439
//
// and start the API with GOOGLE_API_BASE=http://127.0.0.1:18439,
// GOOGLE_INTEGRATIONS_CLIENT_ID=fake-client.apps.googleusercontent.com and
// GOOGLE_INTEGRATIONS_CLIENT_SECRET=fake-secret. The consent screen lets you
// pick the account and untick scopes. Every revoke is printed, by token
// prefix only.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/fadymondy/moharrik/internal/google/googletest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18439", "listen address")
	flag.Parse()
	f, err := googletest.NewAt(*addr)
	if err != nil {
		log.Fatal(err)
	}
	f.Set(func(s *googletest.Server) { s.Interactive = true })
	fmt.Printf("fake Google on %s (client %s)\n", f.URL, f.ClientID)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	seen := 0
	tick := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-stop:
			f.Close()
			return
		case <-tick.C:
			rev := f.Revoked()
			for ; seen < len(rev); seen++ {
				t := rev[seen]
				if len(t) > 14 {
					t = t[:14] + "…"
				}
				fmt.Printf("%s REVOKED %s (revoke calls: %d)\n", time.Now().Format(time.TimeOnly), t, f.Calls("revoke"))
			}
		}
	}
}
