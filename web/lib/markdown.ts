import { marked } from "marked"
import DOMPurify from "dompurify"

// Chat/WhatsApp formatting (MH-556): a member's reply is Markdown source
// (stored in InboxMessage.body); this renders it for DISPLAY only, purely
// client-side, the same two-layer-safety shape as inbox-html.ts even though
// there is no server-sanitised body_html to fall back on for these channels.
marked.setOptions({ gfm: true, breaks: true })

export function renderMarkdownPreview(src: string): string {
  if (!src.trim()) return ""
  const html = marked.parse(src, { async: false }) as string
  return DOMPurify.sanitize(html, { FORBID_TAGS: ["script", "style"], ADD_ATTR: ["target"] })
}
