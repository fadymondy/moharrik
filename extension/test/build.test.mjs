// MH-399: the downloadable zip.
//
// The API serves whatever build.json names and refuses a file whose checksum
// no longer matches it, so these pin down the packager's side of that promise:
// the zip holds the extension and not its harness, build.json's checksum is
// the checksum of the file written, and the same source gives the same bytes.

import { test } from "node:test"
import assert from "node:assert/strict"
import { execFileSync, spawnSync } from "node:child_process"
import { createHash } from "node:crypto"
import { mkdtempSync, readFileSync, readdirSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"
import zlib from "node:zlib"

const ext = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")
const build = (...args) => {
  const out = mkdtempSync(path.join(tmpdir(), "moh-ext-build-"))
  execFileSync(process.execPath, [path.join(ext, "build.mjs"), "--out", out, ...args], { stdio: "pipe" })
  const info = JSON.parse(readFileSync(path.join(out, "build.json"), "utf8"))
  return { out, info, zip: readFileSync(path.join(out, info.file)) }
}

/** The entries of a zip, read from its central directory: name → contents. */
function unzip(buf) {
  const end = buf.lastIndexOf(Buffer.from([0x50, 0x4b, 0x05, 0x06]))
  const count = buf.readUInt16LE(end + 10)
  let at = buf.readUInt32LE(end + 16)
  const files = {}
  for (let i = 0; i < count; i++) {
    const method = buf.readUInt16LE(at + 10)
    const size = buf.readUInt32LE(at + 20)
    const nameLen = buf.readUInt16LE(at + 28)
    const local = buf.readUInt32LE(at + 42)
    const name = buf.subarray(at + 46, at + 46 + nameLen).toString("utf8")
    const localNameLen = buf.readUInt16LE(local + 26)
    const data = buf.subarray(local + 30 + localNameLen, local + 30 + localNameLen + size)
    files[name] = method === 8 ? zlib.inflateRawSync(data) : data
    at += 46 + nameLen
  }
  return files
}

test("the zip is the extension, without its test harness, and its checksum is the file's", () => {
  const { info, zip } = build()
  const manifest = JSON.parse(readFileSync(path.join(ext, "manifest.json"), "utf8"))
  assert.equal(info.version, manifest.version)
  assert.equal(info.file, `moharrik-extension-${manifest.version}.zip`)
  assert.equal(info.bytes, zip.length)
  assert.equal(info.sha256, createHash("sha256").update(zip).digest("hex"))

  const files = unzip(zip)
  const names = Object.keys(files)
  for (const needed of ["manifest.json", "background.js", "driver.js", "pick.js", "hosts.js", "popup.html", "popup.css", "connect.js", "offscreen.html", "offscreen.js", "options.html", "options.js", "driver/cdp.js", "driver/human.js", "driver/locate.js", "driver/extract.js", "driver/run-tabs-core.js", "driver/runs.js", "driver/tabs.js", "fonts/geist-latin-wght-normal.woff2", "icons/icon-16.png", "icons/icon-32.png", "icons/icon-48.png", "icons/icon-128.png"]) {
    assert.ok(names.includes(needed), `${needed} is shipped`)
  }
  for (const name of names) {
    assert.doesNotMatch(name, /^(node_modules|test|dist)\/|package(-lock)?\.json$|build\.mjs$/, `${name} is harness, not extension`)
  }
  // Every source file is shipped: the exclusions are by rule, not a list of includes.
  const source = [...readdirSync(ext).filter((f) => f.endsWith(".js")), ...readdirSync(path.join(ext, "driver")).map((f) => `driver/${f}`)]
  for (const f of source) assert.ok(names.includes(f), `${f} is missing from the zip`)
  // The icon generator is a build tool, not part of the extension.
  assert.ok(!names.includes("make-icons.mjs"))

  const shipped = JSON.parse(files["manifest.json"].toString("utf8"))
  assert.deepEqual(shipped.host_permissions, ["https://app.moharrik.com/*"])
  assert.match(files["config.js"].toString("utf8"), /siteOrigin: "https:\/\/app\.moharrik\.com"/)
})

test("the same source packages to the same bytes", () => {
  assert.equal(build().info.sha256, build().info.sha256)
})

test("another deployment's site is baked in, and localhost only with --dev", () => {
  const staging = build("--site", "https://staging.moharrik.example")
  const files = unzip(staging.zip)
  assert.match(files["config.js"].toString("utf8"), /siteOrigin: "https:\/\/staging\.moharrik\.example"/)
  assert.deepEqual(JSON.parse(files["manifest.json"]).host_permissions, ["https://staging.moharrik.example/*"])

  const refused = spawnSync(process.execPath, [path.join(ext, "build.mjs"), "--site", "http://localhost:3506", "--out", mkdtempSync(path.join(tmpdir(), "x-"))])
  assert.notEqual(refused.status, 0)
  assert.match(String(refused.stderr), /pass --dev/)

  const dev = build("--site", "http://localhost:3506", "--api", "http://localhost:8506", "--dev")
  assert.equal(dev.info.dev, true)
  assert.match(dev.info.file, /-dev\.zip$/)
  const devManifest = JSON.parse(unzip(dev.zip)["manifest.json"])
  assert.match(devManifest.name, /\(dev\)$/)
  assert.ok(devManifest.host_permissions.includes("http://localhost:3506/*"))
})
