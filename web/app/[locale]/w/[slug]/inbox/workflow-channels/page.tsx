"use client"

/*
MH-571: folded into the unified Channels page — see inbox/email/page.tsx for
the full rationale. This type's form (WorkflowChannelForm) moved to
@/components/channels/workflow-channel-form.tsx.
*/

import { useParams, useRouter } from "next/navigation"
import { useEffect } from "react"

export default function WorkflowChannelsRedirect() {
  const router = useRouter()
  const { locale, slug } = useParams<{ locale: string; slug: string }>()
  useEffect(() => {
    router.replace(`/${locale}/w/${slug}/inbox/channels?type=workflow`)
  }, [router, locale, slug])
  return null
}
