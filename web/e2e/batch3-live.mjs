// Read-only live check for the MH-586/585/514/588 deploy. Loads pages and reads the DOM only:
// no import, no ask, no connect, no save.
//   - contact page shows the Contact Context panel (MH-586)
//   - contacts → vCard page shows the Google Contacts panel (MH-585)
//   - prospects' profile shows the CRM ICP with an edit link to it (MH-514)
//   - contacts list and contact page render an avatar per row (MH-588), no sideways overflow
// Across en/ar × light/dark at 1440, plus 390 for the contacts list.
// HOST_RULES="MAP app.moharrik.com <ip>" bash demo-run.sh <base> e2e/batch3-live.mjs <base>
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = (process.argv[2] ?? "https://app.moharrik.com").replace(/\/$/, "")
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const seen = (page, sel, timeout = 15000) =>
  page
    .waitForSelector(sel, { timeout })
    .then(() => true)
    .catch(() => false)
const noOverflow = (page) =>
  page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1)

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "b3live-")), {
  executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
  headless: true,
  args,
  viewport: { width: 1440, height: 900 },
})
const page = await ctx.newPage()
const errs = []
page.on("pageerror", (e) => errs.push(String(e)))
await page.goto(`${base}/en/login`, { waitUntil: "load" })
await page.fill("#email", process.env.DEMO_EMAIL)
await page.fill("#password", process.env.DEMO_PASSWORD)
await page.click("button[type=submit]")
await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

let contactHref = null
for (const locale of ["en", "ar"])
  for (const scheme of ["light", "dark"]) {
    const tag = `${locale}-${scheme}`
    await page.emulateMedia({ colorScheme: scheme })
    await page.setViewportSize({ width: 1440, height: 900 })

    await page.goto(`${base}/${locale}/w/${slug}/contacts`, { waitUntil: "load" })
    const rows = await seen(page, "[data-contact-row]")
    const a = await page.evaluate(() => {
      const rows = [...document.querySelectorAll("[data-contact-row]")]
      return {
        rows: rows.length,
        withAvatar: rows.filter((r) => r.querySelector("[data-avatar]")).length,
        href: rows[0]?.querySelector("a[href*='/contacts/']")?.getAttribute("href") ?? null,
      }
    })
    contactHref ??= a.href
    check(`${tag}:contacts-avatars`, rows && a.rows > 0 && a.withAvatar === a.rows, a)
    check(`${tag}:contacts-no-overflow`, await noOverflow(page))
    if (scheme === "light") {
      await page.setViewportSize({ width: 390, height: 844 })
      await page.reload({ waitUntil: "load" })
      await seen(page, "[data-contact-row], [data-avatar]")
      check(`${tag}:contacts-390-no-overflow`, await noOverflow(page))
      await page.setViewportSize({ width: 1440, height: 900 })
    }

    if (contactHref) {
      await page.goto(new URL(contactHref.replace(/^\/(en|ar)\//, `/${locale}/`), base).href, {
        waitUntil: "load",
      })
      check(`${tag}:contact-avatar`, await seen(page, "[data-avatar]"))
      check(
        `${tag}:contact-context-panel`,
        await seen(page, "[data-testid=contact-context-question]"),
      )
      check(`${tag}:contact-no-overflow`, await noOverflow(page))
    }

    await page.goto(`${base}/${locale}/w/${slug}/contacts/vcard`, { waitUntil: "load" })
    check(`${tag}:google-contacts-panel`, await seen(page, "[data-testid=google-contacts-panel]"))

    await page.goto(`${base}/${locale}/w/${slug}/prospects`, { waitUntil: "load" })
    const icp = await seen(page, "[data-action=edit-icp]")
    const icpHref = icp
      ? await page.getAttribute("[data-action=edit-icp]", "href").catch(() => null)
      : null
    check(`${tag}:prospects-icp-link`, icp && (icpHref ?? "").includes("/settings/icp"), {
      icp,
      icpHref,
    })
  }
check("errors", errs.length === 0, errs.slice(0, 3))
await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
