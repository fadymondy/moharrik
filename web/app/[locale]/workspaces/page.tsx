"use client"

import Link from "next/link"
import { ChevronRightIcon, Settings2Icon, UsersIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
import { CreateWorkspaceForm } from "@/components/workspace/create-workspace-form"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { UserMenu } from "@/components/workspace/user-menu"
import { WorkspaceMark } from "@/components/workspace/workspace-mark"
import { Ltr } from "@/components/copy-field"
import { useTranslations } from "@/lib/i18n"
import { useMyWorkspaces } from "@/lib/queries"
import { useRequireAuth } from "@/lib/use-require-auth"
import { useDocumentTitle } from "@/lib/title"
import { BdiBlock } from "@/components/bidi"

export default function WorkspacesPage() {
  const { t, locale } = useTranslations()
  useDocumentTitle(t("workspaces.title"))
  const me = useRequireAuth()
  const list = useMyWorkspaces(!!me.data)

  if (!me.data) {
    return (
      <PublicFrame title={t("workspaces.title")}>
        <PublicPanel>
          {me.error ? <ErrorState error={me.error} /> : <Skeleton className="h-24 w-full" />}
        </PublicPanel>
      </PublicFrame>
    )
  }

  const workspaces = list.data ?? []

  return (
    <PublicFrame
      eyebrow={<Ltr>{me.data.email}</Ltr>}
      title={t("workspaces.title")}
      description={t("workspaces.pickHint")}
    >
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
          <ul className="divide-y divide-line" data-workspace-list>
            {workspaces.map((w) => (
              <li
                key={w.id}
                className="flex flex-wrap items-center gap-x-4 gap-y-3 px-4 py-4"
                data-workspace-row={w.slug}
              >
                <Link
                  href={`/${locale}/w/${w.slug}`}
                  className="flex min-w-0 flex-1 basis-56 items-center gap-4 outline-none focus-visible:underline"
                >
                  <WorkspaceMark name={w.name} avatarUrl={w.avatar_url} />
                  <span className="min-w-0 flex-1">
                    <BdiBlock className="block truncate text-sm font-medium">{w.name}</BdiBlock>
                    <span className="flex flex-wrap items-center gap-x-2 text-xs text-grid-muted">
                      <Ltr mono>/{w.slug}</Ltr>
                      <span aria-hidden>·</span>
                      <span className="inline-flex items-center gap-1" data-member-count>
                        <UsersIcon className="size-3" />
                        {w.member_count === 1
                          ? t("workspaces.memberCountOne")
                          : t("workspaces.memberCount", { count: w.member_count ?? 0 })}
                      </span>
                    </span>
                  </span>
                </Link>
                <div className="flex items-center gap-2 max-sm:w-full max-sm:ps-13">
                  <Badge variant="outline">{t(`role.${w.role}`)}</Badge>
                  <span className="flex-1 sm:hidden" />
                  <Button
                    variant="outline"
                    size="sm"
                    nativeButton={false}
                    render={<Link href={`/${locale}/w/${w.slug}/settings/general`} />}
                    data-workspace-manage
                  >
                    <Settings2Icon />
                    {t("workspaces.manage")}
                  </Button>
                  <Button
                    size="sm"
                    nativeButton={false}
                    render={<Link href={`/${locale}/w/${w.slug}`} />}
                    data-workspace-open
                  >
                    {t("workspaces.open")}
                    <ChevronRightIcon className="rtl:rotate-180" />
                  </Button>
                </div>
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
