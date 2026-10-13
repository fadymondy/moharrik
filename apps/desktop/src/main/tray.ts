// The tray icon and the unread count: in the tray (a dot on the icon and the count in the tooltip
// and menu), on the taskbar button (Windows overlay icon) and on the Dock (macOS badge).
import { app, Menu, nativeImage, nativeTheme, Tray, type BrowserWindow } from "electron"

import { translator, type Locale } from "@moharrik/client"

import type { Unread } from "../shared/types"
import { encodePng, rasterMark } from "./png"

export interface TrayDeps {
  locale: () => Locale
  badge: () => boolean
  signedIn: () => boolean
  show: () => void
}

let tray: Tray | null = null
let deps: TrayDeps
let unread: Unread = { conversations: 0, notifications: 0 }

const total = () => unread.conversations + unread.notifications

/**
 * macOS: a template image (black + alpha) the system tints for the menu bar. Windows/Linux: the
 * brand colours for the taskbar's light or dark, with the gold cell, redrawn when that changes.
 */
function icon(dot: boolean): Electron.NativeImage {
  const mac = process.platform === "darwin"
  const dark = nativeTheme.shouldUseDarkColorsForSystemIntegratedUI
  const img = nativeImage.createEmpty()
  for (const scale of [1, 2]) {
    const size = (mac ? 18 : 16) * scale
    const px = mac
      ? rasterMark(size, { color: "#000000", accent: "#000000", dot: dot ? "#000000" : null })
      : rasterMark(size, { color: dark ? "#2EC4CB" : "#00A0A8", accent: "#C9A227", dot: dot ? "#D9455F" : null })
    img.addRepresentation({ scaleFactor: scale, width: size, height: size, buffer: encodePng(px, size, size) })
  }
  if (mac) img.setTemplateImage(true)
  return img
}

function build(): void {
  if (!tray) return
  const t = translator(deps.locale())
  const n = total()
  tray.setToolTip(n ? `${t("app.name")} — ${t("tray.unread", { n })}` : t("app.name"))
  tray.setImage(icon(deps.signedIn() && n > 0))
  tray.setContextMenu(
    Menu.buildFromTemplate([
      ...(deps.signedIn() && n ? [{ label: t("tray.unread", { n }), enabled: false }, { type: "separator" as const }] : []),
      { label: t("tray.open"), click: deps.show },
      { type: "separator" },
      { label: t("tray.quit"), role: "quit" },
    ]),
  )
}

export function createTray(d: TrayDeps): void {
  deps = d
  tray = new Tray(icon(false))
  tray.on("click", () => d.show())
  nativeTheme.on("updated", build)
  build()
}

/** The count on the app icon. Windows draws it from the window (renderer canvas, badge.ts there). */
export function setUnread(next: Unread, win: BrowserWindow | null): void {
  unread = next
  build()
  const n = deps.badge() && deps.signedIn() ? total() : 0
  if (process.platform === "darwin") app.dock?.setBadge(n ? (n > 99 ? "99+" : String(n)) : "")
  else if (process.platform === "linux") app.setBadgeCount(n)
  else if (win && !n) win.setOverlayIcon(null, "")
}

/** Windows: the overlay the window drew for the current count (a PNG data URL). */
export function setOverlay(win: BrowserWindow | null, dataUrl: string, label: string): void {
  if (process.platform !== "win32" || !win) return
  const n = deps.badge() && deps.signedIn() ? total() : 0
  win.setOverlayIcon(n && dataUrl ? nativeImage.createFromDataURL(dataUrl) : null, n ? label : "")
}

export const getUnread = () => unread
export const refreshTray = () => build()

