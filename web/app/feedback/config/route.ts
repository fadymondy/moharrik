// GET /feedback/config — the widget's look (MH-464).
//
// Mahaam's widget asks `<data-endpoint>/config?key=…` for the look set on the project's feedback key
// (colours, launcher, locale). Our endpoint is /feedback, so it asked /feedback/config, which did
// not exist: the locale proxy redirected it to /{locale}/feedback/config and it 404'd on every
// workspace page. This answers it from Mahaam's own config endpoint with Moharrik's real key; the
// key the widget sends is the reporter token and is ignored here. The look is not secret, so no
// session is needed — the widget sends none anyway (credentials: "omit").
export const dynamic = "force-dynamic"

const TTL_MS = 60_000
let cached: { at: number; widget: unknown } | null = null

export async function GET() {
  const key = process.env.MAHAAM_FEEDBACK_KEY
  if (!key) return Response.json({ widget: null })
  if (cached && Date.now() - cached.at < TTL_MS) return look(cached.widget)
  const base = process.env.MAHAAM_URL ?? "https://console.mahaam.app"
  try {
    const res = await fetch(`${base}/api/feedback/embed/config?key=${encodeURIComponent(key)}`, {
      cache: "no-store",
      // The widget gives up after 2 s and mounts with its default look; answer before that.
      signal: AbortSignal.timeout(1500),
    })
    if (!res.ok) return look(null)
    const body = (await res.json()) as { widget?: unknown }
    cached = { at: Date.now(), widget: body.widget ?? null }
    return look(cached.widget)
  } catch {
    return look(null)
  }
}

function look(widget: unknown) {
  return Response.json({ widget: widget ?? null }, { headers: { "cache-control": "public, max-age=60" } })
}
