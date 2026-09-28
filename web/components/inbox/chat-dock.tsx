"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"
import { useEffect, useMemo, useState, type FormEvent } from "react"
import { useSWRConfig } from "swr"
import {
  MessageCircleIcon,
  MinusIcon,
  SearchIcon,
  XIcon,
} from "lucide-react"
import { toast } from "sonner"

import { useMemberNames } from "@/components/crm/shared"
import { hrefFor } from "@/components/alerts/notification-bell"
import { PersonAvatar } from "@/components/avatar-with-fallback"
import { Bdi, BdiBlock } from "@/components/bidi"
import { ChatThread } from "@/components/inbox/chat-thread"
import { ContactInfoPanel } from "@/components/inbox/contact-info-panel"
import { ThreadHeaderMenu } from "@/components/inbox/thread-header-menu"
import { ThreadSearch } from "@/components/inbox/thread-search"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ErrorState, LoadingRows } from "@/components/states"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { ScrollArea } from "@/components/ui/scroll-area"
import { api, ApiError, type BellItem, type InboxMessage } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { relativeTimeFormat } from "@/lib/i18n-locale"
import { fetchOlderMessages, mergeMessages, useBell, useConversation } from "@/lib/queries"

/*
The docked chat inbox (MH-663): replaces the MH-587 floating launcher/side-over
(floating-chat.tsx, removed) with the to-go.dev UI-Copilot pattern Fady asked
for — a small pill launcher pinned at the bottom, inline-end side, on every
workspace page except the inbox (which already IS the chat UI), and every chat
opened from it stacks at the bottom edge as its own small window instead of
taking over a side panel.

Desktop: up to DESKTOP_CAP windows sit side by side, growing from the launcher
toward the inline-start; past the cap the oldest one collapses to a bare avatar
bubble (still one click from reopening — never lost, just out of the way).
Mobile (narrower than the `sm` breakpoint): only one window at a time, full
width, as a bottom sheet.

Each window reuses ChatThread — the identical thread/composer the full inbox
renders — so a reply typed from a dock and one typed on /inbox behave the same
and opening a dock marks it read the same way (GET /api/w/conversations/{id}
does that server-side; see routes.go's "opening it marks it read").

Docked/minimised state is small (just ids + a minimised flag) and persisted to
localStorage per workspace, so it survives client navigation and a reload —
the actual conversation content always comes fresh from the API/SWR cache, the
dock only remembers WHICH threads were open.

New inbound messages pop the matching dock open (or, if it is already there
but minimised, just leave the badge to tell the story) in realtime — using the
`alert` field the backend already computes per-viewer per conversation
(internal/realtime/hub.go's Event.For): it is false for a muted conversation,
so nothing here has to re-implement mute rules — muting is entirely the
backend's call.
*/

const DESKTOP_CAP = 3
const MOBILE_QUERY = "(max-width: 639px)"

type DockEntry = { id: string; minimized: boolean }

function storageKey(workspaceId: string) {
  return `moharrik.chatDock.${workspaceId}`
}

function loadDocks(workspaceId: string): DockEntry[] {
  if (typeof window === "undefined") return []
  try {
    const raw = window.localStorage.getItem(storageKey(workspaceId))
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    return parsed.filter(
      (d): d is DockEntry =>
        !!d && typeof d === "object" && typeof d.id === "string" && typeof d.minimized === "boolean",
    )
  } catch {
    return []
  }
}

function saveDocks(workspaceId: string, docks: DockEntry[]) {
  if (typeof window === "undefined") return
  try {
    window.localStorage.setItem(storageKey(workspaceId), JSON.stringify(docks))
  } catch {
    // Storage full/unavailable (private mode): the dock just won't survive a reload.
  }
}

function useIsMobile(): boolean {
  const [mobile, setMobile] = useState(false)
  useEffect(() => {
    const mq = window.matchMedia(MOBILE_QUERY)
    setMobile(mq.matches)
    const onChange = () => setMobile(mq.matches)
    mq.addEventListener("change", onChange)
    return () => mq.removeEventListener("change", onChange)
  }, [])
  return mobile
}

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

