import type { NextRequest } from "next/server"

import { issueWidgetKey, sessionReporter } from "@/lib/feedback-token"

// GET /feedback/session — the widget key for the signed-in account (MH-464; see lib/feedback-token).
// 503 when feedback is not configured on this deployment, so the page shows no "Report a problem"
// button that could never send; 401 when nobody is signed in.
export const dynamic = "force-dynamic"

export async function GET(req: NextRequest) {
  if (!process.env.MAHAAM_FEEDBACK_KEY) {
    return Response.json(
      { detail: "Feedback is not configured." },
      { status: 503, headers: { "cache-control": "no-store" } },
    )
  }
  const me = await sessionReporter(req.headers.get("cookie"))
  if (!me)
    return Response.json(
      { detail: "Sign in to report a problem." },
      { status: 401, headers: { "cache-control": "no-store" } },
    )
  return Response.json({ key: issueWidgetKey(me) }, { headers: { "cache-control": "no-store" } })
}
