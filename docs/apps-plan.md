# Moharrik apps — plan (phase 1)

Companion apps for working the inbox and getting alerted on the go: **iOS + Android**
(Expo / React Native) and **desktop** (Electron, Windows + macOS, with a tray).

## 1. Template choice: Mahaam (structure) + Zekra (store CI, Firebase)

The three existing apps were compared.

| | Mahaam (`managy/apps`) | Zekra (`togo/cabrain`) | Health Debug |
|---|---|---|---|
| Layout | npm-workspaces `apps/` monorepo: `shared`, `desktop`, `tray`, `mobile` | `mobile/` and `desktop/` side by side, no shared package | `app/` (Expo) and `desktop/` (Electron hosting the Expo web build) |
| Shared client | `@mahaam/client`: types generated from the server's OpenAPI (`openapi-typescript`), vitest tests, brand tokens | hand-written per app | `shared/` is UI only |
| Mobile | Expo 57, RN 0.86, expo-router, react-query + persister, secure-store, expo-notifications, jest-expo | Expo + `@react-native-firebase/*` (FCM, Crashlytics), google-signin | Expo |
| Desktop | Electron 44 + Vite, esbuild main, electron-builder, electron-updater, notarize, deep-link protocol, loopback PKCE sign-in | Electron | Electron wrapping a web build |
| Native sign-in | **system browser + PKCE → device PAT** (RFC 8252) | Google sign-in SDK | — |
| Store CI | Codemagic OTA patches only | **Codemagic `android-release` (Play internal) + `ios-release` (TestFlight)** | — |
| Server push | `internal/push` (APNs/FCM via golang-jwt) | **`push_fcm.go`: stdlib-only FCM HTTP v1** + `device_tokens` | — |

**Mahaam is the template.** It is the most mature of the three. Its monorepo has a typed
client generated from the server's spec, which is exactly what conventions rule 5
(API-first) asks for. Its sign-in uses the browser plus PKCE, so it works with
Moharrik's Google-only sign-in without an embedded web view. And its Electron app
already ships updater, notarize and deep-link plumbing.

**Zekra fills Mahaam's two gaps:**
- store-build CI: TestFlight, plus a Play internal *draft*;
- Firebase messaging: FCM tokens on both platforms, and the stdlib-only Go FCM v1 sender.

Health Debug's desktop app just wraps a web build, so it is not a model for a native
tray app.

## 2. Architecture

```
apps/
  package.json          npm workspaces: shared, desktop   (mobile stays standalone, like Mahaam:
                        Expo pins its own React/RN and must not be hoisted against Electron's)
  shared/               @moharrik/client — API client, auth (PKCE helpers), types, i18n (en/ar), brand
    src/api.gen.ts      generated: openapi-typescript ../../openapi.json   (npm run gen:types)
  mobile/               Expo 57 · expo-router · react-query · expo-secure-store · @react-native-firebase/messaging
  desktop/              Electron · Vite + React renderer · safeStorage · tray badge · native notifications
codemagic.yaml          moharrik-android-internal + moharrik-ios-testflight (+ CI typecheck/test)
```

- **IDs.** Bundle ids follow the other apps:
  - mobile `com.fadymondy.moharrik`;
  - desktop `com.fadymondy.moharrik.desktop`;
  - URL scheme `moharrik://`;
  - Apple team `JW9HJH86GC`.
- **Branding** comes from `web/`:
  - the lattice mark (`web/app/icon.svg`);
  - teal `#00A0A8` with gold accent `#C9A227`;
  - Inter for Latin, Lusail for Arabic.
  - The apps are Arabic-first: the locale defaults to the device's and RTL is forced
    when it is `ar`. Dark and light follow the system, with an override.
