"use client"

import { useEffect, useRef, useState } from "react"
import {
  DownloadIcon,
  MapPinIcon,
  PaperclipIcon,
  PauseIcon,
  PlayIcon,
  TriangleAlertIcon,
  UserRoundIcon,
} from "lucide-react"

import { type InboxAttachment } from "@/lib/api"
import { renderEmailHtml } from "@/lib/inbox-html"
import { useTranslations } from "@/lib/i18n"
import { Dialog, DialogContent, DialogTitle } from "@fadymondy/nasaq/web"
import { cn } from "@/lib/utils"

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

/** kindOf: the column added in MH-658 drives rendering, falling back to the
 * content_type sniff every attachment had before that column existed — so an
 * email attachment filed before this shipped still renders exactly as it did.
 */
function kindOf(a: InboxAttachment): string {
  if (a.kind) return a.kind
  if (a.content_type.startsWith("image/")) return "image"
  if (a.content_type.startsWith("video/")) return "video"
  if (a.content_type.startsWith("audio/")) return "audio"
  return "document"
}

/** A failed WhatsApp media download (MH-658): the message still filed, this
 * chip says so instead of rendering a broken image or a 0-byte player. */
function FailedChip({ a }: { a: InboxAttachment }) {
  const { t } = useTranslations()
  return (
    <div
      className="flex items-center gap-1.5 rounded-md border border-dashed border-nq-line bg-nq-surface-soft px-2 py-1 text-xs text-muted-foreground"
      data-attachment-failed
    >
      <TriangleAlertIcon className="size-3.5 shrink-0" />
      <span>{t("inbox.media.failed")}</span>
    </div>
  )
}

/** The image lightbox (MH-658): a Base UI Dialog rather than a new-tab link,
 * so opening a photo never loses the thread underneath it. */
function ImageLightbox({
  images,
  index,
  onClose,
}: {
  images: InboxAttachment[]
  index: number | null
  onClose: () => void
}) {
  const { t } = useTranslations()
  const a = index !== null ? images[index] : null
  return (
    <Dialog open={index !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className="max-w-[calc(100%-2rem)] border-0 bg-transparent p-0 shadow-none ring-0 sm:max-w-3xl"
        showClose
      >
        <DialogTitle className="sr-only">{a?.filename || t("inbox.media.openImage")}</DialogTitle>
        {a ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={a.url}
            alt={a.filename}
            className="max-h-[80vh] w-full rounded-lg object-contain"
          />
        ) : null}
      </DialogContent>
    </Dialog>
  )
}

/** WhatsApp-style voice player (MH-658): play/pause, a seek bar, elapsed and
 * total duration. currentTime drives the seek bar via a real <audio> element
 * — no waveform is fabricated, since the channel never sends one. */
function VoicePlayer({ a }: { a: InboxAttachment }) {
  const { t } = useTranslations()
  const ref = useRef<HTMLAudioElement>(null)
  const [playing, setPlaying] = useState(false)
  const [current, setCurrent] = useState(0)
  // WhatsApp reports the length up front; the audio's own metadata replaces it
  // once known. An Ogg/Opus file's duration can read as Infinity until the
  // browser has seeked to its end, so a non-finite value keeps the reported one.
  const [duration, setDuration] = useState(a.meta?.seconds ?? 0)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const onTime = () => setCurrent(el.currentTime)
    const onMeta = () => {
      if (Number.isFinite(el.duration) && el.duration > 0) setDuration(el.duration)
    }
    const onEnd = () => setPlaying(false)
    onMeta() // metadata may have loaded before this effect ran
    el.addEventListener("timeupdate", onTime)
    el.addEventListener("loadedmetadata", onMeta)
    el.addEventListener("durationchange", onMeta)
    el.addEventListener("ended", onEnd)
    return () => {
      el.removeEventListener("timeupdate", onTime)
      el.removeEventListener("loadedmetadata", onMeta)
      el.removeEventListener("durationchange", onMeta)
      el.removeEventListener("ended", onEnd)
    }
  }, [])

  function toggle() {
    const el = ref.current
    if (!el) return
    if (el.paused) {
      void el.play()
      setPlaying(true)
    } else {
      el.pause()
      setPlaying(false)
    }
  }

  function seek(e: React.ChangeEvent<HTMLInputElement>) {
    const el = ref.current
    if (!el) return
    const t = Number(e.target.value)
    el.currentTime = t
    setCurrent(t)
  }

  function fmt(s: number) {
    if (!Number.isFinite(s) || s < 0) s = 0
    const m = Math.floor(s / 60)
    const r = Math.floor(s % 60)
    return `${m}:${String(r).padStart(2, "0")}`
  }

  return (
    <div className="flex w-56 items-center gap-2" data-attachment-voice dir="ltr">
      <audio ref={ref} preload="metadata" src={a.url} className="hidden" />
      <button
        type="button"
        onClick={toggle}
        aria-label={playing ? t("inbox.media.pause") : t("inbox.media.play")}
        className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground"
      >
        {playing ? <PauseIcon className="size-4" /> : <PlayIcon className="size-4 ms-0.5" />}
      </button>
      <input
        type="range"
        min={0}
        max={duration || 0}
        step={0.1}
        value={Math.min(current, duration || 0)}
        onChange={seek}
        aria-label={t("inbox.media.voiceNote")}
        className="h-1.5 min-w-0 flex-1 accent-primary"
      />
      <span className="w-9 shrink-0 text-[0.6875rem] tabular-nums text-muted-foreground">
        {fmt(playing || current > 0 ? current : duration)}
      </span>
    </div>
  )
}

