/*
Package the extension for download (MH-399).

  node extension/build.mjs                                   # production: https://app.moharrik.com
  node extension/build.mjs --site https://staging.example    # another deployment
  node extension/build.mjs --site http://localhost:3506 --api http://localhost:8506 --dev

Chrome loads this directory as it is — nothing in it is compiled — so the build
is a zip of the directory, with three things a plain zip would not do:

  IT KEEPS THE HARNESS OUT. node_modules, the tests and the npm files are what
  `npm test` needs, not what a browser loads. They are excluded by rule rather
  than the extension files being listed, so a new driver file is shipped
  without anyone remembering to add it here.

  IT BAKES IN THE SITE when one is given. The site the extension talks to is
  written into config.js and the manifest's host_permissions, so a build for
  another deployment pairs with that deployment without the operator typing
  its address. A build pointed at localhost is refused without --dev: it would
  install, look fine, and never connect to anything.

  IT WRITES A CHECKSUM of the exact bytes written, beside the zip, in
  build.json. The API serves that number with the file and refuses to serve a
  file that no longer matches it — so the number on the install page is the
  number of the file it hands out.

No dependencies: a ZIP container is simple enough to write directly, and a
packaging step that needs `npm install` breaks in an image build the day a
registry is slow.
*/
import { createHash } from "node:crypto"
import { execFileSync, execSync } from "node:child_process"
import fs from "node:fs"
import os from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"
import zlib from "node:zlib"

const here = path.dirname(fileURLToPath(import.meta.url))

function arg(name, fallback = "") {
  const i = process.argv.indexOf(`--${name}`)
  return i > -1 && process.argv[i + 1] && !process.argv[i + 1].startsWith("--") ? process.argv[i + 1] : fallback
}
const flag = (name) => process.argv.includes(`--${name}`)

const PRODUCTION = "https://app.moharrik.com"
const site = arg("site", PRODUCTION).replace(/\/+$/, "")
const api = arg("api").replace(/\/+$/, "")
const out = path.resolve(arg("out", path.join(here, "dist")))
const dev = flag("dev")

if (!/^https?:\/\/[^/]+$/.test(site) || (api && !/^https?:\/\/[^/]+$/.test(api))) {
  console.error("build: --site and --api must be bare http(s) origins, e.g. --site https://app.moharrik.com")
  process.exit(2)
}
const local = /^https?:\/\/(localhost|127\.|0\.0\.0\.0|\[::1\])/i
if (!dev && (local.test(site) || (api && local.test(api)))) {
  console.error(`build: refusing to package a build pointed at ${site} — pass --dev for a local-only build`)
  process.exit(2)
}
if (!dev && (!site.startsWith("https://") || (api && !api.startsWith("https://")))) {
  console.error("build: a release build must use https — the socket carries the browser's token")
  process.exit(2)
}

/* ------------------------------------------------------------ the files */

// What stays behind. Everything else in the directory is the extension.
const EXCLUDE_DIRS = new Set(["node_modules", "test", "dist", ".git"])
const EXCLUDE_FILES = new Set(["package.json", "package-lock.json", "build.mjs", ".gitignore", ".DS_Store"])

function collect(rel = "") {
  const full = path.join(here, rel)
  return fs
    .readdirSync(full, { withFileTypes: true })
    .flatMap((d) => {
      const child = rel ? `${rel}/${d.name}` : d.name
      if (d.isDirectory()) return EXCLUDE_DIRS.has(d.name) ? [] : collect(child)
      if (EXCLUDE_FILES.has(d.name) || d.name.endsWith(".md") || d.name.endsWith(".mjs")) return []
      return [child]
    })
    .sort()
}

const files = collect()
if (!files.includes("manifest.json")) {
  console.error("build: no manifest.json — run this from the extension directory's parent or pass nothing")
  process.exit(1)
}

/*
Every script must parse before it is packaged: Chrome refuses a whole module
over one stray line break in a string, and the popup then opens dead with no
error anyone sees. Checked as ES modules, which is how the extension loads them.
*/
{
  const scratch = fs.mkdtempSync(path.join(os.tmpdir(), "moh-ext-check-"))
  const broken = []
  for (const file of files.filter((f) => f.endsWith(".js"))) {
    const copy = path.join(scratch, file.replace(/[\\/]/g, "__") + ".mjs")
    fs.copyFileSync(path.join(here, file), copy)
    try {
      execFileSync(process.execPath, ["--check", copy], { stdio: "pipe" })
    } catch (err) {
      broken.push(`${file}\n${String(err.stderr || err.message).split("\n").slice(0, 4).join("\n")}`)
    }
  }
  fs.rmSync(scratch, { recursive: true, force: true })
  if (broken.length) {
    console.error(`build: ${broken.length} file(s) do not parse:\n\n${broken.join("\n\n")}`)
    process.exit(1)
  }
}

const manifest = JSON.parse(fs.readFileSync(path.join(here, "manifest.json"), "utf8"))
const version = manifest.version
if (!/^\d+\.\d+\.\d+$/.test(version || "")) {
  console.error(`build: manifest version ${JSON.stringify(version)} is not x.y.z`)
  process.exit(1)
}

// The site's origin granted up front, so pairing asks for no extra permission.
// Everything else stays optional, exactly as in the source.
const hosts = new Set([`${site}/*`])
if (api) hosts.add(`${api}/*`)
// A development build drives a local stack, whose test pages are on loopback
// at whatever port: granted up front, because the prompt Chrome shows for an
// optional host cannot be clicked by an automated check. Never in a release
// build — there every site is asked for, one click per allowed site.
if (dev) {
  hosts.add("http://localhost/*")
  hosts.add("http://127.0.0.1/*")
}
manifest.host_permissions = [...hosts]
if (dev) manifest.name = `${manifest.name} (dev)`

