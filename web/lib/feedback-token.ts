import { createHmac, timingSafeEqual } from "node:crypto"

// The reporter token for the "Report a problem" widget (MH-464). Server-only.
//
// Mahaam's widget (console.mahaam.app/embed/v1.js) sends its report and its config request with
// `credentials: "omit"`, so the session cookie never reaches our /feedback route, even on the same
// origin — every report was refused as "sign in to report a problem". Instead, the signed-in page
// asks /feedback/session (a normal same-origin request, cookie included) for a short-lived token
// and hands it to the widget as its data-key; the widget sends the key back as `public_key` on the
// report, and /feedback verifies it. The token says only "this account may file a report until
// exp". It is signed with a secret derived from MAHAAM_FEEDBACK_KEY, which the route needs anyway.

export const WIDGET_KEY_PREFIX = "pfk_moharrik."
const TTL_SECONDS = 24 * 60 * 60

export type Reporter = { id: string | number | null; email: string }

function secret(): Buffer | null {
  const key = process.env.MAHAAM_FEEDBACK_KEY
  if (!key) return null
  return createHmac("sha256", key).update("moharrik feedback reporter token v1").digest()
}

function sign(payload: string, s: Buffer): string {
  return createHmac("sha256", s).update(payload).digest("base64url")
}

/** A widget key carrying a signed reporter token, or null when feedback is not configured. */
export function issueWidgetKey(r: Reporter, now = Date.now()): string | null {
  const s = secret()
  if (!s) return null
  const payload = Buffer.from(
    JSON.stringify({ sub: r.id, email: r.email, exp: Math.floor(now / 1000) + TTL_SECONDS }),
  ).toString("base64url")
  return `${WIDGET_KEY_PREFIX}${payload}.${sign(payload, s)}`
}

/** The reporter a widget key vouches for, or null if it is not ours, tampered with, or expired. */
export function verifyWidgetKey(key: string | null | undefined, now = Date.now()): Reporter | null {
  const s = secret()
  if (!s || !key || !key.startsWith(WIDGET_KEY_PREFIX)) return null
  const [payload, sig, extra] = key.slice(WIDGET_KEY_PREFIX.length).split(".")
  if (!payload || !sig || extra !== undefined) return null
  const want = Buffer.from(sign(payload, s))
  const got = Buffer.from(sig)
  if (want.length !== got.length || !timingSafeEqual(want, got)) return null
  try {
    const body = JSON.parse(Buffer.from(payload, "base64url").toString("utf8")) as {
      sub?: string | number
      email?: string
      exp?: number
    }
    if (!body.email || typeof body.exp !== "number" || body.exp * 1000 <= now) return null
    return { id: body.sub ?? null, email: body.email }
  } catch {
    return null
  }
}

const API_ORIGIN = process.env.API_ORIGIN ?? "http://localhost:8120"

type Me = { id?: string | number; email?: string }

/** The signed-in account behind a request's cookies, asked of the API. */
export async function sessionReporter(cookie: string | null | undefined): Promise<Reporter | null> {
  if (!cookie) return null
  try {
    const res = await fetch(`${API_ORIGIN}/api/auth/me`, { headers: { cookie }, cache: "no-store" })
    if (!res.ok) return null
    const body = (await res.json()) as Me | { user?: Me }
    const me = "user" in body && body.user ? body.user : (body as Me)
    return me.email ? { id: me.id ?? null, email: me.email } : null
  } catch {
    return null
  }
}
