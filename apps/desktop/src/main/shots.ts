// MOHARRIK_SHOTS=<dir> (scripts/shots.mjs): signs in against the local demo stack, walks every
// screen in Arabic/light and English/dark, saves a PNG of the window for each (capturePage, so
// nothing else on the desktop is in the picture) and checks that a customer message arriving
// on the live stream raises a native notification and the unread count. Then the app quits.
import type { BrowserWindow } from "electron"
import { existsSync, mkdirSync, writeFileSync } from "node:fs"
import { join } from "node:path"

import type { Prefs, Route } from "../shared/types"
import { alertsShown } from "./live"
import * as session from "./session"
import { getUnread } from "./tray"

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))

export async function runShots(win: BrowserWindow, dir: string): Promise<void> {
  mkdirSync(dir, { recursive: true })
  const result: Record<string, unknown> = {}
  // Renderer errors land in the log, so a blank screen in a shot comes with its cause.
  win.webContents.on("console-message", (e) => {
    if (e.level === "error" || e.level === "warning") console.error(`[renderer ${e.level}]`, e.message, e.sourceId, e.lineNumber)
  })
  const js = <T>(code: string) => win.webContents.executeJavaScript(code, true) as Promise<T>
  const until = async (what: string, fn: () => boolean | Promise<boolean>, ms = 20_000) => {
    const end = Date.now() + ms
    while (!(await fn())) {
      if (Date.now() > end) throw new Error(`shots: timed out waiting for ${what}`)
      await sleep(200)
    }
  }
  let n = 0
  const shot = async (name: string, settle = 1500) => {
    await sleep(settle)
    const img = await win.webContents.capturePage()
    const file = join(dir, `desktop-${String(++n).padStart(2, "0")}-${name}.png`)
    writeFileSync(file, img.toPNG())
    console.log("[shots]", file)
  }
  const go = (r: Route) => win.webContents.send("navigate", r)
  const prefs = (p: Partial<Prefs>) => js(`window.moharrik.invoke("app:setPrefs", ${JSON.stringify(p)})`)

  try {
    if (win.webContents.isLoading()) await new Promise((r) => win.webContents.once("did-finish-load", () => r(undefined)))
    await prefs({ locale: "ar", theme: "light" })
    await until("the sign-in screen", () => js<boolean>(`!!document.querySelector("[data-signin]")`))
    await shot("signin")

    await js(`document.querySelector("[data-signin]").click()`)
    await until("sign-in", () => !!session.current())
    await until("the workspace list", () => js<boolean>(`!!document.querySelector("[data-workspace]")`))
    await shot("workspaces")

    await js(`(document.querySelector('[data-workspace="nile-studio"]') || document.querySelector("[data-workspace]")).click()`)
    await until("the inbox", () => js<boolean>(`!!document.querySelector("[data-conversation-row]")`))
    await shot("inbox")

    const a = session.getApi()!
    const convs = (await a.conversations()).conversations ?? []
    const ar = convs.find((c) => /[؀-ۿ]/.test(c.subject ?? "")) ?? convs[0]
    const en = convs.find((c) => /Arabic version/.test(c.subject ?? "")) ?? convs[0]
    if (ar) {
      go({ screen: "conversation", id: ar.id })
      await until("the thread", () => js<boolean>(`!!document.querySelector("[data-message]")`))
      await shot("conversation")
    }

    // A customer message arrives on the live stream: the notification, the badge and the list.
    const before = alertsShown()
    go({ screen: "inbox" })
    writeFileSync(process.env.MOHARRIK_PING || join(dir, ".ping"), "")
    await until("the live alert", () => alertsShown() > before, 30_000).catch((e) => (result.liveError = String(e)))
    result.alerts = alertsShown() - before
    result.unread = getUnread()
    await shot("inbox-live")

    go({ screen: "notifications" })
    await until("notifications", () => js<boolean>(`!!document.querySelector("[data-notification]")`))
    await shot("notifications")

    go({ screen: "contacts" })
    await until("contacts", () => js<boolean>(`!!document.querySelector("[data-contact-row]")`))
    await shot("contacts")
    const people = (await a.contacts()).contacts ?? []
    const ahmed = people.find((p) => p.email?.startsWith("ahmed@")) ?? people[0]
    if (ahmed) {
      go({ screen: "contact", id: ahmed.id })
      await until("the contact", () => js<boolean>(`!!document.querySelector("[data-contact-card]")`))
      await shot("contact")
    }
    go({ screen: "contacts", tab: "companies" })
    await until("companies", () => js<boolean>(`!!document.querySelector("[data-company-row]")`))
    await shot("companies")

    go({ screen: "deals" })
    await until("the pipeline", () => js<boolean>(`!!document.querySelector("[data-deal]")`))
    await shot("deals")

    go({ screen: "settings" })
    await until("settings", () => js<boolean>(`!!document.querySelector("[data-settings]")`))
    await shot("settings")

    await prefs({ locale: "en", theme: "dark" })
    go({ screen: "inbox" })
    await until("the inbox", () => js<boolean>(`!!document.querySelector("[data-conversation-row]")`))
    await shot("inbox-en-dark")
    if (en) {
      go({ screen: "conversation", id: en.id })
      await until("the thread", () => js<boolean>(`!!document.querySelector("[data-message]")`))
      await shot("conversation-en-dark")
    }
    go({ screen: "deals" })
    await until("the pipeline", () => js<boolean>(`!!document.querySelector("[data-deal]")`))
    await shot("deals-en-dark")
    if (ahmed) {
      go({ screen: "contact", id: ahmed.id })
      await until("the contact", () => js<boolean>(`!!document.querySelector("[data-contact-card]")`))
      await shot("contact-en-dark")
    }
    result.ok = true
  } catch (e) {
    result.ok = false
    result.error = String((e as Error)?.stack ?? e)
    console.error("[shots]", e)
    try {
      await shot("failure", 200)
    } catch {
      /* the window is gone */
    }
  } finally {
    try {
      writeFileSync(join(dir, "desktop-result.json"), JSON.stringify(result, null, 2))
    } catch (e) {
      console.error("[shots] result:", e)
    }
  }
}
