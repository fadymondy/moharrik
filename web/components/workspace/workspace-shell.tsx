"use client"

import { useEffect, useMemo, useState, type MouseEvent, type ReactNode } from "react"
import { useParams, usePathname, useRouter } from "next/navigation"
import {
  AppHeader,
  AppMain,
  AppShell,
  Button,
  CommandPalette,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DropdownMenuItem,
  ProductMark,
  SearchTrigger,
  Sidebar,
  SidebarContent,
  SidebarExpandedOnly,
  SidebarFooter,
  SidebarGroup,
  SidebarHeader,
  SidebarItem,
  SidebarSubItem,
  SidebarTrigger,
  Skeleton,
  UserMenu,
  WorkspaceSwitcher,
  toast,
  useOptionalAppShell,
  useRegisterCommands,
  type Command,
  type Workspace,
} from "@fadymondy/nasaq/web"
import { BookOpenIcon, MessageSquarePlusIcon, Settings2Icon } from "lucide-react"
import { useSWRConfig } from "swr"

import { LiveAlerts } from "@/components/alerts/live-alerts"
import { NotificationBell } from "@/components/alerts/notification-bell"
import { ChatDock } from "@/components/inbox/chat-dock"
import {
  MahaamFeedbackWidget,
  openFeedback,
  useFeedbackReady,
} from "@/components/feedback/mahaam-widget"
import { NotFoundState } from "@/components/not-found-state"
import { ErrorState } from "@/components/states"
import { CreateWorkspaceForm } from "@/components/workspace/create-workspace-form"
import { WorkspaceContext } from "@/components/workspace/workspace-context"
import { api, resetCsrf, type WorkspaceSummary } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { itemActive, visibleEntries, visibleItems } from "@/lib/nav-config"
import {
  FOOTER_NAV,
  TOP_NAV,
  allTopPages,
  topForPath,
  visibleTop,
  type TopItem,
} from "@/lib/nav-top"
import { useCurrentWorkspace, useMyWorkspaces } from "@/lib/queries"
import { useRequireAuth } from "@/lib/use-require-auth"
import { rememberWorkspace } from "@/lib/last-workspace"
import { mcpDocsHref } from "@/lib/mcp-docs"

/**
 * SidebarItem is a native <a>: plain clicks route client-side, modified clicks keep the browser's
 * behaviour. Below md the sidebar is a sheet over the page, so a plain click also closes it (MH-1073).
 */
function useNavClick() {
  const router = useRouter()
  const shell = useOptionalAppShell()
  return (href: string) => (e: MouseEvent<HTMLAnchorElement>) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey)
      return
    e.preventDefault()
    shell?.setMobileOpen(false)
    router.push(href)
  }
}

function Brand() {
  const { t, locale } = useTranslations()
  const go = useNavClick()
  const href = `/${locale}/workspaces`
  return (
    <a href={href} onClick={go(href)} className="flex items-center gap-2.5 px-1 py-1">
      <ProductMark brand="moharrik" size={24} />
      <span className="text-body font-medium">{t("brand.name")}</span>
    </a>
  )
}

