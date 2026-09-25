"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"
import { useState, type FormEvent } from "react"
import { ArrowLeftIcon, MessageCircleIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { useMemberNames } from "@/components/crm/shared"
import { hrefFor } from "@/components/alerts/notification-bell"
import { PersonAvatar } from "@/components/avatar-with-fallback"
import { Bdi, BdiBlock } from "@/components/bidi"
import { ChatThread } from "@/components/inbox/chat-thread"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ErrorState, LoadingRows } from "@/components/states"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet"
import { api, ApiError, type BellItem, type InboxMessage } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { relativeTimeFormat } from "@/lib/i18n-locale"
import { fetchOlderMessages, mergeMessages, useBell, useConversation } from "@/lib/queries"

/*
The floating chat launcher (MH-587): every workspace page except the inbox
itself (which already IS the full chat view) gets a corner button with the
unread conversation count. It opens a panel that lists the same conversations
the bell already tracks (/api/w/alerts, MH-378) and, once one is picked,
reuses ChatThread — the identical WhatsApp-style bubbles/composer as the full
inbox page — so a reply typed here and one typed on /inbox behave the same.

Scoped down from the full inbox thread on purpose: no assignee/snooze/close
controls — this is a quick-glance/quick-reply surface, not a second inbox.
That is a feature-set choice, not a dead control: everything rendered here
works. It DOES get MH-648's "load earlier" paging, though: a thread opened
here can be just as long as one opened on /inbox (an 80-message WhatsApp
history doesn't get shorter because it's in the launcher), so ChatThread's
scroll-up-to-load-more has to work here too.

@shadcnspace/chat-bubble-02 and -03 (the launcher patterns named in the
card) returned FORBIDDEN when fetched via the shadcn MCP server in this
environment — catalog-listed, not installable/viewable here — so this is
hand-built from Sheet/Avatar/ScrollArea/Badge, the same primitives the bell
above already uses for its own popover.
*/

function useRelative() {
  const { locale } = useTranslations()
  return (iso: string) => {
    const s = Math.round((new Date(iso).getTime() - Date.now()) / 1000)
    const rtf = relativeTimeFormat(locale, { numeric: "auto", style: "short" })
    const abs = Math.abs(s)
    if (abs < 60) return rtf.format(s, "second")
    if (abs < 3600) return rtf.format(Math.round(s / 60), "minute")
    if (abs < 86400) return rtf.format(Math.round(s / 3600), "hour")
    return rtf.format(Math.round(s / 86400), "day")
  }
}

function ConversationList({ items, onOpen }: { items: BellItem[]; onOpen: (id: string) => void }) {
  const { t } = useTranslations()
  const relative = useRelative()
  if (items.length === 0) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
        <MessageCircleIcon className="size-8 text-grid-muted/50" />
        <p className="text-sm text-grid-muted">{t("inbox.launcher.empty")}</p>
      </div>
    )
  }
  return (
    <ul className="divide-y divide-line">
      {items.map((item) => {
        const name = item.title || t("alerts.visitor")
        return (
          <li
            key={item.id}
            data-launcher-conversation={item.id}
            data-launcher-unread={item.unread ? "true" : "false"}
          >
            <button
              type="button"
              onClick={() => onOpen(item.id)}
              className="flex w-full items-start gap-3 px-4 py-3 text-start transition-colors hover:bg-grid-soft"
            >
              <PersonAvatar
                id={item.id}
                name={name}
                photoUrl={item.avatar_url}
                className="size-9 shrink-0"
              />
              <div className="min-w-0 flex-1">
                <span className="flex items-center gap-2">
                  <BdiBlock className="truncate text-sm font-medium">{name}</BdiBlock>
                  {item.unread ? (
                    <span
                      className="size-2 shrink-0 rounded-full bg-grid-action"
                      aria-label={t("inbox.unread")}
                    />
                  ) : null}
                </span>
                {item.snippet ? (
                  <BdiBlock className="mt-0.5 block truncate text-xs text-grid-muted">
                    {item.snippet}
                  </BdiBlock>
                ) : null}
                <p className="mt-1 text-xs text-grid-muted">
                  {item.channel ? `${t(`inbox.channel.${item.channel}`)} · ` : ""}
                  {relative(item.at)}
                </p>
              </div>
            </button>
          </li>
        )
      })}
    </ul>
  )
}