function DockList({ items, onOpen }: { items: BellItem[]; onOpen: (id: string) => void }) {
  const { t } = useTranslations()
  const relative = useRelative()
  const [q, setQ] = useState("")
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase()
    const base = needle
      ? items.filter(
          (i) =>
            i.title.toLowerCase().includes(needle) || i.snippet.toLowerCase().includes(needle),
        )
      : items
    return [...base].sort((a, b) => {
      if (a.unread !== b.unread) return a.unread ? -1 : 1
      return new Date(b.at).getTime() - new Date(a.at).getTime()
    })
  }, [items, q])

  return (
    <div className="flex max-h-96 w-72 flex-col gap-2" data-dock-list>
      <div className="relative">
        <SearchIcon className="absolute top-1/2 start-2 size-3.5 -translate-y-1/2 text-grid-muted" />
        <Input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("inbox.dock.search")}
          className="ps-7"
          aria-label={t("inbox.dock.search")}
          data-dock-search
        />
      </div>
      <ScrollArea className="-mx-2.5 h-72">
        {filtered.length === 0 ? (
          <div className="flex h-40 flex-col items-center justify-center gap-2 px-6 text-center">
            <MessageCircleIcon className="size-8 text-grid-muted/50" />
            <p className="text-sm text-grid-muted">{t("inbox.launcher.empty")}</p>
          </div>
        ) : (
          <ul className="divide-y divide-line">
            {filtered.map((item) => {
              const name = item.title || t("alerts.visitor")
              return (
                <li key={item.id} data-dock-list-item={item.id} data-dock-list-unread={item.unread}>
                  <button
                    type="button"
                    onClick={() => onOpen(item.id)}
                    className="flex w-full items-start gap-3 rounded-lg px-2 py-2 text-start transition-colors hover:bg-grid-soft"
                  >
                    <PersonAvatar id={item.id} name={name} photoUrl={item.avatar_url} className="size-9 shrink-0" />
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
                      <p className="mt-1 text-xs text-grid-muted">{relative(item.at)}</p>
                    </div>
                  </button>
                </li>
              )
            })}
          </ul>
        )}
      </ScrollArea>
    </div>
  )
}

function DockThread({
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
  // MH-661: the Subject override (and template picker) on an email reply.
  const [subject, setSubject] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [earlier, setEarlier] = useState<{ messages: InboxMessage[]; cursor: string } | null>(null)
  const [loadingEarlier, setLoadingEarlier] = useState(false)
  // MH-662: the docked window gets the thread ⋮ menu and search too — at
  // minimum, per the card. Select-messages and Contact info stay full-inbox-only.
  const [searchOpen, setSearchOpen] = useState(false)
  const [contactInfoOpen, setContactInfoOpen] = useState(false)
  const [scrollToMessageId, setScrollToMessageId] = useState<string | null>(null)

  if (detail.error) return <ErrorState error={detail.error} />
  if (!detail.data) return <LoadingRows rows={3} />
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
        json: { body: text, ...(subject.trim() ? { subject: subject.trim() } : {}) },
        workspaceId,
      })
      setReply("")
      setSubject("")
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
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 items-center justify-end gap-1 border-b border-line px-2 py-1" data-dock-thread-actions>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          aria-label={t("inbox.menu.search")}
          onClick={() => setSearchOpen((v) => !v)}
          data-thread-search-trigger
        >
          <SearchIcon />
        </Button>
        <ThreadHeaderMenu
          c={conversation}
          onChanged={() => {
            detail.mutate()
            onChanged()
          }}
          onContactInfo={() => setContactInfoOpen(true)}
          onSearch={() => setSearchOpen(true)}
          onSelectMessages={() => {}}
          onToggleStatus={() =>
            api(`/api/w/conversations/${encodeURIComponent(id)}`, {
              method: "PATCH",
              json: { status: conversation.status === "closed" ? "open" : "closed" },
              workspaceId,
            })
              .then(() => {
                detail.mutate()
                onChanged()
              })
              .catch((e) => toast.error(e instanceof ApiError ? e.message : t("common.networkError")))
          }
        />
      </div>

      {searchOpen ? (
        <ThreadSearch
          conversationId={id}
          onClose={() => setSearchOpen(false)}
          onJump={setScrollToMessageId}
        />
      ) : null}

      <ContactInfoPanel
        c={conversation}
        open={contactInfoOpen}
        onOpenChange={setContactInfoOpen}
        onChanged={() => detail.mutate()}
      />

      <ChatThread
        fill
        conversation={conversation}
        messages={messages}
        memberName={members.name}
        replyLanguage={replyLang}
        canWrite={canWrite}
        reply={reply}
        onReplyChange={setReply}
        subject={subject}
        onSubjectChange={setSubject}
        onSubmit={send}
        busy={busy}
        error={err}
        onInsertCanned={insert}
        hasOlder={!!cursor}
        loadingOlder={loadingEarlier}
        onLoadOlder={loadEarlier}
        scrollToMessageId={scrollToMessageId}
      />
    </div>
  )
}

