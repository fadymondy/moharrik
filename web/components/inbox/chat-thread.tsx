"use client"

import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ClipboardEvent,
  type DragEvent,
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
  FileUpIcon,
  SendIcon,
  XIcon,
  StarIcon,
} from "lucide-react"

import { Bdi } from "@/components/bidi"
import { PersonAvatar } from "@/components/avatar-with-fallback"
import { AttachMenu, pickedFromFile, type PickedFile } from "@/components/inbox/attach-menu"
import { CannedPicker } from "@/components/inbox/canned-picker"
import { ChatText, firstUrl } from "@/components/inbox/chat-text"
import { EmailTemplatePicker } from "@/components/inbox/email-template-picker"
import { EmojiPicker } from "@/components/inbox/emoji-picker"
import { LinkPreviewCard } from "@/components/inbox/link-preview"
import { AttachmentList, MessageBody, isStickerOnly } from "@/components/inbox/message-body"
import {
  MessageActions,
  QuoteBlock,
  ReactionChips,
  ReplyPreview,
  quoteAuthor,
} from "@/components/inbox/message-reactions"
import { VoiceRecorder } from "@/components/inbox/voice-recorder"
import { Button } from "@/components/ui/button"
import { FieldError } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { Ltr } from "@/components/copy-field"
import { channelIcon } from "@/lib/inbox-channels"
import {
  type Conversation,
  type InboxMessage,
  type InboxReaction,
  type ReactionResult,
  type ReplyAttachmentInput,
} from "@/lib/api"
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

MH-664 added emoji reactions and reply-to-message quotes: hovering (or
tapping) a bubble shows React and Reply beside it, reactions sit as chips
under it, and a quoted message heads the bubble that answers it — clicking
the quote jumps to the original. See message-reactions.tsx. Both are
optional props (onReact / onReplyToChange): without them nothing appears.

Not here yet, because the backend cannot back it (no dead controls,
CLAUDE.md non-negotiable):
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
      <span className="rounded-full border border-nq-line bg-nq-surface-overlay px-3 py-1 text-xs text-muted-foreground">
        {label}
      </span>
    </div>
  )
}

/** MH-664: what a message can do beyond being read — react, be replied to,
 *  and have its quote jump to the original. Any handler omitted hides that
 *  control; reactionsOf applies a just-returned reaction set before the
 *  thread refetches. */
type MessageExtras = {
  customerName: string
  reactionsOf: (m: InboxMessage) => InboxReaction[]
  onReact?: (m: InboxMessage, emoji: string) => void
  onReply?: (m: InboxMessage) => void
  onJump: (id: string) => void
}

