// MH-679: managing a workspace from the page. Drives a real Chromium against
// a throwaway stack (NEVER production — every step below writes) and asserts:
//
//   - /workspaces lists each workspace with its mark, member count and a
//     Manage + Open action
//   - Settings → General uploads a picture (a large one is scaled down before
//     it goes up), and the new mark shows in Settings, the switcher and the list
//   - rename saves and shows in the switcher
//   - the switcher has "Manage workspaces", which opens /workspaces
//   - members: an admin+ changes a role and removes someone; they can't touch
//     themselves; plain members see no controls
//   - a member leaves from Settings → General and lands on /workspaces
//   - delete stays disabled until the slug is typed, then removes the workspace
//   - en/ar × light/dark × 1440/390 render with no sideways overflow, and the
//     console carries no Base UI warnings
//
//   node e2e/mh679-workspaces-check.mjs <webBase> <seed.json>
//
// seed.json: {pw, owner, admin, member, ws, ar, tmp} — three users already in
// `ws` (owner/admin/member), plus `ar` and `tmp` owned by `owner`. `tmp` is
// deleted by this run.
import { chromium } from "playwright-core"
import { mkdirSync, readFileSync } from "node:fs"

const base = (process.argv[2] ?? "http://localhost:3679").replace(/\/$/, "")
const seed = JSON.parse(readFileSync(process.argv[3] ?? "E:/tmp/seed679.json", "utf8"))
const SHOTS = process.env.SHOTS ?? "E:/tmp/shots679"
mkdirSync(SHOTS, { recursive: true })
const CHROME =
  process.env.CHROME ??
  "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe"

const failures = []
const check = (ok, what) => {
  console.log(`${ok ? "ok  " : "FAIL"} ${what}`)
  if (!ok) failures.push(what)
}

const browser = await chromium.launch({ executablePath: CHROME })
const warnings = []

const signedIn = {}
async function session(email, { width = 1440, theme = "light" } = {}) {
  const ctx = await browser.newContext({
    viewport: { width, height: 900 },
    colorScheme: theme,
    storageState: signedIn[email],
  })
  await ctx.addInitScript((t) => localStorage.setItem("theme", t), theme)
  const page = await ctx.newPage()
  page.on("console", (m) => {
    if (m.type() === "error" || /Base UI/.test(m.text())) warnings.push(m.text().slice(0, 200))
  })
  page.on(
    "framenavigated",
    (f) => f === page.mainFrame() && process.env.NAVLOG && console.log("  nav", f.url()),
  )
  page.on("pageerror", (e) => warnings.push(String(e).slice(0, 200)))
  if (signedIn[email]) return { ctx, page }
  await page.goto(`${base}/en/login`)
  const status = await page.evaluate(
    async ({ email, pw }) => {
      const { csrf_token } = await (await fetch("/api/auth/csrf")).json()
      const r = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "content-type": "application/json", "X-CSRF-Token": csrf_token },
        body: JSON.stringify({ email, password: pw }),
      })
      return r.status
    },
    { email, pw: seed.pw },
  )
  if (status !== 200) throw new Error(`login ${email}: ${status}`)
  signedIn[email] = { cookies: await ctx.cookies(), origins: [] }
  return { ctx, page }
}

const api = (page, method, path, wsId) =>
  page.evaluate(
    async ({ method, path, wsId }) => {
      const r = await fetch(path, { method, headers: wsId ? { "X-Workspace-ID": wsId } : {} })
      return { status: r.status, body: r.status === 204 ? null : await r.json() }
    },
    { method, path, wsId },
  )

