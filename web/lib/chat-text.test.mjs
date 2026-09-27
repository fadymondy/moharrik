// MH-656: a chat bubble's text — WhatsApp's formatting and links, as React
// nodes, so nothing a customer types can become markup.
import { test } from "node:test"
import assert from "node:assert/strict"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"

import { ChatText, firstUrl, urlsIn } from "../components/inbox/chat-text.tsx"

const html = (text) => renderToStaticMarkup(createElement(ChatText, { text }))

test("WhatsApp formatting", () => {
  assert.equal(html("*bold* _it_ ~gone~"), "<strong>bold</strong> <em>it</em> <s>gone</s>")
  assert.equal(html("**md bold**"), "<strong>md bold</strong>")
  assert.match(html("```x = 1```"), /<code[^>]*>x = 1<\/code>/)
  // A delimiter with space inside is not formatting, nor is snake_case.
  assert.equal(html("2 * 3 * 4"), "2 * 3 * 4")
  assert.equal(html("user_first_name"), "user_first_name")
})

test("links, without the sentence's punctuation", () => {
  const out = html("see https://example.com/a_b_c. ok")
  assert.match(out, /<a href="https:\/\/example.com\/a_b_c"[^>]*>https:\/\/example.com\/a_b_c<\/a>\. ok/)
  assert.match(html("[docs](https://x.dev/d)"), /<a href="https:\/\/x.dev\/d"[^>]*>docs<\/a>/)
  assert.equal(firstUrl("a https://one.io, b https://two.io"), "https://one.io")
  assert.deepEqual(urlsIn("(https://en.wikipedia.org/wiki/Go_(game))"), [
    "https://en.wikipedia.org/wiki/Go_(game)",
  ])
  assert.equal(firstUrl("no link here"), null)
})

test("markup in a message stays text", () => {
  const out = html('<img src=x onerror="alert(1)"> *hi*')
  assert.ok(!out.includes("<img"), out)
  assert.ok(out.includes("&lt;img"), out)
  assert.ok(!html("javascript:alert(1)").includes("<a"))
})
