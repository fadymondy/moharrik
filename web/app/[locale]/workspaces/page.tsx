"use client"

import Link from "next/link"
import { ChevronRightIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Skeleton } from "@/components/ui/skeleton"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
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
      <PublicFrame title={t("workspaces.title")}>
        <PublicPanel>{me.error ? <ErrorState error={me.error} /> : <Skeleton className="h-24 w-full" />}</PublicPanel>
      </PublicFrame>
    )
  }

  const workspaces = list.data ?? []

  return (
    <PublicFrame eyebrow={<Ltr>{me.data.email}</Ltr>} title={t("workspaces.title")} description={t("workspaces.pickHint")}>
      <section className="grid-screen-line-top grid-screen-line-bottom border-x border-line">
      <div className="flex items-center justify-between gap-4 border-b border-line px-4 py-2">
        <span className="grid-micro">{t("workspaces.title")}</span>
        <UserMenu email={me.data.email} />
      </div>

      {list.error ? (
        <ErrorState error={list.error} />
      ) : list.isLoading ? (
        <LoadingRows rows={2} />
      ) : workspaces.length === 0 ? (
        <EmptyState title={t("workspaces.emptyTitle")} body={t("workspaces.emptyBody")} />
      ) : (
        <ul className="divide-y divide-line">
          {workspaces.map((w) => (
            <li key={w.id}>
              <Link
                href={`/${locale}/w/${w.slug}`}
                className="flex items-center gap-4 px-4 py-4 transition-colors hover:bg-grid-soft"
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

      </section>
      <div className="grid-separator" aria-hidden />
      <section className="grid-screen-line-top grid-screen-line-bottom grid border-x border-line md:grid-cols-2">
        <div className="border-line p-4 max-md:border-b md:border-e">
          <h2 className="mb-1 text-xl font-medium">{t("workspaces.createTitle")}</h2>
          <p className="text-sm text-grid-muted">{t("workspaces.createHint")}</p>
        </div>
        <div className="p-4">
          <CreateWorkspaceForm autoFocus={!list.isLoading && workspaces.length === 0} />
        </div>
      </section>
    </PublicFrame>
  )
}
