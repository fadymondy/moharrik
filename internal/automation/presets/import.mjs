/*
Builds presets.json — Moharrik's preset workflow library — from fadymondy.com's
browser plugins (MH-478, phase 3a).

  node internal/automation/presets/import.mjs <plugins.json>

<plugins.json> is fadymondy.com's automation_plugins table as a JSON array
(name, slug, icon, colour, description, hosts, inputs, steps, version),
exported READ-ONLY from its production database. It is not committed: it
carries one person's name, phone numbers and CV. What this script writes is.

The plugins are copied faithfully — names, slugs, descriptions, sites, inputs,
steps, icons, and the "(publishes)" / "(submits)" / dry-run markers — with one
change: they are DE-PERSONALISED, never dropped.

  - "WhatsApp: send a message to Fady (own number only)" becomes a message to
    the WORKSPACE's own number. The number is put into the steps when the
    preset is added ({{workspace.whatsapp_number}}), so at run time there is
    still no recipient input — the guarantee the original was built around.
  - The CV-based job applications take their defaults from the WORKSPACE
    PROFILE ({{profile.first_name}}, …), filled in when the preset is added.
  - Everything else that names a person — "for Fady", a phone number, an
    account handle, a path on one machine — is reworded or removed.

Anything the rules below do not recognise stops the script, so a new plugin
with personal data in it cannot slip through: SCRUB must be empty afterwards.
*/
import fs from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

const here = path.dirname(fileURLToPath(import.meta.url))
const src = process.argv[2]
if (!src) {
  console.error("usage: node import.mjs <plugins.json>")
  process.exit(2)
}
const plugins = JSON.parse(fs.readFileSync(src, "utf8"))

/* ---------------------------------------------------------------- groups */

// The platform a preset belongs to, for the gallery's grouping.
function groupOf(slug) {
  const rules = [
    [/^youtube-/, "youtube"],
    [/^facebook-/, "facebook"],
    [/^instagram-/, "instagram"],
    [/^x-/, "x"],
    [/^linkedin-/, "linkedin"],
    [/^threads-/, "threads"],
    [/^bluesky-/, "bluesky"],
    [/^tiktok-/, "tiktok"],
    [/^gmail-/, "gmail"],
    [/^whatsapp-/, "whatsapp"],
    [/^telegram-/, "telegram"],
    [/^google-news-/, "google-news"],
    [/^google-trends-/, "google-trends"],
    [/^search-/, "search"],
    [/^(jobs?-)/, "jobs"],
    [/^(news-|devto-|medium-|techwd-|majarra-|hbr-|mittr-)/, "news"],
  ]
  for (const [re, g] of rules) if (re.test(slug)) return g
  throw new Error(`no group for ${slug}`)
}

/* ------------------------------------------------------ what a preset does */

// A preset that publishes, sends or submits on someone's behalf. The name
// says so on fadymondy.com in most cases; the description's "WRITE" marker
// catches the rest (a comment, a message, an email).
function writesOf(p) {
  if (/dry run|never posts/i.test(p.name)) return false
  return /\((publishes|submits)\)/i.test(p.name) || /^WRITE\b/.test(p.description) || /\b(create a post|comment on|post a video|send an email|send a message)\b/i.test(p.name)
}

/* ------------------------------------------------------- de-personalising */

const PROFILE_INPUTS = {
  first_name: "first_name",
  last_name: "last_name",
  email: "email",
  phone: "phone",
  location: "location",
  current_company: "current_company",
  linkedin: "linkedin",
  github: "github",
  website: "website",
  city: "city",
}