const config = fs
  .readFileSync(path.join(here, "config.js"), "utf8")
  .replace(/siteOrigin:\s*"[^"]*"/, `siteOrigin: ${JSON.stringify(site)}`)
  .replace(/apiOrigin:\s*"[^"]*"/, `apiOrigin: ${JSON.stringify(api)}`)
if (!config.includes(`siteOrigin: ${JSON.stringify(site)}`)) {
  // If the DEFAULTS block ever changes shape, this must not silently ship the
  // wrong site — that is the whole reason the site is baked in.
  console.error("build: could not bake the site into config.js — its DEFAULTS block has changed shape")
  process.exit(1)
}

/* ------------------------------------------------------------ zip writer */

const CRC_TABLE = new Uint32Array(256).map((_, n) => {
  let c = n
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
  return c >>> 0
})
function crc32(buffer) {
  let crc = 0xffffffff
  for (const byte of buffer) crc = CRC_TABLE[(crc ^ byte) & 0xff] ^ (crc >>> 8)
  return (crc ^ 0xffffffff) >>> 0
}

// A fixed timestamp, so the same source produces the same bytes and therefore
// the same checksum: a build whose hash changes every run cannot be verified.
const DOS_TIME = 0
const DOS_DATE = ((2026 - 1980) << 9) | (1 << 5) | 1

function zip(entries) {
  const locals = []
  const centrals = []
  let offset = 0
  for (const { name, data } of entries) {
    const nameBytes = Buffer.from(name, "utf8")
    const deflated = zlib.deflateRawSync(data, { level: 9 })
    const useDeflate = deflated.length < data.length
    const body = useDeflate ? deflated : data
    const crc = crc32(data)

    const local = Buffer.alloc(30)
    local.writeUInt32LE(0x04034b50, 0)
    local.writeUInt16LE(20, 4)
    local.writeUInt16LE(0x0800, 6) // UTF-8 names
    local.writeUInt16LE(useDeflate ? 8 : 0, 8)
    local.writeUInt16LE(DOS_TIME, 10)
    local.writeUInt16LE(DOS_DATE, 12)
    local.writeUInt32LE(crc, 14)
    local.writeUInt32LE(body.length, 18)
    local.writeUInt32LE(data.length, 22)
    local.writeUInt16LE(nameBytes.length, 26)
    locals.push(local, nameBytes, body)

    const central = Buffer.alloc(46)
    central.writeUInt32LE(0x02014b50, 0)
    central.writeUInt16LE(20, 4)
    central.writeUInt16LE(20, 6)
    central.writeUInt16LE(0x0800, 8)
    central.writeUInt16LE(useDeflate ? 8 : 0, 10)
    central.writeUInt16LE(DOS_TIME, 12)
    central.writeUInt16LE(DOS_DATE, 14)
    central.writeUInt32LE(crc, 16)
    central.writeUInt32LE(body.length, 20)
    central.writeUInt32LE(data.length, 24)
    central.writeUInt16LE(nameBytes.length, 28)
    central.writeUInt32LE(offset, 42)
    centrals.push(central, nameBytes)

    offset += local.length + nameBytes.length + body.length
  }
  const centralSize = centrals.reduce((sum, part) => sum + part.length, 0)
  const end = Buffer.alloc(22)
  end.writeUInt32LE(0x06054b50, 0)
  end.writeUInt16LE(entries.length, 8)
  end.writeUInt16LE(entries.length, 10)
  end.writeUInt32LE(centralSize, 12)
  end.writeUInt32LE(offset, 16)
  return Buffer.concat([...locals, ...centrals, end])
}

/* ------------------------------------------------------------ assemble */

const entries = files.map((file) => {
  if (file === "manifest.json") return { name: file, data: Buffer.from(JSON.stringify(manifest, null, 2) + "\n") }
  if (file === "config.js") return { name: file, data: Buffer.from(config) }
  return { name: file, data: fs.readFileSync(path.join(here, file)) }
})

const archive = zip(entries)
const sha256 = createHash("sha256").update(archive).digest("hex")

fs.mkdirSync(out, { recursive: true })
// Earlier builds are removed: the API serves what build.json names, and a
// directory of stale zips is an invitation to serve the wrong one by hand.
for (const old of fs.readdirSync(out)) {
  if (/^moharrik-extension-.*\.zip$/.test(old)) fs.rmSync(path.join(out, old))
}
const zipName = `moharrik-extension-${version}${dev ? "-dev" : ""}.zip`
fs.writeFileSync(path.join(out, zipName), archive)

let commit = process.env.GIT_COMMIT || ""
if (!commit) {
  try {
    commit = execSync("git rev-parse --short HEAD", { cwd: here, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim()
  } catch {
    /* not a checkout (an image build) — fine */
  }
}

// What the API reads. Written LAST, so a half-finished build never has a
// build.json naming a zip that is not there.
const info = {
  version,
  file: zipName,
  bytes: archive.length,
  sha256,
  site,
  api: api || site,
  dev,
  commit,
  built_at: new Date().toISOString(),
  files: entries.map((e) => e.name),
}
fs.writeFileSync(path.join(out, "build.json"), JSON.stringify(info, null, 2) + "\n")

console.log(`built ${zipName}: ${entries.length} files, ${archive.length} bytes, sha256 ${sha256}`)
console.log(`site ${site}${api ? ` · api ${api}` : ""}${dev ? " · DEV" : ""}`)
