/*
Application autofill (MH-478), generalised from fadymondy.com's job-board
readers (FM-143/199, driver/apply.js there).

fadymondy.com reads Greenhouse, Lever, Ashby and Workable by name, each with
its own label-finding rules, then asks the site to PLAN which profile field
goes where. This is the generalised version Fady asked for: one reader that
works on any form, on any allowed site, matching the workspace profile by
label and autocomplete keywords — no board list, no server round trip, no
plan step. The trade a generaliser makes is precision for reach; a board with
an unusual layout gets a shorter preview rather than a wrong fill, because
nothing is EVER filled without first being named in the preview the person
reviews.

Three rules carried over unchanged from fadymondy.com's apply.js, because they
are what makes autofill safe to offer at all:

  THE CURRENT TAB ONLY. This never opens a tab, navigates one, or reaches past
  the one the person already has open — the same tab id from popup.js's
  `chrome.tabs.query({active:true})` all the way through.

  A FIELD THAT ALREADY HAS AN ANSWER IS NEVER TOUCHED. Checked once for the
  preview and again, per field, right before it is filled — someone's own
  typing is never overwritten by a stale preview.

  NOTHING IS EVER SUBMITTED. No submit button, no Enter key, no click on
  anything. Filling in real form fields with real events (input/change) is as
  far as this goes; the person reviews the page and presses Submit themselves.

readForm and fillMatched are INJECTED (chrome.scripting.executeScript), so
each is self-contained: no imports, no closure over anything outside itself.
matchProfileToFields runs in the extension's own context and is safe to
import and unit-test directly.
*/

// The kinds a value can go into. Never file (no attaching a résumé from here
// — that stays the extension's `upload` step, gated by the folder the person
// named on the Sites page) and never checkbox/radio (a profile value is
// text; a consent or a Yes/No choice is a decision, not a fact to copy in).
const FILLABLE_KINDS = new Set(["text", "email", "tel", "url", "textarea", "select"])

/**
 * Injected. Reads every visible, fillable field on the page: a selector
 * durable enough to refill later (an id or a name — nothing built from
 * position, which breaks the moment the page re-renders), its best-guess
 * label, its kind, and whether it already holds an answer.
 */
export function readForm() {
  // CSS.escape is not implemented everywhere this function might run (a test
  // harness, an older embedder) — a plain fallback keeps it self-contained.
  const esc = (s) => (typeof CSS !== "undefined" && CSS.escape ? CSS.escape(s) : String(s).replace(/[^a-zA-Z0-9_-]/g, (c) => "\\" + c))
  const clean = (s) => String(s || "").replace(/\s+/g, " ").trim()
  const visible = (el) => {
    if (!el || el.disabled) return false
    const r = el.getBoundingClientRect()
    const s = getComputedStyle(el)
    return r.width > 0 && r.height > 0 && s.visibility !== "hidden" && s.display !== "none"
  }
  const labelFor = (el) => {
    if (el.id) {
      const direct = document.querySelector(`label[for="${esc(el.id)}"]`)
      // innerText respects layout (so a visually-hidden "*" is not read as
      // the label); textContent is the fallback where innerText is not
      // implemented at all — some embedders, and jsdom in the test harness.
      const text0 = direct && (direct.innerText || direct.textContent)
      if (text0 && clean(text0)) return clean(text0)
    }
    const aria = el.getAttribute("aria-label")
    if (aria && clean(aria)) return clean(aria)
    const labelledby = el.getAttribute("aria-labelledby")
    if (labelledby) {
      const text = labelledby
        .split(/\s+/)
        .map((id) => {
          const node = document.getElementById(id)
          return (node && (node.innerText || node.textContent)) || ""
        })
        .join(" ")
      if (clean(text)) return clean(text)
    }
    const wrap = el.closest("label")
    if (wrap) {
      const copy = wrap.cloneNode(true)
      copy.querySelectorAll("input, textarea, select").forEach((n) => n.remove())
      if (clean(copy.textContent)) return clean(copy.textContent)
    }
    if (el.placeholder && clean(el.placeholder)) return clean(el.placeholder)
    // The nearest label-shaped text just before the field, inside its own
    // wrapper — most form builders put the question directly above the box.
    const wrapper = el.closest("div, li, fieldset") || el.parentElement
    if (wrapper) {
      for (const node of wrapper.querySelectorAll("label, span, p, legend")) {
        if (node.contains(el)) continue
        const text = clean(node.textContent)
        if (text && text.length < 120) return text
      }
    }
    return ""
  }
  const fields = []
  const seen = new Set()
  for (const el of document.querySelectorAll("input, textarea, select")) {
    if (fields.length >= 250) break
    const type = (el.getAttribute("type") || "text").toLowerCase()
    if (["hidden", "submit", "button", "image", "reset", "search", "file", "checkbox", "radio", "password"].includes(type)) continue
    if (!visible(el)) continue
    let kind
    if (el.tagName === "SELECT") kind = "select"
    else if (el.tagName === "TEXTAREA") kind = "textarea"
    else if (["tel", "email", "url"].includes(type)) kind = type
    else kind = "text"
    let sel = null
    if (el.id) sel = `#${esc(el.id)}`
    else if (el.name) sel = `[name="${esc(el.name)}"]`
    if (!sel || seen.has(sel)) continue // nothing durable enough to refill by → skip, rather than guess
    seen.add(sel)
    const hasValue =
      kind === "select" ? Boolean(el.value && el.selectedIndex > 0) : Boolean(String(el.value || "").trim())
    fields.push({
      sel,
      label: labelFor(el),
      kind,
      required: el.required || el.getAttribute("aria-required") === "true",
      autocomplete: (el.getAttribute("autocomplete") || "").toLowerCase(),
      hasValue,
      options: kind === "select" ? [...el.options].map((o) => clean(o.textContent)).filter(Boolean).slice(0, 60) : undefined,
    })
  }
  return { url: location.href, title: clean(document.title), fields }
}