function DockWindowHeader({
  item,
  id,
  locale,
  slug,
  onMinimize,
  onClose,
}: {
  item: BellItem | undefined
  id: string
  locale: string
  slug: string
  onMinimize: () => void
  onClose: () => void
}) {
  const { t } = useTranslations()
  const name = item?.title || t("alerts.visitor")
  return (
    <div className="flex h-11 shrink-0 items-center gap-2 border-b border-line px-3" data-dock-window-header>
      <Link
        href={hrefFor({ kind: "conversation", id }, locale, slug)}
        className="flex min-w-0 flex-1 items-center gap-2 text-start"
        title={t("inbox.launcher.openFullInbox")}
        data-dock-window-title
      >
        <PersonAvatar id={item?.id} name={name} photoUrl={item?.avatar_url} size="sm" className="size-6" />
        <BdiBlock className="min-w-0 flex-1 truncate text-sm font-medium">{name}</BdiBlock>
        {item?.unread ? (
          <span className="size-2 shrink-0 rounded-full bg-grid-action" aria-label={t("inbox.unread")} />
        ) : null}
      </Link>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label={t("inbox.dock.minimize")}
        onClick={onMinimize}
        data-dock-minimize
      >
        <MinusIcon />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon-sm"
        aria-label={t("common.close")}
        onClick={onClose}
        data-dock-close
      >
        <XIcon />
      </Button>
    </div>
  )
}

