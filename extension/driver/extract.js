/*
Reading data out of a page — the point of most runs (MH-478).

Two shapes, both injected into the page and therefore self-contained:

  structured  { fields: {name: selector}, list?: rowSelector, limit? }
              ported from fadymondy.com (FM-92, FM-281): one object, or one
              object per row. A field selector is `css` or `css|how`, where
              how is `@attr` (an attribute) or `html`; `@href`, `:scope@href`,
              `:scope` and `|@href` address the ROW ITSELF.
  legacy      { selector, all?, attribute? }
              what recorded and hand-written workflows have always used: one
              element's text (or an attribute), or every match's.

What comes back is the text exactly as the page renders it, with no
interpretation — the server stores it and a model reads it.
*/

/** The structured shape. Exported for the Node test (test/extract-row-self.test.mjs). */
export function extractInPage(spec) {
  const clean = (s) => (s || "").replace(/\s+/g, " ").trim()
  const read = (el, how) => {
    if (!el) return null
    if (how === "html") return el.innerHTML
    if (how && how.startsWith("@")) return el.getAttribute(how.slice(1))
    if (el.tagName === "INPUT" || el.tagName === "TEXTAREA") return el.value
    return clean(el.innerText || el.textContent)
  }
  const field = (base, selector) => {
    const s = String(selector).trim()
    let css, how
    if (s.startsWith("@")) [css, how] = ["", s]
    else if (s.startsWith(":scope@")) [css, how] = ["", s.slice(":scope".length)]
    else {
      const [c, h] = s.split("|")
      ;[css, how] = [c.trim(), h?.trim()]
    }
    const self = css === "" || css === ":scope"
    return read(self ? base : base.querySelector(css), how)
  }
  if (spec.list) {
    const rows = [...document.querySelectorAll(spec.list)]
    return {
      rows: rows.slice(0, spec.limit || 200).map((row) => {
        const out = {}
        for (const [name, selector] of Object.entries(spec.fields || {})) out[name] = field(row, selector)
        return out
      }),
      total: rows.length,
    }
  }
  const out = {}
  for (const [name, selector] of Object.entries(spec.fields || {})) out[name] = field(document.documentElement, selector)
  return { fields: out }
}

/** The legacy shape. */
export function extractLegacyInPage(sel, many, attr) {
  const read = (el) => (attr ? el.getAttribute(attr) : (el.innerText || el.textContent || "").trim())
  if (many) return { values: [...document.querySelectorAll(sel)].slice(0, 200).map(read) }
  const el = document.querySelector(sel)
  return el ? { value: read(el) } : null
}

/** Where the page is, for the result — read in the page, as fadymondy.com's extract does. */
export function pageInfoInPage() {
  return { url: location.href, title: document.title }
}
