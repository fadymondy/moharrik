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
| API (Go) | **8120** — REST `/api`, GraphQL `/graphql`, docs `/docs`, MCP `/api/mcp`, admin MCP `/api/admin/mcp` |
| Web (Next dev) | **3120** — proxies `/api/*` and `/graphql` to 8120 |
| Postgres (docker compose, container `moharrik-postgres`) | **55470** — databases `moharrik`, `moharrik_test` |

## Run it

```bash
cp .env.example .env           # set the moharrik_app password and AUTH_SECRET
make db                        # Postgres on :55470
make migrate                   # schema, app role, FKs, RLS (idempotent)
make api                       # :8120
make web                       # :3120  (first time: cd web && npm install)
```

Open http://localhost:3120 → register → create a workspace.

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

## Email marketing (first product module)

Ported from fadymondy.com-v2 (`internal/mail`, subscribe/confirm/unsubscribe, campaigns) into
`internal/marketing`, per workspace:

- Tables `mail_lists`, `contacts`, `list_subscriptions` (per-list consent: pending → subscribed /
  unsubscribed / bounced), `campaigns`, `smtp_settings` — all `workspace_id`, FORCE RLS, in `TenantTables`.
- Endpoints under `/api/w/` (app pool + explicit `workspace_id`), authz actions `contacts:*`,
  `campaigns:*`, `mail:settings` (members keep contacts, managers send, admins own SMTP).
- Double opt-in: adding a contact to a double-opt-in list emails a confirm link; the public
  `/{locale}/mail/confirm` and `/{locale}/mail/unsubscribe` pages act only on a click
  (`POST /api/mail/confirm|unsubscribe`, token = authorization; RFC 8058 one-click header).
  Links use `PUBLIC_URL`, else `APP_URL` — set it to the **web** origin.
- SMTP per workspace (Settings → Email sending); password AES-GCM sealed with a key derived from
  `AUTH_SECRET`, never returned. Campaigns send in the background with progress on the row.
- `node web/e2e/marketing-check.mjs [base]` drives it end to end with a local SMTP sink on :2525.

## Leads (MH-390)

- **Inquiry forms** are `subscribe_forms` rows with `kind = 'inquiry'` (no list), created on the
  Website forms page. Their embed snippet captures UTM parameters, gclid, referrer and landing page
  in the visitor's browser and posts to the public `POST /api/inquiry/{key}` — the same gate as the
  subscribe form (`marketing.ResolvePublicForm`: key of the right kind, form on, Origin on the
  allow-list, empty allows nothing), a honeypot (`nickname`), rate limits per client address and per
  form, and one identical answer for new and known addresses.
- Each lead lands in `leads` (RLS, `internal/db/post/32_leads.sql`) and raises a notification-center
  item. The Leads page filters by status and searches; **Convert** makes the contact (deduped on
  email), the company from the email domain (never for free mail — `internal/leads/freemail.go`),
  a deal on the first open stage and the message as its first activity.
- MCP: `list_leads`, `get_lead` (`leads:read`), `update_lead`, `convert_lead` (`leads:write`;
  `convert_lead` also needs `contacts:write`).

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
- **Dedicated host (MH-516)** — `MCP_PUBLIC_URL`, e.g. `https://mcp.moharrik.com`, gives the
  same user MCP a second, easier-to-connect entry point: its root path (`POST /`, no `/api/mcp`
  suffix) is the transport, and `/.well-known/oauth-protected-resource` (+ trailing-slash) is
  served there too — everything else on that host is a 404, even the legacy `/api/mcp` path.
  Unset (the default), none of this mounts, and `/api/mcp` on `APP_URL` stays the only way in.
  The authorization server is still `APP_URL`/`OAUTH_ISSUER` either way — only the resource
  server moves. The two entry points are separate OAuth audiences (`internal/mcp/oauth.go`,
  `authOAuthForResource`): a token minted for `MCP_PUBLIC_URL` is refused at `/api/mcp` and vice
  versa, even though both reach the same tools — existing `mhr_` tokens and OAuth grants against
  `/api/mcp` are unaffected. `internal/mcp/oauth.go`'s `MCPServerURL()` is the one place that
  decides which URL to SHOW a person (Settings' `server_url`, the pasted-token config, the
  claude.ai/ChatGPT instructions): the dedicated host once configured, else the legacy resource.
  See `internal/mcp/public_host.go`.

## Problem reports → Mahaam

Moharrik receives no issues. It files its own to its Mahaam project:

- **Web:** the Mahaam Feedback widget (`web/components/feedback/mahaam-widget.tsx`, loaded from
  `NEXT_PUBLIC_MAHAAM_EMBED_ORIGIN`) is mounted in the signed-in workspace shell; "Report a
  problem" in the header and user menu opens it (both are hidden until it can send). The widget
  sends with `credentials: "omit"`, so its `data-key` is a 24-hour signed reporter token from
  `web/app/feedback/session/route.ts`; it posts to `web/app/feedback/route.ts`, which verifies the
  token (or the session cookie), adds workspace/user/host to `meta`, and forwards server-to-server
  to `$MAHAAM_URL/api/feedback/embed` with `MAHAAM_FEEDBACK_KEY` and `Origin: $MAHAAM_APP_URL`
  (which must be on the key's allowed origins). Its look comes from `web/app/feedback/config`.
  Put those in `web/.env.local`.
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