function MessageGroup({
  group,
  conversation,
  memberName,
  showSubject,
  selectMode = false,
  selectedIds,
  onToggleSelect,
  extras,
}: {
  group: InboxMessage[]
  conversation: Conversation
  memberName: (id: string | null | undefined) => string
  /** MH-600: does THIS message's own subject differ from the one before it
   *  in the whole thread — see subjectVisibility(). */
  showSubject: (id: string) => boolean
  /** MH-662: select-messages mode — a checkbox appears on every bubble and a
   *  click toggles instead of doing nothing. Omitted/false changes nothing. */
  selectMode?: boolean
  selectedIds?: Set<string>
  onToggleSelect?: (id: string) => void
  extras: MessageExtras
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
            data-message-id={m.id}
            data-message-layout="email"
            className={`group/msg w-full min-w-0 border border-nq-line bg-card text-sm ${out ? "border-s-2 border-s-primary" : ""}`}
          >
            <header className="flex min-w-0 items-center gap-2 border-b border-nq-line px-4 py-2 text-xs text-muted-foreground">
              <Icon className="size-3 shrink-0" />
              <span className="min-w-0 truncate font-medium text-foreground">
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
              {!selectMode && (extras.onReact || extras.onReply) ? (
                <div className="-my-1 shrink-0 opacity-0 transition-opacity group-hover/msg:opacity-100 group-focus-within/msg:opacity-100 has-[[aria-expanded=true]]:opacity-100 [@media(hover:none)]:opacity-100">
                  <MessageActions
                    out={false}
                    onReact={extras.onReact ? (e) => extras.onReact!(m, e) : undefined}
                    onReply={extras.onReply ? () => extras.onReply!(m) : undefined}
                  />
                </div>
              ) : null}
              <span className="shrink-0">
                <Ltr>{timeOf(m.created_at, locale)}</Ltr>
              </span>
            </header>
            <div className="px-4 py-3">
              {m.reply_to ? (
                <QuoteBlock
                  q={m.reply_to}
                  author={quoteAuthor(m.reply_to, memberName, extras.customerName, t)}
                  onJump={extras.onJump}
                  className="mb-2"
                />
              ) : null}
              <MessageBody body={m.body} bodyHtml={m.body_html} attachments={m.attachments} />
              <AttachmentList attachments={m.attachments} />
            </div>
            {extras.reactionsOf(m).length ? (
              <div className="px-2 pb-2">
                <ReactionChips
                  reactions={extras.reactionsOf(m)}
                  channel={conversation.channel}
                  out={false}
                  memberName={memberName}
                  customerName={extras.customerName}
                  onReact={extras.onReact ? (e) => extras.onReact!(m, e) : undefined}
                />
              </div>
            ) : null}
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
            selectMode={selectMode}
            selected={selectedIds?.has(m.id) ?? false}
            onToggleSelect={onToggleSelect}
            channel={conversation.channel}
            memberName={memberName}
            extras={extras}
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
    return <AlertCircleIcon className="size-3.5 text-nq-danger-text" aria-label={label} />
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
  selectMode = false,
  selected = false,
  onToggleSelect,
  channel,
  memberName,
  extras,
}: {
  m: InboxMessage
  out: boolean
  first: boolean
  /** A group thread's sender, named inside the first bubble of their run. */
  senderName: string | null
  /** MH-662: select-messages mode (see MessageGroup). Omitted/false changes nothing. */
  selectMode?: boolean
  selected?: boolean
  onToggleSelect?: (id: string) => void
  channel: Conversation["channel"]
  memberName: (id: string | null | undefined) => string
  extras: MessageExtras
}) {
  const { t, locale } = useTranslations()
  // MH-664: a tap shows the React/Reply buttons on a phone, where there is
  // no hover; the next tap elsewhere hides them again.
  const [active, setActive] = useState(false)
  const reactions = extras.reactionsOf(m)
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
      className="relative top-1.5 float-end ms-2 flex items-center gap-1 text-[0.6875rem] leading-4 text-muted-foreground"
      data-message-meta
    >
      {m.starred ? <StarIcon className="size-3 fill-current text-amber-500" aria-hidden /> : null}
      <Ltr>{timeOf(m.created_at, locale)}</Ltr>
      {out ? <Ticks status={m.status} /> : null}
    </span>
  )
  const bubble = (
    <div
      data-message={m.direction}
      data-message-id={m.id}
      data-message-status={m.status || undefined}
      data-sticker-only={stickerOnly || undefined}
      data-starred={m.starred || undefined}
      className={
        stickerOnly
          ? "relative flow-root max-w-[min(36rem,85%)] text-sm text-foreground"
          : `relative flow-root max-w-[min(36rem,85%)] rounded-[0.5rem] px-2.5 pt-1.5 pb-2 text-sm text-foreground shadow-[0_1px_0.5px_color-mix(in_oklab,var(--nq-fg)_14%,transparent)] ${first ? (out ? "rounded-se-none" : "rounded-ss-none") : ""}`
      }
      style={stickerOnly ? undefined : { background: out ? "var(--bubble-out)" : "var(--bubble-in)" }}
      onClick={selectMode ? () => onToggleSelect?.(m.id) : () => setActive((v) => !v)}
      role={selectMode ? "checkbox" : undefined}
      aria-checked={selectMode ? selected : undefined}
    >
      {first && !stickerOnly ? <BubbleTail out={out} /> : null}
      {senderName ? (
        <p className="mb-0.5 truncate text-xs font-medium text-primary" data-bubble-sender>
          <Bdi>{senderName}</Bdi>
        </p>
      ) : null}
      {m.reply_to ? (
        <QuoteBlock
          q={m.reply_to}
          author={quoteAuthor(m.reply_to, memberName, extras.customerName, t)}
          onJump={selectMode ? undefined : extras.onJump}
          className="mb-1"
        />
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
  // MH-662: select-messages mode adds a checkbox beside the bubble rather
  // than inside it — the bubble's own onClick above still toggles it too, so
  // either target works.
  if (!selectMode) {
    const hasActions = !!(extras.onReact || extras.onReply)
    const chips = reactions.length ? (
      <ReactionChips
        reactions={reactions}
        channel={channel}
        out={out}
        memberName={memberName}
        customerName={extras.customerName}
        onReact={extras.onReact ? (e) => extras.onReact!(m, e) : undefined}
      />
    ) : null
    if (!hasActions && !chips) return bubble
    return (
      <div
        className={`flex w-full flex-col ${out ? "items-end" : "items-start"}`}
        data-message-row={m.id}
        onMouseLeave={() => setActive(false)}
      >
        <div className={`group/msg flex w-full items-center gap-1 ${out ? "flex-row-reverse" : ""}`}>
          {bubble}
          {hasActions ? (
            <div
              className={`shrink-0 transition-opacity group-hover/msg:opacity-100 group-focus-within/msg:opacity-100 has-[[aria-expanded=true]]:opacity-100 ${active ? "opacity-100" : "opacity-0"}`}
            >
              <MessageActions
                out={out}
                onReact={extras.onReact ? (e) => extras.onReact!(m, e) : undefined}
                onReply={extras.onReply ? () => extras.onReply!(m) : undefined}
              />
            </div>
          ) : null}
        </div>
        {chips}
      </div>
    )
  }
  return (
    <label
      className={`flex w-full max-w-[min(36rem,85%)] cursor-pointer items-center gap-2 ${out ? "flex-row-reverse self-end" : "self-start"}`}
      data-message-select-row
    >
      <input
        type="checkbox"
        className="size-4 shrink-0 accent-(--primary)"
        checked={selected}
        onChange={() => onToggleSelect?.(m.id)}
        aria-label={m.id}
      />
      {bubble}
    </label>
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
  selectMode = false,
  selectedIds,
  onToggleSelect,
  scrollToMessageId,
  onReact,
  replyTo = null,
  onReplyToChange,
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
  /** MH-662: select-messages mode — every bubble grows a checkbox. Omitted/false changes nothing. */
  selectMode?: boolean
  selectedIds?: Set<string>
  onToggleSelect?: (id: string) => void
  /** MH-662: in-chat search "jump to match" — set to a message id to scroll the
   *  group containing it into view and flash-highlight the bubble. Changing it
   *  to the same id again still re-triggers (the caller bumps a counter via key
   *  if the reader hits "next" on the same match twice, e.g. after a resize). */
  scrollToMessageId?: string | null
  /** MH-664: react to a message (the same emoji again takes it back). Omitted
   *  hides the React button. The returned set is shown at once; the realtime
   *  "reaction" event's refetch replaces it. */
  onReact?: (messageId: string, emoji: string) => Promise<ReactionResult>
  /** MH-664: the message the next reply quotes, or null. */
  replyTo?: InboxMessage | null
  /** MH-664: pick / clear the quoted message. Omitted hides the Reply button. */
  onReplyToChange?: (m: InboxMessage | null) => void
}) {
  const { t, locale } = useTranslations()
  const items = buildItems(messages, t, locale, conversation.is_group)
  // MH-664: the reaction set a PUT just returned, kept only while the message
  // object it was computed against is still the one on screen — a refetch
  // brings a new `reactions` array and the server's answer wins again.
  const [reacted, setReacted] = useState<
    Record<string, { base: InboxReaction[] | undefined; reactions: InboxReaction[] }>
  >({})
  const reactionsOf = (m: InboxMessage): InboxReaction[] => {
    const o = reacted[m.id]
    if (o && o.base === m.reactions) return o.reactions
    return m.reactions ?? []
  }
  async function react(m: InboxMessage, emoji: string) {
    if (!onReact) return
    try {
      const r = await onReact(m.id, emoji)
      setReacted((prev) => ({ ...prev, [m.id]: { base: m.reactions, reactions: r.reactions } }))
    } catch {
      setAttachError(t("inbox.reactions.failed"))
    }
  }
  const composerOpen = canWrite && !conversation.blocked
  const extras: MessageExtras = {
    customerName: conversation.customer_name || t("inbox.customer"),
    reactionsOf,
    onReact: onReact && canWrite ? react : undefined,
    onReply:
      onReplyToChange && composerOpen
        ? (m) => {
            onReplyToChange(m)
            requestAnimationFrame(() => textareaRef.current?.focus())
          }
        : undefined,
    onJump: (id) => jumpTo(id),
  }
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
  // MH-672: files dragged over the thread. dragenter/dragleave fire for every child the pointer
  // crosses, so the overlay follows a depth count rather than the last event.
  const [dragging, setDragging] = useState(false)
  const dragDepth = useRef(0)
  const [pendingPreview, setPendingPreview] = useState<string | null>(null)
  // A file dropped just outside the thread would otherwise make the browser open it and leave
  // the inbox. Drops some other zone handled are already defaultPrevented and left alone.
  useEffect(() => {
    if (!onSendAttachment) return
    const guard = (e: globalThis.DragEvent) => {
      if (!e.defaultPrevented && Array.from(e.dataTransfer?.types ?? []).includes("Files")) e.preventDefault()
    }
    window.addEventListener("dragover", guard)
    window.addEventListener("drop", guard)
    return () => {
      window.removeEventListener("dragover", guard)
      window.removeEventListener("drop", guard)
    }
  }, [onSendAttachment])
  useEffect(() => {
    if (!pendingFile || pendingFile.kind !== "image") {
      setPendingPreview(null)
      return
    }
    const url = URL.createObjectURL(pendingFile.file)
    setPendingPreview(url)
    return () => URL.revokeObjectURL(url)
  }, [pendingFile])
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

  // MH-662: find-in-chat "jump to match" — scroll the virtualized group
  // holding the target message into view, then, once its real DOM node
  // exists, nudge to center it and flash a highlight.
  // MH-664 reuses the same jump for a quote: jumpTo() scrolls to a loaded
  // message, and when the original is older than what is loaded it pulls
  // earlier pages (pendingJumpRef) until it arrives or there are none left.
  const pendingJumpRef = useRef<string | null>(null)
  function jumpTo(targetId: string): boolean {
    const idx = items.findIndex(
      (it) => it.kind === "group" && it.group.some((m) => m.id === targetId),
    )
    if (idx < 0) {
      if (hasOlder && onLoadOlder) {
        pendingJumpRef.current = targetId
        if (!loadingOlder) onLoadOlder()
      }
      return false
    }
    pendingJumpRef.current = null
    anchorRef.current = false
    virtualizer.scrollToIndex(idx, { align: "center" })
    requestAnimationFrame(() => {
      const el = parentRef.current?.querySelector<HTMLElement>(`[data-message-id="${CSS.escape(targetId)}"]`)
      if (!el) return
      el.scrollIntoView({ block: "center", behavior: "smooth" })
      el.classList.add("mh-search-hit")
      window.setTimeout(() => el.classList.remove("mh-search-hit"), 1600)
    })
    return true
  }
  useEffect(() => {
    if (scrollToMessageId) jumpTo(scrollToMessageId)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollToMessageId])
  useEffect(() => {
    const pending = pendingJumpRef.current
    if (!pending || loadingOlder) return
    if (!jumpTo(pending) && !hasOlder) pendingJumpRef.current = null
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messages.length, loadingOlder, hasOlder])

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
    if (e.key === "Escape" && replyTo && onReplyToChange) {
      e.preventDefault()
      onReplyToChange(null)
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
    // A voice note carries no caption on WhatsApp, so the reply box keeps its text for the next
    // message instead of the thread showing words the customer never received.
    const caption = attachment.kind === "voice" ? "" : reply
    try {
      await onSendAttachment(attachment, caption)
      setPendingFile(null)
      if (caption) onReplyChange("")
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
      await sendAttachment(
        pendingFile.kind === "voice"
          ? { kind: "voice", file: pendingFile.file, filename: pendingFile.file.name || "audio" }
          : { kind: pendingFile.kind, file: pendingFile.file },
      )
      return
    }
    onSubmit(e)
  }

  const attachDisabled = ATTACHMENT_UNSUPPORTED_CHANNELS.has(conversation.channel)
  const attachDisabledReason = attachDisabled
    ? t("inbox.composer.attachUnsupported", { channel: t(`inbox.channel.${conversation.channel}`) })
    : undefined

  // MH-672: drop (or paste) a photo, video, audio file or document to attach it, the same as
  // the paperclip. Only file drags are taken — dragging text or a link keeps its normal meaning.
  const canDrop = !!onSendAttachment && canWrite && !conversation.blocked
  const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes("Files")
  function takeFiles(files: FileList | File[]) {
    const list = Array.from(files)
    if (!list.length) return
    if (attachDisabled) {
      setAttachError(attachDisabledReason ?? null)
      return
    }
    onPickFile(pickedFromFile(list[0]))
    if (list.length > 1) setAttachError(t("inbox.composer.dropOneFile"))
  }
  const dropProps = canDrop
    ? {
        onDragEnter: (e: DragEvent) => {
          if (!hasFiles(e)) return
          e.preventDefault()
          dragDepth.current += 1
          setDragging(true)
        },
        onDragOver: (e: DragEvent) => {
          if (!hasFiles(e)) return
          e.preventDefault()
          e.dataTransfer.dropEffect = attachDisabled || attachBusy ? "none" : "copy"
        },
        onDragLeave: (e: DragEvent) => {
          if (!hasFiles(e)) return
          dragDepth.current = Math.max(0, dragDepth.current - 1)
          if (dragDepth.current === 0) setDragging(false)
        },
        onDrop: (e: DragEvent) => {
          if (!hasFiles(e)) return
          e.preventDefault()
          dragDepth.current = 0
          setDragging(false)
          if (!attachBusy) takeFiles(e.dataTransfer.files)
        },
      }
    : {}
  function onComposerPaste(e: ClipboardEvent<HTMLTextAreaElement>) {
    if (!canDrop || !e.clipboardData.files.length) return
    e.preventDefault()
    takeFiles(e.clipboardData.files)
  }
  const micDisabledReason = attachDisabled
    ? t("inbox.composer.micUnsupported", { channel: t(`inbox.channel.${conversation.channel}`) })
    : undefined

  return (
    <div
      className={`relative flex min-h-0 flex-col ${fill ? "h-full" : ""}`}
      style={fill ? undefined : { height: "min(68vh, 40rem)" }}
      data-chat-thread
      {...dropProps}
    >
      {dragging ? (
        <div
          className="pointer-events-none absolute inset-2 z-30 flex flex-col items-center justify-start gap-2 rounded-lg border-2 border-dashed border-primary bg-nq-surface-overlay/90 px-4 pt-[min(30%,8rem)] text-center backdrop-blur-sm"
          data-drop-zone
          data-drop-disabled={attachDisabled ? "" : undefined}
        >
          <FileUpIcon className="size-8 text-primary" />
          <p className="text-sm font-medium">
            {attachDisabled ? attachDisabledReason : t("inbox.composer.dropToAttach")}
          </p>
          {attachDisabled ? null : (
            <p className="text-xs text-muted-foreground">{t("inbox.composer.dropHint")}</p>
          )}
        </div>
      ) : null}
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
            <span className="rounded-full border border-nq-line bg-nq-surface-overlay px-3 py-1 text-xs text-muted-foreground shadow-sm">
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
                    selectMode={selectMode}
                    selectedIds={selectedIds}
                    onToggleSelect={onToggleSelect}
                    extras={extras}
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
          className="shrink-0 border-t border-nq-line px-4 py-3 text-center text-sm text-muted-foreground"
          data-blocked-note
        >
          {t("inbox.org.blockedComposer")}
        </p>
      ) : canWrite ? (
        <form
          ref={formRef}
          onSubmit={handleSubmit}
          noValidate
          className="@container shrink-0 border-t border-nq-line px-4 py-3"
          data-form="reply"
        >
          {windowNote ? (
            <p className="mb-2 text-xs text-muted-foreground" data-whatsapp-window>
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
            <p className="mb-2 truncate text-xs text-muted-foreground" data-email-reply-note>
              <Bdi>{emailNote}</Bdi>
            </p>
          ) : null}
          {pendingFile ? (
            <div
              className="mb-2 flex items-center gap-2 rounded-md border border-nq-line bg-nq-surface-overlay px-2 py-1.5 text-sm"
              data-pending-attachment
              data-pending-attachment-kind={pendingFile.kind}
            >
              {pendingPreview ? (
                <img
                  src={pendingPreview}
                  alt=""
                  className="size-10 shrink-0 rounded object-cover"
                  data-pending-attachment-preview
                />
              ) : (
                <FileIcon className="size-4 shrink-0 text-muted-foreground" />
              )}
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
          {replyTo && onReplyToChange ? (
            <ReplyPreview
              m={replyTo}
              author={
                replyTo.direction === "in"
                  ? conversation.is_group
                    ? replyTo.sender_name || t("inbox.customer")
                    : extras.customerName
                  : replyTo.author_user_id
                    ? memberName(replyTo.author_user_id)
                    : t("inbox.team")
              }
              onCancel={() => onReplyToChange(null)}
              onJump={(id) => jumpTo(id)}
            />
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
            onPaste={onComposerPaste}
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
        <p className="shrink-0 border-t border-nq-line px-4 py-4 text-sm text-muted-foreground">
          {t("inbox.readOnly")}
        </p>
      )}
    </div>
  )
}
