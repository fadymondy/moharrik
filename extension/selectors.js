/*
Turning an element into a selector that will still find it tomorrow (MH-235).

A recorded step is only worth keeping if it replays. The generator below is
ordered by how long a selector tends to survive, not by how short it is:

  1. #id                  — when the id looks authored rather than generated
  2. [name="..."]         — form fields keep their name across redeploys
  3. [data-testid] etc.   — put there on purpose, for exactly this
  4. [aria-label="..."]   — the accessible name, which is user-visible copy
  5. a short structural path with :nth-of-type

Framework-generated ids and classes are skipped. A Tailwind/CSS-module class
like `css-1x7fk9p` or an id like `:r3h:` changes on the next build, so a
selector built from one records a step that works this afternoon and fails
next week — the worst kind of failure, because nobody connects it to a deploy.

Injected into the page by chrome.scripting, so it is a self-contained function
with no imports: it installs globalThis.__mohSelectors for the recorder to use.
*/

export function installSelectors() {
  if (globalThis.__mohSelectors) return true

  // React/Emotion/CSS-modules style: a word with a run of random-looking
  // characters, or one of the known generated prefixes.
  const GENERATED = /(^|[-_])(?:css|sc|jsx|emotion|chakra|mui)[-_]|^:r[0-9a-z]+:$|[0-9a-f]{6,}|\d{4,}/i
  const STABLE_ATTRS = ["data-testid", "data-test-id", "data-test", "data-cy", "data-qa"]

  const looksGenerated = (value) => !value || GENERATED.test(value)

  const cssEscape = (value) =>
    globalThis.CSS && CSS.escape ? CSS.escape(value) : String(value).replace(/["\\]/g, "\\$&")

  const quote = (value) => `"${String(value).replace(/["\\]/g, "\\$&")}"`

  /** Is this selector unique on the page right now? */
  const unique = (selector, el) => {
    try {
      const found = document.querySelectorAll(selector)
      return found.length === 1 && found[0] === el
    } catch {
      return false
    }
  }

  /** One element's own best selector, ignoring its ancestors. */
  const ownSelector = (el) => {
    const tag = el.tagName.toLowerCase()

    if (el.id && !looksGenerated(el.id)) {
      const candidate = `#${cssEscape(el.id)}`
      if (unique(candidate, el)) return candidate
    }

    for (const attr of STABLE_ATTRS) {
      const value = el.getAttribute(attr)
      if (value) {
        const candidate = `${tag}[${attr}=${quote(value)}]`
        if (unique(candidate, el)) return candidate
      }
    }

    const name = el.getAttribute("name")
    if (name && !looksGenerated(name)) {
      const candidate = `${tag}[name=${quote(name)}]`
      if (unique(candidate, el)) return candidate
    }

    const label = el.getAttribute("aria-label")
    if (label && label.length < 60) {
      const candidate = `${tag}[aria-label=${quote(label)}]`
      if (unique(candidate, el)) return candidate
    }

    // An input's type narrows it usefully and never changes.
    if (tag === "input") {
      const type = el.getAttribute("type")
      if (type) {
        const candidate = `input[type=${quote(type)}]`
        if (unique(candidate, el)) return candidate
      }
    }

    const role = el.getAttribute("role")
    if (role) {
      const candidate = `${tag}[role=${quote(role)}]`
      if (unique(candidate, el)) return candidate
    }

    return null
  }

  /** Where this element sits among its same-tag siblings. */
  const nthOfType = (el) => {
    const tag = el.tagName.toLowerCase()
    let n = 1
    let sibling = el.previousElementSibling
    while (sibling) {
      if (sibling.tagName.toLowerCase() === tag) n++
      sibling = sibling.previousElementSibling
    }
    return `${tag}:nth-of-type(${n})`
  }

  /**
   * selectorFor returns the most durable selector it can find for el, or "" if
   * it cannot produce one that uniquely identifies it. An ambiguous selector is
   * worse than none: it replays against the wrong element silently.
   */
  const selectorFor = (el) => {
    if (!(el instanceof Element)) return ""

    const own = ownSelector(el)
    if (own) return own

    // Walk up, prefixing with each ancestor's own selector until the path is
    // unique. Bounded: a ten-deep selector is not durable either.
    const parts = [nthOfType(el)]
    let node = el.parentElement
    for (let depth = 0; node && depth < 5; depth++) {
      const anchor = ownSelector(node)
      if (anchor) {
        const candidate = `${anchor} > ${parts.join(" > ")}`
        if (unique(candidate, el)) return candidate
      }
      parts.unshift(nthOfType(node))
      const candidate = parts.join(" > ")
      if (unique(candidate, el)) return candidate
      node = node.parentElement
    }

    const full = parts.join(" > ")
    return unique(full, el) ? full : ""
  }

  globalThis.__mohSelectors = { selectorFor }
  return true
}
