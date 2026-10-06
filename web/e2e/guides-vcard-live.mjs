// Read-only production check for MH-594 and MH-584. Every "add channel" type
// opens its sheet with a step-by-step setup guide (then closes with Escape —
// nothing is saved), and the contacts vCard page parses a pasted card into a
// preview WITHOUT importing it (preview writes nothing; commit is never
// clicked). en/ar × light/dark × 1440/390.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/guides-vcard-live.mjs https://app.moharrik.com [shots]
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "https://app.moharrik.com"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []
const fixture = [
  "BEGIN:VCARD",
  "VERSION:3.0",
  "FN:Preview Only",
  "N:Only;Preview;;;",
  "TEL;TYPE=CELL:+201000000001",
  "EMAIL:preview.only@example.test",
  "END:VCARD",
  "",
].join("\r\n")

for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "guides-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    args,
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []
  page.on("pageerror", (e) => errs.push(String(e)))
  // Belt and braces: this check must never write. Any non-GET besides the
  // vCard preview (and sign-in) fails it.
  const writes = []
  page.on("request", (r) => {
    const u = new URL(r.url())
    if (
      r.method() !== "GET" &&
      u.pathname.startsWith("/api/") &&
      !/^\/api\/(auth\/|w\/contacts\/vcard\/preview$)/.test(u.pathname)
    )
      writes.push(`${r.method()} ${u.pathname}`)
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/settings/integrations`, { waitUntil: "load" })
      await page.waitForSelector("[data-add-channel]", { timeout: 30000 })
      await page.click("[data-add-channel]")
      await page.waitForSelector("[data-add-type]", { timeout: 10000 })
      const types = await page.$$eval("[data-add-type]", (els) =>
        els.map((e) => e.getAttribute("data-add-type")),
      )
      await page.keyboard.press("Escape")
      const guides = {}
      for (const ty of types) {
        await page.click("[data-add-channel]")
        await page.click(`[data-add-type="${ty}"]`)
        const g = await page
          .waitForSelector(`[data-add-channel-sheet="${ty}"] [data-setup-guide]`, {
            timeout: 10000,
          })
          .then(
            () => true,
            () => false,
          )
        // A type the workspace already has starts with its guide collapsed —
        // open it (a disclosure toggle, not a write) before counting steps.
        const sel = `[data-add-channel-sheet="${ty}"] [data-setup-guide]`
        if (g && !(await page.$(`${sel} li`))) await page.click(`${sel} button`)
        if (g) await page.waitForSelector(`${sel} li`, { timeout: 5000 }).catch(() => {})
        const steps = g
          ? await page.$$eval(
              `[data-add-channel-sheet="${ty}"] [data-setup-guide] li`,
              (l) => l.length,
            )
          : 0
        guides[ty] = steps
        if (shots && ty === types[0])
          await page.screenshot({ path: path.join(shots, `guide-${ty}-${k}.png`) })
        await page.keyboard.press("Escape")
        await page
          .waitForSelector(`[data-add-channel-sheet="${ty}"]`, { state: "hidden", timeout: 5000 })
          .catch(() => {})
      }
      check(`${k}-guides`, types.length > 0 && Object.values(guides).every((n) => n >= 2), guides)

      await page.goto(`${base}/${loc}/w/${slug}/contacts/vcard`, { waitUntil: "load" })
      await page.waitForSelector("[data-testid=vcard-text-input]", { timeout: 30000 })
      await page.fill("[data-testid=vcard-text-input]", fixture)
      await page.click("[data-action=preview-vcard]")
      const previewed = await page
        .waitForSelector("[data-testid=vcard-preview-table]", { timeout: 15000 })
        .then(
          () => true,
          () => false,
        )
      const rowText = previewed
        ? await page.locator("[data-testid=vcard-preview-table]").innerText()
        : ""
      check(
        `${k}-vcard-preview`,
        previewed && rowText.includes("Preview Only"),
        rowText.slice(0, 120),
      )
      if (shots) await page.screenshot({ path: path.join(shots, `vcard-${k}.png`) })

      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth,
      )
      check(`${k}-overflow`, overflow <= 1, overflow)
    }
  check(`noWrites-${scheme}`, writes.length === 0, writes)
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
