"use client"

import Link from "next/link"
import { useEffect, useState, type ReactNode } from "react"
import { useParams, usePathname, useRouter } from "next/navigation"
import {
  BellIcon,
  BellRingIcon,
  ChartLineIcon,
  BotIcon,
  WorkflowIcon,
  ClipboardListIcon,
  Building2Icon,
  CheckIcon,
  ChevronsUpDownIcon,
  HandshakeIcon,
  InboxIcon,
  KeyRoundIcon,
  ScrollTextIcon,
  LayoutGridIcon,
  MenuIcon,
  PanelLeftCloseIcon,
  PanelLeftOpenIcon,
  MailIcon,
  MessageSquarePlusIcon,
  PlusIcon,
  SearchCheckIcon,
  SearchIcon,
  SendIcon,
  PlugIcon,
  ServerIcon,
  WebhookIcon,
  TrendingUpIcon,
  UserPlusIcon,
  UsersIcon,
  YoutubeIcon,
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
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet"
import { LiveAlerts } from "@/components/alerts/live-alerts"
import { NotificationBell } from "@/components/alerts/notification-bell"
import { CubeMark } from "@/components/brand/cube-mark"
import { LanguageSwitcher, ThemeToggle } from "@/components/header-controls"
import { MahaamFeedbackWidget, openFeedback, useFeedbackReady } from "@/components/feedback/mahaam-widget"
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
            <Button variant="ghost" className="h-9 max-w-[16rem] min-w-0 shrink justify-between gap-2 px-2.5" aria-label={t("shell.switchWorkspace")} />
          }
        >
          <span className="flex size-6 shrink-0 items-center justify-center border border-line bg-grid-card text-xs font-medium text-grid-brand">
            {active.name.slice(0, 1).toUpperCase()}
          </span>
          <BdiBlock className="truncate text-sm font-medium">{active.name}</BdiBlock>
          <ChevronsUpDownIcon className="text-grid-muted" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="min-w-64">
          <DropdownMenuGroup>
            <DropdownMenuLabel>{t("shell.workspaces")}</DropdownMenuLabel>
            {all.map((w) => (
              <DropdownMenuItem key={w.id} onClick={() => router.push(hrefFor(w.slug))}>
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

function NavLink({ href, icon, children, exact = false, except, onNavigate, mini = false }: { href: string; icon: ReactNode; children: ReactNode; exact?: boolean; except?: string; onNavigate?: () => void; mini?: boolean }) {
  const pathname = usePathname()
  // except: a sub-section with its own entry, so only that entry lights up there.
  const inside = (p: string) => pathname === p || pathname.startsWith(p + "/")
  const active = (exact ? pathname === href : inside(href)) && !(except && inside(except))
  return (
    <Link
      href={href}
      onClick={onNavigate}
      aria-current={active ? "page" : undefined}
      title={mini && typeof children === "string" ? children : undefined}
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

export function WorkspaceShell({ children }: { children: ReactNode }) {
  const { slug } = useParams<{ slug: string }>()
  const { t, locale } = useTranslations()
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
          <div className={cn("flex h-14 shrink-0 items-center border-b border-line px-4", mini && "justify-center px-0")}>
            <Brand mini={mini} />
          </div>
          {summary ? <ShellNav base={base} mini={mini} can={current.data?.can} /> : null}
        </aside>
      </div>

      <div className="flex min-w-0 flex-col">
        <header className="flex h-14 items-center justify-between gap-2 border-b border-line px-3 sm:px-4">
          <div className="flex min-w-0 items-center gap-1">
            {summary ? (
              <Button variant="ghost" size="icon-sm" className="md:hidden" aria-label={t("shell.navigation")} onClick={() => setNavOpen(true)}>
                <MenuIcon />
              </Button>
            ) : null}
            <Button variant="ghost" size="icon-sm" className="hidden md:inline-flex" aria-label={t("shell.toggleSidebar")} aria-pressed={mini} title={t("shell.toggleSidebar")} onClick={toggleMini} data-nav-toggle>
              {mini ? <PanelLeftOpenIcon className="rtl:-scale-x-100" /> : <PanelLeftCloseIcon className="rtl:-scale-x-100" />}
            </Button>
            {summary && list.data ? <WorkspaceSwitcher active={summary} all={list.data} /> : <Skeleton className="h-6 w-40" />}
          </div>
          <div className="flex shrink-0 items-center gap-1">
            {me.data && canReport ? (
              <Button variant="outline" size="sm" onClick={() => openFeedback()} aria-label={t("feedback.report")}>
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
            {me.data ? <UserMenu email={me.data.email} onReport={canReport ? () => openFeedback() : undefined} /> : null}
          </div>
        </header>
        <main className="min-w-0 flex-1">{body}</main>
      </div>

      <Sheet open={navOpen} onOpenChange={setNavOpen}>
        <SheetContent side={isRtl ? "right" : "left"} className="w-72 gap-0 p-0" data-mobile-nav>
          <SheetTitle className="flex h-14 items-center border-b border-line px-4">
            <Brand />
          </SheetTitle>
          {summary ? <ShellNav base={base} can={current.data?.can} onNavigate={() => setNavOpen(false)} /> : null}
        </SheetContent>
      </Sheet>

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

/** The workspace navigation: the sticky sidebar on desktop, a slide-out sheet below md. */
function ShellNav({ base, onNavigate, mini = false, can }: { base: string; onNavigate?: () => void; mini?: boolean; can?: string[] }) {
  const { t } = useTranslations()
  const L = (props: { href: string; icon: ReactNode; children: ReactNode; exact?: boolean; except?: string }) => <NavLink {...props} onNavigate={onNavigate} mini={mini} />
  return (
    <nav className="flex flex-col py-3" aria-label={t("shell.navigation")}>
      <L href={base} exact icon={<LayoutGridIcon />}>
        {t("nav.overview")}
      </L>
      <L href={`${base}/leads`} icon={<UserPlusIcon />}>
        {t("nav.leads")}
      </L>
      <L href={`${base}/companies`} icon={<Building2Icon />}>
        {t("nav.companies")}
      </L>
      <L href={`${base}/contacts`} icon={<MailIcon />}>
        {t("nav.contacts")}
      </L>
      <L href={`${base}/deals`} icon={<HandshakeIcon />}>
        {t("nav.deals")}
      </L>
      <L href={`${base}/campaigns`} icon={<SendIcon />}>
        {t("nav.campaigns")}
      </L>
      <L href={`${base}/subscribe-forms`} icon={<ClipboardListIcon />}>
        {t("nav.subscribeForms")}
      </L>
      <L href={`${base}/inbox`} icon={<InboxIcon />}>
        {t("nav.inbox")}
      </L>
      <L href={`${base}/automation`} except={`${base}/automation/workflows`} icon={<BotIcon />}>
        {t("nav.automation")}
      </L>
      {/* The workflow canvas (MH-480): one click from anywhere. */}
      <L href={`${base}/automation/workflows`} icon={<WorkflowIcon />}>
        {t("nav.workflows")}
      </L>
      <L href={`${base}/trends`} exact icon={<TrendingUpIcon />}>
        {t("nav.trends")}
      </L>
      {/* The Google data pages (MH-444..442): hidden, not disabled, for a role that cannot read them. */}
      {can?.includes("analytics:read") ? (
        <L href={`${base}/analytics`} icon={<ChartLineIcon />}>
          {t("nav.analytics")}
        </L>
      ) : null}
      {can?.includes("analytics:read") ? (
        <L href={`${base}/search-console`} icon={<SearchIcon />}>
          {t("nav.searchConsole")}
        </L>
      ) : null}
      {can?.includes("analytics:read") ? (
        <L href={`${base}/youtube`} icon={<YoutubeIcon />}>
          {t("nav.youtube")}
        </L>
      ) : null}
      {/* Hidden, not disabled, for roles that cannot read runs: the API would only answer 403. */}
      {can?.includes("research:read") ? (
        <L href={`${base}/research`} icon={<SearchCheckIcon />}>
          {t("nav.research")}
        </L>
      ) : null}
      <L href={`${base}/notifications`} exact icon={<BellIcon />}>
        {t("nav.notifications")}
      </L>
      <L href={`${base}/members`} icon={<UsersIcon />}>
        {t("nav.members")}
      </L>
      {mini ? <div className="mt-3 border-t border-line pt-3" /> : <p className="grid-micro mt-4 border-t border-line px-4 pt-4 pb-1">{t("nav.settings")}</p>}
      <L href={`${base}/settings/email`} icon={<ServerIcon />}>
        {t("nav.email")}
      </L>
      <L href={`${base}/settings/integrations`} icon={<PlugIcon />}>
        {t("nav.integrations")}
      </L>
      <L href={`${base}/notifications/sources`} icon={<WebhookIcon />}>
        {t("notifications.sources")}
      </L>
      <L href={`${base}/settings/mcp`} icon={<KeyRoundIcon />}>
        {t("nav.mcpTokens")}
      </L>
      {/* Owners and admins only (MH-392): hidden, not disabled, for everyone else. */}
      {can?.includes("audit:read") ? (
        <L href={`${base}/settings/audit`} icon={<ScrollTextIcon />}>
          {t("nav.audit")}
        </L>
      ) : null}
      {can?.includes("push:manage") ? (
        <L href={`${base}/settings/delivery`} icon={<BellRingIcon />}>
          {t("nav.delivery")}
        </L>
      ) : null}
    </nav>
  )
}