// ---- owner: list, picture, rename, switcher ----------------------------------
{
  const { ctx, page } = await session(seed.owner)

  await page.goto(`${base}/en/workspaces`)
  await page.locator(`[data-workspace-row="${seed.ws}"]`).waitFor()
  const row = page.locator(`[data-workspace-row="${seed.ws}"]`)
  check(
    (await row.locator("[data-member-count]").innerText()).trim() === "3 members",
    "list: member count",
  )
  check(
    (await row.locator("[data-workspace-manage]").getAttribute("href")) ===
      `/en/w/${seed.ws}/settings/general`,
    "list: Manage opens Settings → General",
  )
  check(
    (await row.locator("[data-workspace-open]").getAttribute("href")) === `/en/w/${seed.ws}`,
    "list: Open",
  )

  await page.goto(`${base}/en/w/${seed.ws}/settings/general`)
  const profile = page.locator("[data-workspace-profile]")
  await profile.waitFor()

  // A 1600×1000 photo: scaled to 512 on its longer edge before upload.
  const png = await page.evaluate(async () => {
    const c = document.createElement("canvas")
    c.width = 1600
    c.height = 1000
    const g = c.getContext("2d")
    g.fillStyle = "#4B00B6"
    g.fillRect(0, 0, 1600, 1000)
    g.fillStyle = "#f5c518"
    g.beginPath()
    g.arc(800, 500, 380, 0, Math.PI * 2)
    g.fill()
    const b = await new Promise((r) => c.toBlob(r, "image/png"))
    return Array.from(new Uint8Array(await b.arrayBuffer()))
  })
  const upload = page.waitForResponse(
    (r) => r.url().endsWith("/api/w/workspace/avatar") && r.request().method() === "PUT",
  )
  await page.locator("[data-workspace-avatar-input]").setInputFiles({
    name: "team.png",
    mimeType: "image/png",
    buffer: Buffer.from(png),
  })
  const up = await upload
  check(up.status() === 200, `picture upload: ${up.status()}`)
  const sent = JSON.parse(up.request().postData() ?? "{}")
  check(
    sent.content_type !== "image/png" || png.length < 100_000,
    `picture scaled before upload (${sent.content_type}, ${Math.round((sent.data?.length ?? 0) / 1024)} KB b64)`,
  )
  await profile.locator('[data-workspace-mark="image"] img').waitFor()
  const natural = await profile.locator('[data-workspace-mark="image"] img').evaluate(async (i) => {
    await i.decode().catch(() => {})
    return [i.naturalWidth, i.naturalHeight]
  })
  check(natural[0] === 512 && natural[1] === 320, `picture served at 512×320 (${natural})`)
  check(
    await page
      .getByRole("button", { name: "Switch workspace" })
      .locator('[data-workspace-mark="image"]')
      .isVisible(),
    "switcher shows the picture",
  )
  check(
    await page.locator("[data-workspace-avatar-remove]").isVisible(),
    "remove button shows once there is a picture",
  )

  // Rename, then put it back.
  const renamed = `Nile Team ${Date.now() % 100000}`
  await page.locator("[data-workspace-name]").fill(renamed)
  await page.locator("[data-workspace-name-save]").click()
  const switcher = page.getByRole("button", { name: "Switch workspace" })
  await switcher.getByText(renamed, { exact: true }).waitFor()
  check(true, "rename shows in the switcher")
  // Typed while the first save's refresh may still be settling: must survive it.
  await page.locator("[data-workspace-name]").fill("Nile Team")
  await page.waitForTimeout(300)
  check(
    (await page.locator("[data-workspace-name]").inputValue()) === "Nile Team",
    "rename: a draft typed after saving is kept",
  )
  await page.locator("[data-workspace-name-save]").click()
  await switcher.getByText("Nile Team", { exact: true }).waitFor()

  // Switcher → Manage workspaces.
  await switcher.click()
  await page.locator("[data-manage-workspaces]").click()
  await page.waitForURL(`${base}/en/workspaces`)
  check(true, "switcher: Manage workspaces opens /workspaces")
  await page
    .locator(`[data-workspace-row="${seed.ws}"] [data-workspace-mark="image"] img`)
    .waitFor()
  check(true, "list shows the picture")

  // ---- delete: disabled until the slug is typed -----------------------------
  await page.goto(`${base}/en/w/${seed.tmp}/settings/general`)
  await page.locator("[data-delete-open]").click()
  const confirm = page.locator("[data-delete-confirm]")
  check(await confirm.isDisabled(), "delete: disabled before typing")
  await page.locator("[data-delete-confirm-input]").fill(seed.tmp + "x")
  check(await confirm.isDisabled(), "delete: disabled on a wrong slug")
  await page.locator("[data-delete-confirm-input]").fill(seed.tmp)
  check(await confirm.isEnabled(), "delete: enabled on the slug")
  await confirm.click()
  await page.waitForURL(`${base}/en/workspaces`)
  await page.locator(`[data-workspace-row="${seed.ws}"]`).waitFor()
  check(
    (await page.locator(`[data-workspace-row="${seed.tmp}"]`).count()) === 0,
    "delete: gone from the list",
  )
  check(
    (await api(page, "GET", "/api/me/workspaces")).body.workspaces.every(
      (w) => w.slug !== seed.tmp,
    ),
    "delete: gone from the API",
  )
  await ctx.close()
}