function LauncherThread({
  workspaceId,
  id,
  canWrite,
  onChanged,
}: {
  workspaceId: string
  id: string
  canWrite: boolean
  onChanged: () => void
}) {
  const { t } = useTranslations()
  const detail = useConversation(workspaceId, id)
  const members = useMemberNames(workspaceId)
  const [reply, setReply] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  // MH-648: same "load earlier" shape as the full inbox thread (page.tsx's
  // Thread) — opening shows the newest page, ChatThread's scroll-up loads
  // what came before it, and a message arriving mid-scroll can't open a gap
  // because `earlier` and the live page are merged, never replaced.
  const [earlier, setEarlier] = useState<{ messages: InboxMessage[]; cursor: string } | null>(null)
  const [loadingEarlier, setLoadingEarlier] = useState(false)

  if (detail.error) return <ErrorState error={detail.error} />
  if (!detail.data) return <LoadingRows rows={4} />
  const { conversation, reply_language: replyLang } = detail.data
  const messages = mergeMessages(earlier?.messages ?? [], detail.data.messages)
  const cursor = earlier ? earlier.cursor : detail.data.messages_next_cursor

  async function loadEarlier() {
    if (!cursor || !detail.data) return
    setLoadingEarlier(true)
    try {
      const page = await fetchOlderMessages(workspaceId, id, cursor)
      setEarlier({ messages: mergeMessages(page.messages, messages), cursor: page.next_cursor })
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setLoadingEarlier(false)
    }
  }

  async function send(e: FormEvent) {
    e.preventDefault()
    const text = reply.trim()
    if (!text) return
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/conversations/${encodeURIComponent(id)}/reply`, {
        json: { body: text },
        workspaceId,
      })
      setReply("")
      detail.mutate()
      onChanged()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  function insert(text: string) {
    setReply((cur) => (cur.trim() ? `${cur.trimEnd()}\n${text}` : text))
  }

  return (
    <ChatThread
      fill
      conversation={conversation}
      messages={messages}
      memberName={members.name}
      replyLanguage={replyLang}
      canWrite={canWrite}
      reply={reply}
      onReplyChange={setReply}
      onSubmit={send}
      busy={busy}
      error={err}
      onInsertCanned={insert}
      hasOlder={!!cursor}
      loadingOlder={loadingEarlier}
      onLoadOlder={loadEarlier}
    />
  )
}

export function FloatingChat({
  workspaceId,
  slug,
  can,
}: {
  workspaceId: string
  slug: string
  can: (permission: string) => boolean
}) {
  const { t, locale, isRtl } = useTranslations()
  const pathname = usePathname()
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState<string | null>(null)
  const bell = useBell(workspaceId)
  const canWrite = can("inbox:write")

  useWorkspaceEvents(workspaceId, (e) => {
    if (e.kind === "message" || e.kind === "conversation") void bell.mutate()
  })

  // The full inbox at /inbox (and its sub-routes) already IS the chat UI —
  // a second floating copy of it on top of itself would be redundant, and
  // MH-571's Channels page under /inbox/channels must render exactly as it
  // does today, untouched by anything this launcher adds.
  const onInboxRoute = pathname?.includes("/inbox") ?? false
  const data = bell.data
  if (onInboxRoute || !data?.can_inbox) return null

  const conversations = (data.items ?? []).filter((i) => i.kind === "conversation")
  const unread = data.unread_conversations

  function close() {
    setOpen(false)
    setSelected(null)
  }

  return (
    <>
      <Button
        type="button"
        size="icon-lg"
        className="fixed bottom-5 end-5 z-40 size-14 rounded-full shadow-lg"
        aria-label={
          unread > 0 ? t("inbox.launcher.openUnread", { n: unread }) : t("inbox.launcher.open")
        }
        onClick={() => setOpen(true)}
        data-floating-chat
      >
        <MessageCircleIcon className="size-6" />
        {unread > 0 ? (
          <Badge
            className="pointer-events-none absolute -top-1 -end-1 h-5 min-w-5 rounded-full px-1 text-xs leading-none tabular-nums"
            data-floating-chat-count={unread}
          >
            {unread > 99 ? "99+" : unread}
          </Badge>
        ) : null}
      </Button>

      <Sheet open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
        <SheetContent
          side={isRtl ? "left" : "right"}
          className="flex w-96 max-w-[calc(100vw-1.5rem)] flex-col gap-0 p-0"
          data-floating-chat-panel
        >
          <SheetTitle className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-4">
            {selected ? (
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={t("common.back")}
                onClick={() => setSelected(null)}
                data-floating-chat-back
              >
                <ArrowLeftIcon className="rtl:-scale-x-100" />
              </Button>
            ) : (
              <MessageCircleIcon className="size-4" />
            )}
            <span className="min-w-0 flex-1 truncate text-sm font-medium">
              {selected ? (
                <Bdi>
                  {conversations.find((c) => c.id === selected)?.title || t("inbox.launcher.title")}
                </Bdi>
              ) : (
                t("inbox.launcher.title")
              )}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={t("common.close")}
              onClick={close}
              data-floating-chat-close
            >
              <XIcon />
            </Button>
          </SheetTitle>

          {selected ? (
            <div className="flex min-h-0 flex-1 flex-col">
              <LauncherThread
                workspaceId={workspaceId}
                id={selected}
                canWrite={canWrite}
                onChanged={() => void bell.mutate()}
              />
            </div>
          ) : (
            <ScrollArea className="min-h-0 flex-1">
              <ConversationList items={conversations} onOpen={setSelected} />
            </ScrollArea>
          )}

          <div className="shrink-0 border-t border-line px-3 py-2">
            <Button
              variant="ghost"
              size="sm"
              className="w-full"
              nativeButton={false}
              render={
                <Link
                  href={
                    selected
                      ? hrefFor({ kind: "conversation", id: selected }, locale, slug)
                      : `/${locale}/w/${slug}/inbox`
                  }
                  onClick={close}
                />
              }
              data-floating-chat-open-inbox
            >
              {t("inbox.launcher.openFullInbox")}
            </Button>
          </div>
        </SheetContent>
      </Sheet>
    </>
  )
}
