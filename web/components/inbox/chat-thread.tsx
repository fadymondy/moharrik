"use client"

import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type FormEvent,
  type KeyboardEvent,
} from "react"
import { useVirtualizer } from "@tanstack/react-virtual"
import {
  AlertCircleIcon,
  ArrowDownIcon,
  CheckCheckIcon,
  CheckIcon,
  FileIcon,
  SendIcon,
  XIcon,
} from "lucide-react"

import { Bdi } from "@/components/bidi"
import { PersonAvatar } from "@/components/avatar-with-fallback"
import { AttachMenu, type PickedFile } from "@/components/inbox/attach-menu"
import { CannedPicker } from "@/components/inbox/canned-picker"
import { ChatText, firstUrl } from "@/components/inbox/chat-text"
import { EmailTemplatePicker } from "@/components/inbox/email-template-picker"
import { EmojiPicker } from "@/components/inbox/emoji-picker"
import { LinkPreviewCard } from "@/components/inbox/link-preview"
import { AttachmentList, MessageBody, isStickerOnly } from "@/components/inbox/message-body"
import { VoiceRecorder } from "@/components/inbox/voice-recorder"
import { Button } from "@/components/ui/button"
import { FieldError } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Ltr } from "@/components/copy-field"
import { channelIcon } from "@/lib/inbox-channels"
import { type Conversation, type InboxMessage, type ReplyAttachmentInput } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"

// MH-659: the same set service.go's ReplyWithAttachments rejects an
// attachment for server-side (attachmentUnsupported) — mirrored here so the
// attach/mic controls disable themselves with a reason instead of letting a
// send round-trip just to fail (CLAUDE.md's "don't ship dead controls").
const ATTACHMENT_UNSUPPORTED_CHANNELS = new Set(["telegram", "slack", "discord"])
const MAX_ATTACHMENT_SIZE = 20 * 1024 * 1024

/*
The WhatsApp-style thread (MH-587): grouped bubbles, day separators and a
virtualised, self-scrolling message pane with a sticky composer underneath —
replacing the plain <ol data-messages> list this used to be. Registry-first
per CLAUDE.md: chat-application-01 (@shadcnspace) WAS fetched and its layout
ideas (bubble grouping, sticky composer, its own scroll region) are carried
over here, hand-adapted to real data, because `npx shadcn add` fails in this
worktree — web/components.json has no "registries" key, a pre-existing
environment gap, not something this change caused. chat-bubble-02/-03 are
catalog-listed but returned FORBIDDEN on view/fetch in this environment, so
the composer and bubbles below are hand-built from primitives already in the
system (Textarea, Button, CannedPicker) rather than a literal port of those
two items.

MH-656 made the bubbles WhatsApp's: a tail on the first of a run, the time
and delivery tick inside each bubble (ticks from WhatsApp's own receipts,
messages.status), the sender named inside the bubble in a group, WhatsApp's
*bold* _italic_ ~strike~ formatting with links, and a preview card for the
first URL (components/inbox/link-preview.tsx). Email keeps its card layout.

MH-659 added the composer's attach button (photo/video, document, location),
an emoji picker and a mic button (MediaRecorder voice notes) — see
attach-menu.tsx, emoji-picker.tsx and voice-recorder.tsx. Attachments and
voice notes disable themselves with a reason on telegram/slack/discord, which
the backend's ReplyWithAttachments also refuses (no dead controls).

Not here yet, because the backend cannot back them (no dead controls,
CLAUDE.md non-negotiable) — each is its own card:
- reactions and reply quotes — MH-664.
- an editable reply CHANNEL picker — Reply() always answers through the
  conversation's own channel; see the read-only note instead. The SUBJECT is
  editable on an email conversation (MH-661): `subject`/`onSubjectChange` are
  optional props so floating-chat.tsx, which has no subject state, keeps
  compiling and simply shows no subject field.
*/

const GROUP_GAP_MS = 5 * 60 * 1000
const WHATSAPP_WINDOW_MS = 24 * 60 * 60 * 1000

type Item =
  | { kind: "day"; key: string; label: string }
  | { kind: "group"; key: string; direction: "in" | "out"; group: InboxMessage[] }

