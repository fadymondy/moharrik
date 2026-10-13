// Moharrik for Windows and macOS. The main process owns the session, the network and the live
// stream; the window (src/renderer) is sandboxed and talks to it only over the IPC channels in
// preload.ts. Closing the window keeps the app in the tray, so notifications and the unread count
// keep working; Quit is in the tray menu.
import { app, BrowserWindow, ipcMain, nativeTheme, shell } from "electron"
import { join } from "node:path"

import { ApiError, isRTL, targetOfUrl } from "@moharrik/client"

import type { ApiMethod, ApiResult, CurrentWorkspace, Prefs, Route, Target } from "../shared/types"
import { shotsDir } from "./config"
import { initLive, refreshUnread, startLive, stopLive } from "./live"
import * as session from "./session"
import { runShots } from "./shots"
import * as store from "./store"
import { createTray, refreshTray, setOverlay, setUnread } from "./tray"

if (process.env.MOHARRIK_USER_DATA) app.setPath("userData", process.env.MOHARRIK_USER_DATA)

// The calls the window may make, by name: reads and the inbox/CRM actions of the MVP. Account
// calls (signOut, devices) go through the session channels instead.
const API: ReadonlySet<ApiMethod> = new Set<ApiMethod>([
  "me",
  "workspaces",
  "conversations",
  "conversation",
  "messages",
  "reply",
  "updateConversation",
  "organise",
  "labels",
  "members",
  "bell",
  "notifications",
  "markNotification",
  "markAllNotificationsRead",
  "contacts",
  "contact",
  "timeline",
  "companies",
  "company",
  "deals",
  "stages",
  "updateDeal",
  "pushPrefs",
  "setPushPrefs",
])

let win: BrowserWindow | null = null
let quitting = false
let prefs: Prefs
/** The conversation open in the window, so its own messages don't pop a notification. */
let onScreen = ""
/** A link that arrived before the window could take it. */
let pendingRoute: Route | null = null

const devUrl = process.env.MOHARRIK_DEV_URL

function applyTheme(): void {
  nativeTheme.themeSource = prefs.theme
}

function createWindow(): BrowserWindow {
  const w = new BrowserWindow({
    width: 1240,
    height: 820,
    minWidth: 900,
    minHeight: 600,
    show: false,
    title: "Moharrik",
    backgroundColor: nativeTheme.shouldUseDarkColors ? "#0B1429" : "#F0EBE1",
    autoHideMenuBar: true,
    webPreferences: {
      preload: join(__dirname, "preload.cjs"),
      sandbox: true,
      contextIsolation: true,
      nodeIntegration: false,
      spellcheck: true,
    },
  })
  w.removeMenu()
  w.once("ready-to-show", () => {
    if (!shotsDir || process.env.MOHARRIK_SHOW === "1") w.show()
  })
  w.on("close", (e) => {
    if (quitting || shotsDir) return
    e.preventDefault()
    w.hide()
  })
  w.on("focus", () => void refreshUnread())
  w.webContents.on("did-finish-load", () => void refreshUnread())
  // Links in messages open in the browser, never inside the app.
  w.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:|^mailto:|^tel:/.test(url)) void shell.openExternal(url)
    return { action: "deny" }
  })
  w.webContents.on("will-navigate", (e, url) => {
    if (devUrl && url.startsWith(devUrl)) return
    e.preventDefault()
  })
  if (devUrl) void w.loadURL(devUrl)
  else void w.loadFile(join(__dirname, "renderer", "index.html"))
  return w
}

function show(): void {
  if (!win) win = createWindow()
  if (win.isMinimized()) win.restore()
  win.show()
  win.focus()
}

const send = (channel: string, ...args: unknown[]) => win?.webContents.send(channel, ...args)

/** Opens a target, switching to its workspace first when it is not the current one. */
async function open(t: Target | Route): Promise<void> {
  show()
  const ws = "workspaceId" in t || "slug" in t ? { id: (t as Target).workspaceId, slug: (t as Target).slug } : {}
  const cur = session.current()
  if (cur && (ws.id || ws.slug) && cur.workspace?.id !== ws.id && cur.workspace?.slug !== ws.slug) {
    try {
      const list = (await session.getApi()!.workspaces()).workspaces ?? []
      const next = list.find((w) => w.id === ws.id || w.slug === ws.slug)
      if (next) session.selectWorkspace(next as CurrentWorkspace)
    } catch {
      /* offline: open in the current workspace */
    }
  }
  if (win?.webContents.isLoading()) pendingRoute = t as Route
  else send("navigate", t)
}

function openUrl(url: string): void {
  const t = targetOfUrl(url)
  if (t) void open(t)
}

