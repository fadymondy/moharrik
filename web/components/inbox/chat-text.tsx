import { Fragment, type ReactNode } from "react"

/*
A chat message's text the way WhatsApp shows it (MH-656): *bold*, _italic_,
~strike~ and ```mono```, plus the Markdown a member's own reply is written in
(**bold**, [label](url)), with every URL a link. Built as React nodes, never
as HTML — a customer's message is text, and nothing in it can become markup.
*/

const URL_RE = /\bhttps?:\/\/[^\s<>"'`]+/gi
// Trailing punctuation that ends a sentence, not the URL ("see https://x.com.").
const TRAILING = /[.,;:!?)\]}'"،؛]+$/

/** The URLs in a message, in order, without trailing punctuation. */
export function urlsIn(text: string): string[] {
  return Array.from(text.matchAll(URL_RE), (m) => trimUrl(m[0]))
}

/** The URL a message's preview card is for: WhatsApp previews the first. */
export function firstUrl(text: string): string | null {
  return urlsIn(text)[0] ?? null
}

function trimUrl(u: string): string {
  let out = u.replace(TRAILING, "")
  // Keep a closing paren that closes one inside the URL (Wikipedia links).
  if (u.length > out.length && u[out.length] === ")" && out.includes("(")) out += ")"
  return out
}

function Link({ href, children }: { href: string; children: ReactNode }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer noopener"
      className="break-all text-primary underline underline-offset-2 hover:no-underline"
    >
      {children}
    </a>
  )
}

// URLs are cut out first, so an underscore or tilde inside one never reads as
// formatting; a Markdown link keeps its label.
const LINK_RE = /\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)|\bhttps?:\/\/[^\s<>"'`]+/gi

// One inline span of formatting. The delimiter must hug the text (no space
// just inside it), the way WhatsApp decides "*a*" is bold and "a * b * c" is not.
const INLINE_RE =
  /```([\s\S]+?)```|\*\*(\S(?:[^*\n]*\S)?)\*\*|\*(\S(?:[^*\n]*\S)?)\*|(?<![\p{L}\p{N}])_(\S(?:[^_\n]*\S)?)_(?![\p{L}\p{N}])|~(\S(?:[^~\n]*\S)?)~/gu

function format(text: string, key: string): ReactNode[] {
  const out: ReactNode[] = []
  let last = 0
  let i = 0
  for (const m of text.matchAll(INLINE_RE)) {
    const start = m.index ?? 0
    if (start > last) out.push(text.slice(last, start))
    const k = `${key}-f${i++}`
    const [, mono, bold2, bold, italic, strike] = m
    if (mono !== undefined)
      out.push(
        <code key={k} className="rounded-sm bg-grid-soft px-1 font-mono text-[0.92em]">
          {mono}
        </code>,
      )
    else if (bold2 !== undefined || bold !== undefined)
      out.push(<strong key={k}>{format(bold2 ?? bold!, k)}</strong>)
    else if (italic !== undefined) out.push(<em key={k}>{format(italic, k)}</em>)
    else if (strike !== undefined) out.push(<s key={k}>{format(strike, k)}</s>)
    last = start + m[0].length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

function render(text: string): ReactNode[] {
  const out: ReactNode[] = []
  let last = 0
  let i = 0
  for (const m of text.matchAll(LINK_RE)) {
    const start = m.index ?? 0
    const [whole, label, href] = m
    const url = label === undefined ? trimUrl(whole) : href!
    if (start > last) out.push(...format(text.slice(last, start), `t${i}`))
    out.push(
      <Link key={`l${i++}`} href={url}>
        {label ?? url}
      </Link>,
    )
    last = start + (label === undefined ? url.length : whole.length)
  }
  if (last < text.length) out.push(...format(text.slice(last), `t${i}`))
  return out
}

export function ChatText({ text }: { text: string }) {
  return (
    <>
      {render(text).map((n, i) => (
        <Fragment key={i}>{n}</Fragment>
      ))}
    </>
  )
}
