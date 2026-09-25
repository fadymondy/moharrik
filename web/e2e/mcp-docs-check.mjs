// The public MCP reference (MH-428) in a real browser: en/ar × light/dark × 1440/390.
//
//   node e2e/mcp-docs-check.mjs [webBase] [card]
//
// Asserts the page lists every tool the API documents (GET /api/mcp/docs), that a tool opens to
// its arguments, example call and result, that #tool-<name> deep links open that tool, that the
// filter works, and that nothing overflows sideways; then saves a full-page screenshot per
// combination to $SHOTS/<card>-<locale>-<theme>-<width>.png.
import { chromium } from "playwright-core"

const [webArg, card = "MH-428-docs"] = process.argv.slice(2)
const web = (webArg ?? "http://localhost:3518").replace(/\/$/, "")
const SHOTS = process.env.SHOTS ?? "E:/Sites/fadymondy.com-v2/.setup/shots"
const CHROMIUM =
  process.env.EXT_CHROME ??
  "C:/Users/EngFa/AppData/Local/ms-playwright/chromium-1243/chrome-win64/chrome.exe"

const fail = []
const check = (ok, msg) => {
  if (!ok) fail.push(msg)
}

const browser = await chromium.launch({ executablePath: CHROMIUM, headless: true })
try {
  for (const locale of ["en", "ar"]) {
    const cat = await (await fetch(`${web}/api/mcp/docs?lang=${locale}`)).json()
    for (const theme of ["light", "dark"]) {
      for (const width of [1440, 390]) {
        const tag = `${locale}/${theme}/${width}`
        const ctx = await browser.newContext({
          viewport: { width, height: 900 },
          colorScheme: theme,
        })
        const page = await ctx.newPage()
        const errors = []
        page.on("pageerror", (e) => errors.push(String(e)))
        await page.goto(`${web}/${locale}/docs/mcp`, { waitUntil: "load" })
        await page.waitForSelector("[data-docs-tool]", { timeout: 60000 })
        await page.waitForTimeout(1500)

        const info = await page.evaluate(() => ({
          dir: document.documentElement.dir,
          dark: document.documentElement.classList.contains("dark"),
          overflow: document.documentElement.scrollWidth - window.innerWidth,
          title: document.title,
          tools: [...document.querySelectorAll("[data-docs-tool]")].map((e) =>
            e.getAttribute("data-docs-tool"),
          ),
          scopes: document.querySelectorAll("[data-docs-scopes] > li").length,
          safety: document.querySelectorAll("[data-docs-safety] > li").length,
          h1: document.querySelector("h1")?.textContent ?? "",
        }))
        check(info.dir === (locale === "ar" ? "rtl" : "ltr"), `${tag}: dir=${info.dir}`)
        check(info.dark === (theme === "dark"), `${tag}: dark=${info.dark}`)
        check(info.overflow <= 0, `${tag}: overflows sideways by ${info.overflow}px`)
        check(
          info.tools.length === cat.tool_count,
          `${tag}: ${info.tools.length} tools on the page, API documents ${cat.tool_count}`,
        )
        check(
          info.scopes === cat.scopes.length,
          `${tag}: ${info.scopes} scopes listed, want ${cat.scopes.length}`,
        )
        check(info.safety >= 10, `${tag}: ${info.safety} safety rules`)
        check(/MCP/.test(info.title), `${tag}: title ${info.title}`)

        // A tool opens to its arguments, example call and example result.
        await page
          .locator('[data-docs-tool="create_contact"] [data-slot="collapsible-trigger"]')
          .click()
        await page.waitForTimeout(400)
        const opened = await page.evaluate(() => {
          const li = document.querySelector('[data-docs-tool="create_contact"]')
          return { code: li?.querySelectorAll("code").length ?? 0, text: li?.textContent ?? "" }
        })
        check(
          opened.code >= 2 && opened.text.includes('"tools/call"') && opened.text.includes("email"),
          `${tag}: create_contact did not open to its example`,
        )

        const shot = `${SHOTS}/${card}-${locale}-${theme}-${width}.png`
        await page.screenshot({ path: shot, fullPage: true })

        // The filter narrows the list.
        await page.locator('input[type="search"]').fill("whatsapp")
        await page.waitForTimeout(300)
        const filtered = await page.evaluate(
          () => document.querySelectorAll("[data-docs-tool]").length,
        )
        check(
          filtered > 0 && filtered < cat.tool_count,
          `${tag}: filter "whatsapp" left ${filtered} tools`,
        )

        check(errors.length === 0, `${tag}: page errors ${errors.join(" | ")}`)
        console.log(
          `${tag}: ${info.tools.length} tools, filter→${filtered}, overflow ${info.overflow}, "${info.title}" → ${shot}`,
        )
        await ctx.close()
      }
    }

    // A deep link opens its tool.
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    const page = await ctx.newPage()
    await page.goto(`${web}/${locale}/docs/mcp#tool-send_whatsapp`, { waitUntil: "load" })
    await page.waitForSelector("[data-docs-tool]", { timeout: 60000 })
    await page.waitForTimeout(1500)
    const deep = await page.evaluate(() =>
      document
        .querySelector('[data-docs-tool="send_whatsapp"] [data-slot="collapsible-trigger"]')
        ?.getAttribute("aria-expanded"),
    )
    check(deep === "true", `${locale}: #tool-send_whatsapp did not open it (aria-expanded=${deep})`)
    await page.screenshot({ path: `${SHOTS}/${card}-${locale}-deeplink-1440.png` })
    await ctx.close()
  }
} finally {
  await browser.close()
}

if (fail.length) {
  console.error("FAIL\n" + fail.join("\n"))
  process.exit(1)
}
console.log("OK")
