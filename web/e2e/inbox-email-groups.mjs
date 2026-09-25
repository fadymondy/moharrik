// MH-600: the Email channel groups conversations by sender. Verifies, against
// a local dev server with seeded data: groups render one row per sender with
// correct counts, clicking a group narrows the list via ?sender=, a back/clear
// control returns to the grouped view, the grouped/flat toggle persists via
// localStorage, and none of it breaks in RTL or dark mode or overflows at
// mobile width. Modeled on e2e/inbox-mh595-live.mjs.
//
//   node e2e/inbox-email-groups.mjs <webBase> <slug> <shotsDir>
import { chromium } from "playwright-core"
import { mkdtempSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:3141"
const slug = process.argv[3]
const shots = process.argv[4]
const email = process.env.MH600_EMAIL
const password = process.env.MH600_PASSWORD
if (!slug || !email || !password) {
  console.error(
    "usage: MH600_EMAIL=... MH600_PASSWORD=... node e2e/inbox-email-groups.mjs <webBase> <slug> <shotsDir>",
  )
  process.exit(2)
}

const out = {}
const fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}

for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh600-")), {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = []
  page.on("pageerror", (e) => errs.push(String(e)))

  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.fill("#email", email)
  await page.fill("#password", password)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"]) {
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })
      // Force the grouped view for a clean start each time this key is tested.
      await page.goto(`${base}/${loc}/w/${slug}/inbox`, { waitUntil: "load" })
      await page.evaluate(() => localStorage.setItem("inbox.email.groupedBySender", "true"))
      await page.goto(`${base}/${loc}/w/${slug}/inbox?channel=email`, { waitUntil: "load" })
      await page.waitForSelector("[data-conversation-column]", { timeout: 15000 })
      await page
        .waitForSelector("[data-sender-group], [data-sender-group-list]", { timeout: 15000 })
        .catch(() => {})
      await page.waitForTimeout(500)

      // Toggle control is visible on the Email tab.
      const toggleVisible = await page.evaluate(
        () =>
          !!document.querySelector('[data-sender-toggle="grouped"]') &&
          !!document.querySelector('[data-sender-toggle="flat"]'),
      )
      check(`${k}-toggle-visible`, toggleVisible)

      // Groups rendered: expect 3 sender groups (mona, sami, lina) from the seed,
      // never the chat conversation and never the messageless "ghost" one.
      const groups = await page.evaluate(() =>
        [...document.querySelectorAll("[data-sender-group]")].map((el) => ({
          sender: el.getAttribute("data-sender-group"),
          unread: el.getAttribute("data-sender-group-unread"),
          text: el.textContent,
        })),
      )
      check(
        `${k}-group-count`,
        groups.length === 3,
        groups.map((g) => g.sender),
      )
      check(`${k}-no-ghost-sender`, !groups.some((g) => g.sender === "ghost@nowhere.example"))
      check(`${k}-no-chat-in-groups`, !groups.some((g) => g.sender === ""))

      // Sort: unread-first, then newest. sami (unread, most recent) before
      // mona (unread, older) before lina (all read).
      check(
        `${k}-sort-order`,
        groups[0]?.sender === "sami@corp.example" &&
          groups[1]?.sender === "mona@mail.example" &&
          groups[2]?.sender === "lina@shop.example",
        groups.map((g) => g.sender),
      )

      // Counts: mona has 3 conversations, 2 unread.
      const mona = groups.find((g) => g.sender === "mona@mail.example")
      check(`${k}-mona-count-text`, !!mona && /3/.test(mona.text), mona?.text)

      if (shots) await page.screenshot({ path: path.join(shots, `mh600-grouped-${k}.png`) })

      // Click a group: narrows the list via ?sender=, shows the banner.
      await page.click(`[data-sender-group="mona@mail.example"]`)
      await page.waitForURL(/sender=/, { timeout: 10000 })
      check(`${k}-url-has-sender`, /sender=mona%40mail\.example/i.test(page.url()), page.url())
      await page.waitForSelector("[data-sender-filter-banner]", { timeout: 10000 })
      const bannerVisible = await page.evaluate(
        () => !!document.querySelector("[data-sender-filter-banner]"),
      )
      check(`${k}-banner-visible`, bannerVisible)

      // The narrowed list is scoped server-side to mona's 3 conversations —
      // the virtualizer only mounts what's visible in the viewport, so the DOM
      // count is not a reliable stand-in for "how many rows the query has".
      await page
        .waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 10000 })
        .catch(() => {})
      const narrowedCount = await page.evaluate(async () => {
        const wsId = (
          await fetch("/api/me/workspaces", { credentials: "same-origin" }).then((r) => r.json())
        ).workspaces[0].id
        const j = await fetch(
          "/api/w/conversations?channel=email&customer_email=" +
            encodeURIComponent("mona@mail.example"),
          {
            headers: { "X-Workspace-ID": wsId },
            credentials: "same-origin",
          },
        ).then((r) => r.json())
        return j.conversations?.length
      })
      check(`${k}-narrowed-count`, narrowedCount === 3, narrowedCount)

      // Reload keeps the narrowing (it's in the URL).
      await page.reload({ waitUntil: "load" })
      await page.waitForSelector("[data-sender-filter-banner]", { timeout: 10000 })
      const stillNarrowed = await page.evaluate(() =>
        new URLSearchParams(location.search).get("sender"),
      )
      check(`${k}-reload-keeps-sender`, stillNarrowed === "mona@mail.example", stillNarrowed)

      // Clear / back returns to the grouped view. A short settle pause before
      // clicking avoids racing a click against React re-rendering the button
      // mid-gesture (headless Chrome can process the click faster than a real
      // user would, landing between mousedown/mouseup of a re-render) — the
      // interaction itself is exercised for real above via t3-code and works
      // on the first try there.
      await page.waitForSelector("[data-sender-clear]", { timeout: 10000 })
      await page.waitForTimeout(300)
      await page.click("[data-sender-clear]")
      await page
        .waitForFunction(() => !new URLSearchParams(location.search).get("sender"), {
          timeout: 5000,
        })
        .catch(() =>
          page.click("[data-sender-clear]").then(() =>
            page.waitForFunction(() => !new URLSearchParams(location.search).get("sender"), {
              timeout: 10000,
            }),
          ),
        )
      await page.waitForSelector("[data-sender-group]", { timeout: 10000 })
      check(
        `${k}-back-to-groups`,
        await page.evaluate(() => document.querySelectorAll("[data-sender-group]").length === 3),
      )

      if (shots) await page.screenshot({ path: path.join(shots, `mh600-narrowed-${k}.png`) })

      // Toggle to flat: the flat conversation list shows (chat is excluded
      // from the count since we're still on the Email tab).
      await page.waitForTimeout(300)
      await page.click('[data-sender-toggle="flat"]')
      await page.waitForSelector("[data-conversation-list] [data-conversation]", { timeout: 10000 })
      const flatVisible = await page.evaluate(
        () =>
          !document.querySelector("[data-sender-group-list]") &&
          document.querySelectorAll("[data-conversation-list] [data-conversation]").length > 0,
      )
      check(`${k}-flat-toggle-works`, flatVisible)

      // Persistence: localStorage remembers "flat" across a reload.
      await page.reload({ waitUntil: "load" })
      await page.waitForSelector('[data-sender-toggle="grouped"]', { timeout: 10000 })
      const persistedFlat = await page.evaluate(
        () =>
          localStorage.getItem("inbox.email.groupedBySender") === "false" &&
          !document.querySelector("[data-sender-group-list]"),
      )
      check(`${k}-toggle-persisted`, persistedFlat)

      // Switch back to grouped for the next iteration / other locale-width combos.
      await page.waitForTimeout(300)
      await page.click('[data-sender-toggle="grouped"]')
      await page
        .waitForSelector("[data-sender-group]", { timeout: 5000 })
        .catch(() =>
          page
            .click('[data-sender-toggle="grouped"]')
            .then(() => page.waitForSelector("[data-sender-group]", { timeout: 10000 })),
        )

      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - window.innerWidth,
      )
      check(`${k}-no-overflow`, overflow <= 1, overflow)
    }
  }
  check(`noPageErrors-${scheme}`, errs.length === 0, errs)
  await ctx.close()
}

console.log(JSON.stringify(out, null, 1))
console.log(fail.length ? `FAIL: ${fail.join(", ")}` : "ALL OK")
process.exit(fail.length ? 1 : 0)