- **Auth: the web's own sign-in, handed to the app.**
  - The app opens `https://app.moharrik.com/{locale}/app-signin?redirect_uri=…&code_challenge=…&state=…&device=…`
    in the system browser:
    - mobile uses `expo-web-browser` `openAuthSessionAsync` (ASWebAuthenticationSession / Custom Tabs);
    - desktop uses `shell.openExternal` plus a one-shot loopback listener on 127.0.0.1.
  - The person signs in on the web as usual (Google), then confirms.
  - The page gets a one-time code bound to the PKCE challenge. The app trades code + verifier
    for a **device token**: a `togo_pat_` personal access token named for the device.
  - The token is stored in the Keychain/Keystore (`expo-secure-store`) or with Electron
    `safeStorage`.
  - Every API call sends `Authorization: Bearer <token>` plus `X-Workspace-ID`. CSRF does not
    apply to Bearer requests.
- **Workspace picker.** `GET /api/me/workspaces` lists the workspaces; the choice is
  persisted, then sent as `X-Workspace-ID`.
- **Data** goes through the same REST endpoints the web uses, so roles, RLS and authz
  apply unchanged. Nothing app-specific happens server-side apart from sign-in, devices
  and preferences.

## 3. API endpoints

### Existing endpoints the apps use

| Purpose | Endpoint |
|---|---|
| Me / workspaces | `GET /api/auth/me` (plugin), `GET /api/me/workspaces` |
| Inbox list (status, channel, unread, assignee, label, q) | `GET /api/w/conversations` |
| Conversation + messages | `GET /api/w/conversations/{id}`, `GET /api/w/conversations/{id}/messages` |
| Reply | `POST /api/w/conversations/{id}/reply` |
| Close / snooze / assign / read / labels | `PATCH /api/w/conversations/{id}`, `PATCH /api/w/conversations/{id}/organise` (label_ids, mute), `GET /api/w/conversation_labels` |
| Notifications | `GET /api/w/notifications`, `PATCH /api/w/notifications/{id}`, `GET /api/w/alerts` (unread counts) |
| Contacts / companies | `GET /api/w/contacts?q=`, `GET /api/w/companies?q=`, `GET /api/w/contacts/{id}`, `GET /api/w/contacts/{id}/timeline` |
| Deals | `GET /api/w/deals`, `GET /api/w/pipeline/stages`, `PATCH /api/w/deals/{id}` (stage) |
| Members (assign picker) | `GET /api/w/members` |
| Live updates (desktop tray, foreground mobile) | `GET /api/w/events` (SSE) |

The generated types in `@moharrik/client` (from `go run ./cmd/api openapi`) are authoritative.

### New in PR 1

| Endpoint | Tier | What |
|---|---|---|
| `POST /api/me/app-signin` | signed in | The web page asks for a one-time code (2 min, hash stored, bound to challenge + redirect) and gets `redirect_to` |
| `POST /api/auth/app/token` | public, rate-limited per IP | code + `code_verifier` + `redirect_uri` → device token (`togo_pat_…`), email, name |
| `POST /api/me/app-signout` | signed in (Bearer) | Revokes the calling device token and removes this device's push token |
| `GET /api/me/devices` | signed in | The caller's own app devices (push tokens) |
| `POST /api/me/devices` | signed in | Registers or refreshes an FCM token: `{token, platform: ios\|android, locale, name, app_version}`. Upsert by token, so a token moves to the caller and is never duplicated |
| `DELETE /api/me/devices/{id}` | signed in | Removes one of the caller's own devices |
| `GET /api/w/push/prefs` / `PUT /api/w/push/prefs` | workspace | The caller's push preferences in this workspace: `messages`, `assigned_only`, `assignments`, `notifications` |
| `POST /api/w/push/test` | workspace | (extended) Now also reaches the caller's app devices |

A web page `web/app/[locale]/app-signin` holds the confirm step.

## 4. Push design

One audience decision, two transports. `alerts.Emit` was already the single entry point for
every alerting event: a customer's message on any channel, or a notification-center item.
It decides who is alerted once, through `realtime.Event.For`. PR 1 adds three things:

