// Command moharrik-admin is platform administration from a shell — the bootstrap
// for the admin MCP, which needs a platform admin and an admin token before an
// agent can use it.
//
//	go run ./cmd/moharrik-admin promote <email>        grant the platform admin role
//	go run ./cmd/moharrik-admin demote  <email>        remove it (kills their admin tokens)
//	go run ./cmd/moharrik-admin token   <email> -name <label> -scopes a,b [-days 90]
//	                                                  mint an admin MCP token (printed once)
//	go run ./cmd/moharrik-admin revoke  <token-prefix> revoke an admin token by its visible prefix
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/togo-framework/togo"

	"github.com/fadymondy/moharrik/internal/envfile"
	"github.com/fadymondy/moharrik/internal/mcp"
	_ "github.com/fadymondy/moharrik/internal/plugins"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: moharrik-admin promote|demote <email> | token <email> -name N -scopes a,b [-days 90] | revoke <prefix>")
	os.Exit(2)
}

func main() {
	if len(os.Args) < 3 {
		usage()
	}
	envfile.Load(".env")
	k := togo.New()
	defer k.Close()
	ctx := context.Background()
	db, err := k.SQL(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "database unavailable:", err)
		os.Exit(1)
	}
	cmd, arg := os.Args[1], os.Args[2]

	switch cmd {
	case "promote", "demote":
		var roles string
		if err := db.QueryRowContext(ctx, `SELECT roles FROM users WHERE lower(email) = lower($1)`, arg).Scan(&roles); err != nil {
			fail("no user with that email (register in the app first)")
		}
		set := map[string]bool{}
		for _, r := range strings.Split(roles, ",") {
			if r = strings.TrimSpace(r); r != "" {
				set[r] = true
			}
		}
		set["admin"] = cmd == "promote"
		out := []string{}
		for r, on := range set {
			if on {
				out = append(out, r)
			}
		}
		if _, err := db.ExecContext(ctx, `UPDATE users SET roles = $2 WHERE lower(email) = lower($1)`, arg, strings.Join(out, ",")); err != nil {
			fail(err.Error())
		}
		fmt.Printf("%s: platform roles = %q\n", arg, strings.Join(out, ","))

	case "token":
		fs := flag.NewFlagSet("token", flag.ExitOnError)
		name := fs.String("name", "admin agent", "token label")
		scopes := fs.String("scopes", "", "comma-separated admin scopes (required): "+adminScopeList())
		days := fs.Int("days", 90, "expiry in days, 0 = never")
		_ = fs.Parse(os.Args[3:])

		var userID, roles string
		if err := db.QueryRowContext(ctx, `SELECT id, roles FROM users WHERE lower(email) = lower($1)`, arg).Scan(&userID, &roles); err != nil {
			fail("no user with that email")
		}
		if !mcp.IsPlatformAdmin(roles) {
			fail("that user is not a platform admin — run `promote` first")
		}
		list := []string{}
		for _, s := range strings.Split(*scopes, ",") {
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			if !knownAdminScope(s) {
				fail("unknown admin scope " + s + " (known: " + adminScopeList() + ")")
			}
			list = append(list, s)
		}
		if len(list) == 0 {
			fail("choose at least one scope with -scopes (there is no wildcard): " + adminScopeList())
		}
		var expires *time.Time
		if *days > 0 {
			t := time.Now().Add(time.Duration(*days) * 24 * time.Hour).UTC()
			expires = &t
		}
		plain, visible := mcp.NewToken(mcp.AdminPrefix)
		raw, _ := json.Marshal(list)
		if _, err := db.ExecContext(ctx, `
			INSERT INTO admin_mcp_tokens (user_id, name, token_hash, prefix, scopes, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6)`, userID, *name, mcp.HashToken(plain), visible, string(raw), expires); err != nil {
			fail(err.Error())
		}
		fmt.Printf("Admin MCP token for %s (%s), scopes %v — shown once:\n\n  %s\n\n", arg, visible, list, plain)
		fmt.Printf("claude mcp add --transport http moharrik-admin http://localhost%s/api/admin/mcp --header \"Authorization: Bearer %s\"\n",
			os.Getenv("ADDR"), plain)

	case "revoke":
		res, err := db.ExecContext(ctx, `UPDATE admin_mcp_tokens SET revoked_at = now() WHERE prefix = $1 AND revoked_at IS NULL`, arg)
		if err != nil {
			fail(err.Error())
		}
		n, _ := res.RowsAffected()
		fmt.Printf("revoked %d token(s)\n", n)

	default:
		usage()
	}
}

func knownAdminScope(s string) bool {
	for _, d := range mcp.AdminScopes {
		if d.Scope == s {
			return true
		}
	}
	return false
}

func adminScopeList() string {
	out := []string{}
	for _, d := range mcp.AdminScopes {
		out = append(out, d.Scope)
	}
	return strings.Join(out, ",")
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}
