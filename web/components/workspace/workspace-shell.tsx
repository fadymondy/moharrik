"use client"

import Link from "next/link"
import { useEffect, useState, type ReactNode } from "react"
import { useParams, usePathname, useRouter } from "next/navigation"
import {
  ArrowLeftIcon,
  BookOpenIcon,
  CheckIcon,
  ChevronRightIcon,
  ChevronsUpDownIcon,
  LayoutDashboardIcon,
  MenuIcon,
  MessageSquarePlusIcon,
  PanelLeftCloseIcon,
  PanelLeftOpenIcon,
  PlusIcon,
  Settings2Icon,
} from "lucide-react"

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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet"
import { WorkspaceMark } from "@/components/workspace/workspace-mark"
import { LiveAlerts } from "@/components/alerts/live-alerts"
import { NotificationBell } from "@/components/alerts/notification-bell"
import { CubeMark } from "@/components/brand/cube-mark"
import { ChatDock } from "@/components/inbox/chat-dock"
import { LanguageSwitcher, ThemeToggle } from "@/components/header-controls"
import {
  MahaamFeedbackWidget,
  openFeedback,
  useFeedbackReady,
} from "@/components/feedback/mahaam-widget"
import { NotFoundState } from "@/components/not-found-state"
import { ErrorState } from "@/components/states"
import { CreateWorkspaceForm } from "@/components/workspace/create-workspace-form"
import { UserMenu } from "@/components/workspace/user-menu"
import { WorkspaceContext } from "@/components/workspace/workspace-context"
import { MOHARRIK_MARK } from "@/lib/brand/mark"
import type { WorkspaceSummary } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import {
  groupForPath,
  itemActive,
  visibleEntries,
  visibleItems,
  type NavGroup,
  type NavItem,
} from "@/lib/nav-config"
import { useCurrentWorkspace, useMyWorkspaces } from "@/lib/queries"
import { useRequireAuth } from "@/lib/use-require-auth"
import { cn } from "@/lib/utils"
import { mcpDocsHref } from "@/lib/mcp-docs"
import { BdiBlock } from "@/components/bidi"

