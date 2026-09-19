# Moharrik

**Moharrik (محرّك)** — the AI engine behind a business. A business connects its channels and
accounts; Moharrik watches (notifications, trends, research), acts (automation), talks to
customers (chat, email, WhatsApp) and remembers them (CRM) — every capability an MCP tool.
Bilingual (Arabic + English). Domain: moharrik.com.

Built on [ToGO](https://github.com/togo-framework) (Go: chi + Huma + gqlgen + sqlc + Postgres)
and Next.js 16 on the fadymondy.com grid. Product brief: `fadymondy.com-v2/.setup/handoffs/moharrik.md`.
Scaffold: extracted from Mahaam's P0 (workspaces, members, invitations, MCP tokens, RLS).

**Status: P0 — local development only.** No deploy, no DNS.

## Ports (local dev)

| What | Port |
|---|---|
| API (Go) | **8110** — REST `/api`, GraphQL `/graphql`, docs `/docs`, MCP `/api/mcp`, admin MCP `/api/admin/mcp` |
| Web (Next dev) | **3010** — proxies `/api/*` and `/graphql` to 8110 |
| Postgres (docker compose, container `moharrik-postgres`) | **55460** — databases `moharrik`, `moharrik_test` |

## Run it

```bash
cp .env.example .env           # set the moharrik_app password and AUTH_SECRET
make db                        # Postgres on :55460
make migrate                   # schema, app role, FKs, RLS (idempotent)
make api                       # :8110
make web                       # :3010  (first time: cd web && npm install)
```

Open http://localhost:3010 → register → create a workspace.

Admin MCP bootstrap (after registering in the UI):

```bash
go run ./cmd/moharrik-admin promote you@example.com
go run ./cmd/moharrik-admin token you@example.com -name "claude" -scopes admin:workspaces:read,admin:users:read
```

User MCP tokens are minted in the app: Workspace → MCP tokens.

## Tests

```bash
make test       # unit + CI guards: every MCP tool has a scope; every sqlc query on a
                # tenant table carries workspace_id = $n; authz matrix; slugs
make test-e2e   # boots the real stack on moharrik_test and sweeps EVERY route as
                # anonymous / non-member / other-workspace member (+ MCP checks)
make grid-check # generated grid copies match fadymondy.com-v2
```

## Tenancy

- `workspaces`, `workspace_members` (owner/admin/manager/member/client), `invitations`;
  `workspace_id` on every tenant row. Web routes `/[locale]/w/{slug}`; the API reads
  `X-Workspace-ID` (id or slug). Not a member → **404**, no session → **401**.
- One default-deny guard (`internal/server/guard.go`) wraps every route incl. plugin routes.
  Generated CRUD sits in the workspace tier **and** behind `authz.Generated`
  (`internal/authz/can.go`), which is itself default-deny per table. Tenancy tables are
  read-only through generated CRUD; membership changes go through `/api/w/*` and `/api/me/*`.
- **RLS**: two pools. `DATABASE_URL` = owner (migrations, auth, tenant resolution, admin MCP,
  explicit predicates everywhere). `APP_DATABASE_URL` = `moharrik_app` (no superuser, no
  BYPASSRLS, owns nothing, no grant on `users`/`admin_mcp_tokens`). Every ORM/generated query
  runs on the app pool, whose connection wrapper (`internal/tenant/rlsdb.go`) runs
  `set_config('app.workspace_id', …)` before each statement. Boot refuses to start if the app
  role could bypass RLS.

## MCP

- **User MCP** `POST /api/mcp` — `mhr_` tokens owned by a user in a workspace, SHA-256 hashed,
  explicit scopes (no wildcard), narrowed to the user's *current* role on every call; same
  `tenant.Scope`, authz and RLS as the browser. Tools (P0): `whoami`, `list_members`.
  Scopes for the coming tools (contacts, inbox, campaigns, automation, notifications, research,
  trends) can be minted now and grant nothing until a tool declares them.
- **Admin MCP** `POST /api/admin/mcp` — `mhra_` tokens, only for platform admins
  (`users.roles` contains `admin`, re-checked per call). Tools: `list_workspaces`, `get_workspace`, `update_workspace`, `add_member`, `set_member_role`,
  `remove_member`, `list_users`, `get_user`.
- Transport ported from fadymondy.com-v2 `internal/mcp`; its opt-in scope weakness (FM-22) is
  inverted: a tool without a scope fails the build (`scopes_test.go`) and is denied at dispatch.
  Protocol `2024-11-05`, also negotiates `2025-03-26` / `2025-06-18`.

## Problem reports → Mahaam

Moharrik receives no issues. It files its own to its Mahaam project:

- **Web:** the Mahaam Feedback widget (`web/components/feedback/mahaam-widget.tsx`, loaded from
  `NEXT_PUBLIC_MAHAAM_EMBED_ORIGIN`) is mounted in the signed-in workspace shell; "Report a
  problem" in the header and user menu opens it. It posts to `web/app/feedback/route.ts`, which
  checks the session against the Go API, adds workspace/user/host to `meta`, and forwards
  server-to-server to `$MAHAAM_URL/api/feedback/embed` with `MAHAAM_FEEDBACK_KEY` and
  `Origin: $MAHAAM_APP_URL`. Put those in `web/.env.local`.
- **API:** `internal/feedback` is Mahaam's Go SDK (vendored from `managy/sdk/go`);
  `internal/server/problems.go` reports panics (with stack) and 5xx responses (deduplicated per
  route for 10 min). No key = no-op.

## togo framework notes (found building P0, CLI 0.2.31 / togo v0.21.0)

1. `make:resource` in a `--db postgres` project still writes **SQLite** DDL
   (`randomblob`, `datetime`), `?` placeholders in sqlc queries, `engine: sqlite` in
   `sqlc.yaml`, and no Atlas HCL — while `togo.resources.yaml` records the right `pg:` types.
   The scaffold's `togo migrate` then runs that DDL against Postgres. Schema files here are
   hand-written Postgres; `cmd/migrate` is our own.
2. On Postgres + `database/sql`, sqlc emits `sql.Null*`, but the generated resources/ORM/
   GraphQL expect pointers → overrides in `sqlc.yaml`; generated code also expects `PageURL`
   → `initialisms: [id, url]`.
3. Nullable timestamps are `String` in the generated GraphQL but `*time.Time` in Go
   (gqlgen: "String is incompatible with *time.Time").
4. Generated Huma create bodies make every nullable field a **required** key (no `omitempty`),
   and put `validate:"required"` on int/bool fields so `0`/`false` are rejected. Fixed in the
   handler fragments.
5. `togo generate` swallows gqlgen's failure; the cause was `golang.org/x/tools` v0.30 vs
   go1.27 ("package context without types") → bumped to v0.50.
6. `togo new` installs `auth-dev` (admin session for an empty POST in any non-production env)
   and `dashboard`; there is no uninstall command (plugins.gen.go hand-edited). The scaffold's
   `/events` SSE broadcasts every generated row to every subscriber and `/api/_meta/resources`
   dumps the schema — both left unmounted. Kernel `LoadConfig` never reads `.env`
   (`internal/envfile`).
7. The ORM holds a concrete `*sql.DB` (no executor interface), so per-request
   `SET LOCAL` in a transaction is impossible for generated code — hence the per-statement
   connection wrapper. `db-postgres` exposes no pgx hooks.
