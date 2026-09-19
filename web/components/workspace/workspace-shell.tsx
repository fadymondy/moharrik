"use client"

import Link from "next/link"
import { useState, type ReactNode } from "react"
import { useParams, usePathname, useRouter } from "next/navigation"
import { CheckIcon, ChevronsUpDownIcon, KeyRoundIcon, LayoutGridIcon, MessageSquarePlusIcon, PlusIcon, UsersIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { CubeMark } from "@/components/brand/cube-mark"
import { LanguageSwitcher, ThemeToggle } from "@/components/header-controls"
import { MahaamFeedbackWidget, openFeedback } from "@/components/feedback/mahaam-widget"
import { NotFoundState } from "@/components/not-found-state"
import { ErrorState } from "@/components/states"
import { CreateWorkspaceForm } from "@/components/workspace/create-workspace-form"
import { UserMenu } from "@/components/workspace/user-menu"
import { WorkspaceContext } from "@/components/workspace/workspace-context"
import { MOHARRIK_MARK } from "@/lib/brand/mark"
import type { WorkspaceSummary } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCurrentWorkspace, useMyWorkspaces } from "@/lib/queries"
import { useRequireAuth } from "@/lib/use-require-auth"
import { cn } from "@/lib/utils"

function Brand() {
  const { t, locale } = useTranslations()
  return (
    <Link href={`/${locale}/workspaces`} className="flex items-center gap-2.5">
      <CubeMark mark={MOHARRIK_MARK} size={24} />
      <span className="text-[15px] font-medium">{t("brand.name")}</span>
    </Link>
  )
}

function WorkspaceSwitcher({ active, all }: { active: WorkspaceSummary; all: WorkspaceSummary[] }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const pathname = usePathname()
  const [creating, setCreating] = useState(false)

  // Switching keeps the section (members, MCP) when it exists in every workspace.
  function hrefFor(slug: string) {
    const prefix = `/${locale}/w/${active.slug}`
    const rest = pathname.startsWith(prefix) ? pathname.slice(prefix.length) : ""
    return `/${locale}/w/${slug}${rest}`
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button variant="ghost" className="h-9 max-w-[16rem] justify-between gap-2 px-2.5" aria-label={t("shell.switchWorkspace")} />
          }
        >
          <span className="flex size-6 shrink-0 items-center justify-center border border-line bg-grid-card text-xs font-medium text-grid-brand">
            {active.name.slice(0, 1).toUpperCase()}
          </span>
          <span className="truncate text-sm font-medium">{active.name}</span>
          <ChevronsUpDownIcon className="text-grid-muted" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-64">
          <DropdownMenuGroup>
            <DropdownMenuLabel>{t("shell.workspaces")}</DropdownMenuLabel>
            {all.map((w) => (
              <DropdownMenuItem key={w.id} onClick={() => router.push(hrefFor(w.slug))}>
                <span className="min-w-0 flex-1 truncate">{w.name}</span>
                <span className="text-xs text-grid-muted">{t(`role.${w.role}`)}</span>
                {w.id === active.id ? <CheckIcon /> : null}
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem onClick={() => setCreating(true)}>
            <PlusIcon />
            {t("workspaces.create")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <Dialog open={creating} onOpenChange={setCreating}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("workspaces.createTitle")}</DialogTitle>
            <DialogDescription>{t("workspaces.createHint")}</DialogDescription>
          </DialogHeader>
          <CreateWorkspaceForm autoFocus onCreated={() => setCreating(false)} />
        </DialogContent>
      </Dialog>
    </>
  )
}

function NavLink({ href, icon, children, exact = false }: { href: string; icon: ReactNode; children: ReactNode; exact?: boolean }) {
  const pathname = usePathname()
  const active = exact ? pathname === href : pathname === href || pathname.startsWith(href + "/")
  return (
    <Link
      href={href}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex items-center gap-2.5 border-s-2 px-3 py-2 text-sm transition-colors [&_svg]:size-4 [&_svg]:shrink-0",
        active
          ? "border-grid-action bg-grid-soft font-medium text-grid-fg"
          : "border-transparent text-grid-body hover:bg-grid-soft hover:text-grid-fg",
      )}
    >
      {icon}
      {children}
    </Link>
  )
}

export function WorkspaceShell({ children }: { children: ReactNode }) {
  const { slug } = useParams<{ slug: string }>()
  const { t, locale } = useTranslations()
  const me = useRequireAuth()
  const list = useMyWorkspaces(!!me.data)

  const summary = list.data?.find((w) => w.slug === slug)
  const current = useCurrentWorkspace(summary?.id)
  const base = `/${locale}/w/${slug}`

  let body: ReactNode
  if (me.error || list.error) body = <ErrorState error={me.error ?? list.error} />
  else if (!me.data || !list.data) body = <ShellSkeleton />
  else if (!summary)
    body = (
      <NotFoundState
        title={t("shell.unknownWorkspace")}
        body={t("shell.unknownWorkspaceBody", { slug })}
        backHref={`/${locale}/workspaces`}
        backLabel={t("shell.allWorkspaces")}
      />
    )
  else if (current.error) body = <ErrorState error={current.error} />
  else
    body = (
      <WorkspaceContext.Provider
        value={{
          summary,
          current: current.data,
          id: summary.id,
          slug: summary.slug,
          can: (p) => !!current.data?.can?.includes(p),
        }}
      >
        {children}
      </WorkspaceContext.Provider>
    )

  return (
    <div className="grid-shell">
      <aside className="grid-shell-nav flex flex-col">
        <div className="flex h-14 items-center border-b border-line px-4">
          <Brand />
        </div>
        {summary ? (
          <nav className="flex flex-col py-3" aria-label={t("shell.navigation")}>
            <NavLink href={base} exact icon={<LayoutGridIcon />}>
              {t("nav.overview")}
            </NavLink>
            <NavLink href={`${base}/members`} icon={<UsersIcon />}>
              {t("nav.members")}
            </NavLink>
            <NavLink href={`${base}/settings/mcp`} icon={<KeyRoundIcon />}>
              {t("nav.mcpTokens")}
            </NavLink>
            <p className="mt-4 border-t border-line px-4 pt-4 text-xs text-grid-muted">{t("nav.comingNext")}</p>
          </nav>
        ) : null}
      </aside>

      <div className="flex min-w-0 flex-col">
        <header className="flex h-14 items-center justify-between gap-2 border-b border-line px-3 sm:px-4">
          <div className="min-w-0">
            {summary && list.data ? <WorkspaceSwitcher active={summary} all={list.data} /> : <Skeleton className="h-6 w-40" />}
          </div>
          <div className="flex items-center gap-1">
            {me.data ? (
              <Button variant="outline" size="sm" onClick={() => openFeedback()} aria-label={t("feedback.report")}>
                <MessageSquarePlusIcon />
                <span className="hidden sm:inline">{t("feedback.report")}</span>
              </Button>
            ) : null}
            <LanguageSwitcher />
            <ThemeToggle />
            {me.data ? <UserMenu email={me.data.email} onReport={() => openFeedback()} /> : null}
          </div>
        </header>
        <main className="min-w-0 flex-1">{body}</main>
      </div>

      {me.data ? <MahaamFeedbackWidget /> : null}
    </div>
  )
}

function ShellSkeleton() {
  return (
    <div className="space-y-4 p-6">
      <Skeleton className="h-8 w-56" />
      <Skeleton className="h-4 w-96 max-w-full" />
      <Skeleton className="h-32 w-full" />
    </div>
  )
}
