"use client"

import { useEffect } from "react"
import { useRouter } from "next/navigation"

import { Skeleton } from "@fadymondy/nasaq/web"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
import { ErrorState } from "@/components/states"
import { useTranslations } from "@/lib/i18n"
import { pickHomeWorkspace, rememberedWorkspace } from "@/lib/last-workspace"
import { useMyWorkspaces } from "@/lib/queries"
import { useRequireAuth } from "@/lib/use-require-auth"
import { useDocumentTitle } from "@/lib/title"

/**
 * MH-1108: where a signed-in visitor lands. Opens the dashboard of the last workspace they used
 * (or were last active in); someone with no workspace yet gets the list, where they can make one.
 */
export function OpenLastWorkspace() {
  const { t, locale } = useTranslations()
  useDocumentTitle(t("workspaces.title"))
  const router = useRouter()
  const me = useRequireAuth()
  const list = useMyWorkspaces(!!me.data)

  useEffect(() => {
    if (!list.data) return
    const slug = pickHomeWorkspace(list.data, rememberedWorkspace())
    router.replace(slug ? `/${locale}/w/${slug}` : `/${locale}/workspaces`)
  }, [list.data, locale, router])

  const error = me.error ?? list.error
  return (
    <PublicFrame title={t("workspaces.title")}>
      <PublicPanel>
        {error ? <ErrorState error={error} /> : <Skeleton className="h-24 w-full" />}
      </PublicPanel>
    </PublicFrame>
  )
}