function scrubText(s) {
  return String(s || "")
    .replace(/Fady's own WhatsApp number \(\+?\d+, WHATSAPP in web\/lib\/contact\.ts\)/g, "the workspace's own WhatsApp number")
    .replace(/Fady's own number \(\+?\d+\)/g, "the workspace's own number")
    .replace(/\(signed in as \+?\d+\)/g, "")
    .replace(/web\.whatsapp\.com\/send\?phone=\d+/g, "web.whatsapp.com/send?phone=<the workspace's number>")
    .replace(/"Type a message to Fady Mondy"/g, '"Type a message …"')
    .replace(/"Fady Mondy" = from Fady/g, "the workspace's own name = sent by this account")
    .replace(/"Comment as Fady Mondy"/g, '"Comment as <your name>"')
    .replace(/Verified 2026-09-17 on @engfadymondy\./g, "Verified 2026-09-17.")
    .replace(/\(default "Cairo, Egypt", per Fady\)/g, "(from the workspace profile)")
    .replace(/, per Fady\)/g, ")")
    .replace(/\(per Fady: ([^)]*)\)/g, "($1)")
    .replace(/Fady: always ([^"]*)/g, "e.g. $1")
    .replace(/after Fady confirmed that exact share and only into groups he marked promotion=yes/g, "after a person confirmed that exact share, and only into groups marked for promotion")
    .replace(/Only the growth group-sharing queue runs it/g, "Run it only after a person has confirmed the exact share")
    .replace(/The growth module slices the rules from that marker\./g, "")
    .replace(/Fady's LinkedIn profile/g, "the signed-in LinkedIn profile")
    .replace(/Fady's network/g, "the account's network")
    .replace(/a job Fady has chosen/g, "a job a person has chosen")
    .replace(/so Fady can see/g, "so a person can see")
    .replace(/left EMPTY for Fady/g, "left EMPTY for a person")
    .replace(/left for Fady/g, "left for a person")
    .replace(/need Fady\)/g, "need a person)")
    .replace(/what is left for Fady/g, "what is left for a person")
    .replace(/from the published CV/gi, "from the workspace profile")
    .replace(/the CV does not cover/g, "the profile does not cover")
    .replace(/CV-derived defaults/g, "defaults from the workspace profile")
    .replace(/fills the mobile phone from the CV/g, "fills the mobile phone from the workspace profile")
    .replace(/on the Windows browser/g, "in the browser")
    .replace(/\s+/g, " ")
    .trim()
}

function scrubInput(slug, input) {
  const out = { ...input }
  // CV-derived defaults come from the workspace profile, filled when added.
  if (/^(job-apply-|linkedin-easy-apply)/.test(slug) && PROFILE_INPUTS[out.name] && out.name !== "city") {
    out.default = `{{profile.${PROFILE_INPUTS[out.name]}}}`
    out.help = "From the workspace profile"
  }
  if (out.name === "city" && /Cairo/.test(out.default || "")) {
    out.default = "{{profile.city}}"
    out.help = "Location (city) typeahead, from the workspace profile"
  }
  if (out.name === "cv_path") {
    out.help = "The CV PDF on the browser's computer, inside the folder the extension allows uploads from (its Sites page)"
  }
  if (out.name === "phone" && slug === "whatsapp-send") out.help = "Digits with country code, no + (e.g. 201050028002)"
  if (out.label) out.label = scrubText(out.label)
  if (out.help) out.help = scrubText(out.help)
  return out
}

function scrubSteps(slug, steps) {
  const raw = JSON.stringify(steps)
    .replaceAll("https://web.whatsapp.com/send?phone=201207860084", "https://web.whatsapp.com/send?phone={{workspace.whatsapp_number}}")
    // The composer of one's own chat is labelled with one's own name; the
    // fixed number in the URL is the guard, and the prefix matches it.
    .replaceAll('[aria-label=\\"Type a message to Fady Mondy\\"]', '[aria-label^=\\"Type a message\\"]')
  const out = JSON.parse(raw)
  // fadymondy.com's type appends unless the step says clear: true; Moharrik's
  // replaces unless it says clear: false. Said explicitly, so each preset
  // types exactly as it did there.
  for (const st of out) {
    if (st.command === "type" && st.args?.selector && st.args.clear === undefined) st.args.clear = false
  }
  return out
}

const NAMES = {
  "whatsapp-send-self": "WhatsApp: send a message to the workspace's own number (own number only)",
  "whatsapp-read-self": "WhatsApp: read the workspace's own chat",
}

/* ------------------------------------------------------------------ build */

const out = plugins.map((p) => {
  const preset = {
    slug: p.slug,
    name: NAMES[p.slug] || scrubText(p.name),
    group: groupOf(p.slug),
    icon: p.icon,
    colour: p.colour,
    description: scrubText(p.description),
    hosts: p.hosts,
    inputs: (p.inputs || []).map((i) => scrubInput(p.slug, i)),
    steps: scrubSteps(p.slug, p.steps),
    writes: writesOf(p),
    source_version: p.version,
  }
  // A dry-run twin names the preset it rehearses.
  if (/-dryrun$/.test(p.slug)) preset.dry_run_of = p.slug.replace(/-dryrun$/, "")
  if (plugins.some((q) => q.slug === `${p.slug}-dryrun`)) preset.dry_run = `${p.slug}-dryrun`
  return preset
})

// Nothing personal may survive. A new plugin that trips this needs a rule above.
const SCRUB = /Fady|fadymondy|201207860084|201098937831|3x1\.io|engfadymondy|E:\\\\Sites|One Studio|Heliopolis/
const leaks = out.filter((p) => SCRUB.test(JSON.stringify(p))).map((p) => `${p.slug}: ${JSON.stringify(p).match(SCRUB)[0]}`)
if (leaks.length) {
  console.error("personal data left in:\n" + leaks.join("\n"))
  process.exit(1)
}

const file = { version: 1, source: "fadymondy.com automation_plugins (production), exported 2026-09-23", presets: out }
fs.writeFileSync(path.join(here, "presets.json"), JSON.stringify(file, null, 1) + "\n")
console.log(`wrote ${out.length} presets (${out.filter((p) => p.writes).length} write) to presets.json`)
