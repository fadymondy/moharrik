// The window: signed out → sign-in; no workspace → the picker; otherwise the sidebar and a screen.
// Navigation is a route in state (shared/types Route), which notifications and moharrik:// links
// set from the main process ("navigate").
import { useQueryClient } from "@tanstack/react-query"
import { Bell, Handshake, Inbox, Settings as SettingsIcon, Users } from "lucide-react"
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react"

import type { LiveOut, Route, SessionOut, Unread } from "../shared/types"
import { invoke, on } from "./bridge"
import { usePrefs } from "./prefs"
import { Avatar, Count, Mark, Toaster } from "./ui"
import { CompanyScreen } from "./views/company"
import { ContactScreen } from "./views/contact"
import { ContactsScreen } from "./views/contacts"
import { DealsScreen } from "./views/deals"
import { InboxScreen } from "./views/inbox"
import { NotificationsScreen } from "./views/notifications"
import { SettingsScreen } from "./views/settings"
import { SignIn } from "./views/sign-in"
import { Workspaces } from "./views/workspaces"

interface SessionCtx {
  session: SessionOut
  server: string
  ws: string
}
const Session = createContext<SessionCtx | null>(null)
export function useSession(): SessionCtx {
  const s = useContext(Session)
  if (!s) throw new Error("useSession outside a signed-in window")
  return s
}

interface NavCtx {
  route: Route
  go: (r: Route) => void
  back: () => void
}
const Nav = createContext<NavCtx | null>(null)
export function useNav(): NavCtx {
  const n = useContext(Nav)
  if (!n) throw new Error("useNav outside the app")
  return n
}

export function App({ initial }: { initial: { session: SessionOut | null; server: string; route: Route | null } }) {
  const [session, setSession] = useState(initial.session)
  const [server, setServer] = useState(initial.server)
  const [picking, setPicking] = useState(false)
  const [stack, setStack] = useState<Route[]>([initial.route ?? { screen: "inbox" }])
  const route = stack[stack.length - 1]!
  const qc = useQueryClient()

  useEffect(
    () =>
      on<[SessionOut | null, string]>("session", (s, reason) => {
        setSession(s)
        if (s?.server) setServer(s.server)
        if (reason === "workspace") {
          qc.clear()
          setPicking(false)
        }
        if (!s) qc.clear()
      }),
    [qc],
  )
  const go = useCallback((r: Route) => {
    setPicking(false)
    setStack((s) => {
      const top = s[s.length - 1]
      // Sidebar screens reset the stack; detail screens push onto it.
      const root = r.screen === "inbox" || r.screen === "notifications" || r.screen === "contacts" || r.screen === "deals" || r.screen === "settings"
      if (top && JSON.stringify(top) === JSON.stringify(r)) return s
      if (root) return [r]
      // From one conversation to another stays one step deep.
      if (r.screen === "conversation" && top?.screen === "conversation") return [...s.slice(0, -1), r]
      return [...s, r].slice(-20)
    })
  }, [])
  const back = useCallback(() => setStack((s) => (s.length > 1 ? s.slice(0, -1) : [{ screen: "inbox" }])), [])
  useEffect(() => on<[Route]>("navigate", go), [go])

  // A live event refreshes the lists it touches; "resync" (after a reconnect) refreshes all.
  useEffect(
    () =>
      on<[LiveOut]>("live", (e) => {
        if (e.kind === "resync") return void qc.invalidateQueries()
        if (e.kind === "notification") return void qc.invalidateQueries({ queryKey: ["notifications"] })
        void qc.invalidateQueries({ queryKey: ["conversations"] })
        if (e.conversation_id) void qc.invalidateQueries({ queryKey: ["conversation", e.conversation_id] })
      }),
    [qc],
  )

  const nav = useMemo(() => ({ route, go, back }), [route, go, back])

  if (!session) return <SignIn server={server} onServer={setServer} />
  if (!session.workspace || picking) return <Workspaces current={session.workspace?.id} onCancel={session.workspace ? () => setPicking(false) : undefined} />

  return (
    <Session.Provider value={{ session, server, ws: session.workspace.id }}>
      <Nav.Provider value={nav}>
        <Shell onSwitch={() => setPicking(true)}>
          <Screen route={route} />
        </Shell>
        <Toaster />
      </Nav.Provider>
    </Session.Provider>
  )
}

