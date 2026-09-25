"use client"

import { useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { ArrowDownIcon, SendIcon } from "lucide-react"

import { Bdi } from "@/components/bidi"
import { CannedPicker } from "@/components/inbox/canned-picker"
import { AttachmentList, MessageBody } from "@/components/inbox/message-body"
import { Button } from "@/components/ui/button"
import { FieldError } from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import { Ltr } from "@/components/copy-field"
import { channelIcon, channelLabel } from "@/lib/inbox-channels"
import { type Conversation, type InboxMessage } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"

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

Deliberately NOT here, because the backend cannot back them (no dead
controls, CLAUDE.md non-negotiable):
- delivery ticks — InboxMessage carries no per-message status/delivery field.
- an attach button — Reply(id, text) takes no upload/attachment parameter.
- an editable reply-channel or subject picker — Reply() always answers
  through the conversation's own channel; see the read-only note instead.
The per-message channel badge uses the CONVERSATION's channel (every message
in a thread shares it today) — true per-message channels need the unified-
identity work tracked separately as MH-583, not yet built.
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

/** Day separators + consecutive-same-sender grouping, in one pass over an already-ordered thread. */
function buildItems(messages: InboxMessage[], t: (key: string) => string, locale: string): Item[] {
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
    const sameSender = !!last && last.direction === m.direction && (m.direction !== "out" || last.author_user_id === m.author_user_id)
    const close = !!last && new Date(m.created_at).getTime() - new Date(last.created_at).getTime() < GROUP_GAP_MS
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
      <span className="rounded-full border border-line bg-grid-elevated px-3 py-1 text-xs text-grid-muted">{label}</span>
    </div>
  )
}

