"use client"

/*
MH-571: folded into the unified Channels page — see inbox/email/page.tsx for
the full rationale. Widgets are the "chat" channel type there. This type's
create form (WidgetChannelForm) moved to
@/components/channels/widget-channel-form.tsx; its full settings editor
(WidgetSettingsSheet) is unchanged at @/components/inbox/widget-settings.
*/

import { useParams, useRouter } from "next/navigation"
import { useEffect } from "react"

export default function ChatWidgetsRedirect() {
  const router = useRouter()
  const { locale, slug } = useParams<{ locale: string; slug: string }>()
  useEffect(() => {
    router.replace(`/${locale}/w/${slug}/settings/integrations?type=chat`)
  }, [router, locale, slug])
  return null
}
