"use client"

import { useState } from "react"
import { PlusIcon, ReplyIcon, SmilePlusIcon, XIcon } from "lucide-react"

import { Bdi } from "@/components/bidi"
import { EmojiGrid } from "@/components/inbox/emoji-picker"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Conversation, InboxMessage, InboxReaction, InboxReplyTo } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

/*
MH-664: emoji reactions and reply-to-message quotes on a bubble.

- MessageActions: what hovering (or tapping, on a phone) a bubble reveals —
  React (six quick emoji, "+" for the full MH-659 picker) and Reply.
- ReactionChips: one chip per emoji under the bubble; mine is outlined, and
  clicking a chip reacts with that emoji (so clicking my own takes it back).
- QuoteBlock: the quoted message inside a bubble / email card; clicking it
  jumps to the original (MH-662's scrollToMessageId path).
- ReplyPreview: the same quote above the composer while replying.

Only WhatsApp carries a reaction to the customer. Everywhere else it is stored
in Moharrik only, and every chip says so ("Shown here only — not delivered on
{channel}") rather than pretending it went out.
*/

export const QUICK_REACTIONS = ["👍", "❤️", "😂", "😮", "😢", "🙏"] as const

/** Channels whose reaction is actually delivered to the other side. */
export function reactionsDelivered(channel: Conversation["channel"]): boolean {
  return channel === "whatsapp"
}

export function MessageActions({
  out,
  onReact,
  onReply,
  onOpenChange,
}: {
  out: boolean
  onReact?: (emoji: string) => void
  onReply?: () => void
  /** Keeps the bar visible while its popover is open (the pointer has left the bubble). */
  onOpenChange?: (open: boolean) => void
}) {
  const { t } = useTranslations()
  const [open, setOpen] = useState(false)
  const [full, setFull] = useState(false)
  const set = (v: boolean) => {
    setOpen(v)
    if (!v) setFull(false)
    onOpenChange?.(v)
  }
  const pick = (emoji: string) => {
    onReact?.(emoji)
    set(false)
  }
  return (
    <div className={`flex shrink-0 items-center gap-0.5 ${out ? "flex-row-reverse" : ""}`} data-message-actions>
      {onReact ? (
        <Popover open={open} onOpenChange={set}>
          <PopoverTrigger
            render={
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="rounded-full text-muted-foreground"
                data-action="react"
                aria-label={t("inbox.reactions.react")}
              />
            }
          >
            <SmilePlusIcon />
          </PopoverTrigger>
          <PopoverContent
            side="top"
            align={out ? "end" : "start"}
            className={full ? "w-72 p-2" : "w-auto rounded-full p-1"}
            data-reaction-bar
          >
            {full ? (
              <EmojiGrid onPick={pick} />
            ) : (
              <div className="flex items-center gap-0.5">
                {QUICK_REACTIONS.map((e) => (
                  <button
                    key={e}
                    type="button"
                    className="flex size-9 items-center justify-center rounded-full text-xl transition-transform hover:scale-110 hover:bg-nq-surface-overlay"
                    data-quick-reaction={e}
                    onClick={() => pick(e)}
                  >
                    {e}
                  </button>
                ))}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  className="rounded-full"
                  aria-label={t("inbox.reactions.more")}
                  data-action="react-more"
                  onClick={() => setFull(true)}
                >
                  <PlusIcon />
                </Button>
              </div>
            )}
          </PopoverContent>
        </Popover>
      ) : null}
      {onReply ? (
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="rounded-full text-muted-foreground"
          data-action="reply-to"
          aria-label={t("inbox.reactions.reply")}
          onClick={onReply}
        >
          <ReplyIcon className="rtl:-scale-x-100" />
        </Button>
      ) : null}
    </div>
  )
}

