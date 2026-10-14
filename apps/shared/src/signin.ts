// App sign-in (docs/apps-plan.md §2): OAuth-for-native-apps shaped (RFC 8252 + PKCE), against
// Moharrik's own endpoints rather than an OAuth server.
//
//   1. start(): the app makes a verifier and opens the SYSTEM browser on
//      {server}/{locale}/app-signin?redirect_uri&code_challenge&state&device — the page signs the
//      member in exactly as the web does (password, two-factor, Google, CircleXO) and asks them
//      to confirm this device.
//   2. The page redirects to the app's redirect (moharrik://auth/callback on phones, a
//      127.0.0.1 port on the desktop) with ?code&state.
//   3. finish(): the app checks the state and trades code + verifier at POST /api/auth/app/token
//      for its device token, stored in the OS keystore. The code is useless without the verifier,
//      which never left the app.
//
// Crypto is injected (expo-crypto on phones, node:crypto in Electron) so this stays dependency-free.
import { ApiError, type MoharrikClient } from "./client"

export interface PkceCrypto {
  randomBytes(n: number): Uint8Array | Promise<Uint8Array>
  sha256(data: Uint8Array): Promise<Uint8Array>
}

export interface Pending {
  url: string
  verifier: string
  state: string
  redirectUri: string
}

export function base64url(bytes: Uint8Array): string {
  const abc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
  let out = ""
  let i = 0
  for (; i + 2 < bytes.length; i += 3) {
    const n = (bytes[i]! << 16) | (bytes[i + 1]! << 8) | bytes[i + 2]!
    out += abc[(n >> 18) & 63]! + abc[(n >> 12) & 63]! + abc[(n >> 6) & 63]! + abc[n & 63]!
  }
  const rest = bytes.length - i
  if (rest === 1) {
    const n = bytes[i]! << 16
    out += abc[(n >> 18) & 63]! + abc[(n >> 12) & 63]!
  } else if (rest === 2) {
    const n = (bytes[i]! << 16) | (bytes[i + 1]! << 8)
    out += abc[(n >> 18) & 63]! + abc[(n >> 12) & 63]! + abc[(n >> 6) & 63]!
  }
  return out
}

const utf8 = (s: string) => new TextEncoder().encode(s)

/** The S256 challenge of a verifier (RFC 7636 §4.2). */
export async function challengeOf(verifier: string, crypto: PkceCrypto): Promise<string> {
  return base64url(await crypto.sha256(utf8(verifier)))
}

export type SocialProvider = "apple" | "google"

export async function startSignIn(o: {
  serverUrl: string
  redirectUri: string
  device: string
  locale: "en" | "ar"
  crypto: PkceCrypto
  /** Go straight to this provider on the sign-in page (Google, and Apple on Android). */
  provider?: SocialProvider
}): Promise<Pending> {
  const verifier = base64url(await o.crypto.randomBytes(32)) // 43 chars
  const state = base64url(await o.crypto.randomBytes(16)) // 22 chars
  const p = new URLSearchParams({
    redirect_uri: o.redirectUri,
    code_challenge: await challengeOf(verifier, o.crypto),
    code_challenge_method: "S256",
    state,
    device: o.device.slice(0, 200),
  })
  if (o.provider) p.set("provider", o.provider)
  const url = `${o.serverUrl.replace(/\/+$/, "")}/${o.locale}/app-signin?${p.toString()}`
  return { url, verifier, state, redirectUri: o.redirectUri }
}

/** Reads the code from the redirect the browser came back with, refusing a foreign state. */
export function readCallback(callbackUrl: string, pending: Pick<Pending, "state">): string {
  const q = callbackUrl.includes("?") ? callbackUrl.slice(callbackUrl.indexOf("?") + 1).split("#")[0]! : ""
  const p = new URLSearchParams(q)
  if (p.get("state") !== pending.state) throw new Error("sign-in state mismatch")
  const code = p.get("code")
  if (!code) throw new Error(p.get("error") || "no sign-in code")
  return code
}

/** Trades the code for this install's device token and stores it on the client. */
export async function finishSignIn(
  client: MoharrikClient,
  pending: Pick<Pending, "verifier" | "redirectUri">,
  code: string,
): Promise<{ email: string }> {
  const r = await client.call("post", "/api/auth/app/token", {
    workspace: null,
    body: { code, code_verifier: pending.verifier, redirect_uri: pending.redirectUri },
  })
  if (!r?.token) throw new ApiError(500, "no token in the sign-in response")
  await client.setToken(r.token)
  return { email: r.email }
}

/** Which sign-in options this server offers (GET /api/auth/methods, the auth plugin's list). */
export interface SignInOptions {
  /** Sign in with Apple in the browser (the web, and Android through the browser flow). */
  appleWeb: boolean
  /** Native Sign in with Apple (iOS): POST /api/auth/native/apple. */
  appleNative: boolean
  /** Sign in with Google in the browser. */
  googleWeb: boolean
}

export function parseSignInOptions(data: unknown): SignInOptions {
  const out: SignInOptions = { appleWeb: false, appleNative: false, googleWeb: false }
  const list = (data as { methods?: unknown } | null | undefined)?.methods
  if (!Array.isArray(list)) return out
  for (const m of list) {
    if (!m || typeof m !== "object") continue
    const { name, type } = m as { name?: unknown; type?: unknown }
    if (name === "apple" && type === "oauth") out.appleWeb = true
    if (name === "apple" && type === "native") out.appleNative = true
    if (name === "google" && type === "oauth") out.googleWeb = true
  }
  return out
}

export async function signInOptions(client: MoharrikClient): Promise<SignInOptions> {
  return parseSignInOptions(await client.raw<unknown>("get", "/api/auth/methods", { workspace: null }))
}

/** What the native Sign in with Apple sheet returned, plus the raw nonce the app made. */
export interface AppleCredential {
  identityToken: string
  authorizationCode: string
  nonce: string
  givenName?: string | null
  familyName?: string | null
}

/** Trades a native Sign in with Apple result for this install's device token. */
export async function finishAppleSignIn(
  client: MoharrikClient,
  cred: AppleCredential,
  device: string,
): Promise<{ email: string }> {
  const r = await client.call("post", "/api/auth/native/apple", {
    workspace: null,
    body: {
      identity_token: cred.identityToken,
      authorization_code: cred.authorizationCode,
      nonce: cred.nonce,
      given_name: cred.givenName ?? undefined,
      family_name: cred.familyName ?? undefined,
      device: device.slice(0, 200),
    },
  })
  if (!r?.token) throw new ApiError(500, "no token in the sign-in response")
  await client.setToken(r.token)
  return { email: r.email }
}

/** The refusal code a native sign-in or the account endpoints sent ({"error", "code"}), if any. */
export function errorCode(err: unknown): string | undefined {
  if (err instanceof ApiError && err.body && typeof err.body === "object") {
    const c = (err.body as { code?: unknown }).code
    return typeof c === "string" ? c : undefined
  }
  return undefined
}
