/*
Finding the thing a step is talking about (MH-478, ported from fadymondy.com's
driver/locate.js).

THE ORDER IS PART OF THE CONTRACT, because a workflow's author has to be able
to predict which element a selector will hit:

  1. An explicit prefix always wins and nothing else is tried:
       css=.results a      text=Add to cart      role=button[name="Save"]
  2. Without a prefix: CSS first (if it parses), then exact visible text, then
     ARIA role + accessible name.
  3. Within a strategy, the FIRST match in document order wins, and only
     elements that are visible and not disabled are considered — an offscreen
     duplicate in a hidden menu is not what anyone meant.

One addition to that rule, for plain CSS only: when no element matching the
selector is visible, the first one in document order is still used. That is
what every recorded workflow before this driver was replayed with
(document.querySelector), and a step that worked yesterday must not stop
working because the rule got stricter. Text and role selectors — new with this
driver — stay visible-only. `visible` in the answer says which it was, and a
real pointer cannot be aimed at an element with no box: the debugger path
refuses one (driver.js).

It only READS, with one exception: given a `mark`, it tags the element it found
with data-moh-t so the NEXT injection — the one that acts — finds exactly this
element without resolving the selector a second time. The next locate in the
same page removes it.
*/

/**
 * Injected into the page. Self-contained on purpose: it is serialised across
 * the boundary and cannot close over anything here.
 */
export function findInPage(selector, mark) {
  const trimmed = String(selector || "").trim()
  if (!trimmed) return { found: false, reason: "this step has no selector" }

  const shown = (el) => {
    if (!(el instanceof Element)) return false
    if (el.hasAttribute("disabled") || el.getAttribute("aria-disabled") === "true") return false
    const style = getComputedStyle(el)
    if (style.visibility === "hidden" || style.display === "none" || style.opacity === "0") return false
    const rect = el.getBoundingClientRect()
    return rect.width > 0 && rect.height > 0
  }

  let cssParsed = true
  const byCss = (css, anyAsLastResort) => {
    let all
    try {
      all = [...document.querySelectorAll(css)]
    } catch {
      cssParsed = false
      return null // not CSS; the caller falls through to the next strategy
    }
    return all.find(shown) || (anyAsLastResort ? all[0] || null : null)
  }

  const textOf = (el) => (el.innerText || el.textContent || "").replace(/\s+/g, " ").trim()
  const depth = (el) => {
    let n = 0
    for (let p = el; p; p = p.parentElement) n++
    return n
  }

  const byText = (needle) => {
    const wanted = needle.replace(/\s+/g, " ").trim().toLowerCase()
    if (!wanted) return null
    const candidates = [...document.querySelectorAll("button,a,[role],label,summary,input,td,th,li,span,div,p,h1,h2,h3,h4")]
    // Exact first, so "Save" does not land on "Save and continue".
    const exact = candidates.find((el) => shown(el) && textOf(el).toLowerCase() === wanted)
    if (exact) return exact
    const partial = candidates.filter((el) => shown(el) && textOf(el).toLowerCase().includes(wanted))
    // The innermost element that still contains the text, not <body>.
    return partial.sort((a, b) => depth(b) - depth(a))[0] || null
  }

  const accessibleName = (el) =>
    (
      el.getAttribute("aria-label") ||
      (el.getAttribute("aria-labelledby") && document.getElementById(el.getAttribute("aria-labelledby"))?.innerText) ||
      (el.labels && el.labels[0]?.innerText) ||
      el.getAttribute("title") ||
      el.getAttribute("placeholder") ||
      el.getAttribute("alt") ||
      el.value ||
      textOf(el)
    )
      ?.replace(/\s+/g, " ")
      .trim() || ""

  const implicitRole = (el) => {
    const tag = el.tagName.toLowerCase()
    if (tag === "a" && el.hasAttribute("href")) return "link"
    if (tag === "button") return "button"
    if (tag === "select") return "combobox"
    if (tag === "textarea") return "textbox"
    if (tag === "img") return "img"
    if (/^h[1-6]$/.test(tag)) return "heading"
    if (tag === "input") {
      const type = (el.getAttribute("type") || "text").toLowerCase()
      if (type === "checkbox") return "checkbox"
      if (type === "radio") return "radio"
      if (["submit", "button", "reset", "image"].includes(type)) return "button"
      if (type === "search") return "searchbox"
      return "textbox"
    }
    return null
  }

  const byRole = (role, name) => {
    const wanted = (name || "").replace(/\s+/g, " ").trim().toLowerCase()
    const all = [...document.querySelectorAll("*")].filter(shown)
    const roleOf = (el) => el.getAttribute("role") || implicitRole(el)
    return (
      all.find((el) => roleOf(el) === role && (!wanted || accessibleName(el).toLowerCase() === wanted)) ||
      all.find((el) => roleOf(el) === role && wanted && accessibleName(el).toLowerCase().includes(wanted)) ||
      null
    )
  }

  let element = null
  let strategy = ""
  if (trimmed.startsWith("css=")) {
    strategy = "css"
    element = byCss(trimmed.slice(4), true)
  } else if (trimmed.startsWith("text=")) {
    strategy = "text"
    element = byText(trimmed.slice(5))
  } else if (trimmed.startsWith("role=")) {
    strategy = "role"
    const m = /^role=([\w-]+)(?:\[name=["']?(.*?)["']?\])?$/.exec(trimmed)
    element = m ? byRole(m[1], m[2]) : null
  } else {
    strategy = "css"
    element = byCss(trimmed, false)
    if (!element) {
      element = byText(trimmed)
      if (element) strategy = "text"
    }
    if (!element && /^[a-z][\w-]*$/.test(trimmed)) {
      element = byRole(trimmed, "")
      if (element) strategy = "role"
    }
    // Last resort for plain CSS: the first match, shown or not (see above).
    if (!element && cssParsed) {
      element = byCss(trimmed, true)
      if (element) strategy = "css"
    }
  }

  if (!element) return { found: false, reason: `nothing on the page matches ${trimmed}` }

  if (mark) {
    // One tagged element per page at a time: the previous step's tag goes.
    for (const old of document.querySelectorAll("[data-moh-t]")) old.removeAttribute("data-moh-t")
    element.setAttribute("data-moh-t", mark)
  }
  const rect = element.getBoundingClientRect()
  return {
    found: true,
    strategy,
    visible: shown(element),
    tag: element.tagName.toLowerCase(),
    type: element.getAttribute("type") || null,
    text: textOf(element).slice(0, 120),
    rect: { x: rect.x, y: rect.y, width: rect.width, height: rect.height },
    inViewport:
      rect.top >= 0 && rect.left >= 0 && rect.bottom <= (window.innerHeight || 0) && rect.right <= (window.innerWidth || 0),
    viewport: { width: window.innerWidth, height: window.innerHeight },
    scrollY: window.scrollY,
  }
}
