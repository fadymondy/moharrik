import DOMPurify from "dompurify"

import { type InboxAttachment } from "@/lib/api"

/*
Client-side email/Markdown rendering (MH-556) — the SECOND of two independent
sanitisation passes. The first already ran on the server (internal/inbox/sanitize.go,
bluemonday) before this HTML was ever stored; this one runs again, immediately
before the string is handed to the sandboxed <iframe> as srcdoc
(web/components/inbox/message-body.tsx). A value read back from the API is
still "HTML from a variable" and gets the same treatment a fresh string would.

Two more things happen here that only make sense client-side, since they are
about what to SHOW, not what is safe:

  - remote images are blocked by default (an <img src> is how a tracking pixel
    confirms a message was opened) — everything except our own signed
    /api/attachments/ URL is stripped to data-blocked-src, and "Show images"
    (message-body.tsx) re-renders with blockImages:false to put them back.
  - a trailing quoted-reply block ("On ... wrote:", a blockquote, Gmail/Outlook/
    Apple Mail's own quote wrappers) is collapsed under <details>, closed by
    default, so a long thread's history does not bury the new part.
  - a `cid:` reference (an inline attachment embedded IN the message, not a
    remote fetch) is rewritten to the matching attachment's signed download
    URL, given the message's own attachment list — resolved here, client-side,
    rather than server-side, because this is also the layer that already owns
    "what to show" for every other image path and the message's attachments
    are already on hand wherever this renders. A `cid:` that matches nothing
    is stripped exactly like a blocked remote image: it is not this
    workspace's content either, just an unresolved reference.
*/

const purifyConfig = {
  // Same intent as the server's bluemonday policy (sanitize.go): structure and
  // inline styling survive, nothing that runs or phones home does.
  FORBID_TAGS: ["script", "style", "iframe", "object", "embed", "form", "link", "meta", "base"],
  FORBID_ATTR: ["srcset"],
  ADD_ATTR: ["target"],
}

function isLocalAttachmentURL(src: string): boolean {
  return src.startsWith("/api/attachments/") || /^https?:\/\/[^/]+\/api\/attachments\//.test(src)
}

/** A content-id header is often wrapped in `<...>`; a `cid:` URI's value never is. Compare on the same normal form either way. */
function normalizeContentId(id: string): string {
  return id.trim().replace(/^</, "").replace(/>$/, "").toLowerCase()
}

/** The matching attachment's signed URL for a `cid:` reference, or null if nothing matches. */
function resolveCid(src: string, attachments: InboxAttachment[]): string | null {
  if (!src.toLowerCase().startsWith("cid:")) return null
  let raw = src.slice(4)
  try {
    raw = decodeURIComponent(raw)
  } catch {
    // Not URI-encoded: use as-is.
  }
  const target = normalizeContentId(raw)
  if (!target) return null
  const match = attachments.find((a) => a.content_id && normalizeContentId(a.content_id) === target)
  return match?.url ?? null
}

/** Quote-block selectors used by the mail clients that actually produce them. */
const QUOTE_SELECTORS = [
  ".gmail_quote",
  ".gmail_extra",
  ".yahoo_quoted",
  ".moz-cite-prefix",
  ".OutlookMessageHeader",
  "blockquote[type=cite]",
  "blockquote",
]

export function renderEmailHtml(
  rawHtml: string,
  opts: { blockImages: boolean },
  attachments: InboxAttachment[] = [],
): { html: string; hadRemoteImages: boolean } {
  const clean = DOMPurify.sanitize(rawHtml, purifyConfig)
  if (typeof window === "undefined" || typeof DOMParser === "undefined") {
    return { html: clean, hadRemoteImages: false }
  }
  const doc = new DOMParser().parseFromString(`<div>${clean}</div>`, "text/html")
  const root = doc.body.firstElementChild as HTMLElement | null
  if (!root) return { html: clean, hadRemoteImages: false }

  function blockImg(img: HTMLImageElement, src: string) {
    img.setAttribute("data-blocked-src", src)
    img.removeAttribute("src")
    img.setAttribute("alt", img.getAttribute("alt") || "")
    img.style.border = "1px dashed currentColor"
    img.style.minHeight = "1.5em"
    img.style.minWidth = "3em"
    img.style.display = "inline-block"
  }

  // rawHtml is re-sanitised fresh on every call (never a previously-blocked
  // string), so every <img> still carries its real src at this point — when
  // blockImages is false there is nothing to restore, only to leave alone.
  let hadRemoteImages = false
  root.querySelectorAll("img").forEach((img) => {
    const src = img.getAttribute("src") || ""
    if (!src) return

    // A cid: reference resolves independently of blockImages — it is the
    // message's own inline content, the same reasoning that already exempts
    // /api/attachments/ URLs below, never a remote fetch to gate. One that
    // matches nothing is stripped exactly like a blocked remote image: it
    // points at nothing real either.
    if (src.toLowerCase().startsWith("cid:")) {
      const resolved = resolveCid(src, attachments)
      if (resolved) img.setAttribute("src", resolved)
      else blockImg(img, src)
      return
    }

    if (opts.blockImages && !isLocalAttachmentURL(src)) {
      hadRemoteImages = true
      blockImg(img, src)
    }
  })

  // Collapse quoted history: the first quote-marker element found, and
  // everything after it in its parent, moves under <details>.
  for (const sel of QUOTE_SELECTORS) {
    const quote = root.querySelector(sel)
    if (!quote || quote.closest("details")) continue
    const parent = quote.parentElement
    if (!parent) continue
    const details = doc.createElement("details")
    details.className = "inbox-quote"
    const summary = doc.createElement("summary")
    summary.textContent = "…"
    summary.className = "inbox-quote-summary"
    details.appendChild(summary)
    let node: ChildNode | null = quote
    const toMove: ChildNode[] = []
    while (node) {
      toMove.push(node)
      node = node.nextSibling
    }
    toMove.forEach((n) => details.appendChild(n))
    parent.appendChild(details)
    break
  }

  return { html: root.innerHTML, hadRemoteImages }
}
