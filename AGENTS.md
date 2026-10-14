# AGENTS.md

Guidance for AI coding agents (Claude Code, Codex, Cursor and others) working in this repository.

## Commands

```bash
make db          # Postgres in Docker on :55470
make migrate     # schema, app role, constraints, RLS (idempotent)
make generate    # sqlc -> gqlgen -> OpenAPI export (compiles everything; the integration gate)
make api         # API on :8120
make web         # web on :3120 (first time: cd web && npm install)
make test        # unit tests + guards (MCP scopes, sqlc tenant predicate, authz)
make test-e2e    # cross-workspace isolation sweep over every route
```

## Invariants

- **Tenancy.** Every tenant row has `workspace_id`. Routes go through one default-deny guard
  (`internal/server/guard.go`); not a member is a 404, no session a 401. Tenant queries run on the
  RLS app pool (`APP_DATABASE_URL`, `internal/tenant/rlsdb.go`) and also carry an explicit
  `workspace_id = $n` predicate, which a test enforces for every sqlc query on a tenant table.
- **New routes and MCP tools** must be taught to the isolation sweep (`internal/server`), and every
  MCP tool must declare a scope (`scopes_test.go`).
- **Generated code** (`internal/db/gen`, `internal/graph/gen`, `*.gen.go`) is never edited by hand;
  see [.claude/rules/conventions.md](.claude/rules/conventions.md).
- Every UI change must work in English and Arabic (RTL), light and dark.

## More

- The design record is [docs/architecture.md](docs/architecture.md).
- Contribution rules, checks and the DCO sign-off are in [CONTRIBUTING.md](CONTRIBUTING.md).
- Never commit secrets, `.env` files, personal data, real hostnames or IP addresses. Use
  RFC 5737 addresses (`203.0.113.x`) and `example.com` in tests and docs.
