"use client"

// MH-1148: Channels is a tab of Settings › Integrations now. Keeps the filters an old link
// carried (?type=, ?status=, ?q=). A client redirect for the same reason as inbox/email.

import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Suspense, useEffect } from "react"

export default function ChannelsRedirectPage() {
  return (
    <Suspense fallback={null}>
      <ChannelsRedirect />
    </Suspense>
  )
}

function ChannelsRedirect() {
  const router = useRouter()
  const { locale, slug } = useParams<{ locale: string; slug: string }>()
  const qs = useSearchParams().toString()
  useEffect(() => {
    router.replace(`/${locale}/w/${slug}/settings/integrations${qs ? `?${qs}` : ""}`)
  }, [router, locale, slug, qs])
  return null
}
