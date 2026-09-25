// Unit test for cid: image resolution (MH-556). Run with `npm test` (tsx --test) from web/.
// Sets up a minimal jsdom global environment BEFORE importing inbox-html.ts, since dompurify
// and DOMParser both need a real `window`/`document` at import/call time — inbox-html.ts itself
// has no test-only branch, this is the real client code, run for real in a fake DOM.
import { test } from "node:test"
import assert from "node:assert/strict"
import { JSDOM } from "jsdom"

const dom = new JSDOM("<!doctype html><html><body></body></html>")
globalThis.window = dom.window
globalThis.document = dom.window.document
globalThis.DOMParser = dom.window.DOMParser
globalThis.Node = dom.window.Node
globalThis.HTMLElement = dom.window.HTMLElement
globalThis.Element = dom.window.Element

const { renderEmailHtml } = await import("./inbox-html.ts")

/** @type {import("./api").InboxAttachment[]} */
const attachments = [
  {
    id: "att-1",
    filename: "logo.png",
    content_type: "image/png",
    size_bytes: 1234,
    inline: true,
    content_id: "<logo123@mail.example.com>",
    url: "/api/attachments/att-1?ws=w1&exp=1&sig=abc",
  },
]

test("a cid: reference resolves to the matching attachment's URL (angle brackets stripped, case-insensitive)", () => {
  const html = `<p>hi</p><img src="cid:LOGO123@MAIL.EXAMPLE.COM">`
  const { html: out } = renderEmailHtml(html, { blockImages: true }, attachments)
  assert.match(out, /<img src="\/api\/attachments\/att-1\?ws=w1&amp;exp=1&amp;sig=abc">/)
})

test("a cid: reference resolves even without angle brackets on either side", () => {
  const html = `<img src="cid:logo123@mail.example.com">`
  const { html: out } = renderEmailHtml(html, { blockImages: true }, attachments)
  assert.match(out, /src="\/api\/attachments\/att-1/)
})

test("a non-matching cid: reference is stripped, not left broken", () => {
  const html = `<img src="cid:does-not-exist@nowhere">`
  const { html: out } = renderEmailHtml(html, { blockImages: true }, attachments)
  assert.ok(!/<img[^>]*\ssrc="cid:/.test(out), "the cid src attribute must not survive unresolved")
  assert.match(out, /data-blocked-src="cid:does-not-exist@nowhere"/)
})

test("a plain http(s) remote image is still blocked when blockImages is true", () => {
  const html = `<img src="https://tracker.example.com/pixel.gif">`
  const { html: out, hadRemoteImages } = renderEmailHtml(html, { blockImages: true }, attachments)
  assert.equal(hadRemoteImages, true)
  assert.ok(!/<img[^>]*\ssrc="https:\/\/tracker\.example\.com/.test(out), "remote src attribute must be stripped")
  assert.match(out, /data-blocked-src="https:\/\/tracker\.example\.com\/pixel\.gif"/)
})

test("our own signed attachment URL is never blocked", () => {
  const html = `<img src="/api/attachments/att-1?ws=w1&exp=1&sig=abc">`
  const { html: out, hadRemoteImages } = renderEmailHtml(html, { blockImages: true }, attachments)
  assert.equal(hadRemoteImages, false)
  assert.match(out, /src="\/api\/attachments\/att-1/)
})