export function ChatDock({
  workspaceId,
  slug,
  can,
}: {
  workspaceId: string
  slug: string
  can: (permission: string) => boolean
}) {
  const { t, locale } = useTranslations()
  const pathname = usePathname()
  const { mutate } = useSWRConfig()
  const bell = useBell(workspaceId)
  const canWrite = can("inbox:write")
  const isMobile = useIsMobile()

  const [docks, setDocks] = useState<DockEntry[]>([])
  const [hydrated, setHydrated] = useState(false)
  const [listOpen, setListOpen] = useState(false)

  useEffect(() => {
    setDocks(loadDocks(workspaceId))
    setHydrated(true)
  }, [workspaceId])

  useEffect(() => {
    if (!hydrated) return
    saveDocks(workspaceId, docks)
  }, [workspaceId, docks, hydrated])

  function focusDock(id: string) {
    setDocks((prev) => [...prev.filter((d) => d.id !== id), { id, minimized: false }])
  }

  function addIfMissing(id: string) {
    setDocks((prev) => (prev.some((d) => d.id === id) ? prev : [...prev, { id, minimized: false }]))
  }

  function toggleMinimize(id: string) {
    setDocks((prev) => prev.map((d) => (d.id === id ? { ...d, minimized: !d.minimized } : d)))
  }

  function closeDock(id: string) {
    setDocks((prev) => prev.filter((d) => d.id !== id))
  }

  useWorkspaceEvents(workspaceId, (e) => {
    if (e.kind !== "message" && e.kind !== "conversation" && e.kind !== "message_status") return
    if (e.kind !== "message_status") void bell.mutate()
    if (e.conversation_id) {
      void mutate([`/api/w/conversations/${encodeURIComponent(e.conversation_id)}`, workspaceId])
    }
    // A customer's message that this viewer is meant to be alerted for (the
    // backend already excludes muted conversations and other members' own
    // replies here — see Event.For in internal/realtime/hub.go) pops that
    // conversation's dock open; if it is already docked-but-minimised this
    // just leaves it be — its unread badge already comes from the bell data
    // the effect above just refreshed.
    if (e.kind === "message" && e.direction === "in" && e.alert && e.conversation_id) {
      addIfMissing(e.conversation_id)
    }
  })

  const onInboxRoute = pathname?.includes("/inbox") ?? false
  const data = bell.data
  if (onInboxRoute || !hydrated) return null

  const conversations = (data?.items ?? []).filter((i) => i.kind === "conversation")
  const byId = new Map(conversations.map((c) => [c.id, c]))
  const unread = data?.unread_conversations ?? 0
  const canInbox = data?.can_inbox ?? false
  if (!canInbox) return null

  function openFromList(id: string) {
    focusDock(id)
    setListOpen(false)
  }

  const expanded = docks.filter((d) => !d.minimized)
  const minimized = docks.filter((d) => d.minimized)
  const cap = isMobile ? 1 : DESKTOP_CAP
  const visible = expanded.slice(-cap)
  const overflowed = expanded.slice(0, expanded.length - visible.length)
  const desktopVisible = isMobile ? [] : visible
  const mobileEntry = isMobile ? visible[visible.length - 1] : undefined

  return (
    <>
      {mobileEntry ? (
        <div
          className="fixed inset-x-0 bottom-14 top-14 z-40 flex flex-col overflow-hidden rounded-t-xl border border-line bg-grid-card shadow-xl"
          data-dock-window
          data-dock-sheet
          data-dock-id={mobileEntry.id}
        >
          <DockWindowHeader
            item={byId.get(mobileEntry.id)}
            id={mobileEntry.id}
            locale={locale}
            slug={slug}
            onMinimize={() => toggleMinimize(mobileEntry.id)}
            onClose={() => closeDock(mobileEntry.id)}
          />
          <div className="flex min-h-0 flex-1 flex-col">
            <DockThread
              workspaceId={workspaceId}
              id={mobileEntry.id}
              canWrite={canWrite}
              onChanged={() => void bell.mutate()}
            />
          </div>
        </div>
      ) : null}

      <div
        className="pointer-events-none fixed inset-x-0 bottom-0 z-40 flex items-end justify-end gap-2 px-3 pb-3 sm:gap-3 sm:px-4 sm:pb-4"
        data-chat-dock-bar
      >
        {overflowed.map((d) => {
          const item = byId.get(d.id)
          const name = item?.title || t("alerts.visitor")
          return (
            <button
              key={d.id}
              type="button"
              onClick={() => focusDock(d.id)}
              className="pointer-events-auto relative shrink-0 rounded-full ring-2 ring-background"
              aria-label={name}
              data-dock-bubble={d.id}
            >
              <PersonAvatar id={d.id} name={name} photoUrl={item?.avatar_url} className="size-10" />
              {item?.unread ? (
                <span
                  className="pointer-events-none absolute -top-0.5 -end-0.5 size-2.5 rounded-full bg-grid-action ring-2 ring-background"
                  aria-hidden
                />
              ) : null}
            </button>
          )
        })}

        {minimized.map((d) => {
          const item = byId.get(d.id)
          const name = item?.title || t("alerts.visitor")
          return (
            <div
              key={d.id}
              className="pointer-events-auto flex h-11 max-w-48 shrink-0 items-center gap-1 rounded-t-lg border border-b-0 border-line bg-grid-elevated px-2 shadow-md"
              data-dock-minimized={d.id}
            >
              <button
                type="button"
                onClick={() => focusDock(d.id)}
                className="flex min-w-0 flex-1 items-center gap-2 py-2 text-start"
              >
                <PersonAvatar id={d.id} name={name} photoUrl={item?.avatar_url} size="sm" className="size-6" />
                <BdiBlock className="min-w-0 truncate text-xs font-medium">{name}</BdiBlock>
                {item?.unread ? (
                  <span className="size-2 shrink-0 rounded-full bg-grid-action" aria-label={t("inbox.unread")} />
                ) : null}
              </button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={t("common.close")}
                onClick={() => closeDock(d.id)}
                data-dock-close
              >
                <XIcon />
              </Button>
            </div>
          )
        })}

        {desktopVisible.map((d) => (
          <div
            key={d.id}
            className="pointer-events-auto flex h-[28rem] w-80 max-w-[calc(100vw-2rem)] shrink-0 flex-col overflow-hidden rounded-t-lg border border-line bg-grid-card shadow-xl"
            data-dock-window
            data-dock-id={d.id}
          >
            <DockWindowHeader
              item={byId.get(d.id)}
              id={d.id}
              locale={locale}
              slug={slug}
              onMinimize={() => toggleMinimize(d.id)}
              onClose={() => closeDock(d.id)}
            />
            <div className="flex min-h-0 flex-1 flex-col">
              <DockThread
                workspaceId={workspaceId}
                id={d.id}
                canWrite={canWrite}
                onChanged={() => void bell.mutate()}
              />
            </div>
          </div>
        ))}

        <Popover open={listOpen} onOpenChange={setListOpen}>
          <PopoverTrigger
            render={
              <Button
                type="button"
                className="pointer-events-auto relative flex h-11 shrink-0 items-center gap-2 rounded-full px-4 shadow-lg"
                aria-label={
                  unread > 0 ? t("inbox.launcher.openUnread", { n: unread }) : t("inbox.launcher.open")
                }
                data-chat-dock-launcher
              />
            }
          >
            <MessageCircleIcon className="size-4" />
            <span className="text-sm font-medium">{t("inbox.launcher.title")}</span>
            {unread > 0 ? (
              <Badge
                className="pointer-events-none h-5 min-w-5 rounded-full px-1 text-xs leading-none tabular-nums"
                data-chat-dock-count={unread}
              >
                {unread > 99 ? "99+" : unread}
              </Badge>
            ) : null}
          </PopoverTrigger>
          <PopoverContent side="top" align="end" className="pointer-events-auto w-auto p-2.5">
            <DockList items={conversations} onOpen={openFromList} />
            <Button
              variant="ghost"
              size="sm"
              className="w-full"
              nativeButton={false}
              render={<Link href={`/${locale}/w/${slug}/inbox`} onClick={() => setListOpen(false)} />}
              data-dock-open-inbox
            >
              {t("inbox.launcher.openFullInbox")}
            </Button>
          </PopoverContent>
        </Popover>
      </div>
    </>
  )
}