// Autocomplete tokens (the HTML standard's own vocabulary) map straight to a
// profile field — the strongest signal a page can give, when it gives one.
const AUTOCOMPLETE_MAP = {
  "given-name": "first_name",
  "additional-name": "first_name",
  "family-name": "last_name",
  email: "email",
  tel: "phone",
  "tel-national": "phone",
  url: "website",
  "address-level2": "city",
  organization: "current_company",
  "organization-title": "current_company",
}

// Label keywords, tried in order — the first that matches wins. Word
// boundaries throughout, so "email" does not also catch "emailing preference".
const LABEL_PATTERNS = [
  ["email", /\be-?mail\b/i],
  ["phone", /\b(phone|mobile|cell)(\s*number)?\b/i],
  ["linkedin", /linkedin/i],
  ["github", /\bgit\s*hub\b/i],
  ["website", /\b(website|portfolio|personal\s*site)\b/i],
  ["current_company", /\b(current\s+)?(company|employer|organi[sz]ation)\b/i],
  ["first_name", /\b(first|given)\s*name\b/i],
  ["last_name", /\b(last|family|sur)\s*name\b/i],
  ["city", /\bcity\b/i],
  ["location", /\b(location|address|based\s+in)\b/i],
]

const FULL_NAME_RE = /^(your\s+)?(full\s+)?name$/i

/**
 * Matches the workspace profile onto a form's fields. Pure and importable —
 * unit-tested directly, no page to inject into. Returns one row per FILLED
 * candidate: what it would put where, for the preview to show before
 * anything runs, and for fillMatched to act on once it is confirmed.
 */
export function matchProfileToFields(fields, profile) {
  const p = profile || {}
  const out = []
  for (const f of fields || []) {
    if (!FILLABLE_KINDS.has(f.kind) || f.hasValue) continue
    // The label a human wrote is tried first — it is what tells "LinkedIn"
    // apart from "GitHub" apart from a plain "Website", all of which a form
    // may equally mark autocomplete="url". Autocomplete is the fallback, for
    // the (common) field with no readable label at all.
    let key = null
    for (const [k, re] of LABEL_PATTERNS) {
      if (re.test(f.label)) {
        key = k
        break
      }
    }
    if (!key) key = AUTOCOMPLETE_MAP[f.autocomplete] || null
    let value = key ? p[key] : ""
    // "Name" alone, with no separate first/last match: the two combined.
    if (!value && FULL_NAME_RE.test(f.label) && (p.first_name || p.last_name)) {
      key = "full_name"
      value = [p.first_name, p.last_name].filter(Boolean).join(" ")
    }
    if (!key || !String(value || "").trim()) continue
    if (f.kind === "select") {
      const needle = String(value).trim().toLowerCase()
      const match = (f.options || []).find((o) => o.toLowerCase() === needle) || (f.options || []).find((o) => o.toLowerCase().includes(needle))
      if (!match) continue // no option reads like the profile's value — left for the person, not guessed at
      value = match
    }
    out.push({ sel: f.sel, label: f.label, kind: f.kind, profileKey: key, value: String(value) })
  }
  return out
}

/**
 * Injected. Fills exactly the matches given — the ones the person reviewed
 * and confirmed in the preview, nothing recomputed here. Checks each field's
 * current value again right before touching it, so a field the person
 * started typing into between preview and Fill keeps what they typed.
 */
export function fillMatched(matches) {
  const results = []
  for (const m of matches || []) {
    const el = document.querySelector(m.sel)
    if (!el) {
      results.push({ sel: m.sel, ok: false, reason: "removed" })
      continue
    }
    const already =
      m.kind === "select" ? Boolean(el.value && el.selectedIndex > 0) : Boolean(String(el.value || "").trim())
    if (already) {
      results.push({ sel: m.sel, ok: false, reason: "already-has-value" })
      continue
    }
    if (m.kind === "select") {
      const opt = [...el.options].find((o) => o.textContent.replace(/\s+/g, " ").trim() === m.value)
      if (!opt) {
        results.push({ sel: m.sel, ok: false, reason: "option-gone" })
        continue
      }
      el.value = opt.value
      el.dispatchEvent(new Event("change", { bubbles: true }))
      results.push({ sel: m.sel, ok: true })
      continue
    }
    el.focus()
    // Set through the native setter and fire input/change, or a
    // React-controlled field ignores the value entirely.
    const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
    const setter = Object.getOwnPropertyDescriptor(proto, "value")?.set
    if (setter) setter.call(el, m.value)
    else el.value = m.value
    el.dispatchEvent(new Event("input", { bubbles: true }))
    el.dispatchEvent(new Event("change", { bubbles: true }))
    el.blur()
    results.push({ sel: m.sel, ok: true })
  }
  return results
}