1. **An FCM transport.** `internal/alerts/fcm.go` is a stdlib-only FCM HTTP v1 sender,
   ported from Zekra's `push_fcm.go`:
   - service-account JWT (RS256) and a cached OAuth access token;
   - `isDeadToken` pruning;
   - Android priority HIGH, APNs priority 10;
   - the collapse key is the thread tag.

   `alerts.Push` delivers to the member's web subscriptions **and** their app devices
   (`device_tokens`), for the same `ev.For(viewer)` audience. So a tab, a lock screen and a
   phone can never disagree about who is told.

   Config is env only:
   - `FCM_SERVICE_ACCOUNT_JSON` (raw JSON or base64) or `GOOGLE_APPLICATION_CREDENTIALS`;
   - optional `FCM_PROJECT_ID` and `FCM_DRY_RUN`.

   With no credentials, FCM is off and web push works exactly as before.
2. **Assignment alerts.** Assigning a conversation to someone else emits an alerting
   `assigned` event with `AlertOnly` set to the new assignee. Self-assignment and
   unassignment stay quiet.
3. **Per-user preferences** (`push_prefs`: tenant table, RLS, one row per member).
   - They are checked per viewer before any push: message, assignment, notification item,
     and `assigned_only` (only conversations assigned to me).
   - No row means everything is on.
   - Preferences only gate **push**. The open web tab keeps its own in-page alerts, which
     the member controls there.

**Payload** (FCM `data`, all strings):

| Field | Value |
|---|---|
| `kind` | `message` \| `assigned` \| `notification` \| `test` |
| `workspace_id`, `slug` | the workspace |
| `conversation_id` / `item_id` | the thread or notification item |
| `url` | the web path |

The notification block carries a localized title and body, written in the device's
registered locale (en/ar).

**Deep link.** Tapping a push opens `moharrik://w/{slug}/inbox/{conversation_id}` (or
`/notifications/{item_id}`). The app switches workspace first if needed.

**Desktop** has no FCM. It holds the SSE stream (`/api/w/events`) for each signed-in
workspace while running:
- it raises native `Notification`s for events with `alert: true` (the server already
  decided the audience per viewer);
- it sets the tray/dock badge from `GET /api/w/alerts` unread counts, refreshed on each event.

### Reusable for Hosbah (sibling thread)

These pieces are written to be copied as-is into the Hosbah repository:
- `internal/alerts/fcm.go`: the sender has no Moharrik imports. `FCMFromEnv()`,
  `(*FCM).Send(ctx, token, Message)` and the dead-token error are its whole surface.
- The `device_tokens` schema plus `POST/GET/DELETE /api/me/devices`.
- The app sign-in pair (`/api/me/app-signin` + `/api/auth/app/token`) and its web page.
  This is the same shape as Mahaam's `internal/account/appsignin.go`.
- `@moharrik/client`'s `auth.ts` (PKCE + loopback / custom-scheme redirect) and the mobile
  `push.ts` (permission, token, refresh, tap → deep link).

Keep the env names (`FCM_SERVICE_ACCOUNT_JSON`, `FCM_PROJECT_ID`, `FCM_DRY_RUN`) and the
payload keys the same in both products.

## 5. Build / CI

- **Codemagic** (`codemagic.yaml`, mirrors Zekra):
  - `moharrik-android-internal`: `expo prebuild`, then `.aab`, then Google Play **internal**
    track as a **draft**.
  - `moharrik-ios-testflight`: App Store Connect integration, then **TestFlight only**.
  - Never review or production.
- **Desktop:** electron-builder with NSIS (Windows) and dmg/zip (macOS), using appId
  `com.fadymondy.moharrik.desktop` and the `moharrik` protocol.
- **iOS archiving runs on the Mac (or Codemagic), not on this Windows machine.** This
  repo only prepares the Expo config.