export function ReactionChips({
  reactions,
  channel,
  out,
  memberName,
  customerName,
  onReact,
}: {
  reactions: InboxReaction[]
  channel: Conversation["channel"]
  out: boolean
  memberName: (id: string | null | undefined) => string
  customerName: string
  onReact?: (emoji: string) => void
}) {
  const { t } = useTranslations()
  if (!reactions.length) return null
  const inAppOnly = reactionsDelivered(channel)
    ? null
    : t("inbox.reactions.inAppOnly", { channel: t(`inbox.channel.${channel}`) })
  const who = (r: InboxReaction) =>
    r.reactors
      .map((x) =>
        x.kind === "customer"
          ? customerName
          : x.kind === "phone"
            ? t("inbox.reactions.phone")
            : memberName(x.user_id),
      )
      .join(", ")
  return (
    <div
      className={`relative z-[1] -mt-1.5 flex flex-wrap gap-1 px-2 ${out ? "justify-end" : "justify-start"}`}
      data-reactions
    >
      {reactions.map((r) => (
        <Tooltip key={r.emoji}>
          <TooltipTrigger
            render={
              <button
                type="button"
                disabled={!onReact}
                onClick={() => onReact?.(r.emoji)}
                className={`flex h-6 items-center gap-1 rounded-full border bg-nq-surface-overlay px-1.5 text-sm leading-none shadow-sm ${r.mine ? "border-primary" : "border-nq-line"}`}
                data-reaction={r.emoji}
                data-reaction-mine={r.mine || undefined}
                data-reaction-in-app={inAppOnly ? "" : undefined}
                aria-pressed={r.mine}
                aria-label={`${r.emoji} ${r.count}`}
              />
            }
          >
            <span>{r.emoji}</span>
            {r.count > 1 ? <span className="text-xs text-muted-foreground tabular-nums">{r.count}</span> : null}
          </TooltipTrigger>
          <TooltipContent className="max-w-64">
            <span className="block">
              <Bdi>{who(r)}</Bdi>
            </span>
            {inAppOnly ? (
              <span className="block opacity-80" data-reaction-in-app-note>
                {inAppOnly}
              </span>
            ) : null}
          </TooltipContent>
        </Tooltip>
      ))}
    </div>
  )
}

/** Who wrote the quoted message, as the quote's small header says it. */
export function quoteAuthor(
  q: InboxReplyTo,
  memberName: (id: string | null | undefined) => string,
  customerName: string,
  t: (key: string) => string,
): string {
  if (q.direction === "in") return customerName
  if (q.direction === "out") return q.author_user_id ? memberName(q.author_user_id) : t("inbox.team")
  return customerName
}

export function QuoteBlock({
  q,
  author,
  onJump,
  className = "",
}: {
  q: InboxReplyTo
  author: string
  onJump?: (id: string) => void
  className?: string
}) {
  const { t } = useTranslations()
  const text = q.body.trim() || (q.attachment ? t("inbox.quote.attachment") : "")
  const canJump = !!q.message_id && !!onJump
  const inner = (
    <>
      <span className="block truncate text-xs font-medium text-primary">
        <Bdi>{author}</Bdi>
      </span>
      <span className="line-clamp-2 text-xs break-words whitespace-pre-wrap text-muted-foreground" dir="auto">
        {text}
      </span>
    </>
  )
  const cls = `block w-full min-w-0 rounded-md border-s-4 border-s-primary bg-[color-mix(in_oklab,var(--nq-fg)_6%,transparent)] px-2 py-1 text-start ${className}`
  if (!canJump)
    return (
      <div className={cls} data-quote data-quote-id={q.message_id ?? undefined}>
        {inner}
      </div>
    )
  return (
    <button
      type="button"
      className={`${cls} cursor-pointer hover:bg-[color-mix(in_oklab,var(--nq-fg)_10%,transparent)]`}
      onClick={(e) => {
        e.stopPropagation()
        onJump!(q.message_id!)
      }}
      aria-label={t("inbox.quote.jump")}
      data-quote
      data-quote-id={q.message_id!}
    >
      {inner}
    </button>
  )
}

/** The message being replied to, above the composer, with a way out. */
export function ReplyPreview({
  m,
  author,
  onCancel,
  onJump,
}: {
  m: InboxMessage
  author: string
  onCancel: () => void
  onJump?: (id: string) => void
}) {
  const { t } = useTranslations()
  const q: InboxReplyTo = {
    message_id: m.id,
    body: m.body,
    direction: m.direction,
    author_user_id: m.author_user_id,
    attachment: (m.attachments ?? []).some((a) => !a.inline),
  }
  return (
    <div className="mb-2 flex items-center gap-2" data-reply-preview={m.id}>
      <div className="min-w-0 flex-1">
        <span className="sr-only">{t("inbox.quote.replyingTo", { name: author })}</span>
        <QuoteBlock q={q} author={author} onJump={onJump} />
      </div>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        onClick={onCancel}
        aria-label={t("inbox.quote.cancel")}
        data-action="reply-to-cancel"
      >
        <XIcon />
      </Button>
    </div>
  )
}
