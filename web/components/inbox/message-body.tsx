"use client"

import { useEffect, useRef, useState } from "react"
import { DownloadIcon, PaperclipIcon } from "lucide-react"

import { type InboxAttachment } from "@/lib/api"
import { renderEmailHtml } from "@/lib/inbox-html"
import { useTranslations } from "@/lib/i18n"

/*
One message's body (MH-556): HTML (an inbound email's own markup, or a
Markdown reply's rendering — internal/inbox/markdown.go and sanitize.go build
body_html server-side, sanitised) renders in a SANDBOXED, auto-sizing iframe;
anything with no body_html (a plain-text email, or an inbound chat/WhatsApp
message, which is never rendered as markup) falls back to plain text.

The iframe has no `allow-scripts` — nothing inside it can run script, execute
a form, or navigate the parent — only `allow-same-origin`, which is what lets
this component read its content height to auto-size it. That combination is
what makes the XSS corpus (attempted script tags, event handlers, javascript:
URLs, SVG script vectors) inert even if a future bug let one past both
sanitiser passes.
*/

const IFRAME_STYLE = `
  :root { color-scheme: light dark; }
  html, body { margin: 0; padding: 0; }
  body {
    font: 14px/1.55 ui-sans-serif, system-ui, sans-serif;
    color: light-dark(#111827, #e5e7eb);
    background: transparent;
    word-wrap: break-word;
    overflow-wrap: anywhere;
  }
  img { max-width: 100%; height: auto; }
  table { max-width: 100%; }
  a { color: light-dark(#4B00B6, #b794f6); }
  .inbox-quote { margin-top: 8px; }
  .inbox-quote-summary { cursor: pointer; font-size: 12px; color: light-dark(#6b7280, #9ca3af); user-select: none; }
`

function HtmlMessage({ html, attachments }: { html: string; attachments?: InboxAttachment[] }) {
  const ref = useRef<HTMLIFrameElement>(null)
  const [height, setHeight] = useState(40)
  // Images load straight away (MH-592): an email read without its images was
  // not readable, and a "Show images" step on every message was friction.
  // The iframe still sends no referrer and runs no script.
  const rendered = renderEmailHtml(html, { blockImages: false }, attachments)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    let cancelled = false
    function measure() {
      const doc = el?.contentDocument
      if (!doc?.documentElement) return
      const h = Math.max(doc.documentElement.scrollHeight, doc.body?.scrollHeight ?? 0)
      if (!cancelled && h > 0) setHeight(h + 4)
    }
    el.addEventListener("load", measure)
    // Images finish loading after the iframe's own load event; a couple of
    // follow-up measurements catch the height settling without a full
    // MutationObserver/ResizeObserver wire-up for a sandboxed document.
    const timers = [100, 400, 1200].map((ms) => window.setTimeout(measure, ms))
    return () => {
      cancelled = true
      el.removeEventListener("load", measure)
      timers.forEach((id) => window.clearTimeout(id))
    }
  }, [rendered.html])

  return (
    <div data-email-body>
      <iframe
        ref={ref}
        title="email"
        sandbox="allow-same-origin allow-popups"
        referrerPolicy="no-referrer"
        style={{ width: "100%", height, border: 0, display: "block" }}
        srcDoc={`<!doctype html><html><head><meta charset="utf-8"><style>${IFRAME_STYLE}</style></head><body>${rendered.html}</body></html>`}
      />
    </div>
  )
}

/**
 * A message's non-inline attachments (MH-556, extended for MH-587): an image
 * gets a thumbnail (WhatsApp's media grid), audio gets a native player (a
 * voice note, whatever codec the channel actually sent — no waveform is
 * fabricated), and everything else falls back to the file chip. Driven only
 * by the attachment's own content_type — nothing here assumes a channel.
 */
export function AttachmentList({ attachments }: { attachments?: InboxAttachment[] }) {
  const { t } = useTranslations()
  const files = (attachments ?? []).filter((a) => !a.inline)
  if (files.length === 0) return null
  const images = files.filter((a) => a.content_type.startsWith("image/"))
  const audio = files.filter((a) => a.content_type.startsWith("audio/"))
  const other = files.filter((a) => !a.content_type.startsWith("image/") && !a.content_type.startsWith("audio/"))

  return (
    <div className="mt-2 flex flex-col gap-2" data-attachments>
      {images.length > 0 ? (
        <ul className="grid grid-cols-3 gap-1.5" data-attachment-images>
          {images.map((a) => (
            <li key={a.id}>
              <a href={a.url} target="_blank" rel="noreferrer noopener" title={a.filename} className="block overflow-hidden rounded-md border border-grid-border">
                {/* Signed, short-lived attachment URLs: next/image would cache
                    past that window, so a plain <img> is correct here. */}
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img src={a.url} alt={a.filename} className="aspect-square size-full object-cover" loading="lazy" />
              </a>
            </li>
          ))}
        </ul>
      ) : null}
      {audio.map((a) => (
        <div key={a.id} className="flex items-center gap-2" data-attachment-audio>
          <audio controls preload="none" src={a.url} className="h-9 max-w-64" />
          <span className="sr-only">{a.filename}</span>
        </div>
      ))}
      {other.length > 0 ? (
        <ul className="flex flex-wrap gap-2" data-attachment-files>
          {other.map((a) => (
            <li key={a.id}>
              <a
                href={a.url}
                target="_blank"
                rel="noreferrer noopener"
                download={a.filename}
                className="flex items-center gap-1.5 rounded-md border border-grid-border bg-grid-soft px-2 py-1 text-xs hover:bg-grid-card"
                title={`${a.filename} (${Math.ceil(a.size_bytes / 1024)} KB)`}
              >
                <PaperclipIcon className="size-3.5 shrink-0" />
                <span className="max-w-40 truncate">{a.filename}</span>
                <DownloadIcon className="size-3 shrink-0 text-grid-muted" />
              </a>
            </li>
          ))}
        </ul>
      ) : null}
      <span className="sr-only">{t("inbox.attachmentsCount", { n: files.length })}</span>
    </div>
  )
}

export function MessageBody({ body, bodyHtml, attachments }: { body: string; bodyHtml?: string; attachments?: InboxAttachment[] }) {
  if (bodyHtml && bodyHtml.trim()) {
    return <HtmlMessage html={bodyHtml} attachments={attachments} />
  }
  return (
    <p className="text-start break-words whitespace-pre-wrap" dir="auto">
      {body}
    </p>
  )
}