function LocationCard({ a }: { a: InboxAttachment }) {
  const { t } = useTranslations()
  const lat = a.meta?.lat
  const lng = a.meta?.lng
  const href =
    lat != null && lng != null
      ? `https://www.openstreetmap.org/?mlat=${lat}&mlon=${lng}#map=16/${lat}/${lng}`
      : undefined
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer noopener"
      className={cn(
        "flex min-w-48 items-center gap-2 rounded-md border border-nq-line bg-nq-surface-soft px-2.5 py-2 text-xs hover:bg-card",
        !href && "pointer-events-none opacity-70",
      )}
      data-attachment-location
    >
      <MapPinIcon className="size-4 shrink-0 text-primary" />
      <span className="min-w-0 flex-1">
        <span className="block truncate font-medium text-foreground">
          {a.meta?.name || t("inbox.media.location")}
        </span>
        {a.meta?.address ? (
          <span className="block truncate text-muted-foreground">{a.meta.address}</span>
        ) : null}
        {href ? <span className="block text-primary">{t("inbox.media.openInMaps")}</span> : null}
      </span>
    </a>
  )
}

function ContactCard({ a }: { a: InboxAttachment }) {
  const { t } = useTranslations()
  return (
    <div
      className="flex min-w-48 items-center gap-2 rounded-md border border-nq-line bg-nq-surface-soft px-2.5 py-2 text-xs"
      data-attachment-contact
    >
      <UserRoundIcon className="size-4 shrink-0 text-primary" />
      <span className="min-w-0 flex-1">
        <span className="block truncate font-medium text-foreground">
          {a.meta?.name || t("inbox.media.contactCard")}
        </span>
        {a.meta?.number ? (
          <span className="block truncate text-muted-foreground" dir="ltr">
            {a.meta.number}
          </span>
        ) : null}
      </span>
    </div>
  )
}

/**
 * A message's non-inline attachments (MH-556, extended for MH-658): kind
 * drives rendering — image opens a lightbox, video plays inline, a voice note
 * gets a WhatsApp-style player, a sticker renders small with no bubble chrome,
 * a location becomes a map-link card, a contact becomes a name+number card,
 * and everything else (documents, and a failed download) falls back to the
 * file chip. The SAME component renders every channel's attachments, since
 * kind/content_type carry no channel of their own.
 */
