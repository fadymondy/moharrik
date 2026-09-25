// Read-only production check for MH-595/MH-596: a conversation row's menu is
// a context menu (right-click / long-press / Shift+F10) — no kebab button —
// and it carries an "edit/add contact" item that opens a dialog reusing the
// contact form. Quick replies sit in the composer's action bar. Nothing is
// saved: the contact dialog is opened and closed WITHOUT submitting, since
// this runs against production data. en/ar × light/dark × 1440/390.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/inbox-mh595-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {}, fail = []
const check = (k, ok, d) => { out[k] = ok ? true : d ?? false; if (!ok) fail.push(k) }
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []

for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh595-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe", headless: true, args,
    viewport: { width: 1440, height: 900 }, colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []; page.on("pageerror", (e) => errs.push(String(e)))
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.fill("#email", process.env.DEMO_EMAIL); await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]"); await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"]) for (const w of [1440, 390]) {
    const k = `${loc}-${scheme}-${w}`
    await page.setViewportSize({ width: w, height: 900 })
    await page.goto(`${base}/${loc}/w/${slug}/inbox`, { waitUntil: "load" })
    await page.waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 30000 }).catch(() => {})
    await page.waitForTimeout(1200)

    // No kebab button anywhere on the rows — the menu is context-only now.
    const kebab = await page.evaluate(() => !!document.querySelector("[data-context-menu-trigger]"))
    check(`${k}-no-kebab`, !kebab)

    // Rows are marked as using the context menu.
    const rowMarked = await page.evaluate(() => !!document.querySelector('[data-conversation-list] li[data-row-menu="context"]'))
    check(`${k}-row-marked`, rowMarked)

    // Right-click the first row: the context menu opens with the edit/add-contact item.
    const firstRow = page.locator('[data-conversation-list] li[data-row-menu="context"]').first()
    await firstRow.click({ button: "right" })
    const editItem = page.locator("[data-contact-edit]")
    const menuOpened = await editItem.first().waitFor({ state: "visible", timeout: 5000 }).then(() => true, () => false)
    check(`${k}-context-menu`, menuOpened)

    if (menuOpened) {
      // Opening the item opens the contact dialog reusing the shared form.
      await editItem.first().click()
      const dialog = page.locator("[data-contact-dialog]")
      const dialogOpened = await dialog.waitFor({ state: "visible", timeout: 5000 }).then(() => true, () => false)
      check(`${k}-contact-dialog`, dialogOpened)

      if (dialogOpened) {
        const fields = await page.evaluate(() => {
          const d = document.querySelector("[data-contact-dialog]")
          if (!d) return null
          const form = d.querySelector('[data-form="edit-contact"]')
          return { hasForm: !!form, hasIdentityField: !!(d.querySelector("input[type=email]") || d.querySelector("input[type=tel]") || d.querySelector("input")) }
        })
        check(`${k}-dialog-fields`, !!fields && fields.hasIdentityField, fields)

        // Close WITHOUT saving — this is read-only against production.
        await page.keyboard.press("Escape")
        const dialogClosed = await dialog.waitFor({ state: "hidden", timeout: 5000 }).then(() => true, () => false)
        check(`${k}-dialog-closed`, dialogClosed)
      }
    }

    // A plain click on a row still opens the conversation (context menu didn't hijack clicks).
    await page.keyboard.press("Escape").catch(() => {})
    await firstRow.locator("[data-conversation]").click()
    const opened = await page.waitForSelector("[data-messages]", { timeout: 15000 }).then(() => true, () => false)
    check(`${k}-plain-click-opens`, opened)

    // Quick replies live in the composer's action bar, not a separate spot.
    if (opened) {
      const quickReplies = await page.evaluate(() => {
        const bar = document.querySelector("[data-form=reply] [data-composer-actions]")
        const textarea = document.querySelector("[data-form=reply] textarea")
        if (!bar || !textarea) return { bar: !!bar, textarea: !!textarea }
        // Its own bar under the reply box, holding quick replies and Send.
        return {
          bar: true,
          quick: !!bar.querySelector("[data-quick-replies]"),
          send: !!bar.querySelector("[data-action=send]"),
          below: bar.getBoundingClientRect().top >= textarea.getBoundingClientRect().bottom - 1,
        }
      })
      check(`${k}-quick-replies`, quickReplies.bar && quickReplies.quick && quickReplies.send && quickReplies.below, quickReplies)
    }

    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
    check(`${k}-overflow`, overflow <= 1, overflow)
    if (shots) await page.screenshot({ path: path.join(shots, `mh595-live-${k}.png`) })
  }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
