"use client"

/*
MH-571 folded the seven per-type channel setup pages (email, whatsapp,
widgets, telegram, slack, discord, workflow-channels) into one Channels tab of Settings › Integrations (MH-1148)
that lists every connected channel with filters. This route now only
redirects an old link/bookmark there, pre-filtered to this type. Its form
(MailboxChannelForm) moved to @/components/channels/mailbox-channel-form.tsx.

A client redirect, not next/navigation's server redirect(): this route sits
under the workspace shell's client layout boundary, where a thrown
server-side redirect does not turn into an HTTP redirect in this app's
Next.js/Turbopack setup (confirmed by direct testing in MH-526's Automation
split — see automation/page.tsx). useRouter().replace() is the reliable path
already used everywhere else in this codebase for this shell.
*/

import { useParams, useRouter } from "next/navigation"
import { useEffect } from "react"

export default function MailboxesRedirect() {
  const router = useRouter()
  const { locale, slug } = useParams<{ locale: string; slug: string }>()
  useEffect(() => {
    router.replace(`/${locale}/w/${slug}/settings/integrations?type=email`)
  }, [router, locale, slug])
  return null
}
