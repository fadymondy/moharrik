// FM-281: extract can read the row element's own attributes and text.
//
//   node --test extension/test/
//
// extractInPage runs inside the page, so it is exercised here against a tiny
// fake DOM installed on globalThis.document. extract.js is pure
// and its imports register chrome listeners at load, so `chrome` is a stub that
// absorbs any property access or call.
import { test, before } from "node:test"
import assert from "node:assert/strict"

const absorb = new Proxy(function () {}, {
  get: (_t, k) => (k === "then" ? undefined : absorb),
  apply: () => absorb,
})
globalThis.chrome ??= absorb

let extractInPage
before(async () => {
  ;({ extractInPage } = await import("../driver/extract.js"))
})

/** A minimal element: tag, attributes, text, and children found by class. */
function el(tag, attrs = {}, text = "", children = []) {
  return {
    tagName: tag.toUpperCase(),
    innerText: text,
    textContent: text,
    innerHTML: `<${tag}>${text}</${tag}>`,
    getAttribute: (k) => (k in attrs ? attrs[k] : null),
    // Real browsers: querySelector(":scope") matches descendants only, so null.
    querySelector: (css) => children.find((c) => css === "." + c.cls) ?? null,
  }
}

function page(rows) {
  globalThis.document = {
    documentElement: el("html", { lang: "en" }, "whole page"),
    querySelectorAll: (css) => (css === "a.job" ? rows : []),
    querySelector: () => null,
  }
}

function job(href, title) {
  const t = Object.assign(el("h3", {}, title), { cls: "title" })
  return el("a", { href, class: "job" }, `  ${title}\n  Remote `, [t])
}

test("a row that is itself the link returns its own href", () => {
  page([job("/jobs/1", "Go dev"), job("/jobs/2", "PHP dev")])
  const out = extractInPage({
    list: "a.job",
    fields: { url: "@href", url2: ":scope@href", url3: "|@href", title: ".title", text: ":scope" },
  })
  assert.equal(out.total, 2)
  assert.deepEqual(out.rows[0], { url: "/jobs/1", url2: "/jobs/1", url3: "/jobs/1", title: "Go dev", text: "Go dev Remote" })
  assert.equal(out.rows[1].url, "/jobs/2")
})

test("descendant selectors still work, and a missing one is null", () => {
  page([job("/jobs/1", "Go dev")])
  const out = extractInPage({ list: "a.job", fields: { title: ".title|html", nope: ".missing" } })
  assert.equal(out.rows[0].title, "<h3>Go dev</h3>")
  assert.equal(out.rows[0].nope, null)
})

test("outside a list, the row is the document element", () => {
  page([])
  const out = extractInPage({ fields: { lang: "@lang" } })
  assert.deepEqual(out.fields, { lang: "en" })
})
