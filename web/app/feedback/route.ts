import type { NextRequest } from "next/server"

// "Report a problem" → Moharrik's project board in Mahaam.
//
// Mahaam's public intake (POST /api/feedback/embed) takes a per-project key (pfk_…) and an exact
// Origin allowlist. This route sends the report server to server instead: the key stays here, the
// Origin is Moharrik's own (MAHAAM_APP_URL), and the workspace, account and host are added to
// meta so the report says where it came from. Signed-in accounts only.
export const dynamic = "force-dynamic"

const MAX_BYTES = 12 * 1024 * 1024
const API_ORIGIN = process.env.API_ORIGIN ?? "http://localhost:8120"

type Me = { id?: string | number; email?: string }

async function currentUser(cookie: string | undefined): Promise<Me | null> {
  if (!cookie) return null
  try {
    const res = await fetch(`${API_ORIGIN}/api/auth/me`, { headers: { cookie }, cache: "no-store" })
    if (!res.ok) return null
    const body = (await res.json()) as Me | { user?: Me }
    return "user" in body && body.user ? body.user : (body as Me)
  } catch {
    return null
  }
}

export async function POST(req: NextRequest) {
  const url = `${process.env.MAHAAM_URL ?? "https://console.mahaam.app"}/api/feedback/embed`
  const key = process.env.MAHAAM_FEEDBACK_KEY
  const origin = process.env.MAHAAM_APP_URL ?? "https://moharrik.com"
  if (!key) return Response.json({ detail: "Feedback is not configured." }, { status: 503 })
  if (Number(req.headers.get("content-length") ?? 0) > MAX_BYTES) {
    return Response.json({ detail: "The report is too large." }, { status: 413 })
  }

  const me = await currentUser(req.headers.get("cookie") ?? undefined)
  if (!me?.email) return Response.json({ detail: "Sign in to report a problem." }, { status: 401 })

  let form: FormData
  try {
    form = await req.formData()
  } catch {
    return Response.json({ detail: "Invalid report." }, { status: 400 })
  }
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
    JSON.stringify({ ...meta, product: "moharrik", host, workspace, reporter: me.email, reporter_id: me.id ?? null }),
  )
  form.set("public_key", key)
  form.set("reporter_email", me.email)

  const res = await fetch(url, { method: "POST", body: form, headers: { Origin: origin } })
  const body = await res.text()
  if (!res.ok) {
    return Response.json({ detail: "The report was not accepted." }, { status: res.status === 429 ? 429 : 502 })
  }
  return new Response(body, { status: 200, headers: { "content-type": res.headers.get("content-type") ?? "application/json" } })
}