### Secrets (never committed; names as in the vault / Zekra)

| Secret | Where | Used by |
|---|---|---|
| `GOOGLE_SERVICES_JSON` (base64) | Codemagic group `moharrik_firebase` | Android Firebase config, decoded to `apps/mobile/secrets/google-services.json` |
| `GOOGLE_SERVICE_INFO_PLIST` (base64) | Codemagic group `moharrik_firebase` | iOS Firebase config, decoded to `apps/mobile/secrets/GoogleService-Info.plist` |
| `CM_KEYSTORE` / `CM_KEYSTORE_PASSWORD` / `CM_KEY_ALIAS` / `CM_KEY_PASSWORD` | Codemagic keystore reference `moharrik_upload` | Android upload key |
| `GCLOUD_SERVICE_ACCOUNT_CREDENTIALS` | Codemagic group `moharrik_play` | Play internal upload |
| App Store Connect API key | Codemagic integration `moharrik_asc` (same team key as `zekra_asc`) | TestFlight upload, signing |
| `FCM_SERVICE_ACCOUNT_JSON` | server `.env` (production vault) | server → FCM |
| `APPLE_ID` / `APPLE_APP_SPECIFIC_PASSWORD` / `APPLE_TEAM_ID`, `CSC_LINK` / `CSC_KEY_PASSWORD` | Mac keychain / CI | desktop notarize + sign |

The Firebase project needs Android app `com.fadymondy.moharrik` and iOS app
`com.fadymondy.moharrik`, with the APNs auth key uploaded to Firebase.

## 6. PRs

1. **Server push + API** (`feat/apps-push-api`): sign-in pair, devices, FCM, assignment
   alerts, preferences, web page, tests.
2. **Mobile** (`feat/apps-mobile`): `apps/shared` + `apps/mobile` + `codemagic.yaml`, plus a
   debug APK.
3. **Desktop** (`feat/apps-desktop`): `apps/desktop` (Electron, tray, notifications), plus a
   Windows build.

**Safety.** `reply` sends real messages. During development the apps are pointed at a
local stack (`EXPO_PUBLIC_MOHARRIK_URL` / `MOHARRIK_URL`), and nothing is sent to a real
customer.

## 7. Local testing (as built)

- **Demo stack.** `node apps/scripts/demo-stack.mjs --seed` seeds a workspace ("Nile Studio")
  on a local API (:8131) and serves a sign-in shim on :8130 that completes
  `/{locale}/app-signin` for the demo user and redirects back to the app. Its state file
  (`apps/scripts/.demo-state.json`, credentials and cookies) is gitignored.
- **Desktop screenshots.** `node apps/desktop/scripts/shots.mjs --out <dir>` builds against
  the shim, walks every screen (`src/main/shots.ts`) in AR and EN/dark, and posts a visitor
  chat message so the native alert and tray badge fire.
- **Android.** `MOHARRIK_LOCAL_HTTP=1 npx expo prebuild` (`plugins/with-local-http.js`) lets
  a release APK reach `http://10.0.2.2:8130`; store builds never set it.

### Gotchas (also for Hosbah)

- Chromium 140+ makes `Element.scrollIntoView()` return a Promise. An arrow effect
  `useEffect(() => el.scrollIntoView())` then returns that Promise as its "cleanup", and React
  crashes (`destroy is not a function`) when the component unmounts. Use a braced body.
- A parent process with `ELECTRON_RUN_AS_NODE=1` (VS Code, some terminals) makes Electron
  start as plain Node. Delete it from the child env before spawning Electron.
- A sandboxed renderer with `connect-src 'none'`: every API call and the SSE stream go
  through the main process (`invoke("api", …)`), so tokens never reach the page.
- On a fresh emulator, Chrome's first-run screen blocks the sign-in custom tab. Use
  `/data/local/tmp/chrome-command-line` with `--disable-fre` plus
  `am set-debug-app --persistent com.android.chrome`.
