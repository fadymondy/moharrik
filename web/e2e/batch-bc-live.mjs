// Read-only live check for the MH-645/646/647/600/648 deploy. Loads pages and reads the DOM only:
// no save, no merge, no import, no send.
//   - Settings → General shows the default-country picker (MH-646)
//   - a contact page offers "Merge" (MH-647) — the button is only looked at, never pressed
//   - the inbox Email tab is a plain list, no grouped-by-sender toggle (MH-600)
// Across en/ar × light/dark at 1440, plus 390 for light; no sideways overflow, no page errors.
// HOST_RULES="MAP app.moharrik.com <ip>" bash demo-run.sh <base> e2e/batch-bc-live.mjs <base>
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

const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "bclive-")), {
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
  for (const scheme of ["light", "dark"])
    for (const width of scheme === "light" ? [1440, 390] : [1440]) {
      const tag = `${locale}-${scheme}-${width}`
      await page.emulateMedia({ colorScheme: scheme })
      await page.setViewportSize({ width, height: 900 })

      await page.goto(`${base}/${locale}/w/${slug}/settings/general`, { waitUntil: "load" })
      check(`${tag}:country-picker`, await seen(page, "[data-testid=default-country-trigger]"))
      check(`${tag}:settings-no-overflow`, await noOverflow(page))

      if (!contactHref) {
        await page.goto(`${base}/${locale}/w/${slug}/contacts`, { waitUntil: "load" })
        await seen(page, "[data-contact-row]")
        contactHref = await page
          .getAttribute("[data-contact-row] a[href*='/contacts/']", "href")
          .catch(() => null)
      }
      if (contactHref) {
        await page.goto(new URL(contactHref.replace(/^\/(en|ar)\//, `/${locale}/`), base).href, {
          waitUntil: "load",
        })
        check(`${tag}:merge-offered`, await seen(page, "[data-merge-contact-open]"))
        check(`${tag}:contact-no-overflow`, await noOverflow(page))
      } else check(`${tag}:contact-found`, false)

      await page.goto(`${base}/${locale}/w/${slug}/inbox?channel=email`, { waitUntil: "load" })
      await page.waitForTimeout(1500)
      const grouped = await page.evaluate(
        () => document.querySelectorAll("[data-sender-group], [data-group-by-sender]").length,
      )
      check(`${tag}:no-grouped-ui`, grouped === 0, grouped)
      check(`${tag}:inbox-no-overflow`, await noOverflow(page))
    }
check("errors", errs.length === 0, errs.slice(0, 3))
await ctx.close()
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
