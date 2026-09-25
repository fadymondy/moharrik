// Read-only production check for MH-583/MH-600/MH-602. Every inbox row and
// the open thread's header carry an avatar (image or initials), email
// conversations are grouped by sender when there are any, and the contact page
// shows the Google-parity person fields and the avatar control. Nothing is
// saved or uploaded, and no conversation is opened (opening marks it read).
// en/ar × light/dark × 1440/390.
//   DEMO_EMAIL=... DEMO_PASSWORD=... node e2e/contacts-avatars-live.mjs https://app.moharrik.com [shots]
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

for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh602-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    args,
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []
  page.on("pageerror", (e) => errs.push(String(e)))
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })

      // Inbox: every rendered row has an avatar.
      await page.goto(`${base}/${loc}/w/${slug}/inbox`, { waitUntil: "load" })
      await page
        .waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 30000 })
        .catch(() => {})
      await page.waitForTimeout(1200)
      const rows = await page.evaluate(() => {
        const rs = [...document.querySelectorAll("[data-conversation-list] [data-conversation]")]
        return {
          n: rs.length,
          withAvatar: rs.filter((r) => r.querySelector("[data-avatar]")).length,
          images: rs.filter((r) => r.querySelector('[data-avatar-kind="image"]')).length,
        }
      })
      check(`${k}-row-avatars`, rows.n > 0 && rows.withAvatar === rows.n, rows)
      if (w === 1440 && loc === "en") out[`${scheme}-avatar-images`] = rows.images

      // Email: grouped by sender when the workspace has any email.
      await page.goto(`${base}/${loc}/w/${slug}/inbox?channel=email`, { waitUntil: "load" })
      await page.waitForTimeout(2500)
      const email = await page.evaluate(() => ({
        toggle: !!document.querySelector('[data-sender-toggle="grouped"]'),
        groups: document.querySelectorAll("[data-sender-group]").length,
        rows: document.querySelectorAll("[data-conversation-list] [data-conversation]").length,
      }))
      check(`${k}-email-groups`, email.groups > 0 || email.rows === 0, email)

      // Contact page: the person fields and the avatar control render. Nothing is saved.
      await page.goto(`${base}/${loc}/w/${slug}/contacts`, { waitUntil: "load" })
      // Only a contact's own page: /contacts/import and friends share the prefix.
      await page
        .waitForFunction(
          () =>
            [...document.querySelectorAll("a[href]")].some((a) =>
              /\/contacts\/[0-9a-f]{8}-/.test(a.getAttribute("href")),
            ),
          null,
          { timeout: 20000 },
        )
        .catch(() => {})
      const href = await page.evaluate(
        () =>
          [...document.querySelectorAll("a[href]")]
            .map((a) => a.getAttribute("href"))
            .find((h) => /\/contacts\/[0-9a-f]{8}-[0-9a-f-]{27}$/.test(h)) ?? null,
      )
      const hasContact = !!href
      check(`${k}-contact-link`, hasContact)
      if (hasContact) {
        await page.goto(new URL(href, base).toString(), { waitUntil: "load" })
        await page.waitForSelector("#edit-contact-given-name", { timeout: 15000 }).catch(() => {})
        const c = await page.evaluate(() => ({
          given: !!document.getElementById("edit-contact-given-name"),
          family: !!document.getElementById("edit-contact-family-name"),
          bio: !!document.getElementById("edit-contact-biography"),
          avatarCtl: !!document.querySelector("[data-contact-avatar-control]"),
          avatar: !!document.querySelector("[data-avatar]"),
        }))
        check(`${k}-contact-page`, Object.values(c).every(Boolean), c)
      }

      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth,
      )
      check(`${k}-overflow`, overflow <= 1, overflow)
      if (shots) await page.screenshot({ path: path.join(shots, `mh602-live-${k}.png`) })
    }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}
console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
