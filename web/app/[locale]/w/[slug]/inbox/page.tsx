"use client"

/*
The inbox is a dock on every workspace page now (MH-1147), not a page. This route stays
for the links that leave the app — a web push, a desktop notification, an old bookmark —
and opens the dock over the workspace home: /inbox?c=<id> on that conversation,
/inbox?unread=1 on the unread list, /inbox on the list. A client redirect for the same
reason as inbox/email/page.tsx.
*/

import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Suspense, useEffect } from "react"

import { inboxHref } from "@/lib/inbox-link"

export default function InboxRedirectPage() {
  return (
    <Suspense fallback={null}>
      <InboxRedirect />
    </Suspense>
  )
}

function InboxRedirect() {
  const router = useRouter()
  const { locale, slug } = useParams<{ locale: string; slug: string }>()
  const params = useSearchParams()
  const c = params.get("c")
  const unread = params.get("unread") === "1"
  const channel = params.get("channel") ?? undefined
  useEffect(() => {
    router.replace(`/${locale}/w/${slug}${inboxHref(c ?? (unread ? "unread" : "all"), channel)}`)
  }, [router, locale, slug, c, unread, channel])
  return null
}