function Screen({ route }: { route: Route }) {
  switch (route.screen) {
    case "inbox":
    case "conversation":
      return <InboxScreen openId={route.screen === "conversation" ? route.id : undefined} />
    case "notifications":
      return <NotificationsScreen focus={route.itemId} />
    case "contacts":
      return <ContactsScreen tab={route.tab ?? "contacts"} />
    case "contact":
      return <ContactScreen id={route.id} />
    case "company":
      return <CompanyScreen id={route.id} />
    case "deals":
      return <DealsScreen />
    case "settings":
      return <SettingsScreen />
  }
}

function Shell({ children, onSwitch }: { children: ReactNode; onSwitch: () => void }) {
  const { t, prefs } = usePrefs()
  const { session } = useSession()
  const { route, go } = useNav()
  const unread = useUnread(prefs.badge)
  const ws = session.workspace!
  const item = (screen: Route["screen"], label: string, icon: ReactNode, n = 0, also: Route["screen"][] = []) => {
    const on = route.screen === screen || also.includes(route.screen)
    return (
      <button className={`nav${on ? " on" : ""}`} aria-current={on ? "page" : undefined} onClick={() => go({ screen } as Route)}>
        {icon}
        <span>{label}</span>
        <Count n={n} />
      </button>
    )
  }
  return (
    <div className="shell">
      <nav className="side">
        <div className="brand">
          <Mark size={30} />
          <b>{t("app.name")}</b>
        </div>
        <button className="ws" onClick={onSwitch} title={t("settings.switch")} data-ws={ws.slug}>
          <Avatar name={ws.name} url={ws.avatar_url} size={30} />
          <span style={{ minWidth: 0 }}>
            <div className="m ellipsis">{ws.name}</div>
            <div className="xs muted ellipsis">{t("settings.switch")}</div>
          </span>
        </button>
        {item("inbox", t("tabs.inbox"), <Inbox size={18} />, unread.conversations, ["conversation"])}
        {item("notifications", t("tabs.notifications"), <Bell size={18} />, unread.notifications)}
        {item("contacts", t("tabs.contacts"), <Users size={18} />, 0, ["contact", "company"])}
        {item("deals", t("tabs.deals"), <Handshake size={18} />)}
        <div className="grow" />
        {item("settings", t("tabs.settings"), <SettingsIcon size={18} />)}
        <div className="xs muted ellipsis" style={{ padding: "6px 10px 0" }} title={session.email}>
          {session.email}
        </div>
      </nav>
      <main className="main">{children}</main>
    </div>
  )
}

/** The unread counts the main process keeps (tray, Dock); on Windows the window draws the overlay. */
function useUnread(badge: boolean): Unread {
  const { t } = usePrefs()
  const [u, setU] = useState<Unread>({ conversations: 0, notifications: 0 })
  useEffect(() => on<[Unread]>("unread", setU), [])
  const total = u.conversations + u.notifications
  useEffect(() => {
    if (window.moharrik.platform !== "win32") return
    void invoke("app:overlay", badge && total ? overlay(total) : "", t("tray.unread", { n: total }))
  }, [total, badge, t])
  return u
}

/** A 32px red disc with the count, for the Windows taskbar button. */
function overlay(n: number): string {
  const c = document.createElement("canvas")
  c.width = c.height = 32
  const g = c.getContext("2d")!
  g.fillStyle = "#D9455F"
  g.beginPath()
  g.arc(16, 16, 16, 0, Math.PI * 2)
  g.fill()
  g.fillStyle = "#fff"
  const text = n > 99 ? "99+" : String(n)
  g.font = `bold ${text.length > 2 ? 13 : text.length > 1 ? 17 : 20}px "Segoe UI", sans-serif`
  g.textAlign = "center"
  g.textBaseline = "middle"
  g.fillText(text, 16, 17)
  return c.toDataURL("image/png")
}
