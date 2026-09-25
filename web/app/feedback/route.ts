import type { NextRequest } from "next/server"

import { sessionReporter, verifyWidgetKey } from "@/lib/feedback-token"

// "Report a problem" → Moharrik's project board in Mahaam.
//
// Mahaam's public intake (POST /api/feedback/embed) takes a per-project key (pfk_…) and an exact
// Origin allowlist. This route sends the report server to server instead: the key stays here, the
// Origin is Moharrik's own (MAHAAM_APP_URL), and the workspace, account and host are added to
// meta so the report says where it came from. Signed-in accounts only: the widget sends its report
// with credentials: "omit", so the account is proven by the signed reporter token it carries as its
// key (lib/feedback-token, issued by /feedback/session), or by the session cookie when one is sent.
export const dynamic = "force-dynamic"

const MAX_BYTES = 12 * 1024 * 1024

export async function POST(req: NextRequest) {
  const url = `${process.env.MAHAAM_URL ?? "https://console.mahaam.app"}/api/feedback/embed`
  const key = process.env.MAHAAM_FEEDBACK_KEY
  const origin = process.env.MAHAAM_APP_URL ?? "https://moharrik.com"
  if (!key) return Response.json({ detail: "Feedback is not configured." }, { status: 503 })
  if (Number(req.headers.get("content-length") ?? 0) > MAX_BYTES) {
    return Response.json({ detail: "The report is too large." }, { status: 413 })
  }

  let form: FormData
  try {
    form = await req.formData()
  } catch {
    return Response.json({ detail: "Invalid report." }, { status: 400 })
  }

  const me =
    verifyWidgetKey(String(form.get("public_key") ?? "")) ??
    (await sessionReporter(req.headers.get("cookie")))
  if (!me?.email) return Response.json({ detail: "Sign in to report a problem." }, { status: 401 })
  let meta: Record<string, unknown> = {}
  try {
    meta = JSON.parse(String(form.get("meta") ?? "{}"))
  } catch {}

  // The workspace is read from the page the report was filed on (/{locale}/w/{slug}/…).
  const host = req.headers.get("host") ?? ""
  let workspace: string | null = null
  try {
    const page = new URL(String(form.get("page_url") ?? req.headers.get("referer") ?? ""))
    workspace = page.pathname.match(/^\/[a-z]{2}\/w\/([^/]+)/)?.[1] ?? null
  } catch {}

  form.set(
    "meta",
    JSON.stringify({
      ...meta,
      product: "moharrik",
      host,
      workspace,
      reporter: me.email,
      reporter_id: me.id ?? null,
    }),
  )
  form.set("public_key", key)
  form.set("reporter_email", me.email)

  const res = await fetch(url, { method: "POST", body: form, headers: { Origin: origin } })
  const body = await res.text()
  if (!res.ok) {
    // Mahaam's refusal is logged, never shown: it may name the key or the origin allowlist.
    console.warn(`feedback: Mahaam answered ${res.status}: ${body.slice(0, 300)}`)
    // 402/403 (plan exhausted; key, origin or workspace unavailable) pass through: the widget tells
    // the reporter that reporting is unavailable instead of inviting a retry that cannot succeed.
    const status = res.status === 429 || res.status === 402 || res.status === 403 ? res.status : 502
    return Response.json({ detail: "The report was not accepted." }, { status })
  }
  return new Response(body, {
    status: 200,
    headers: { "content-type": res.headers.get("content-type") ?? "application/json" },
  })
}
