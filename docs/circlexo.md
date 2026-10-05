# CircleXO hub integration

Moharrik can run as an app on the CircleXO hub: hub sign-in next to Moharrik's own login, hub orgs
mapped to workspaces, hub entitlements shown for those workspaces (and enforced where Moharrik has
a natural place to say no), usage reported to the hub, and hub-issued tokens on the MCP endpoint.
**All of it is off unless `CIRCLEXO_ISSUER` is set.** With it unset nothing changes: the routes
below (except the config probe) do not exist and no table is read.

Code: `internal/circlexo` (service, SSO, sync, webhooks, entitlements, usage), `internal/mcp/hub.go`
(federated MCP), wiring in `internal/server/circlexo.go` and `internal/server/guard.go`. Small
hooks: `contacts.OnCreated`, `marketing.BeforeCampaignSend`/`AfterCampaignSend`,
`workspaces.BeforeInvite`. Tests: `internal/server/circlexo_test.go` (against the SDK's
`circlexotest` hub), run by `make test-e2e` and CI.

## Configuration

Names only; values live in the vault. See `.env.example`.

| Variable | Purpose |
| --- | --- |
| `CIRCLEXO_ISSUER` | The hub's base URL. Turns the integration on. |
| `CIRCLEXO_API_URL` | The hub's app API, when not the issuer. |
| `CIRCLEXO_APP_ID` | Moharrik's app id at the hub (also the token audience). |
| `CIRCLEXO_CLIENT_ID` | OIDC client id. |
| `CIRCLEXO_CLIENT_SECRET` or `CIRCLEXO_PRIVATE_KEY` + `CIRCLEXO_KEY_ID` | Client authentication (one of the two). |
| `CIRCLEXO_APP_KEY` | App API key: entitlements, tenants, org members, usage. |
| `CIRCLEXO_WEBHOOK_SECRET` | Signing secret (`whsec_...`) for webhooks and provision/deprovision calls. Required. |
| `CIRCLEXO_SESSION_KEY` | Seals the sign-in state cookie. Empty: derived from `AUTH_SECRET`. |
| `CIRCLEXO_REDIRECT_URL` | Optional. Default `WEB_URL` (else `APP_URL`) + `/auth/circlexo/callback`. |
| `CIRCLEXO_BILLING_URL` | Optional. Hub billing page; `{slug}` is the org slug. Default `<issuer>/orgs/{slug}/billing`. |
| `WEB_URL` | Public web origin, used for redirects and the post-logout page. |

A half-set or invalid configuration is logged at error level at boot and leaves the integration
disabled; Moharrik still starts.

## Endpoints

| Route | Auth | Purpose |
| --- | --- | --- |
| `GET /auth/circlexo/login` | public | Start sign-in (OIDC authorization code + PKCE). `?return_to=/local/path`. |
| `GET /auth/circlexo/callback` | public | Finish sign-in. Failures go to `/login?circlexo_error=<code>`. |
| `GET /auth/circlexo/logout` | public | Clear Moharrik's session cookie, then the hub's end-session; lands on `/login`. |
| `GET /sso/circlexo?org=<slug>` | public | The app launch URL the hub dashboard links to. |
| `POST /api/circlexo/events` | webhook signature | Hub webhooks (below). |
| `POST /api/circlexo/provision` | webhook signature | Create or return the workspace for an org (idempotent). |
| `POST /api/circlexo/deprovision` | webhook signature | Mark the org's link inactive. |
| `GET /api/circlexo/config` | public | `{"enabled": bool, "login_url": "/auth/circlexo/login"}`; the login page shows the button only when enabled. Always mounted. |
| `GET /api/w/circlexo` | session + membership | The workspace's link, plan and hub entitlements (features, limits, meters, which are enforced). |
| `/.well-known/oauth-protected-resource` | public | Existing; lists the hub issuer too when enabled. |
| `POST /api/mcp` (and the public MCP host) | token | Existing; also accepts hub-issued access tokens. |

`circlexo_error` codes: `failed`, `email_unverified`, `account_conflict`, `no_email`, `not_member`,
`unavailable`.

Webhooks handled: `org.app_installed`, `org.app_removed`, `member.added`, `member.role_changed`,
`member.removed`, `session.revoked`, `entitlement.changed`, and any `subscription.*` (invalidate the
entitlement cache). Signatures are verified first (`webhook-id`, `webhook-timestamp`,
`webhook-signature`), replays are dropped by id, and a bad or missing signature is a 401 before
anything runs. The provision/deprovision body shape (`{org_id, ...}` in, `{product_tenant_id, slug}`
out, signed like a webhook) is Moharrik's own: the hub drives the lifecycle through webhooks, so
those two are for manual or future use.

## Tables (owner pool only, no RLS, no app-role grant)

`internal/db/schema/circlexo.sql`, plus `internal/db/post/168_circlexo.sql` (the workspace FK).

- `circlexo_user_links`: hub subject to Moharrik user. Unique both ways.
- `circlexo_org_links`: hub org to workspace, `status` `active` or `inactive`. Unique both ways.
- `circlexo_sessions`: hub session id, user and JWT `iat`, for `session.revoked`.
- `circlexo_webhook_events`: processed event ids.

## Mapping rules

**Sign-in.** In order: a linked subject signs in as its user; otherwise the hub must vouch for the
email (`email_verified`) or it is refused; a Moharrik account with that email is linked only if it
is passwordless (SSO-created), or if the same person is already signed in to it in this browser,
otherwise refused (`account_conflict`), so nobody can register a stranger's address and inherit their
hub sign-in; otherwise a passwordless account is created and linked. The session is Moharrik's
normal one, so every existing login method keeps working (dual login).

