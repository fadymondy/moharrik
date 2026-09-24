// MH-478: Application autofill, generalised from fadymondy.com's job-board
// readers (FM-143/199). readForm and fillMatched run against real jsdom, the
// way chrome.scripting actually serialises and injects them; matchProfileToFields
// is pure and tested directly.

import { test } from "node:test"
import assert from "node:assert/strict"
import { JSDOM } from "jsdom"

import { fillMatched, matchProfileToFields, readForm } from "../driver/autofill.js"

const FORM = `<!doctype html><html><body>
<form>
  <label for="fname">First name</label>
  <input id="fname" name="fname">

  <label for="lname">Last name</label>
  <input id="lname" name="lname">

  <label for="em">Work email</label>
  <input id="em" type="email" autocomplete="email">

  <span id="phone-label">Mobile number</span>
  <input id="phone" aria-labelledby="phone-label">

  <input id="li" autocomplete="url" placeholder="LinkedIn profile URL">

  <input id="site" autocomplete="url" placeholder="Personal site">

  <div>
    <label>City
      <select id="city">
        <option value="">Choose…</option>
        <option value="cai">Cairo</option>
        <option value="alx">Alexandria</option>
      </select>
    </label>
  </div>

  <label for="already">Already filled</label>
  <input id="already" value="Mona Existing">

  <label for="cover">Cover letter</label>
  <textarea id="cover"></textarea>

  <input id="cv" type="file">
  <label><input type="checkbox" id="consent"> I agree</label>
  <label><input type="radio" name="wa" id="wa-yes"> Yes</label>

  <button type="submit" id="submit-btn">Submit application</button>
</form>
</body></html>`

/** A page, with every element given a non-zero box so readForm's visible() check passes. */
function page(html) {
  const dom = new JSDOM(html, { url: "https://boards.example/apply", runScripts: "outside-only", pretendToBeVisual: true })
  const w = dom.window
  w.Element.prototype.getBoundingClientRect = function () {
    return { x: 0, y: 0, left: 0, top: 0, width: 100, height: 20, right: 100, bottom: 20 }
  }
  return w
}

/** Run an injected function the way chrome.scripting does: source in, clone out. */
function inject(w, func, args) {
  const fn = w.eval(`(${func.toString()})`)
  const result = fn(...args)
  return result === undefined ? undefined : JSON.parse(JSON.stringify(result))
}

const PROFILE = {
  first_name: "Mona",
  last_name: "Adel",
  email: "mona@company.test",
  phone: "+20 100 000 0001",
  city: "Cairo",
  linkedin: "linkedin.com/in/mona",
  github: "github.com/mona",
  website: "mona.dev",
}

test("readForm finds fields by label, aria-labelledby and placeholder, and never file/checkbox/radio/submit", () => {
  const w = page(FORM)
  const form = inject(w, readForm, [])
  const byId = Object.fromEntries(form.fields.map((f) => [f.sel, f]))
  assert.equal(byId["#fname"].label, "First name")
  assert.equal(byId["#lname"].label, "Last name")
  assert.equal(byId["#em"].label, "Work email")
  assert.equal(byId["#em"].autocomplete, "email")
  assert.equal(byId["#phone"].label, "Mobile number")
  assert.equal(byId["#li"].label, "LinkedIn profile URL")
  assert.equal(byId["#city"].kind, "select")
  assert.deepEqual(byId["#city"].options, ["Choose…", "Cairo", "Alexandria"])
  assert.equal(byId["#already"].hasValue, true)
  // Never read as fillable: a file input, a checkbox, a radio, or any button.
  for (const sel of ["#cv", "#consent", "#wa-yes", "#submit-btn"]) {
    assert.ok(!(sel in byId), `${sel} should not be read as a fillable field`)
  }
})

test("matchProfileToFields prefers the human label over a generic autocomplete=url, and composes a full name", () => {
  const w = page(FORM)
  const form = inject(w, readForm, [])
  const matches = matchProfileToFields(form.fields, PROFILE)
  const bySel = Object.fromEntries(matches.map((m) => [m.sel, m]))
  assert.equal(bySel["#fname"].value, "Mona")
  assert.equal(bySel["#lname"].value, "Adel")
  assert.equal(bySel["#em"].value, "mona@company.test")
  assert.equal(bySel["#phone"].value, "+20 100 000 0001")
  // Both #li and #site carry autocomplete="url"; the label tells them apart.
  assert.equal(bySel["#li"].value, "linkedin.com/in/mona")
  assert.equal(bySel["#li"].profileKey, "linkedin")
  assert.equal(bySel["#site"].value, "mona.dev")
  assert.equal(bySel["#site"].profileKey, "website")
  // The select is matched by its visible option text, not the profile's raw value.
  assert.equal(bySel["#city"].value, "Cairo")
  // Already has a value: never offered.
  assert.ok(!("#already" in bySel))
  // Nothing to say about a cover letter from a name/contact profile.
  assert.ok(!("#cover" in bySel))

  const withFullName = matchProfileToFields(
    [{ sel: "#name", label: "Your Name", kind: "text", autocomplete: "", hasValue: false }],
    PROFILE,
  )
  assert.equal(withFullName[0].value, "Mona Adel")
  assert.equal(withFullName[0].profileKey, "full_name")
})

test("fillMatched sets real values through the native setter, skips a field someone typed into meanwhile, and never touches a button", () => {
  const w = page(FORM)
  const form = inject(w, readForm, [])
  const matches = matchProfileToFields(form.fields, PROFILE)

  // Between preview and fill, the applicant typed into the email field by hand.
  w.document.getElementById("em").value = "typed-by-hand@x.test"

  const results = inject(w, fillMatched, [matches])
  const bySel = Object.fromEntries(results.map((r) => [r.sel, r]))
  assert.equal(bySel["#fname"].ok, true)
  assert.equal(w.document.getElementById("fname").value, "Mona")
  assert.equal(bySel["#em"].ok, false)
  assert.equal(bySel["#em"].reason, "already-has-value")
  assert.equal(w.document.getElementById("em").value, "typed-by-hand@x.test") // untouched
  assert.equal(w.document.getElementById("city").value, "cai")

  let clicked = false
  w.document.getElementById("submit-btn").addEventListener("click", () => (clicked = true))
  assert.ok(!clicked, "fillMatched must never click anything, least of all Submit")

  // input/change events actually fired, the way a React-controlled field needs
  // — checked on a fresh field, since #lname was just filled above.
  let sawInput = false
  w.document.getElementById("cover").addEventListener("input", () => (sawInput = true))
  inject(w, fillMatched, [[{ sel: "#cover", kind: "textarea", value: "hi" }]])
  assert.ok(sawInput)
})

test("a field removed since preview, and an option gone from a select, are reported and skipped — not thrown", () => {
  const w = page(FORM)
  w.document.getElementById("fname").remove()
  const results = inject(w, fillMatched, [
    [{ sel: "#fname", kind: "text", value: "Mona" }, { sel: "#city", kind: "select", value: "Nowhere" }],
  ])
  assert.equal(results[0].reason, "removed")
  assert.equal(results[1].reason, "option-gone")
})