// ---- admin: members role change + remove -------------------------------------
{
  const { ctx, page } = await session(seed.admin)
  await page.goto(`${base}/en/w/${seed.ws}/members`)
  const memberRow = page.locator(`tr:has([data-member="${seed.member}"])`)
  await memberRow.waitFor()
  check(
    (await page.locator(`tr:has([data-member="${seed.admin}"]) [data-member-role]`).count()) === 0,
    "members: no role control on yourself",
  )
  check(
    (await page.locator(`tr:has([data-member="${seed.owner}"]) [data-member-role]`).count()) === 0,
    "members: an admin can't change an owner",
  )

  const put = page.waitForResponse(
    (r) => /\/api\/w\/members\//.test(r.url()) && r.request().method() === "PUT",
  )
  await memberRow.locator("[data-member-role]").click()
  await page.getByRole("option", { name: "Manager" }).click()
  check((await put).status() === 200, "members: role change saved")
  const wsId = (await api(page, "GET", "/api/me/workspaces")).body.workspaces.find(
    (w) => w.slug === seed.ws,
  ).id
  const list = (await api(page, "GET", `/api/w/members`, wsId)).body
  check(
    list?.members?.find((m) => m.email === seed.member)?.role === "manager",
    "members: role is manager in the API",
  )
  const put2 = page.waitForResponse(
    (r) => /\/api\/w\/members\//.test(r.url()) && r.request().method() === "PUT",
  )
  await memberRow.locator("[data-member-role]").click()
  await page.getByRole("option", { name: "Member" }).click()
  await put2
  await ctx.close()
}

// ---- member: no controls, then leave ------------------------------------------
{
  const { ctx, page } = await session(seed.member)
  await page.goto(`${base}/en/w/${seed.ws}/members`)
  await page.locator(`tr:has([data-member="${seed.member}"])`).waitFor()
  check(
    (await page.locator("[data-member-role]").count()) === 0,
    "members: a member sees no role controls",
  )
  await page.goto(`${base}/en/w/${seed.ws}/settings/general`)
  await page.locator("[data-workspace-profile]").waitFor()
  check(
    (await page.locator("[data-workspace-name]").count()) === 0,
    "general: a member can't rename",
  )
  check((await page.locator("[data-delete-open]").count()) === 0, "general: a member can't delete")
  await page.locator("[data-leave-open]").click()
  await page.locator("[data-leave-confirm]").click()
  await page.waitForURL(`${base}/en/workspaces`)
  check(
    (await api(page, "GET", "/api/me/workspaces")).body.workspaces.length === 0,
    "leave: the member is out",
  )
  await ctx.close()
}

// ---- the matrix: en/ar × light/dark × 1440/390 ----------------------------------
for (const locale of ["en", "ar"]) {
  for (const theme of ["light", "dark"]) {
    for (const width of [1440, 390]) {
      const { ctx, page } = await session(seed.owner, { width, theme })
      for (const [name, path, ready] of [
        ["list", `/workspaces`, `[data-workspace-row="${seed.ws}"]`],
        ["general", `/w/${seed.ws}/settings/general`, "[data-workspace-profile]"],
        ["members", `/w/${seed.ws}/members`, `tr:has([data-member="${seed.admin}"])`],
      ]) {
        await page.goto(`${base}/${locale}${path}`)
        await page.locator(ready).first().waitFor()
        await page.waitForTimeout(400)
        const m = await page.evaluate(() => ({
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        }))
        const tag = `${locale} ${theme} ${width} ${name}`
        check(m.dir === (locale === "ar" ? "rtl" : "ltr"), `${tag}: dir ${m.dir}`)
        check(m.dark === (theme === "dark"), `${tag}: theme`)
        check(m.overflow <= 0, `${tag}: no sideways overflow (${m.overflow})`)
        await page.screenshot({
          path: `${SHOTS}/mh679-${name}-${locale}-${theme}-${width}.png`,
          fullPage: true,
        })
      }
      await ctx.close()
    }
  }
}

// ---- admin removes; owner removes the picture ---------------------------------
{
  const { ctx, page } = await session(seed.owner)
  await page.goto(`${base}/en/w/${seed.ws}/members`)
  const adminRow = page.locator(`tr:has([data-member="${seed.admin}"])`)
  await adminRow.waitFor()
  await adminRow.getByRole("button", { name: /Remove/ }).click()
  await page
    .getByRole("alertdialog")
    .getByRole("button", { name: /Remove/ })
    .click()
  await adminRow.waitFor({ state: "detached" })
  check(true, "members: owner removed the admin")

  await page.goto(`${base}/en/w/${seed.ws}/settings/general`)
  await page.locator("[data-workspace-avatar-remove]").click()
  await page.locator('[data-workspace-profile] [data-workspace-mark="initial"]').waitFor()
  check(true, "picture removed: back to the initial")
  await ctx.close()
}

await browser.close()
const baseUi = warnings.filter((w) => /Base UI/.test(w))
check(baseUi.length === 0, `no Base UI warnings${baseUi.length ? ": " + baseUi[0] : ""}`)
const other = warnings.filter((w) => !/Base UI/.test(w))
if (other.length) console.log("console errors:\n  " + [...new Set(other)].slice(0, 10).join("\n  "))
console.log(failures.length ? `\n${failures.length} FAILED` : "\nall passed")
process.exit(failures.length ? 1 : 0)
