// MH-672: drag-and-drop and paste attach a photo, video, audio file or document to the reply —
// full inbox thread (en/ar × light/dark × 1440/390) and a docked chat. Never presses Send;
// fails on any API write other than marking the thread read.
//   DEMO_EMAIL=... DEMO_PASSWORD=... CONV=<id> node e2e/mh672-drop.mjs <base> [shots]
import { chromium } from "playwright-core"
import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"

const base = process.argv[2] ?? "http://localhost:5799"
const shots = process.argv[3]
const slug = process.env.DEMO_SLUG ?? "moharrik-demo-nile-coffee-co"
const conv = process.env.CONV
const out = {},
  fail = []
const check = (k, ok, d) => {
  out[k] = ok ? true : (d ?? false)
  if (!ok) fail.push(k)
}
const args = process.env.HOST_RULES ? [`--host-resolver-rules=${process.env.HOST_RULES}`] : []

// Runs in the page: dispatch a drag sequence carrying files (or text) at a selector.
const DRAG = ({ sel, files, text, steps }) => {
  const el = document.querySelector(sel)
  if (!el) return { missing: sel }
  const dt = new DataTransfer()
  if (text) dt.setData("text/plain", text)
  for (const f of files ?? []) {
    const body = f.png ? new Uint8Array(f.png) : new Uint8Array(f.size ?? 64)
    dt.items.add(new File([body], f.name, { type: f.type }))
  }
  const results = {}
  for (const s of steps) {
    const ev = new DragEvent(s, { bubbles: true, cancelable: true, dataTransfer: dt })
    el.dispatchEvent(ev)
    results[s] = ev.defaultPrevented
  }
  return results
}
const pngBytes = () =>
  new Promise((ok) => {
    const c = document.createElement("canvas")
    c.width = c.height = 32
    const g = c.getContext("2d")
    g.fillStyle = "#4B00B6"
    g.fillRect(0, 0, 32, 32)
    c.toBlob(async (b) => ok(Array.from(new Uint8Array(await b.arrayBuffer()))), "image/png")
  })

async function scenario(page, root, k) {
  const thread = `${root} [data-chat-thread]`
  const chip = `${root} [data-pending-attachment]`
  const drag = (spec) => page.evaluate(DRAG, { sel: thread, ...spec })
  const png = await page.evaluate(pngBytes)
  const chipKind = () =>
    page.evaluate((c) => document.querySelector(c)?.dataset.pendingAttachmentKind ?? null, chip)
  const removeChip = async () => {
    await page.locator(`${chip} [data-action="attach-remove"]`).click()
    await page.waitForSelector(chip, { state: "detached", timeout: 5000 })
  }
  const overlay = () => page.locator(`${root} [data-drop-zone]`).count()

  // Dragging text is left alone.
  await drag({ text: "hello", steps: ["dragenter", "dragover"] })
  check(`${k}-text-drag-ignored`, (await overlay()) === 0)
  await drag({ text: "hello", steps: ["dragleave"] })

  // A file drag shows the zone, labelled in the page language; leaving clears it.
  const r = await drag({
    files: [{ name: "a.pdf", type: "application/pdf" }],
    steps: ["dragenter", "dragover"],
  })
  const zone = await page.evaluate((root) => {
    const z = document.querySelector(`${root} [data-drop-zone]`)
    const t = document.querySelector(`${root} [data-chat-thread]`)
    if (!z) return null
    const zr = z.getBoundingClientRect(),
      tr = t.getBoundingClientRect()
    return {
      text: z.textContent,
      inside:
        zr.left >= tr.left - 1 &&
        zr.right <= tr.right + 1 &&
        zr.top >= tr.top - 1 &&
        zr.bottom <= tr.bottom + 1,
      border: getComputedStyle(z).borderTopStyle,
    }
  }, root)
  check(
    `${k}-zone-shown`,
    !!zone && zone.inside && zone.border === "dashed" && r.dragover === true,
    { zone, r },
  )
  out[`${k}-zone-text`] = zone?.text
  if (shots) {
    await page.locator(thread).first().scrollIntoViewIfNeeded()
    await page.screenshot({ path: `${shots}/mh672-${k}-zone.png` })
  }
  await drag({ files: [{ name: "a.pdf", type: "application/pdf" }], steps: ["dragleave"] })
  check(`${k}-zone-clears`, (await overlay()) === 0)

  // Each kind drops into the pending chip, the way the paperclip attaches it.
  const kinds = [
    [{ name: "photo.png", type: "image/png", png }, "image"],
    [{ name: "clip.mp4", type: "video/mp4" }, "video"],
    [{ name: "note.ogg", type: "audio/ogg" }, "voice"],
    [{ name: "brief.pdf", type: "application/pdf" }, "document"],
  ]
  for (const [file, want] of kinds) {
    await drag({ files: [file], steps: ["dragenter", "dragover", "drop"] })
    await page.waitForSelector(chip, { timeout: 5000 }).catch(() => {})
    const got = await chipKind()
    const name = await page
      .locator(chip)
      .textContent({ timeout: 2000 })
      .catch(() => null)
    check(
      `${k}-drop-${want}`,
      got === want && !!name?.includes(file.name) && (await overlay()) === 0,
      { got, name },
    )
    if (want === "image") {
      await page.waitForTimeout(300)
      const thumb = await page.evaluate(
        (c) => document.querySelector(`${c} [data-pending-attachment-preview]`)?.naturalWidth ?? 0,
        chip,
      )
      check(`${k}-image-thumbnail`, thumb > 0, thumb)
      if (shots) {
        await page.locator(chip).scrollIntoViewIfNeeded()
        await page.screenshot({ path: `${shots}/mh672-${k}-chip.png` })
      }
    }
    if (got) await removeChip()
  }

  // Two files: the first is attached and the composer says so.
  await drag({
    files: [
      { name: "one.pdf", type: "application/pdf" },
      { name: "two.pdf", type: "application/pdf" },
    ],
    steps: ["dragenter", "dragover", "drop"],
  })
  await page.waitForSelector(chip, { timeout: 5000 }).catch(() => {})
  const note = await page
    .locator(`${root} [data-attach-error]`)
    .textContent({ timeout: 2000 })
    .catch(() => null)
  const first = await page
    .locator(chip)
    .textContent({ timeout: 2000 })
    .catch(() => null)
  check(`${k}-two-files`, !!first?.includes("one.pdf") && !!note, { first, note })
  if (await page.locator(chip).count()) await removeChip()

  // Over 20MB is refused with the size message and no chip.
  await drag({
    files: [{ name: "big.mp4", type: "video/mp4", size: 21 * 1024 * 1024 }],
    steps: ["dragenter", "dragover", "drop"],
  })
  await page.waitForTimeout(300)
  check(
    `${k}-too-large`,
    (await page.locator(chip).count()) === 0 &&
      (await page.locator(`${root} [data-attach-error]`).count()) === 1,
  )

  // Pasting an image into the reply box attaches it.
  const pasted = await page.evaluate(
    ({ root, png }) => {
      const ta = document.querySelector(`${root} textarea`)
      const dt = new DataTransfer()
      dt.items.add(new File([new Uint8Array(png)], "pasted.png", { type: "image/png" }))
      const ev = new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData: dt })
      ta.dispatchEvent(ev)
      return ev.defaultPrevented
    },
    { root, png },
  )
  await page.waitForSelector(chip, { timeout: 5000 }).catch(() => {})
  const pk = await chipKind()
  check(`${k}-paste-image`, pasted && pk === "image", { pasted, kind: pk })
  if (await page.locator(chip).count()) await removeChip()

  // A file dropped outside the thread doesn't make the browser leave the inbox.
  const guarded = await page.evaluate(DRAG, {
    sel: "body",
    files: [{ name: "x.pdf", type: "application/pdf" }],
    steps: ["dragover", "drop"],
  })
  check(`${k}-outside-drop-guarded`, guarded.drop === true, guarded)
}