function dayKey(iso: string): string {
  const d = new Date(iso)
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`
}

function dayLabel(iso: string, t: (key: string) => string, locale: string): string {
  const d = new Date(iso)
  const now = new Date()
  const startOf = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime()
  const diffDays = Math.round((startOf(now) - startOf(d)) / 86400000)
  if (diffDays === 0) return t("inbox.thread.today")
  if (diffDays === 1) return t("inbox.thread.yesterday")
  return dateFormat(locale, { dateStyle: "long" }).format(d)
}

function timeOf(iso: string, locale: string): string {
  return dateFormat(locale, { timeStyle: "short" }).format(new Date(iso))
}

/** MH-604: the identity a group thread's grouping compares consecutive
 *  inbound messages on — the resolved sender contact when there is one, else
 *  the raw sender name, so two different senders never collapse into one
 *  bubble group even when contacts.Upsert couldn't resolve a contact for one
 *  of them. */
function senderKey(m: InboxMessage): string {
  return m.sender_contact_id || m.sender_name || ""
}

/** MH-600: whether to show message `m`'s own subject line — only when it
 *  differs from the PREVIOUS message's, so three emails from the same sender
 *  with the same subject read as one conversation and a fourth with a new
 *  one still announces itself. This is a flat, whole-thread comparison
 *  (previous message in `messages`, not previous message in the same
 *  render-group) since buildItems groups by sender/proximity, not subject —
 *  two emails a minute apart with different subjects still land in one
 *  group, and each still needs its own header. */
function subjectVisibility(messages: InboxMessage[]): Map<string, boolean> {
  const show = new Map<string, boolean>()
  let prev = ""
  for (const m of messages) {
    const subject = m.subject || ""
    show.set(m.id, subject !== "" && subject !== prev)
    prev = subject
  }
  return show
}

/** Day separators + consecutive-same-sender grouping, in one pass over an already-ordered thread.
 *  isGroup (Conversation.is_group, MH-604) makes an inbound message's SENDER part of "same sender"
 *  too — a Telegram/Slack/Discord group thread has many senders sharing the "in" direction, unlike
 *  a 1:1 thread where every inbound message is the same one customer. */
function buildItems(
  messages: InboxMessage[],
  t: (key: string) => string,
  locale: string,
  isGroup: boolean,
): Item[] {
  const items: Item[] = []
  let lastDay = ""
  let group: InboxMessage[] = []

  function flush() {
    if (group.length === 0) return
    items.push({ kind: "group", key: `g-${group[0].id}`, direction: group[0].direction, group })
    group = []
  }

  for (const m of messages) {
    const dk = dayKey(m.created_at)
    if (dk !== lastDay) {
      flush()
      items.push({ kind: "day", key: `day-${dk}`, label: dayLabel(m.created_at, t, locale) })
      lastDay = dk
    }
    const last = group[group.length - 1]
    const sameSender =
      !!last &&
      last.direction === m.direction &&
      (m.direction === "out"
        ? last.author_user_id === m.author_user_id
        : !isGroup || senderKey(last) === senderKey(m))
    const close =
      !!last &&
      new Date(m.created_at).getTime() - new Date(last.created_at).getTime() < GROUP_GAP_MS
    if (last && sameSender && close) group.push(m)
    else {
      flush()
      group = [m]
    }
  }
  flush()
  return items
}

function DaySeparator({ label }: { label: string }) {
  return (
    <div className="flex items-center justify-center py-3" data-day-separator>
      <span className="rounded-full border border-line bg-grid-elevated px-3 py-1 text-xs text-grid-muted">
        {label}
      </span>
    </div>
  )
}

function MessageGroup({
  group,
  conversation,
  memberName,
  showSubject,
}: {
  group: InboxMessage[]
  conversation: Conversation
  memberName: (id: string | null | undefined) => string
  /** MH-600: does THIS message's own subject differ from the one before it
   *  in the whole thread — see subjectVisibility(). */
  showSubject: (id: string) => boolean
}) {
  const { t, locale } = useTranslations()
  const out = group[0].direction === "out"
  const Icon = channelIcon(conversation.channel)
  // MH-604: a group thread's inbound "who" is whichever sender wrote THIS
  // group, not the conversation's own customer_name — that name belongs to
  // the thread as a whole and means nothing once several people write in it.
  const groupSenderName = group[0].sender_name || t("inbox.customer")
  const who = out
    ? group[0].author_user_id
      ? memberName(group[0].author_user_id)
      : t("inbox.team")
    : conversation.is_group
      ? groupSenderName
      : conversation.customer_name || t("inbox.customer")
  const showSenderAvatar = !out && conversation.is_group

  // An email is a document, not a chat line (MH-592): each one is a
  // full-width card with its own sender and time, so a newsletter's layout,
  // tables and images get the whole pane instead of a 36rem bubble.
  if (conversation.channel === "email") {
    return (
      <div className="flex flex-col gap-2 py-1.5" data-message-group={out ? "out" : "in"}>
        {group.map((m) => (
          <article
            key={m.id}
            data-message={m.direction}
            data-message-layout="email"
            className={`w-full min-w-0 border border-line bg-grid-card text-sm ${out ? "border-s-2 border-s-primary" : ""}`}
          >
            <header className="flex min-w-0 items-center gap-2 border-b border-line px-4 py-2 text-xs text-grid-muted">
              <Icon className="size-3 shrink-0" />
              <span className="min-w-0 truncate font-medium text-grid-fg">
                <Bdi>
                  {out ? (m.author_user_id ? memberName(m.author_user_id) : t("inbox.team")) : who}
                </Bdi>
              </span>
              {showSubject(m.id) ? (
                <span className="min-w-0 flex-1 truncate" data-message-subject>
                  <Bdi>{m.subject}</Bdi>
                </span>
              ) : (
                <span className="flex-1" />
              )}
              <span className="shrink-0">
                <Ltr>{timeOf(m.created_at, locale)}</Ltr>
              </span>
            </header>
            <div className="px-4 py-3">
              <MessageBody body={m.body} bodyHtml={m.body_html} attachments={m.attachments} />
              <AttachmentList attachments={m.attachments} />
            </div>
          </article>
        ))}
      </div>
    )
  }

  return (
    <div
      className={`flex gap-2 py-1 ${out ? "flex-row-reverse" : ""}`}
      data-message-group={out ? "out" : "in"}
    >
      {showSenderAvatar ? (
        <div className="shrink-0 self-start" data-group-sender-avatar>
          <PersonAvatar
            id={group[0].sender_contact_id || group[0].sender_name || undefined}
            name={groupSenderName}
            photoUrl={group[0].sender_avatar_url || null}
            size="sm"
          />
        </div>
      ) : null}
      <div className={`flex min-w-0 flex-1 flex-col gap-0.5 ${out ? "items-end" : "items-start"}`}>
        {group.map((m, i) => (
          <ChatBubble
            key={m.id}
            m={m}
            out={out}
            first={i === 0}
            senderName={i === 0 && showSenderAvatar ? who : null}
          />
        ))}
      </div>
    </div>
  )
}

/** WhatsApp's bubble tail: the first bubble of a run gets one, on the side it
 *  was sent from, and loses that top corner's rounding. */
function BubbleTail({ out }: { out: boolean }) {
  return (
    <svg
      viewBox="0 0 8 13"
      width="8"
      height="13"
      aria-hidden
      className={`absolute top-0 rtl:-scale-x-100 ${out ? "-end-2" : "-start-2"}`}
      style={{ fill: out ? "var(--bubble-out)" : "var(--bubble-in)" }}
    >
      <path
        d={
          out
            ? "M5.188 1H0v11.193l6.467-8.625C7.526 2.156 6.958 1 5.188 1z"
            : "M2.812 1H8v11.193L1.533 3.568C.474 2.156 1.042 1 2.812 1z"
        }
        transform="translate(0 -1)"
      />
    </svg>
  )
}

/** The delivery tick on an outbound bubble (MH-656), from WhatsApp's own
 *  receipts: one grey tick sent, two delivered, two in the brand colour read. */
function Ticks({ status }: { status: InboxMessage["status"] }) {
  const { t } = useTranslations()
  if (!status) return null
  const label = t(`inbox.thread.status.${status}`)
  if (status === "failed")
    return <AlertCircleIcon className="size-3.5 text-grid-danger-text" aria-label={label} />
  const Icon = status === "sent" ? CheckIcon : CheckCheckIcon
  return (
    <Icon
      className={`size-3.5 ${status === "read" ? "text-primary" : ""}`}
      aria-label={label}
      data-tick={status}
    />
  )
}

function ChatBubble({
  m,
  out,
  first,
  senderName,
}: {
  m: InboxMessage
  out: boolean
  first: boolean
  /** A group thread's sender, named inside the first bubble of their run. */
  senderName: string | null
}) {
  const { locale } = useTranslations()
  const url = firstUrl(m.body)
  // A chat line is the text it was written as. An outbound reply also has
  // body_html (its Markdown rendered for email), but a bubble shows the
  // source with WhatsApp's own formatting instead of framing an iframe.
  const text = m.body.trim()
  const files = (m.attachments ?? []).filter((x) => !x.inline).length > 0
  // MH-658: WhatsApp shows a lone sticker with no bubble at all — no
  // background, no padding, no tail — the same way it never boxes a photo
  // frame around one on the phone itself.
  const stickerOnly = isStickerOnly(m.attachments, m.body)
  // WhatsApp's time-and-tick: floated to the end of the LAST line, so it
  // shares that line when there is room and drops under it when there is not
  // — whichever way the text runs. An absolute corner badge overlapped the
  // text whenever its direction was not the page's (Arabic in the English UI).
  const meta = (
    <span
      className="relative top-1.5 float-end ms-2 flex items-center gap-1 text-[0.6875rem] leading-4 text-grid-muted"
      data-message-meta
    >
      <Ltr>{timeOf(m.created_at, locale)}</Ltr>
      {out ? <Ticks status={m.status} /> : null}
    </span>
  )
  return (
    <div
      data-message={m.direction}
      data-message-status={m.status || undefined}
      data-sticker-only={stickerOnly || undefined}
      className={
        stickerOnly
          ? "relative flow-root max-w-[min(36rem,85%)] text-sm text-grid-fg"
          : `relative flow-root max-w-[min(36rem,85%)] rounded-[0.5rem] px-2.5 pt-1.5 pb-2 text-sm text-grid-fg shadow-[0_1px_0.5px_color-mix(in_oklab,var(--grid-fg)_14%,transparent)] ${first ? (out ? "rounded-se-none" : "rounded-ss-none") : ""}`
      }
      style={stickerOnly ? undefined : { background: out ? "var(--bubble-out)" : "var(--bubble-in)" }}
    >
      {first && !stickerOnly ? <BubbleTail out={out} /> : null}
      {senderName ? (
        <p className="mb-0.5 truncate text-xs font-medium text-primary" data-bubble-sender>
          <Bdi>{senderName}</Bdi>
        </p>
      ) : null}
      {url ? <LinkPreviewCard url={url} /> : null}
      {text || !m.body_html ? (
        <div className="text-start break-words whitespace-pre-wrap" dir="auto">
          <ChatText text={m.body} />
          {files ? null : meta}
        </div>
      ) : (
        <MessageBody body={m.body} bodyHtml={m.body_html} attachments={m.attachments} />
      )}
      <AttachmentList attachments={m.attachments} />
      {files || (!text && m.body_html) ? meta : null}
    </div>
  )
}

export function ChatThread({
  conversation,
  messages,
  memberName,
  replyLanguage,
  canWrite,
  reply,
  onReplyChange,
  onSubmit,
  busy,
  error,
  onInsertCanned,
  subject,
  onSubjectChange,
  fill = false,
  hasOlder = false,
  loadingOlder = false,
  onLoadOlder,
  onSendAttachment,
  typing = null,
  onTyping,
}: {
  conversation: Conversation
  messages: InboxMessage[]
  memberName: (id: string | null | undefined) => string
  replyLanguage: "en" | "ar"
  canWrite: boolean
  reply: string
  onReplyChange: (value: string) => void
  onSubmit: (e: FormEvent) => void
  busy: boolean
  error: string | null
  onInsertCanned: (text: string) => void
  /** MH-661: the Subject override for an email reply. Omitted (floating-chat.tsx)
   *  keeps the composer exactly as before — no subject field, no template picker. */
  subject?: string
  onSubjectChange?: (value: string) => void
  /** Fill the parent's height (the launcher panel) instead of the inbox page's fixed one. */
  fill?: boolean
  /** MH-648: is there an older page left to load — omit/false hides the top loader entirely. */
  hasOlder?: boolean
  /** MH-648: a load triggered by scrolling near the top is already in flight. */
  loadingOlder?: boolean
  /** MH-648: load the page before whatever is currently the oldest loaded message. */
  onLoadOlder?: () => void
  /** MH-659: send one attachment (file or location), with the current reply
   *  text as its caption. Omitted keeps the composer exactly as before — no
   *  attach button, emoji or mic (e.g. a caller with nowhere to send one). */
  onSendAttachment?: (attachment: ReplyAttachmentInput, caption: string) => Promise<void>
  /** MH-660: the customer's live typing/recording indicator, or null to show nothing. */
  typing?: { label: string } | null
  /** MH-660: fired as the agent edits the reply, so the caller can tell the
   *  customer "composing" (throttled) — this component does no throttling itself. */
  onTyping?: () => void
}) {
  const { t, locale } = useTranslations()
  const items = buildItems(messages, t, locale, conversation.is_group)
  const subjectShown = subjectVisibility(messages)
  const showSubject = (id: string) => subjectShown.get(id) ?? false

  const parentRef = useRef<HTMLDivElement>(null)
  const formRef = useRef<HTMLFormElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const [cannedOpen, setCannedOpen] = useState(false)
  // MH-659: a picked photo/video/document waits here for Send (it goes out
  // with whatever the reply box holds as its caption); a voice note or a
  // location has no "waiting" state — VoiceRecorder's own send bar and
  // LocationPicker's "use this location" button send immediately.
  const [pendingFile, setPendingFile] = useState<PickedFile | null>(null)
  const [attachBusy, setAttachBusy] = useState(false)
  const [attachError, setAttachError] = useState<string | null>(null)
  const [atBottom, setAtBottom] = useState(true)
  const [newCount, setNewCount] = useState(0)
  const lastIdRef = useRef<string | null>(null)
  const firstRenderRef = useRef(true)
  // MH-648: "load older" scroll-anchor bookkeeping. firstIdRef tracks the
  // oldest currently-loaded message so a prepend can be told apart from an
  // append; olderAnchorRef snapshots scroll geometry at the moment a load is
  // triggered so the view can be restored to the same spot once the older
  // page's messages land above it, instead of jumping.
  const firstIdRef = useRef<string | null>(null)
  const olderAnchorRef = useRef<{ scrollHeight: number; scrollTop: number } | null>(null)

  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 88,
    overscan: 8,
    getItemKey: (index) => items[index]!.key,
  })

  function isNearBottom() {
    const el = parentRef.current
    if (!el) return true
    return el.scrollHeight - el.scrollTop - el.clientHeight < 120
  }

  function scrollToBottom(behavior: ScrollBehavior = "smooth") {
    const el = parentRef.current
    if (!el) return
    el.scrollTo({ top: el.scrollHeight, behavior })
    setAtBottom(true)
    setNewCount(0)
  }

  // An email is read from its first line, not its last (MH-598): the pane
  // lands on the newest email's header, not the bottom of its body. The group
  // holding it is brought into the window first, then the card itself is
  // lined up with the top of the pane.
  const isEmail = conversation.channel === "email"
  function scrollToNewestEmail(behavior: ScrollBehavior = "auto") {
    const el = parentRef.current
    if (!el || items.length === 0) return
    // Not scrollToIndex: it keeps re-scrolling for several frames while sizes
    // settle, which undoes the card alignment below.
    const group = virtualizer.measurementsCache[items.length - 1]
    if (group) el.scrollTop = group.start
    requestAnimationFrame(() => {
      const cards = el.querySelectorAll<HTMLElement>("[data-message-layout=email]")
      const card = cards[cards.length - 1]
      if (card) {
        const offset =
          card.getBoundingClientRect().top -
          el.getBoundingClientRect().top -
          parseFloat(getComputedStyle(el).paddingTop)
        el.scrollTo({ top: el.scrollTop + offset, behavior })
      }
      setNewCount(0)
    })
  }
  const jumpToLatest = (behavior: ScrollBehavior) =>
    isEmail ? scrollToNewestEmail(behavior) : scrollToBottom(behavior)

  // Messages grow after the first paint (an email's frame, an image grid), so
  // one scroll lands short. While the thread's height settles, the pane stays
  // on the latest — the newest email's top, or a chat's bottom — until the
  // reader scrolls or touches it themselves, or a few seconds pass.
  const sizerRef = useRef<HTMLDivElement>(null)
  const anchorRef = useRef(false)
  useEffect(() => {
    const el = parentRef.current
    const sizer = sizerRef.current
    if (!el || !sizer) return
    const release = () => {
      anchorRef.current = false
    }
    const ro = new ResizeObserver(() => {
      if (anchorRef.current) jumpToLatest("auto")
    })
    ro.observe(sizer)
    const stop = window.setTimeout(release, 4000)
    const events = ["wheel", "touchstart", "pointerdown", "keydown"] as const
    events.forEach((e) => el.addEventListener(e, release, { passive: true }))
    return () => {
      ro.disconnect()
      window.clearTimeout(stop)
      events.forEach((e) => el.removeEventListener(e, release))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isEmail])

  // New page of messages (a fresh send, a "Load earlier", or a realtime push):
  // stick to the bottom if we were already there, otherwise surface a "new
  // messages" counter instead of yanking the reader's scroll position.
  useEffect(() => {
    const last = messages[messages.length - 1]?.id ?? null
    // The first load that has messages lands the pane — not the first render,
    // which can be before they arrive.
    if (firstRenderRef.current) {
      if (!last) return
      firstRenderRef.current = false
      lastIdRef.current = last
      anchorRef.current = true
      // A second pass once real heights (an email's iframe, an image grid)
      // have settled — the first paints against `estimateSize`, which can
      // land short of the true bottom for a thread with rich messages.
      requestAnimationFrame(() => jumpToLatest("auto"))
      const settle = window.setTimeout(() => jumpToLatest("auto"), 350)
      return () => window.clearTimeout(settle)
    }
    if (last && last !== lastIdRef.current) {
      // A new email never moves the pane — someone may be halfway down the
      // one above it. The counter says it is there.
      if (!isEmail && isNearBottom()) {
        lastIdRef.current = last
        requestAnimationFrame(() => scrollToBottom("smooth"))
      } else {
        setNewCount((n) => n + 1)
        lastIdRef.current = last
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messages.length])

  // MH-648: an older page just landed at the FRONT of `messages` (the oldest
  // loaded message's id changed). Restore the reader's spot relative to the
  // content they were looking at — not the raw scrollTop, which would now
  // point at whatever just got prepended above it — by adding back exactly
  // how much the scroll height grew. useLayoutEffect: this must happen
  // before the browser paints the new layout, or the jump would be visible
  // for a frame.
  useLayoutEffect(() => {
    const first = messages[0]?.id ?? null
    const anchor = olderAnchorRef.current
    const el = parentRef.current
    if (first && first !== firstIdRef.current && anchor && el) {
      el.scrollTop = anchor.scrollTop + (el.scrollHeight - anchor.scrollHeight)
      olderAnchorRef.current = null
    }
    firstIdRef.current = first
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messages.length])

  function triggerLoadOlder() {
    if (!hasOlder || loadingOlder || !onLoadOlder) return
    const el = parentRef.current
    if (!el) return
    olderAnchorRef.current = { scrollHeight: el.scrollHeight, scrollTop: el.scrollTop }
    onLoadOlder()
  }

  function onScroll() {
    const bottom = isNearBottom()
    setAtBottom(bottom)
    if (bottom) setNewCount(0)
    // Near-top: load the previous page. 200px gives the fetch a head start
    // before the reader actually hits the top of what is loaded. Skipped
    // while anchorRef is still holding the pane on open — that transient
    // scrollTop-near-0 is the initial jump-to-latest animation passing
    // through, not the reader asking for older messages.
    const el = parentRef.current
    if (el && !anchorRef.current && el.scrollTop < 200) triggerLoadOlder()
  }

  // WhatsApp's 24h customer-service window (informational only — nothing here
  // enforces it server-side, so it never blocks Send, just explains a delay).
  let windowNote: string | null = null
  if (conversation.channel === "whatsapp") {
    const lastInbound = [...messages].reverse().find((m) => m.direction === "in")
    if (lastInbound) {
      const elapsed = Date.now() - new Date(lastInbound.created_at).getTime()
      windowNote =
        elapsed < WHATSAPP_WINDOW_MS
          ? t("inbox.thread.windowOpen", {
              hours: String(Math.max(1, Math.ceil((WHATSAPP_WINDOW_MS - elapsed) / 3600000))),
            })
          : t("inbox.thread.windowClosed")
    }
  }
  const emailNote =
    conversation.channel === "email"
      ? t("inbox.thread.emailNote", {
          subject: conversation.subject || t("inbox.thread.noSubject"),
        })
      : null

  function onComposerKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault()
      formRef.current?.requestSubmit()
      return
    }
    if (e.key === "/" && reply.trim() === "") {
      e.preventDefault()
      setCannedOpen(true)
    }
  }

  function insertEmoji(emoji: string) {
    const el = textareaRef.current
    if (!el) {
      onReplyChange(reply + emoji)
      return
    }
    const start = el.selectionStart ?? reply.length
    const end = el.selectionEnd ?? reply.length
    const next = reply.slice(0, start) + emoji + reply.slice(end)
    onReplyChange(next)
    requestAnimationFrame(() => {
      el.focus()
      const caret = start + emoji.length
      el.setSelectionRange(caret, caret)
    })
  }

  function onPickFile(picked: PickedFile) {
    setAttachError(null)
    if (picked.file.size > MAX_ATTACHMENT_SIZE) {
      setAttachError(t("inbox.composer.attachTooLarge"))
      return
    }
    setPendingFile(picked)
  }

  async function sendAttachment(attachment: ReplyAttachmentInput) {
    if (!onSendAttachment) return
    setAttachBusy(true)
    setAttachError(null)
    try {
      await onSendAttachment(attachment, reply)
      setPendingFile(null)
      onReplyChange("")
    } catch {
      setAttachError(t("inbox.composer.sendFailed"))
    } finally {
      setAttachBusy(false)
    }
  }

  async function onPickLocation(lat: number, lng: number, name: string) {
    await sendAttachment({ kind: "location", lat, lng, name: name || undefined })
  }

  async function onVoiceRecorded(blob: Blob, mimeType: string) {
    const ext = mimeType.includes("ogg") ? "ogg" : "webm"
    await sendAttachment({ kind: "voice", file: blob, filename: `voice-note.${ext}` })
  }

  async function handleSubmit(e: FormEvent) {
    if (pendingFile) {
      e.preventDefault()
      await sendAttachment({ kind: pendingFile.kind, file: pendingFile.file })
      return
    }
    onSubmit(e)
  }

  const attachDisabled = ATTACHMENT_UNSUPPORTED_CHANNELS.has(conversation.channel)
  const attachDisabledReason = attachDisabled
    ? t("inbox.composer.attachUnsupported", { channel: t(`inbox.channel.${conversation.channel}`) })
    : undefined
  const micDisabledReason = attachDisabled
    ? t("inbox.composer.micUnsupported", { channel: t(`inbox.channel.${conversation.channel}`) })
    : undefined

  return (
    <div
      className={`relative flex min-h-0 flex-col ${fill ? "h-full" : ""}`}
      style={fill ? undefined : { height: "min(68vh, 40rem)" }}
      data-chat-thread
    >
      <div
        ref={parentRef}
        onScroll={onScroll}
        className="chat-wallpaper relative min-h-0 flex-1 overflow-y-auto px-4 py-2"
        data-messages
        data-anchor={isEmail ? "newest-email" : "bottom"}
      >
        {loadingOlder ? (
          <div
            className="sticky top-0 z-10 flex justify-center py-2"
            data-loading-older
            aria-live="polite"
          >
            <span className="rounded-full border border-line bg-grid-elevated px-3 py-1 text-xs text-grid-muted shadow-sm">
              {t("inbox.thread.loadingOlder")}
            </span>
          </div>
        ) : null}
        <div ref={sizerRef} style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {virtualizer.getVirtualItems().map((vi) => {
            const item = items[vi.index]!
            return (
              <div
                key={item.key}
                ref={virtualizer.measureElement}
                data-index={vi.index}
                style={{
                  position: "absolute",
                  top: 0,
                  insetInlineStart: 0,
                  width: "100%",
                  transform: `translateY(${vi.start}px)`,
                }}
              >
                {item.kind === "day" ? (
                  <DaySeparator label={item.label} />
                ) : (
                  <MessageGroup
                    group={item.group}
                    conversation={conversation}
                    memberName={memberName}
                    showSubject={showSubject}
                  />
                )}
              </div>
            )
          })}
        </div>
      </div>

      {(isEmail ? newCount > 0 : !atBottom) ? (
        <div className="pointer-events-none absolute inset-x-0 bottom-24 flex justify-center">
          <Button
            type="button"
            size="sm"
            variant="secondary"
            className="pointer-events-auto shadow-md"
            onClick={() => jumpToLatest("smooth")}
            data-action="scroll-to-bottom"
          >
            <ArrowDownIcon />
            {newCount > 0
              ? t("inbox.thread.newMessages", { n: String(newCount) })
              : t("inbox.thread.scrollToBottom")}
          </Button>
        </div>
      ) : null}

      {canWrite && conversation.blocked ? (
        // MH-657: the API refuses replies to a blocked customer, so no composer.
        <p
          className="shrink-0 border-t border-line px-4 py-3 text-center text-sm text-grid-muted"
          data-blocked-note
        >
          {t("inbox.org.blockedComposer")}
        </p>
      ) : canWrite ? (
        <form
          ref={formRef}
          onSubmit={handleSubmit}
          noValidate
          className="@container shrink-0 border-t border-line px-4 py-3"
          data-form="reply"
        >
          {windowNote ? (
            <p className="mb-2 text-xs text-grid-muted" data-whatsapp-window>
              {windowNote}
            </p>
          ) : null}
          {typing ? (
            <p className="mb-2 animate-pulse text-xs text-primary" data-typing-indicator>
              <Bdi>{typing.label}</Bdi>
            </p>
          ) : null}
          {isEmail && onSubjectChange ? (
            <Input
              id="reply-subject"
              value={subject ?? ""}
              onChange={(e) => onSubjectChange(e.target.value)}
              placeholder={emailNote ?? t("inbox.emailTemplates.subjectPlaceholder")}
              dir="auto"
              className="mb-2"
              aria-label={t("inbox.emailTemplates.subjectLabel")}
              data-reply-subject
            />
          ) : emailNote ? (
            <p className="mb-2 truncate text-xs text-grid-muted" data-email-reply-note>
              <Bdi>{emailNote}</Bdi>
            </p>
          ) : null}
          {pendingFile ? (
            <div
              className="mb-2 flex items-center gap-2 rounded-md border border-line bg-grid-elevated px-2 py-1.5 text-sm"
              data-pending-attachment
              data-pending-attachment-kind={pendingFile.kind}
            >
              <FileIcon className="size-4 shrink-0 text-grid-muted" />
              <span className="min-w-0 flex-1 truncate" dir="auto">
                {pendingFile.file.name}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                onClick={() => setPendingFile(null)}
                aria-label={t("inbox.composer.attachRemove")}
                data-action="attach-remove"
              >
                <XIcon />
              </Button>
            </div>
          ) : null}
          {/* The reply box takes the full width; under it, the action bar (MH-595):
              quick replies at the start, Send at the end. MH-659 added the
              attach/emoji/mic controls to the same bar. */}
          <Textarea
            id="reply-body"
            ref={textareaRef}
            value={reply}
            onChange={(e) => {
              onReplyChange(e.target.value)
              onTyping?.()
            }}
            onKeyDown={onComposerKeyDown}
            placeholder={t("inbox.replyPlaceholder")}
            rows={2}
            dir="auto"
            className="max-h-40 min-w-0 resize-none"
            aria-label={t("inbox.replyLabel")}
          />
          <div className="mt-2 flex items-center gap-1" data-composer-actions>
            <CannedPicker
              lang={replyLanguage}
              onInsert={onInsertCanned}
              open={cannedOpen}
              onOpenChange={setCannedOpen}
            />
            {isEmail && onSubjectChange ? (
              <EmailTemplatePicker
                conversationId={conversation.id}
                lang={replyLanguage}
                onInsert={({ subject: s, body }) => {
                  onInsertCanned(body)
                  onSubjectChange(s)
                }}
              />
            ) : null}
            {onSendAttachment ? (
              <>
                <AttachMenu
                  disabled={attachDisabled || attachBusy || !!pendingFile}
                  disabledReason={attachDisabledReason}
                  onPickFile={onPickFile}
                  onPickLocation={onPickLocation}
                />
                <EmojiPicker onInsert={insertEmoji} />
              </>
            ) : null}
            {onSendAttachment && !pendingFile && !reply.trim() ? (
              <VoiceRecorder
                disabled={attachDisabled || attachBusy}
                disabledReason={micDisabledReason}
                onRecorded={onVoiceRecorded}
              />
            ) : null}
            <Button
              type="submit"
              size="sm"
              className="ms-auto"
              disabled={busy || attachBusy || (!reply.trim() && !pendingFile)}
              data-action="send"
              aria-label={t("inbox.send")}
            >
              <SendIcon />
              {/* MH-659 added attach/emoji/mic to this bar; with all five
                  controls plus the label, "Send reply" no longer fits at
                  narrow widths (measured: 2px short at 390px) — drop the
                  label under the @lg container breakpoint and rely on the
                  icon + aria-label instead. */}
              <span className="hidden @lg:inline">{t("inbox.send")}</span>
            </Button>
          </div>
          {error ? <FieldError>{error}</FieldError> : null}
          {attachError ? <FieldError data-attach-error>{attachError}</FieldError> : null}
        </form>
      ) : (
        <p className="shrink-0 border-t border-line px-4 py-4 text-sm text-grid-muted">
          {t("inbox.readOnly")}
        </p>
      )}
    </div>
  )
}