export function AttachmentList({ attachments }: { attachments?: InboxAttachment[] }) {
  const { t } = useTranslations()
  const files = (attachments ?? []).filter((a) => !a.inline)
  const [openImage, setOpenImage] = useState<number | null>(null)
  if (files.length === 0) return null

  const ready = files.filter((a) => a.status !== "failed")
  const failed = files.filter((a) => a.status === "failed")

  const images = ready.filter((a) => kindOf(a) === "image")
  const videos = ready.filter((a) => kindOf(a) === "video")
  const voices = ready.filter((a) => kindOf(a) === "voice" || kindOf(a) === "audio")
  const stickers = ready.filter((a) => kindOf(a) === "sticker")
  const locations = ready.filter((a) => kindOf(a) === "location")
  const contacts = ready.filter((a) => kindOf(a) === "contact")
  const documents = ready.filter(
    (a) =>
      !["image", "video", "voice", "audio", "sticker", "location", "contact"].includes(kindOf(a)),
  )

  return (
    <div className="mt-2 flex flex-col gap-2" data-attachments>
      {stickers.map((a) => (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          key={a.id}
          src={a.url}
          alt={a.filename || t("inbox.media.sticker")}
          className="size-24 object-contain"
          data-attachment-sticker
        />
      ))}
      {images.length > 0 ? (
        <>
          <ul className="grid grid-cols-3 gap-1.5" data-attachment-images>
            {images.map((a, i) => (
              <li key={a.id}>
                <button
                  type="button"
                  onClick={() => setOpenImage(i)}
                  title={a.filename}
                  aria-label={t("inbox.media.openImage")}
                  className="block w-full overflow-hidden rounded-md border border-nq-line"
                >
                  {/* Signed, short-lived attachment URLs: next/image would cache
                      past that window, so a plain <img> is correct here. */}
                  {/* eslint-disable-next-line @next/next/no-img-element */}
                  <img
                    src={a.url}
                    alt={a.filename}
                    className="aspect-square size-full object-cover"
                    loading="lazy"
                  />
                </button>
              </li>
            ))}
          </ul>
          <ImageLightbox images={images} index={openImage} onClose={() => setOpenImage(null)} />
        </>
      ) : null}
      {videos.map((a) => (
        <video
          key={a.id}
          controls
          preload="metadata"
          src={a.url}
          className="max-h-72 max-w-64 rounded-md"
          data-attachment-video
        >
          <span className="sr-only">{a.filename}</span>
        </video>
      ))}
      {voices.map((a) => (
        <VoicePlayer key={a.id} a={a} />
      ))}
      {locations.map((a) => (
        <LocationCard key={a.id} a={a} />
      ))}
      {contacts.map((a) => (
        <ContactCard key={a.id} a={a} />
      ))}
      {documents.length > 0 ? (
        <ul className="flex flex-wrap gap-2" data-attachment-files>
          {documents.map((a) => (
            <li key={a.id}>
              <a
                href={a.url}
                target="_blank"
                rel="noreferrer noopener"
                download={a.filename}
                className="flex items-center gap-1.5 rounded-md border border-nq-line bg-nq-surface-soft px-2 py-1 text-xs hover:bg-card"
                title={`${a.filename} (${Math.ceil(a.size_bytes / 1024)} KB)`}
              >
                <PaperclipIcon className="size-3.5 shrink-0" />
                <span className="max-w-40 truncate">{a.filename}</span>
                <DownloadIcon className="size-3 shrink-0 text-muted-foreground" />
              </a>
            </li>
          ))}
        </ul>
      ) : null}
      {failed.length > 0 ? (
        <ul className="flex flex-wrap gap-2" data-attachment-failed-list>
          {failed.map((a) => (
            <li key={a.id}>
              <FailedChip a={a} />
            </li>
          ))}
        </ul>
      ) : null}
      <span className="sr-only">{t("inbox.attachmentsCount", { n: files.length })}</span>
    </div>
  )
}

/** True when a message is nothing but a single sticker (MH-658): WhatsApp
 * renders a sticker with no bubble around it at all, not even the usual
 * padding/background — chat-thread.tsx's ChatBubble reads this to drop its
 * own chrome for exactly that case. */
export function isStickerOnly(attachments: InboxAttachment[] | undefined, text: string): boolean {
  const files = (attachments ?? []).filter((a) => !a.inline)
  return files.length === 1 && kindOf(files[0]) === "sticker" && !text.trim()
}

export function MessageBody({
  body,
  bodyHtml,
  attachments,
}: {
  body: string
  bodyHtml?: string
  attachments?: InboxAttachment[]
}) {
  if (bodyHtml && bodyHtml.trim()) {
    return <HtmlMessage html={bodyHtml} attachments={attachments} />
  }
  return (
    <p className="text-start break-words whitespace-pre-wrap" dir="auto">
      {body}
    </p>
  )
}