function Brand({ mini = false }: { mini?: boolean }) {
  const { t, locale } = useTranslations()
  return (
    <Link href={`/${locale}/workspaces`} className="flex items-center gap-2.5">
      <CubeMark mark={MOHARRIK_MARK} size={24} />
      {mini ? null : <span className="text-[15px] font-medium">{t("brand.name")}</span>}
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
            // min-w-0 + shrink: the Button base is shrink-0, so at phone width a
            // long workspace name slid UNDER the header's right-hand controls.
            <Button
              variant="ghost"
              className="h-9 max-w-[16rem] min-w-0 shrink justify-between gap-2 px-2.5"
              aria-label={t("shell.switchWorkspace")}
            />
          }
        >
          <WorkspaceMark name={active.name} avatarUrl={active.avatar_url} size="sm" />
          <BdiBlock className="truncate text-sm font-medium">{active.name}</BdiBlock>
          <ChevronsUpDownIcon className="text-grid-muted" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-64">
          <DropdownMenuGroup>
            <DropdownMenuLabel>{t("shell.workspaces")}</DropdownMenuLabel>
            {all.map((w) => (
              <DropdownMenuItem key={w.id} onClick={() => router.push(hrefFor(w.slug))}>
                <WorkspaceMark name={w.name} avatarUrl={w.avatar_url} size="sm" />
                <BdiBlock className="min-w-0 flex-1 truncate">{w.name}</BdiBlock>
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
          <DropdownMenuItem
            onClick={() => router.push(`/${locale}/workspaces`)}
            data-manage-workspaces
          >
            <Settings2Icon />
            {t("workspaces.manageAll")}
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

/** One navigable row: a real page, whether a top-level link or a page inside a drilled-down group. */
function NavLink({
  href,
  icon,
  children,
  active,
  onNavigate,
  mini = false,
  dataNav,
}: {
  href: string
  icon: ReactNode
  children: ReactNode
  active: boolean
  onNavigate?: () => void
  mini?: boolean
  dataNav?: string
}) {
  return (
    <Link
      href={href}
      onClick={onNavigate}
      aria-current={active ? "page" : undefined}
      title={mini && typeof children === "string" ? children : undefined}
      data-nav-item={dataNav}
      className={cn(
        "flex items-center gap-2.5 border-s-2 px-3 py-2 text-sm transition-colors [&_svg]:size-4 [&_svg]:shrink-0",
        mini && "justify-center px-0 py-2.5",
        active
          ? "border-grid-action bg-grid-soft font-medium text-grid-fg"
          : "border-transparent text-grid-body hover:bg-grid-soft hover:text-grid-fg",
      )}
    >
      {icon}
      {mini ? <span className="sr-only">{children}</span> : children}
    </Link>
  )
}

/** A group row on the root screen: a link to that section's own page (MH-673), whose dashboard it opens. */
function NavGroupLink({
  group,
  href,
  onNavigate,
  mini = false,
}: {
  group: NavGroup
  href: string
  onNavigate?: () => void
  mini?: boolean
}) {
  const { t } = useTranslations()
  const Icon = group.icon
  const label = t(group.labelKey)
  return (
    <Link
      href={href}
      onClick={onNavigate}
      title={mini ? label : undefined}
      data-nav-group={group.key}
      className={cn(
        "flex w-full items-center gap-2.5 border-s-2 px-3 py-2 text-sm transition-colors [&_svg]:size-4 [&_svg]:shrink-0",
        mini && "justify-center px-0 py-2.5",
        "border-transparent text-grid-body hover:bg-grid-soft hover:text-grid-fg",
      )}
    >
      <Icon />
      {mini ? (
        <span className="sr-only">{label}</span>
      ) : (
        <>
          <span className="min-w-0 flex-1 truncate text-start">{label}</span>
          <ChevronRightIcon className="size-3.5 shrink-0 text-grid-muted rtl:rotate-180" />
        </>
      )}
    </Link>
  )
}

export function WorkspaceShell({ children }: { children: ReactNode }) {
  const { slug } = useParams<{ slug: string }>()
  const { t, locale } = useTranslations()
  const pathname = usePathname()
  const me = useRequireAuth()
  // "Report a problem" shows only once the widget can actually send (MH-464): a deployment with
  // no Mahaam key, or a widget script that failed to load, gets no button rather than a dead one.
  const canReport = useFeedbackReady()
  const list = useMyWorkspaces(!!me.data)
  const [navOpen, setNavOpen] = useState(false)
  // The desktop sidebar collapses to an icon rail; the choice is kept per browser. Ctrl/⌘+B toggles.
  const [mini, setMini] = useState(false)
  useEffect(() => {
    setMini(localStorage.getItem("moharrik.nav") === "mini")
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "b") {
        e.preventDefault()
        toggleMini()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])
  function toggleMini() {
    setMini((m) => {
      localStorage.setItem("moharrik.nav", m ? "full" : "mini")
      return !m
    })
  }
  const { isRtl } = useTranslations()

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
    <div className="grid-shell" data-nav={mini ? "mini" : "full"}>
      {/* The column carries the panel colour and its rule for the page's full height; the nav
          inside it is what sticks. With both on one sticky h-dvh element the panel ended one
          screen down and the page background showed below it on a long page. */}
      <div className="grid-shell-nav hidden md:block">
        <aside className="sticky top-0 flex h-dvh flex-col overflow-y-auto">
          <div
            className={cn(
              "flex h-14 shrink-0 items-center border-b border-line px-4",
              mini && "justify-center px-0",
            )}
          >
            <Brand mini={mini} />
          </div>
          {summary ? <ShellNav base={base} mini={mini} can={current.data?.can} /> : null}
        </aside>
      </div>

      <div className="flex min-w-0 flex-col">
        <header className="flex h-14 items-center justify-between gap-2 border-b border-line px-3 sm:px-4">
          <div className="flex min-w-0 items-center gap-1">
            {summary ? (
              <Button
                variant="ghost"
                size="icon-sm"
                className="md:hidden"
                aria-label={t("shell.navigation")}
                onClick={() => setNavOpen(true)}
                data-mobile-nav-toggle
              >
                <MenuIcon />
              </Button>
            ) : null}
            <Button
              variant="ghost"
              size="icon-sm"
              className="hidden md:inline-flex"
              aria-label={t("shell.toggleSidebar")}
              aria-pressed={mini}
              title={t("shell.toggleSidebar")}
              onClick={toggleMini}
              data-nav-toggle
            >
              {mini ? (
                <PanelLeftOpenIcon className="rtl:-scale-x-100" />
              ) : (
                <PanelLeftCloseIcon className="rtl:-scale-x-100" />
              )}
            </Button>
            {summary && list.data ? (
              <WorkspaceSwitcher active={summary} all={list.data} />
            ) : (
              <Skeleton className="h-6 w-40" />
            )}
          </div>
          <div className="flex shrink-0 items-center gap-1">
            {me.data && canReport ? (
              <Button
                variant="outline"
                size="sm"
                onClick={() => openFeedback()}
                aria-label={t("feedback.report")}
              >
                <MessageSquarePlusIcon />
                <span className="hidden sm:inline">{t("feedback.report")}</span>
              </Button>
            ) : null}
            {/* The bell and the live alerts ride on every workspace page (MH-378):
                a customer who writes while the member is on Contacts must not
                wait for them to open the inbox. */}
            {summary && current.data ? (
              <>
                <NotificationBell
                  workspaceId={summary.id}
                  slug={summary.slug}
                  userId={me.data ? String(me.data.id) : undefined}
                  can={(p) => !!current.data?.can?.includes(p)}
                />
                <LiveAlerts workspaceId={summary.id} slug={summary.slug} />
              </>
            ) : null}
            <LanguageSwitcher />
            <ThemeToggle />
            {me.data ? (
              <UserMenu
                email={me.data.email}
                onReport={canReport ? () => openFeedback() : undefined}
              />
            ) : null}
          </div>
        </header>
        {/* MH-663: reserve room for the chat dock's launcher pill so it never
            sits over the last row of page content — the dock itself hides on
            the inbox route, so no padding is needed there either. */}
        <main className={`min-w-0 flex-1 ${pathname?.includes("/inbox") ? "" : "pb-16"}`}>
          {body}
        </main>
      </div>

      <Sheet open={navOpen} onOpenChange={setNavOpen}>
        <SheetContent side={isRtl ? "right" : "left"} className="w-72 gap-0 p-0" data-mobile-nav>
          <SheetTitle className="flex h-14 items-center border-b border-line px-4">
            <Brand />
          </SheetTitle>
          {summary ? (
            <ShellNav base={base} can={current.data?.can} onNavigate={() => setNavOpen(false)} />
          ) : null}
        </SheetContent>
      </Sheet>

      {me.data ? <MahaamFeedbackWidget /> : null}

      {/* Every workspace page except the inbox itself (ChatDock hides itself
          there — the inbox already IS the chat UI, MH-587/MH-663). Its own
          WorkspaceContext.Provider: this dock is a sibling of `body` above,
          not a descendant, so it needs the same context re-supplied —
          CannedPicker (reused inside ChatThread) calls useWorkspace() and
          throws outside one. */}
      {summary && current.data ? (
        <WorkspaceContext.Provider
          value={{
            summary,
            current: current.data,
            id: summary.id,
            slug: summary.slug,
            can: (p) => !!current.data?.can?.includes(p),
          }}
        >
          <ChatDock
            workspaceId={summary.id}
            slug={summary.slug}
            can={(p) => !!current.data?.can?.includes(p)}
          />
        </WorkspaceContext.Provider>
      ) : null}
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

/**
 * The workspace navigation (MH-526): the sticky sidebar on desktop, a slide-out
 * sheet below md. Two screens, not one long list —
 *
 *   ROOT: Overview, then every group button (CRM, Marketing, Inbox, Automation,
 *   Research, Insights, Notifications), then Members, then Settings.
 *
 *   SECTION: a back link to the workspace overview, the section's name, its
 *   dashboard, and only that section's pages.
 *
 * Every row is a real link and which screen shows comes from the URL alone
 * (MH-673): a group row opens the section's own page (its dashboard), and the
 * sidebar follows because that page — like every page inside it — belongs to
 * the section. There is no client-only "drilled" state to get out of step.
 */
function ShellNav({
  base,
  onNavigate,
  mini = false,
  can,
}: {
  base: string
  onNavigate?: () => void
  mini?: boolean
  can?: string[]
}) {
  const { t, locale } = useTranslations()
  const pathname = usePathname()
  const rest = pathname.startsWith(base) ? pathname.slice(base.length).replace(/^\//, "") : ""
  const entries = visibleEntries(can)
  const derivedGroup = groupForPath(rest)
  // A section this role cannot see does not take over the sidebar.
  const openGroup = entries.some((e) => e.type === "group" && e.group.key === derivedGroup?.key)
    ? derivedGroup
    : null

  if (openGroup) {
    const items = visibleItems(openGroup.items, can)
    return (
      <nav
        className="flex flex-col py-3"
        aria-label={t("shell.navigation")}
        data-nav-drilled={openGroup.key}
      >
        <Link
          href={base}
          onClick={onNavigate}
          title={mini ? t("nav.back") : undefined}
          data-nav-back
          className={cn(
            "flex items-center gap-2.5 border-s-2 border-transparent px-3 py-2 text-sm text-grid-muted transition-colors hover:bg-grid-soft hover:text-grid-fg [&_svg]:size-4 [&_svg]:shrink-0",
            mini && "justify-center px-0 py-2.5",
          )}
        >
          <ArrowLeftIcon className="rtl:rotate-180" />
          {mini ? <span className="sr-only">{t("nav.back")}</span> : t("nav.back")}
        </Link>
        {mini ? null : (
          <p className="grid-micro px-4 pt-2 pb-1 text-grid-fg">{t(openGroup.labelKey)}</p>
        )}
        <NavLink
          href={`${base}/${openGroup.home}`}
          icon={<LayoutDashboardIcon />}
          active={rest === openGroup.home}
          onNavigate={onNavigate}
          mini={mini}
          dataNav={`${openGroup.key}-home`}
        >
          {t("nav.sectionOverview")}
        </NavLink>
        {items.map((item) => {
          const Icon = item.icon
          return (
            <NavLink
              key={item.key}
              href={`${base}/${item.path}`}
              icon={<Icon />}
              active={itemActive(item, rest)}
              onNavigate={onNavigate}
              mini={mini}
              dataNav={item.key}
            >
              {t(item.labelKey)}
            </NavLink>
          )
        })}
      </nav>
    )
  }

  return (
    <nav className="flex flex-col py-3" aria-label={t("shell.navigation")} data-nav-drilled="">
      {entries.map((e) => {
        if (e.type === "link") {
          const Icon = e.item.icon
          return (
            <NavLink
              key={e.item.key}
              href={e.item.path ? `${base}/${e.item.path}` : base}
              icon={<Icon />}
              active={itemActive(e.item, rest)}
              onNavigate={onNavigate}
              mini={mini}
              dataNav={e.item.key}
            >
              {t(e.item.labelKey)}
            </NavLink>
          )
        }
        // Settings sits below a divider, apart from the workspace-level groups above it.
        // Its own row already reads "Settings" with the drill-down chevron, so the rule
        // needs no label of its own.
        const divider =
          e.group.key === "settings" ? (
            <div
              key="div"
              className={cn("border-t border-line", mini ? "mt-3 pt-3" : "mt-4 pt-2")}
            />
          ) : null
        return (
          <span key={e.group.key} className="contents">
            {divider}
            <NavGroupLink
              group={e.group}
              href={`${base}/${e.group.home}`}
              onNavigate={onNavigate}
              mini={mini}
            />
          </span>
        )
      })}
      {/* The public MCP reference (MH-428): every tool, its scope and an example.
          Not a workspace page (it needs no group or workspace base), so it sits
          here rather than in nav-config.ts's workspace-relative NAV table. */}
      <NavLink
        href={mcpDocsHref(locale)}
        icon={<BookOpenIcon />}
        active={pathname === mcpDocsHref(locale)}
        onNavigate={onNavigate}
        mini={mini}
        dataNav="mcp-docs"
      >
        {t("nav.mcpDocs")}
      </NavLink>
    </nav>
  )
}
