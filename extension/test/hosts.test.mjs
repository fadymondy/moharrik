// MH-422: the browser matches hosts by the SERVER's rule, not a similar one.
//
// internal/automation/testdata/host_matching.json is run by guardrails_test.go
// against HostAllowed / normaliseHost, and here against hosts.js. A case that
// passes on one side and fails on the other is exactly the drift this exists
// to catch: the server allowing a goto the browser then refuses, or — the
// dangerous direction — the browser acting on a host the server would refuse.

import { test } from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"

import { gotoHost, hostAllowed, normaliseHost, pageVerdict } from "../hosts.js"

const TABLE = JSON.parse(readFileSync(new URL("../../internal/automation/testdata/host_matching.json", import.meta.url), "utf8"))

test("normaliseHost agrees with the server on every shared case", () => {
  assert.ok(TABLE.normalise.length > 0)
  for (const [input, want] of TABLE.normalise) {
    assert.equal(normaliseHost(input), want, `normaliseHost(${JSON.stringify(input)})`)
  }
})

test("hostAllowed agrees with the server on every shared case", () => {
  assert.ok(TABLE.allowed.length > 0)
  for (const c of TABLE.allowed) {
    assert.equal(hostAllowed(c.list, c.host), c.allowed, `hostAllowed(${JSON.stringify(c.list)}, ${JSON.stringify(c.host)})`)
  }
})

test("a page is on the list by its host, ignoring port, path and case", () => {
  const list = ["allowed.test"]
  for (const url of [
    "https://allowed.test/",
    "https://www.allowed.test/a?b=c#d",
    "http://app.allowed.test:8443/x",
    "HTTPS://ALLOWED.TEST/",
  ]) {
    assert.equal(pageVerdict(url, list).state, "allowed", url)
  }
  for (const [url, where] of [
    ["https://evil.test/", "evil.test"],
    ["https://notallowed.test/", "notallowed.test"],
    ["https://allowed.test.evil.io/", "allowed.test.evil.io"],
    ["http://[::1]/", "an address with no host"], // IPv6 never matches, as on the server
  ]) {
    const v = pageVerdict(url, list)
    assert.equal(v.state, "outside", url)
    assert.equal(v.where, where, url)
  }
})

test("about:blank is neither on nor off the list; other schemes are off it", () => {
  const list = ["allowed.test"]
  for (const url of ["about:blank", "", "about:srcdoc", "about:blank#x"]) {
    assert.equal(pageVerdict(url, list).state, "blank", JSON.stringify(url))
  }
  for (const [url, where] of [
    ["data:text/html,<h1>hi</h1>", "a data: URL"],
    ["javascript:alert(1)", "a javascript: URL"],
    ["blob:https://allowed.test/5b1c", "a blob: URL"],
    ["file:///C:/secrets.txt", "a file on this computer"],
    ["chrome-extension://abcdefghijklmnop/popup.html", "an extension's page"],
    ["chrome://settings/", "a chrome: page"],
    ["view-source:https://allowed.test/", "a view-source: page"],
    ["about:settings", "about:settings"],
  ]) {
    const v = pageVerdict(url, list)
    assert.equal(v.state, "outside", url)
    assert.equal(v.where, where, url)
  }
})

test("the verdict never echoes the URL — only the host", () => {
  const v = pageVerdict("https://evil.test/callback?code=SECRET-TOKEN", ["allowed.test"])
  assert.equal(v.where, "evil.test")
  assert.doesNotMatch(JSON.stringify(v), /SECRET-TOKEN/)
})

test("a goto's host is read as the server reads it", () => {
  assert.equal(gotoHost("https://App.Allowed.test:8443/x"), "app.allowed.test")
  assert.equal(gotoHost("allowed.test/x"), "allowed.test")
  assert.equal(gotoHost("allowed.test:8443/x"), "allowed.test")
  assert.equal(gotoHost("data:text/html,x"), "")
  assert.equal(gotoHost("javascript:alert(1)"), "")
  assert.equal(gotoHost(""), "")
})
