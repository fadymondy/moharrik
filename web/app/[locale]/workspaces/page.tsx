"use client"

import Link from "next/link"
import { ChevronRightIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { PublicFrame, SectionHeader } from "@/components/public-frame"
import { CreateWorkspaceForm } from "@/components/workspace/create-workspace-form"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { UserMenu } from "@/components/workspace/user-menu"
import { Ltr } from "@/components/copy-field"
import { useTranslations } from "@/lib/i18n"
import { useMyWorkspaces } from "@/lib/queries"
import { useRequireAuth } from "@/lib/use-require-auth"
import { useDocumentTitle } from "@/lib/title"

export default function WorkspacesPage() {
  const { t, locale } = useTranslations()
  useDocumentTitle(t("workspaces.title"))
  const me = useRequireAuth()
  const list = useMyWorkspaces(!!me.data)

  if (!me.data) {
    return (
      <PublicFrame>
        {me.error ? <ErrorState error={me.error} /> : <div className="p-6"><Skeleton className="h-24 w-full" /></div>}
      </PublicFrame>
    )
  }

  const workspaces = list.data ?? []

  return (
    <PublicFrame>
      <SectionHeader
        micro={<Ltr>{me.data.email}</Ltr>}
        title={t("workspaces.title")}
        description={t("workspaces.pickHint")}
        action={<UserMenu email={me.data.email} />}
      />

      {list.error ? (
        <ErrorState error={list.error} />
      ) : list.isLoading ? (
        <LoadingRows rows={2} />
      ) : workspaces.length === 0 ? (
        <EmptyState title={t("workspaces.emptyTitle")} body={t("workspaces.emptyBody")} />
      ) : (
        <ul className="divide-y divide-line border-y border-line">
          {workspaces.map((w) => (
            <li key={w.id}>
              <Link
                href={`/${locale}/w/${w.slug}`}
                className="flex items-center gap-4 px-6 py-4 transition-colors hover:bg-grid-soft"
              >
                <span className="flex size-9 shrink-0 items-center justify-center border border-line bg-grid-card text-sm font-medium text-grid-brand">
                  {w.name.slice(0, 1).toUpperCase()}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{w.name}</span>
                  <Ltr mono className="text-xs text-grid-muted">
                    /{w.slug}
                  </Ltr>
                </span>
                <Badge variant="outline">{t(`role.${w.role}`)}</Badge>
                <ChevronRightIcon className="size-4 text-grid-muted rtl:rotate-180" />
              </Link>
            </li>
          ))}
        </ul>
      )}

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />
      <section className="px-6 py-6">
        <h2 className="mb-1 text-base font-medium">{t("workspaces.createTitle")}</h2>
        <p className="mb-5 text-sm text-grid-muted">{t("workspaces.createHint")}</p>
        <CreateWorkspaceForm autoFocus={!list.isLoading && workspaces.length === 0} />
      </section>
    </PublicFrame>
  )
}