function MessageGroup({
  group,
  conversation,
  memberName,
}: {
  group: InboxMessage[]
  conversation: Conversation
  memberName: (id: string | null | undefined) => string
}) {
  const { t, locale } = useTranslations()
  const out = group[0].direction === "out"
  const Icon = channelIcon(conversation.channel)
  const who = out ? (group[0].author_user_id ? memberName(group[0].author_user_id) : t("inbox.team")) : conversation.customer_name || t("inbox.customer")

  return (
    <div className={`flex flex-col gap-1 py-1.5 ${out ? "items-end" : "items-start"}`} data-message-group={out ? "out" : "in"}>
      <p className={`flex items-center gap-1 px-1 text-xs text-grid-muted ${out ? "flex-row-reverse" : ""}`}>
        {out ? null : <Bdi>{who}</Bdi>}
        <span className="inline-flex items-center gap-1" data-message-channel={conversation.channel}>
          <Icon className="size-3" />
          {channelLabel(conversation.channel, t)}
        </span>
      </p>
      <div className="flex w-full flex-col gap-0.5">
        {group.map((m) => (
          <div
            key={m.id}
            data-message={m.direction}
            className={`max-w-[36rem] rounded-2xl px-3.5 py-2 text-sm ${out ? "self-end bg-primary text-primary-foreground" : "self-start border border-line bg-grid-card"}`}
          >
            {conversation.channel === "email" && conversation.subject ? (
              <p className={`mb-1 truncate text-xs font-medium ${out ? "text-primary-foreground/80" : "text-grid-muted"}`} data-message-subject>
                <Bdi>{conversation.subject}</Bdi>
              </p>
            ) : null}
            <MessageBody body={m.body} bodyHtml={m.body_html} />
            <AttachmentList attachments={m.attachments} />
          </div>
        ))}
      </div>
      <p className="px-1 text-xs text-grid-muted">
        <Ltr>{timeOf(group[group.length - 1].created_at, locale)}</Ltr>
      </p>
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
  fill = false,
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
  /** Fill the parent's height (the launcher panel) instead of the inbox page's fixed one. */
  fill?: boolean
}) {
  const { t, locale } = useTranslations()
  const items = buildItems(messages, t, locale)

  const parentRef = useRef<HTMLDivElement>(null)
  const formRef = useRef<HTMLFormElement>(null)
  const [cannedOpen, setCannedOpen] = useState(false)
  const [atBottom, setAtBottom] = useState(true)
  const [newCount, setNewCount] = useState(0)
  const lastIdRef = useRef<string | null>(null)
  const firstRenderRef = useRef(true)

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

  // New page of messages (a fresh send, a "Load earlier", or a realtime push):
  // stick to the bottom if we were already there, otherwise surface a "new
  // messages" counter instead of yanking the reader's scroll position.
  useEffect(() => {
    const last = messages[messages.length - 1]?.id ?? null
    if (firstRenderRef.current) {
      firstRenderRef.current = false
      lastIdRef.current = last
      // A second pass once real heights (an email's iframe, an image grid)
      // have settled — the first paints against `estimateSize`, which can
      // land short of the true bottom for a thread with rich messages.
      requestAnimationFrame(() => scrollToBottom("auto"))
      const settle = window.setTimeout(() => scrollToBottom("auto"), 350)
      return () => window.clearTimeout(settle)
    }
    if (last && last !== lastIdRef.current) {
      if (isNearBottom()) {
        lastIdRef.current = last
        requestAnimationFrame(() => scrollToBottom("smooth"))
      } else {
        setNewCount((n) => n + 1)
        lastIdRef.current = last
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messages.length])

  function onScroll() {
    const bottom = isNearBottom()
    setAtBottom(bottom)
    if (bottom) setNewCount(0)
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
          ? t("inbox.thread.windowOpen", { hours: String(Math.max(1, Math.ceil((WHATSAPP_WINDOW_MS - elapsed) / 3600000))) })
          : t("inbox.thread.windowClosed")
    }
  }
  const emailNote = conversation.channel === "email" ? t("inbox.thread.emailNote", { subject: conversation.subject || t("inbox.thread.noSubject") }) : null

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

  return (
    <div
      className={`relative flex min-h-0 flex-col ${fill ? "h-full" : ""}`}
      style={fill ? undefined : { height: "min(68vh, 40rem)" }}
      data-chat-thread
    >
      <div
        ref={parentRef}
        onScroll={onScroll}
        className="relative min-h-0 flex-1 overflow-y-auto bg-[image:var(--grid-hatch)] bg-grid-bg px-4 py-2"
        data-messages
      >
        <div style={{ height: virtualizer.getTotalSize(), position: "relative" }}>
          {virtualizer.getVirtualItems().map((vi) => {
            const item = items[vi.index]!
            return (
              <div
                key={item.key}
                ref={virtualizer.measureElement}
                data-index={vi.index}
                style={{ position: "absolute", top: 0, insetInlineStart: 0, width: "100%", transform: `translateY(${vi.start}px)` }}
              >
                {item.kind === "day" ? (
                  <DaySeparator label={item.label} />
                ) : (
                  <MessageGroup group={item.group} conversation={conversation} memberName={memberName} />
                )}
              </div>
            )
          })}
        </div>
      </div>

      {!atBottom ? (
        <div className="pointer-events-none absolute inset-x-0 bottom-24 flex justify-center">
          <Button
            type="button"
            size="sm"
            variant="secondary"
            className="pointer-events-auto shadow-md"
            onClick={() => scrollToBottom("smooth")}
            data-action="scroll-to-bottom"
          >
            <ArrowDownIcon />
            {newCount > 0 ? t("inbox.thread.newMessages", { n: String(newCount) }) : t("inbox.thread.scrollToBottom")}
          </Button>
        </div>
      ) : null}

      {canWrite ? (
        <form ref={formRef} onSubmit={onSubmit} noValidate className="@container shrink-0 border-t border-line px-4 py-3" data-form="reply">
          {windowNote ? (
            <p className="mb-2 text-xs text-grid-muted" data-whatsapp-window>
              {windowNote}
            </p>
          ) : null}
          {emailNote ? (
            <p className="mb-2 truncate text-xs text-grid-muted" data-email-reply-note>
              <Bdi>{emailNote}</Bdi>
            </p>
          ) : null}
          {/* One row when there is room (the inbox page); in a narrow container (the
              launcher panel) the reply box takes a full line and the actions sit under it. */}
          <div className="flex flex-wrap items-end justify-end gap-2">
            <Textarea
              id="reply-body"
              value={reply}
              onChange={(e) => onReplyChange(e.target.value)}
              onKeyDown={onComposerKeyDown}
              placeholder={t("inbox.replyPlaceholder")}
              rows={1}
              dir="auto"
              className="max-h-40 min-w-0 basis-full resize-none @lg:basis-0 @lg:flex-1"
              aria-label={t("inbox.replyLabel")}
            />
            <CannedPicker lang={replyLanguage} onInsert={onInsertCanned} open={cannedOpen} onOpenChange={setCannedOpen} />
            <Button type="submit" size="icon" disabled={busy || !reply.trim()} aria-label={t("inbox.send")} data-action="send">
              <SendIcon />
            </Button>
          </div>
          {error ? <FieldError>{error}</FieldError> : null}
        </form>
      ) : (
        <p className="shrink-0 border-t border-line px-4 py-4 text-sm text-grid-muted">{t("inbox.readOnly")}</p>
      )}
    </div>
  )
}
