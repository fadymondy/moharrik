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
	"strings"
	"time"

	"github.com/fadymondy/moharrik/internal/google/googletest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18439", "listen address")
	// MH-585: seed one Google Contact for an account before the browser ever
	// reaches consent, so an e2e check (contacts-google-import.mjs) can
	// connect that account for Contacts and immediately "Sync now" against
	// something real instead of an always-empty address book.
	seedAccount := flag.String("seed-contacts-account", "", "if set, seed one fake contact for this account email (matches the consent screen's email)")
	seedName := flag.String("seed-contacts-name", "Fake Sync", "given name of the seeded contact")
	seedEmail := flag.String("seed-contacts-email", "fake-sync@example.test", "email of the seeded contact")
	flag.Parse()
	f, err := googletest.NewAt(*addr)
	if err != nil {
		log.Fatal(err)
	}
	if *seedAccount != "" {
		f.SetContact(googletest.SubOf(*seedAccount), "people/e2e1", googletest.ContactFields{
			"names":          []map[string]any{{"givenName": *seedName}},
			"emailAddresses": []map[string]any{{"value": *seedEmail}},
		})
		fmt.Printf("seeded one contact for %s (%s <%s>)\n", *seedAccount, *seedName, *seedEmail)
	}
	f.Set(func(s *googletest.Server) {
		s.Interactive = true
		// What an account sees follows its address, so the browser check can
		// pick a case at the consent screen: many@… sees several properties,
		// sites (one of each has no data yet) and a channel; empty@… sees one
		// property and one site with no data and a channel with no uploads;
		// anyone else sees one of each, with data.
		s.DataFor = func(a googletest.Account) *googletest.AccountData {
			switch {
			case strings.HasPrefix(a.Email, "many"):
				return googletest.Many(a)
			case strings.HasPrefix(a.Email, "empty"):
				return googletest.Empty(a)
			}
			return nil
		}
	})
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