**Landing.** After sign-in the user lands in the workspace mapped to the token's org (or the org
named by the launch URL), added with their hub role. If the hub confirms the org has the app but
the install webhook has not arrived, the workspace is provisioned on the spot, but only while the
hub still has the install `provisioning` (MH-1149). A lost link to a workspace the hub already
names is restored. If the workspace was deleted in Moharrik, the sign-in creates nothing: it calls
`ReleaseTenant`, the hub shows the app as not installed, and the user lands on `/`. Installing it
again from the hub provisions a fresh workspace. A new workspace adopts the owner's own unlinked
workspace with the org's slug instead of making `<slug>-2`.

**Profile.** Every CircleXO sign-in and every `user.updated` webhook copies the hub's verified
address onto the account (unless another account holds it) and keeps the hub's display name,
picture URL and locale on `circlexo_user_links`.

**Org to workspace.** One active workspace per org. Provision is idempotent: the same org returns
the same workspace, reactivating an inactive link. It reads the org and members from the hub,
creates the workspace, syncs members, then confirms the tenant so the hub marks the install active.

**Roles.** Hub `owner` maps to `owner`, `admin` to `admin`, anything else to `member`. A hub
`member` never demotes a Moharrik-only role (`manager`, `client`) and only lowers an owner or admin
to member. The last owner is never demoted or removed by the hub. `member.removed` suspends the
membership; it does not delete it. Removing the app marks the link inactive and deletes nothing.

## Entitlements: what is enforced (honestly)

Moharrik has no plan engine (`workspaces.plan` is a label nothing reads), so there is nothing to
overlay. For a workspace with an **active** link the hub's entitlements are exposed at
`GET /api/w/circlexo` and enforced only where a natural choke point exists:

- `campaigns` (flag) and `emails_sent` (meter): refuse starting a campaign send (REST and the
  `send_campaign` tool share it) when the feature is off, the subscription is inactive or
  read-only, or the allowance is used up without pay-as-you-go overage.
- `members` (limit): refuses new invitations once the workspace has that many active members.

`inbox`, `crm`, `automation`, `research`, `trends` and the `contacts` meter are exposed but **not**
blocked. Fail closed: if the hub cannot confirm the entitlements (or has none for the org) the gated
actions above are refused; in the SDK's grace window (hub down, stale copy) the last answer applies
read-only. Reading keeps working. Unlinked or inactive workspaces are never touched.
`entitlement.changed` and `subscription.*` drop the cache.

## Usage reporting

Through the SDK `usage.Reporter`, linked workspaces only, keyed by an idempotency key so a retry
never double counts: `contacts` (1 per contact created) and `emails_sent` (count sent per campaign
send attempt). Failures are logged and never block the action. Billing itself (checkout, invoices)
is the hub's; Moharrik has no billing UI to redirect.

## Federated MCP

`/api/mcp` and the public MCP host verify a hub token against the hub's JWKS with audience = the
app id, map the subject through `circlexo_user_links` and the org through `circlexo_org_links`
(active only), and re-read the workspace role on every call. An unlinked user, an unknown or
deinstalled org, a non-member, a token with no org, or a service token is refused. `mhr_` and OAuth
tokens are unchanged.

Scopes: hub gateway scopes (`openid`, `org`, ...) do not describe tools, so a token naming Moharrik
tool scopes (`contacts:write`) gets exactly those; a token carrying an `act` chain (exchanged by the
hub gateway for an agent) gets a working set that never sends to customers or administers
members, mail, automation, integrations or audit; any other hub token gets reads. Every tool
needs a scope, and everything is narrowed by the user's workspace role. Out-of-scope calls fail
in-band naming the missing scope.

## Known limits

- Moharrik's 2FA is not asked for on hub sign-in: the hub owns second factors.
- Moharrik's session is a stateless JWT, so logout ends it in this browser only. `session.revoked`
  works by remembering (user, `iat`) of hub-born sessions and refusing them at the guard on
  session-protected routes (`/api/auth/*` is the auth plugin's own and does not check it). Two hub
  sessions of one user minted in the same second share an `iat`; revoking one ends both.
- The SDK's test hub fixes the org name and slug.

## SDK dependency

`github.com/circlexo/circlexo-go` is a public module pinned at `v0.1.0` (no `replace`). No
credentials or `GOPRIVATE` setting are needed locally, in Docker or in CI.

## Owner cut-over checklist

1. Register Moharrik at the hub; note the app id, client credentials, app key and webhook secret. The manifest asks for `service_account: true`.
2. Register the redirect URI (`<WEB_URL>/auth/circlexo/callback`), the post-logout URI (`<WEB_URL>/login`; the draft manifest says `/`), the launch URL (`<WEB_URL>/sso/circlexo`), the webhook URL (`<API>/api/circlexo/events`) and the provision/deprovision URLs.
3. Set the `CIRCLEXO_*` variables from `.env.example` in the environment (not the repo).
4. Run the migration (`cmd/migrate`): applies `168_circlexo` (four tables and the FK).
5. Make sure `web` proxies `/auth/circlexo/*` and `/sso/circlexo` to the API (`web/next.config.mjs`).
6. Set `CIRCLEXO_ISSUER` last and restart: the routes mount and `GET /api/circlexo/config` reports `enabled`. Check the boot log for the enabled line.
7. Install the app on a test org and confirm: a workspace appears, members sync, the hub shows the tenant active, gated actions follow the hub plan.