function Switcher({ active, all }: { active: WorkspaceSummary; all: WorkspaceSummary[] }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const pathname = usePathname()
  const [creating, setCreating] = useState(false)

  const workspaces = useMemo<Workspace[]>(
    () =>
      all.map((w) => ({
        id: w.id,
        name: w.name,
        description: t(`role.${w.role}`),
        logoSrc: w.avatar_url ?? undefined,
      })),
    [all, t],
  )

  // Switching keeps the section (members, MCP) when it exists in every workspace.
  function hrefFor(slug: string) {
    const prefix = `/${locale}/w/${active.slug}`
    const rest = pathname.startsWith(prefix) ? pathname.slice(prefix.length) : ""
    return `/${locale}/w/${slug}${rest}`
  }

  return (
    <>
      <WorkspaceSwitcher
        workspaces={workspaces}
        value={active.id}
        onValueChange={(id) => {
          const w = all.find((x) => x.id === id)
          if (w) router.push(hrefFor(w.slug))
        }}
        onCreate={() => setCreating(true)}
        labels={{ heading: t("shell.workspaces"), create: t("workspaces.create") }}
      >
        <DropdownMenuItem
          onClick={() => router.push(`/${locale}/workspaces`)}
          data-manage-workspaces
        >
          <Settings2Icon />
          {t("workspaces.manageAll")}
        </DropdownMenuItem>
      </WorkspaceSwitcher>
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

function Account({ email, onReport }: { email: string; onReport?: () => void }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const { mutate } = useSWRConfig()

  async function signOut() {
    try {
      await api("/api/auth/logout", { method: "POST" })
    } catch {
      toast.error(t("common.networkError"))
      return
    }
    resetCsrf()
    // Drop every cached response: the next account must not see this one's data.
    await mutate(() => true, undefined, { revalidate: false })
    router.replace(`/${locale}/login`)
  }

  return (
    <UserMenu user={{ name: email.split("@")[0] || email, email }} onSignOut={signOut}>
      {onReport ? (
        <DropdownMenuItem onClick={onReport}>
          <MessageSquarePlusIcon />
          {t("feedback.report")}
        </DropdownMenuItem>
      ) : null}
    </UserMenu>
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

  const summary = list.data?.find((w) => w.slug === slug)
  const current = useCurrentWorkspace(summary?.id)
  // MH-1108: the next sign-in opens this workspace again.
  useEffect(() => {
    if (summary) rememberWorkspace(summary.slug)
  }, [summary])
  const base = `/${locale}/w/${slug}`
  const can = current.data?.can

  const ctx = summary
    ? {
        summary,
        current: current.data,
        id: summary.id,
        slug: summary.slug,
        can: (p: string) => !!current.data?.can?.includes(p),
      }
    : null

  let body: ReactNode
  if (me.error || list.error) body = <ErrorState error={me.error ?? list.error} />
  else if (!me.data || !list.data) body = <ShellSkeleton />
  else if (!summary || !ctx)
    body = (
      <NotFoundState
        title={t("shell.unknownWorkspace")}
        body={t("shell.unknownWorkspaceBody", { slug })}
        backHref={`/${locale}/workspaces`}
        backLabel={t("shell.allWorkspaces")}
      />
    )
  else if (current.error) body = <ErrorState error={current.error} />
  else body = <WorkspaceContext.Provider value={ctx}>{children}</WorkspaceContext.Provider>

  const sidebar = (
    <Sidebar aria-label={t("shell.navigation")}>
      <SidebarHeader>
        <Brand />
        {summary && list.data ? (
          <Switcher active={summary} all={list.data} />
        ) : (
          <Skeleton className="h-9 w-full" />
        )}
        <SearchTrigger label={t("shell.search")} />
      </SidebarHeader>
      <SidebarContent>{summary ? <ShellNav base={base} can={can} /> : null}</SidebarContent>
      <SidebarFooter>
        {summary ? <ShellFooterNav base={base} can={can} /> : null}
        {me.data ? (
          <Account email={me.data.email} onReport={canReport ? () => openFeedback() : undefined} />
        ) : null}
      </SidebarFooter>
    </Sidebar>
  )

  return (
    <AppShell sidebar={sidebar} resizeLabel={t("shell.resizeSidebar")}>
      <AppHeader>
        <SidebarTrigger label={t("shell.toggleSidebar")} data-nav-toggle />
        <div className="ms-auto flex shrink-0 items-center gap-1">
          {me.data && canReport ? (
            <Button
              variant="secondary"
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
        </div>
      </AppHeader>
      {/* MH-663: reserve room for the chat dock's launcher pill so it never
          sits over the last row of page content — the dock itself hides on
          the inbox route, so no padding is needed there either. */}
      <AppMain className={pathname?.includes("/inbox") ? "p-0" : "p-0 pb-16"}>{body}</AppMain>

      {summary ? <NavCommands base={base} can={can} /> : null}
      <CommandPalette />

      {me.data ? <MahaamFeedbackWidget /> : null}

      {/* Every workspace page except the inbox itself (ChatDock hides itself
          there — the inbox already IS the chat UI, MH-587/MH-663). Its own
          WorkspaceContext.Provider: this dock is a sibling of `body` above,
          not a descendant, so it needs the same context re-supplied —
          CannedPicker (reused inside ChatThread) calls useWorkspace() and
          throws outside one. */}
      {summary && current.data && ctx ? (
        <WorkspaceContext.Provider value={ctx}>
          <ChatDock workspaceId={summary.id} slug={summary.slug} can={ctx.can} />
        </WorkspaceContext.Provider>
      ) : null}
    </AppShell>
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

/** Every page in the sidebar, as navigation commands in the palette (Ctrl/Cmd+K). */
function NavCommands({ base, can }: { base: string; can?: string[] }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const commands = useMemo<Command[]>(() => {
    const out: Command[] = []
    for (const e of visibleEntries(can)) {
      if (e.type === "link") {
        out.push({
          id: `nav.${e.item.key}`,
          section: "navigation",
          label: t(e.item.labelKey),
          icon: e.item.icon,
          perform: () => router.push(e.item.path ? `${base}/${e.item.path}` : base),
        })
        continue
      }
      for (const item of visibleItems(e.group.items, can)) {
        out.push({
          id: `nav.${item.key}`,
          section: "navigation",
          label: t(item.labelKey),
          hint: t(e.group.labelKey),
          icon: item.icon,
          perform: () => router.push(`${base}/${item.path}`),
        })
      }
    }
    // Pages only the intent sidebar names (a section's own dashboard, Import customers, the browser
    // extension): registered once, deduplicated by path against the loop above.
    const seen = new Set<string>()
    for (const e of visibleEntries(can)) {
      if (e.type === "link") seen.add(e.item.path)
      else for (const item of visibleItems(e.group.items, can)) seen.add(item.path)
    }
    const extras: {
      id: string
      label: string
      hint?: string
      path: string
      icon: Command["icon"]
    }[] = []
    for (const sec of [...TOP_NAV.flatMap((s) => s.items), ...FOOTER_NAV]) {
      if (sec.can && !can?.includes(sec.can)) continue
      extras.push({
        id: `nav.top.${sec.key}`,
        label: t(sec.labelKey),
        path: sec.path,
        icon: sec.icon,
      })
    }
    for (const { item, parent } of allTopPages()) {
      if (item.can && !can?.includes(item.can)) continue
      extras.push({
        id: `nav.top.${parent.key}.${item.key}`,
        label: t(item.labelKey),
        hint: t(parent.labelKey),
        path: item.path,
        icon: item.icon,
      })
    }
    for (const x of extras) {
      if (seen.has(x.path)) continue
      seen.add(x.path)
      out.push({
        id: x.id,
        section: "navigation",
        label: x.label,
        hint: x.hint,
        icon: x.icon,
        perform: () => router.push(x.path ? `${base}/${x.path}` : base),
      })
    }
    out.push({
      id: "nav.mcp-docs",
      section: "navigation",
      label: t("nav.mcpDocs"),
      icon: BookOpenIcon,
      perform: () => router.push(mcpDocsHref(locale)),
    })
    return out
  }, [base, can, locale, router, t])
  useRegisterCommands(commands)
  return null
}

/** Where we are inside the workspace, and which sidebar item owns it. */
function useNavState(base: string) {
  const pathname = usePathname()
  const rest = pathname.startsWith(base) ? pathname.slice(base.length).replace(/^\//, "") : ""
  return { pathname, rest, top: topForPath(rest) }
}

/**
 * One sidebar row. The item that owns the current URL expands its sub-pages beneath it (Customers
 * shows Companies, Leads, Import…); every other row stays a single line.
 */
function TopRow({
  item,
  base,
  rest,
  active,
  can,
  nested,
}: {
  item: TopItem
  base: string
  rest: string
  active: boolean
  can?: string[]
  nested: boolean
}) {
  const { t } = useTranslations()
  const go = useNavClick()
  const Icon = item.icon
  const href = item.path ? `${base}/${item.path}` : base
  const subs = nested && active && item.subs ? visibleItems(item.subs, can) : []
  return (
    <>
      <SidebarItem
        href={href}
        onClick={go(href)}
        icon={<Icon />}
        active={active && !subs.some((s) => itemActive(s, rest))}
        aria-current={active ? "page" : undefined}
        data-nav-item={item.key}
      >
        {t(item.labelKey)}
      </SidebarItem>
      {subs.length > 0 ? (
        <SidebarExpandedOnly>
          <div
            className="ms-5 flex flex-col gap-0.5 border-s border-border ps-2"
            data-nav-subs={item.key}
          >
            {subs.map((s) => {
              const subHref = `${base}/${s.path}`
              return (
                <SidebarSubItem
                  key={s.key}
                  href={subHref}
                  onClick={go(subHref)}
                  active={itemActive(s, rest)}
                  data-nav-item={s.key}
                >
                  {t(s.labelKey)}
                </SidebarSubItem>
              )
            })}
          </div>
        </SidebarExpandedOnly>
      ) : null}
    </>
  )
}

/**
 * The workspace navigation (MH-1060), inside Nasaq's Sidebar (a sheet below md, an icon rail when
 * collapsed): one flat list grouped by intent: Home, Customers, Inbox, Opportunities, Research;
 * AUTOMATE; UNDERSTAND; CONNECT. Team and Settings sit in the footer (ShellFooterNav).
 *
 * Every row is a real link and which one is active comes from the URL alone, by the longest
 * matching prefix (nav-top.ts): /deals/<id> lights up Opportunities, /settings/mcp lights up MCP,
 * not Settings. The active row opens its sub-pages beneath it, so each page of a section
 * stays one click away without a second screen to get lost in.
 */
function ShellNav({ base, can }: { base: string; can?: string[] }) {
  const { t, locale } = useTranslations()
  const go = useNavClick()
  const { pathname, rest, top } = useNavState(base)
  return (
    <>
      {TOP_NAV.map((section) => {
        const items = visibleTop(section.items, can)
        if (items.length === 0) return null
        return (
          <SidebarGroup
            key={section.key}
            data-nav-section={section.key}
            label={section.labelKey ? t(section.labelKey) : undefined}
          >
            {items.map((item) => (
              <TopRow
                key={item.key}
                item={item}
                base={base}
                rest={rest}
                active={top?.key === item.key}
                can={can}
                nested
              />
            ))}
          </SidebarGroup>
        )
      })}
      {/* The public MCP reference (MH-428): every tool, its scope and an example.
          Not a workspace page (it needs no workspace base), so it sits here rather
          than in the workspace-relative tables of nav-config.ts / nav-top.ts. */}
      <SidebarGroup data-nav-section="docs">
        <SidebarItem
          href={mcpDocsHref(locale)}
          onClick={go(mcpDocsHref(locale))}
          icon={<BookOpenIcon />}
          active={pathname === mcpDocsHref(locale)}
          data-nav-item="mcp-docs"
        >
          {t("nav.mcpDocs")}
        </SidebarItem>
      </SidebarGroup>
    </>
  )
}

/** Team and Settings: pinned above the user menu. Their sub-pages are cards on the Settings page. */
function ShellFooterNav({ base, can }: { base: string; can?: string[] }) {
  const { rest, top } = useNavState(base)
  return (
    <SidebarGroup data-nav-section="footer">
      {visibleTop(FOOTER_NAV, can).map((item) => (
        <TopRow
          key={item.key}
          item={item}
          base={base}
          rest={rest}
          active={top?.key === item.key}
          can={can}
          nested={false}
        />
      ))}
    </SidebarGroup>
  )
}