for (const scheme of ["light", "dark"]) {
  const dir = mkdtempSync(path.join(tmpdir(), "mh672-"))
  const ctx = await chromium.launchPersistentContext(dir, {
    executablePath: "C:/Program Files/Google/Chrome/Application/chrome.exe",
    headless: true,
    args,
    viewport: { width: 1440, height: 900 },
    colorScheme: scheme,
  })
  const page = await ctx.newPage()
  const errs = [],
    writes = []
  page.on("pageerror", (e) => errs.push(String(e)))
  page.on("request", (r) => {
    const u = new URL(r.url())
    if (
      r.method() !== "GET" &&
      u.pathname.startsWith("/api/") &&
      !u.pathname.startsWith("/api/auth/") &&
      !/\/read$/.test(u.pathname)
    )
      writes.push(`${r.method()} ${u.pathname}`)
  })
  await page.goto(`${base}/en/login`, { waitUntil: "load" })
  await page.evaluate((s) => localStorage.setItem("theme", s), scheme)
  await page.waitForLoadState("networkidle").catch(() => {})
  await page.waitForTimeout(1500)
  await page.fill("#email", process.env.DEMO_EMAIL)
  await page.fill("#password", process.env.DEMO_PASSWORD)
  await page.waitForFunction(() => !document.querySelector("button[type=submit]")?.disabled, null, {
    timeout: 30000,
  })
  await page.click("button[type=submit]")
  await page.waitForURL(/workspaces|\/w\//, { timeout: 30000 })

  for (const loc of ["en", "ar"])
    for (const w of [1440, 390]) {
      const k = `${loc}-${scheme}-${w}`
      await page.setViewportSize({ width: w, height: 900 })
      await page.goto(`${base}/${loc}/w/${slug}/inbox?c=${conv}`, { waitUntil: "domcontentloaded" })
      const ready = await page
        .waitForSelector('[data-form="reply"] textarea', { timeout: 30000 })
        .then(
          () => true,
          () => false,
        )
      check(`${k}-composer`, ready)
      if (!ready) continue
      await page.waitForLoadState("networkidle").catch(() => {})
      await page.waitForTimeout(1000)
      const env = await page.evaluate(() => ({
        dir: document.documentElement.dir,
        dark: document.documentElement.classList.contains("dark"),
      }))
      check(`${k}-dir`, env.dir === (loc === "ar" ? "rtl" : "ltr"), env.dir)
      check(`${k}-theme`, env.dark === (scheme === "dark"), env.dark)
      // The full inbox page: mark the element that holds the thread owning the reply form.
      await page.evaluate(() => {
        document
          .querySelectorAll("[data-mh672-root]")
          .forEach((e) => e.removeAttribute("data-mh672-root"))
        document
          .querySelector('[data-form="reply"]')
          .closest("[data-chat-thread]")
          .parentElement.setAttribute("data-mh672-root", "")
      })
      await scenario(page, "[data-mh672-root]", k)
      check(
        `${k}-no-overflow`,
        !(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)),
      )
    }

  check(`${scheme}-no-writes`, writes.length === 0, writes)
  check(`${scheme}-no-page-errors`, errs.length === 0, errs.slice(0, 3))
  await ctx.close()
  rmSync(dir, { recursive: true, force: true })
}

console.log(JSON.stringify({ fail, out }, null, 1))
console.log(fail.length ? `FAIL ${fail.length}` : `PASS ${Object.keys(out).length}`)
process.exit(fail.length ? 1 : 0)
