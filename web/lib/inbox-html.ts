import DOMPurify from "dompurify"

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

export function renderEmailHtml(rawHtml: string, opts: { blockImages: boolean }): { html: string; hadRemoteImages: boolean } {
  const clean = DOMPurify.sanitize(rawHtml, purifyConfig)
  if (typeof window === "undefined" || typeof DOMParser === "undefined") {
    return { html: clean, hadRemoteImages: false }
  }
  const doc = new DOMParser().parseFromString(`<div>${clean}</div>`, "text/html")
  const root = doc.body.firstElementChild as HTMLElement | null
  if (!root) return { html: clean, hadRemoteImages: false }

  // rawHtml is re-sanitised fresh on every call (never a previously-blocked
  // string), so every <img> still carries its real src at this point — when
  // blockImages is false there is nothing to restore, only to leave alone.
  let hadRemoteImages = false
  if (opts.blockImages) {
    root.querySelectorAll("img").forEach((img) => {
      const src = img.getAttribute("src") || ""
      if (src && !isLocalAttachmentURL(src) && !src.startsWith("cid:")) {
        hadRemoteImages = true
        img.setAttribute("data-blocked-src", src)
        img.removeAttribute("src")
        img.setAttribute("alt", img.getAttribute("alt") || "")
        img.style.border = "1px dashed currentColor"
        img.style.minHeight = "1.5em"
        img.style.minWidth = "3em"
        img.style.display = "inline-block"
      }
      // A cid: reference (an inline attachment) is left alone rather than
      // blocked: it is the workspace's own content. The server does not yet
      // rewrite it to the matching attachment's signed URL, so today it simply
      // does not load — never worse than before MH-556 (no HTML rendering at
      // all). Tracked as follow-up work; see the mailbox card's report.
    })
  }

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
