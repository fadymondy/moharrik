// MH-656: the WhatsApp-style bubble — tail on the first of a run, time and tick inside the
// bubble, WhatsApp formatting and links, the link preview card, the wallpaper — in
// en/ar × light/dark × 1440/390. Read-only unless SEED=1, which posts one reply with a URL
// into the first CHAT (widget) conversation: a chat reply is stored, never sent anywhere.
//   DEMO_EMAIL=... DEMO_PASSWORD=... [SEED=1] node e2e/mh656-bubbles.mjs <base> [shots]
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
const SEED_BODY = "Bubble check (MH-656): *bold* _italic_ ~strike~ — https://github.com/fadymondy"

let convId = process.env.CONV_ID ?? ""
for (const scheme of ["light", "dark"]) {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(path.join(tmpdir(), "mh656-")), {
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
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  if (!convId) {
    convId = await page.evaluate(async (slug) => {
      const r = await fetch("/api/w/conversations?channel=chat&limit=1", {
        headers: { "X-Workspace-ID": slug },
      })
      const j = await r.json()
      return (j.conversations ?? j.items ?? [])[0]?.id ?? `!${r.status} ${Object.keys(j)}`
    }, slug)
    if (convId.startsWith("!")) {
      check("found-chat-conversation", false, convId)
      convId = ""
    }
    if (process.env.SEED === "1" && convId) {
      const status = await page.evaluate(
        async ({ slug, id, body }) => {
          const csrf = (await (await fetch("/api/auth/csrf")).json()).csrf_token
          return (
            await fetch(`/api/w/conversations/${id}/reply`, {
              method: "POST",
              headers: { "X-Workspace-ID": slug, "Content-Type": "application/json", "X-CSRF-Token": csrf },
              body: JSON.stringify({ body }),
            })
          ).status
        },
        { slug, id: convId, body: SEED_BODY },
      )
      check("seed-reply", status === 200 || status === 201, status)
    }
  }

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/inbox?c=${convId}`, { waitUntil: "domcontentloaded" })
      const ok = await page
        .waitForSelector("[data-chat-thread] [data-message]", { timeout: 30000 })
        .then(
          () => true,
          () => false,
        )
      if (!ok) {
        check(`${k}-thread`, false, "no bubbles")
        continue
      }
      await page.waitForSelector("[data-link-preview]", { timeout: 15000 }).catch(() => {})
      await page.waitForTimeout(800)
      const r = await page.evaluate(() => {
        const thread = document.querySelector("[data-chat-thread]")
        const pane = document.querySelector("[data-messages]")
        const bubbles = [...document.querySelectorAll("[data-chat-thread] [data-message]")]
        const outs = bubbles.filter((b) => b.getAttribute("data-message") === "out")
        const last = outs[outs.length - 1] ?? bubbles[bubbles.length - 1]
        const meta = last?.querySelector("[data-message-meta]")
        const firsts = [...document.querySelectorAll("[data-message-group]")].map(
          (g) => g.querySelector("[data-message]"),
        )
        const tails = firsts.filter((b) => b?.querySelector(":scope > svg")).length
        const lb = last?.getBoundingClientRect()
        const mb = meta?.getBoundingClientRect()
        const bgOut = outs[0] ? getComputedStyle(outs[0]).backgroundColor : ""
        const bgIn = bubbles.find((b) => b.getAttribute("data-message") === "in")
        // The time/tick must never sit on the text: compare it with every line box
        // of every text node in the same bubble.
        let overlaps = 0
        for (const b of bubbles) {
          const m = b.querySelector("[data-message-meta]")
          if (!m) continue
          const mr = m.getBoundingClientRect()
          const walker = document.createTreeWalker(b, NodeFilter.SHOW_TEXT)
          for (let n = walker.nextNode(); n; n = walker.nextNode()) {
            if (m.contains(n) || !n.textContent.trim() || n.parentElement.closest("[data-link-preview]"))
              continue
            const range = document.createRange()
            range.selectNodeContents(n)
            for (const r of range.getClientRects())
              if (r.left < mr.right - 1 && r.right > mr.left + 1 && r.top < mr.bottom - 1 && r.bottom > mr.top + 1)
                overlaps++
          }
        }
        return {
          overlaps,
          dir: document.documentElement.dir || "ltr",
          bubbles: bubbles.length,
          groups: firsts.length,
          tails,
          metaInside: !!(lb && mb && mb.left >= lb.left - 1 && mb.right <= lb.right + 1 && mb.bottom <= lb.bottom + 1),
          ticks: [...document.querySelectorAll("[data-tick]")].map((t) => t.getAttribute("data-tick")),
          strong: !!document.querySelector("[data-message] strong"),
          links: document.querySelectorAll("[data-message] div[dir=auto] a[href^='http']").length,
          preview: !!document.querySelector("[data-link-preview]"),
          previewTitle: document.querySelector("[data-link-preview] p")?.textContent ?? "",
          iframesInChat: document.querySelectorAll("[data-message]:not([data-message-layout]) iframe").length,
          wallpaper: pane ? getComputedStyle(pane).backgroundImage.startsWith("radial-gradient") : false,
          outDiffersFromIn: !!bgIn && bgOut !== getComputedStyle(bgIn).backgroundColor,
          overflowX: document.documentElement.scrollWidth > window.innerWidth + 1,
          threadOk: !!thread,
        }
      })
      out[k] = r
      check(`${k}-tails`, r.tails === r.groups && r.groups > 0, `${r.tails}/${r.groups}`)
      check(`${k}-meta-inside`, r.metaInside)
      check(`${k}-meta-clear-of-text`, r.overlaps === 0, r.overlaps)
      check(`${k}-ticks`, r.ticks.length > 0, r.ticks)
      check(`${k}-format`, r.strong && r.links > 0)
      check(`${k}-preview`, r.preview)
      check(`${k}-no-iframe-in-chat`, r.iframesInChat === 0, r.iframesInChat)
      check(`${k}-wallpaper`, r.wallpaper)
      check(`${k}-out-tint`, r.outDiffersFromIn)
      check(`${k}-no-overflow`, !r.overflowX)
      check(`${k}-dir`, r.dir === (loc === "ar" ? "rtl" : "ltr"), r.dir)
      if (shots) {
        await page
          .locator("[data-chat-thread]")
          .screenshot({ path: path.join(shots, `mh656-${k}.png`) })
          .catch(() => page.screenshot({ path: path.join(shots, `mh656-${k}.png`) }))
      }
    }
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  await ctx.close()
}
console.log(JSON.stringify({ convId, fail, out }, null, 1))
process.exit(fail.length ? 1 : 0)
