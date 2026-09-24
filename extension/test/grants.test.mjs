// MH-483: Chrome's side of the allowed sites.
//
// The Connect click asks for every http(s) site; someone who declines connects
// anyway and each site their workspace adds waits for an OK. These pin the
// patterns (inside the manifest's optional hosts, covering subdomains the way
// the workspace's list does), the waiting list, and the badge.

import { test } from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

import { BROAD, badgeFor, grantState, newerThan, originsFor } from "../grants.js"

const ext = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")

/** Chrome's permissions.contains, for a set of granted patterns (exact match, plus broad). */
function perms(granted) {
  const set = new Set(granted)
  return {
    contains: async ({ origins }) =>
      origins.every((o) => set.has(o) || (o.startsWith("https://") && set.has("https://*/*")) || (o.startsWith("http://") && set.has("http://*/*"))),
  }
}

test("broad access is exactly the manifest's optional hosts, so Chrome can grant it", () => {
  const manifest = JSON.parse(readFileSync(path.join(ext, "manifest.json"), "utf8"))
  assert.deepEqual([...BROAD].sort(), [...manifest.optional_host_permissions].sort())
  // "*://*/*" is what the old switch asked for, and Chrome refused it.
  assert.ok(!BROAD.includes("*://*/*"))
  // Every pattern the extension ever asks for sits inside those.
  for (const host of ["example.com", "news.google.com", "127.0.0.1", "localhost"]) {
    for (const o of originsFor(host)) assert.match(o, /^https?:\/\//, o)
  }
})

test("a site's patterns cover it and its subdomains, as the workspace's list does", () => {
  assert.deepEqual(originsFor("Example.com."), ["https://*.example.com/*", "http://*.example.com/*"])
  assert.deepEqual(originsFor("https://www.example.com/path"), ["https://*.www.example.com/*", "http://*.www.example.com/*"])
  assert.deepEqual(originsFor("127.0.0.1"), ["https://127.0.0.1/*", "http://127.0.0.1/*"])
  assert.deepEqual(originsFor("localhost"), ["https://localhost/*", "http://localhost/*"])
  assert.deepEqual(originsFor(""), [])
})

test("with every site granted nothing waits; without, each ungranted site does", async () => {
  const hosts = ["example.com", "news.google.com", "shop.test"]
  assert.deepEqual(await grantState(perms(BROAD), hosts), { mode: "all", pending: [] })
  // Declined: the site this build talks to is granted by the manifest, nothing else.
  assert.deepEqual(await grantState(perms(["https://app.moharrik.com/*"]), hosts), {
    mode: "sites",
    pending: ["example.com", "news.google.com", "shop.test"],
  })
  // One approved in the popup (either scheme counts); the others still wait.
  assert.deepEqual(await grantState(perms(originsFor("news.google.com")), hosts), { mode: "sites", pending: ["example.com", "shop.test"] })
  assert.deepEqual(await grantState(perms(["http://*.shop.test/*"]), hosts), { mode: "sites", pending: ["example.com", "news.google.com"] })
  // Only https granted is not "all": plain-http sites would still fail.
  assert.equal((await grantState(perms(["https://*/*"]), [])).mode, "sites")
  // An empty list waits for nothing.
  assert.deepEqual(await grantState(perms([]), []), { mode: "sites", pending: [] })
})

test("the badge counts the waiting sites first, then says an update is out", () => {
  assert.equal(badgeFor({ pending: ["a.com", "b.com"] }, { available: true, latest: "0.6.0" }).text, "2")
  assert.match(badgeFor({ pending: ["a.com"] }).title, /1 site waiting for your OK/)
  assert.equal(badgeFor({ pending: [] }, { available: true, latest: "0.6.0" }).text, "NEW")
  assert.match(badgeFor({ pending: [] }, { available: true, latest: "0.6.0" }).title, /update available: 0\.6\.0/)
  assert.equal(badgeFor({ pending: [] }, { available: false }).text, "")
  assert.equal(badgeFor(null, null).text, "")
})

test("versions compare as numbers", () => {
  assert.ok(newerThan("0.10.0", "0.9.3"))
  assert.ok(!newerThan("0.4.0", "0.4.0"))
  assert.ok(newerThan("0.4.1", "0.4"))
  assert.ok(!newerThan("", "0.4.0"))
})