async function callApi(method: string, args: unknown[]): Promise<ApiResult> {
  const a = session.getApi()
  if (!a) return { ok: false, status: 401, message: "signed out" }
  if (!API.has(method as ApiMethod)) return { ok: false, status: 400, message: `unknown method ${method}` }
  try {
    const fn = a[method as ApiMethod] as (...x: unknown[]) => Promise<unknown>
    const data = await fn(...(Array.isArray(args) ? args : []))
    return { ok: true, data }
  } catch (e) {
    if (e instanceof ApiError) return { ok: false, status: e.status, message: e.message }
    return { ok: false, status: 0, message: String((e as Error)?.message ?? e) }
  }
}

function ipc(): void {
  ipcMain.handle("app:info", () => ({ version: app.getVersion(), platform: process.platform, rtl: isRTL(prefs.locale) }))
  ipcMain.handle("app:prefs", () => prefs)
  ipcMain.handle("app:setPrefs", (_e, p: Partial<Prefs>) => {
    prefs = { ...prefs, ...p }
    store.savePrefs(prefs)
    applyTheme()
    refreshTray()
    void refreshUnread()
    send("prefs", prefs)
    return prefs
  })
  ipcMain.handle("app:openExternal", (_e, url: string) => {
    if (typeof url === "string" && /^(https?:|mailto:|tel:)/.test(url)) return shell.openExternal(url)
  })
  ipcMain.handle("app:overlay", (_e, dataUrl: string, label: string) => setOverlay(win, String(dataUrl ?? ""), String(label ?? "")))
  ipcMain.handle("session:get", () => {
    const r = pendingRoute
    pendingRoute = null
    return { session: session.current(), server: session.getServer(), route: r }
  })
  ipcMain.handle("session:setServer", (_e, url: string) => session.setServer(String(url)))
  ipcMain.handle("session:signIn", async () => {
    try {
      return { ok: await session.signIn(prefs.locale, show) }
    } catch (e) {
      return { ok: false, message: String((e as Error)?.message ?? e) }
    }
  })
  ipcMain.handle("session:cancelSignIn", () => session.cancelSignIn())
  ipcMain.handle("session:signOut", () => session.signOut())
  ipcMain.handle("session:selectWorkspace", (_e, w: CurrentWorkspace | null) => session.selectWorkspace(w))
  ipcMain.handle("api", (_e, method: string, args: unknown[]) => callApi(method, args))
  ipcMain.handle("app:screen", (_e, conversationId: string) => {
    onScreen = String(conversationId ?? "")
  })
}

// One running app: a second launch (or a moharrik:// link on Windows) focuses the first.
if (!app.requestSingleInstanceLock()) app.quit()
else {
  app.on("second-instance", (_e, argv) => {
    const link = argv.find((a) => a.startsWith("moharrik://"))
    if (link) openUrl(link)
    else show()
  })
  app.on("open-url", (e, url) => {
    e.preventDefault()
    openUrl(url)
  })
  if (process.defaultApp && process.argv[1]) app.setAsDefaultProtocolClient("moharrik", process.execPath, [join(process.cwd(), process.argv[1])])
  else app.setAsDefaultProtocolClient("moharrik")

  app.whenReady().then(async () => {
    if (process.platform === "win32") app.setAppUserModelId("com.fadymondy.moharrik.desktop")
    prefs = store.loadPrefs()
    if (process.env.MOHARRIK_LOCALE === "ar" || process.env.MOHARRIK_LOCALE === "en") prefs.locale = process.env.MOHARRIK_LOCALE
    applyTheme()
    await session.restore()
    ipc()
    createTray({ locale: () => prefs.locale, badge: () => prefs.badge, signedIn: () => !!session.current(), show })
    initLive({
      notify: () => prefs.notify,
      sound: () => prefs.sound,
      locale: () => prefs.locale,
      showing: (id) => !!win?.isVisible() && win.isFocused() && onScreen === id,
      onEvent: (e) => send("live", e),
      onUnread: (u) => {
        setUnread(u, win)
        send("unread", u)
      },
      open: (t) => void open(t),
    })
    session.onSession((s, reason) => {
      send("session", s, reason)
      if (s?.workspace) startLive()
      else {
        stopLive()
        void refreshUnread()
      }
    })
    win = createWindow()
    if (session.current()?.workspace) startLive()
    const link = process.argv.find((a) => a.startsWith("moharrik://"))
    if (link) openUrl(link)
    if (shotsDir) void runShots(win, shotsDir).finally(() => {
      quitting = true
      app.quit()
    })
  })

  app.on("activate", show)
  app.on("before-quit", () => {
    quitting = true
    stopLive()
  })
  // The tray keeps the app alive with no window; only Quit ends it.
  app.on("window-all-closed", () => {})
}
