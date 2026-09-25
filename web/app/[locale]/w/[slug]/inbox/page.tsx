"use client"

import { useEffect, useRef, useState, type CSSProperties, type FormEvent } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useVirtualizer } from "@tanstack/react-virtual"
import { ChevronUpIcon, PencilIcon, UserPlusIcon } from "lucide-react"
import { toast } from "sonner"

import { PersonAvatar } from "@/components/avatar-with-fallback"
import { PickSelect, useMemberNames } from "@/components/crm/shared"
import { ChatThread } from "@/components/inbox/chat-thread"
import { ConversationContactDialog } from "@/components/inbox/contact-row-dialog"
import { SnoozeControl, SnoozedNote } from "@/components/inbox/snooze-control"
import { ThreadCRM } from "@/components/inbox/thread-crm"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  phoneLabel,
  type ChannelCount,
  type Conversation,
  type ConversationStatus,
  type InboxMessage,
  type SenderGroup,
  type ThreadPage,
} from "@/lib/api"
import { channelIcon, channelLabel } from "@/lib/inbox-channels"
import { useTranslations } from "@/lib/i18n"
import { useSWRConfig } from "swr"

import { useWorkspaceEvents } from "@/lib/events"
import {
  useConversation,
  useConversationPages,
  useMailboxes,
  useSenderGroupPages,
} from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, BdiBlock } from "@/components/bidi"

const statusVariant: Record<ConversationStatus, "default" | "secondary" | "outline"> = {
  open: "default",
  snoozed: "secondary",
  closed: "outline",
}

export default function InboxPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.inbox")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")

  // The open thread lives in the URL (?c=<conversation id>), so an alert, a push,
  // the bell or the Overview can link straight to it, and a reload keeps it open (MH-378).
  const router = useRouter()
  const pathname = usePathname()
  const params = useSearchParams()
  const selected = params.get("c")
  function select(id: string | null) {
    const next = new URLSearchParams(params.toString())
    if (id) next.set("c", id)
    else next.delete("c")
    const qs = next.toString()
    router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false })
  }

  // Arriving by link: ?c=<id> may be a closed thread, and the Overview's ?unread=1
  // count covers every status (MH-462), so both widen the status filter.
  const linkedUnread = params.get("unread") === "1"
  const [status, setStatus] = useState(() => (params.get("c") || linkedUnread ? "" : "open"))
  const [unread, setUnread] = useState(linkedUnread)

  // Channel tabs (MH-568): kept in the URL (?channel=) like ?c= and ?q=, so a
  // reload or a shared link keeps the tab you were on. Choosing a channel other
  // than email drops a stale ?mailbox=, since that sub-filter only means
  // anything under Email.
  const channel = params.get("channel") ?? ""
  function setChannel(next: string) {
    const p = new URLSearchParams(params.toString())
    if (next) p.set("channel", next)
    else p.delete("channel")
    if (next !== "email") {
      p.delete("mailbox")
      p.delete("sender")
    }
    const qs = p.toString()
    router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false })
  }
  const mailboxFilter = params.get("mailbox") ?? ""
  function setMailboxFilter(next: string) {
    const p = new URLSearchParams(params.toString())
    if (next) p.set("mailbox", next)
    else p.delete("mailbox")
    const qs = p.toString()
    router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false })
  }
  // MH-600: which sender the email tab's "grouped by sender" view has been
  // narrowed to, kept in the URL (?sender=) for the same reason ?c= and ?q=
  // are — a reload or a shared link keeps the narrowing. Always the group's
  // own lower-cased sender_email, so it round-trips as customer_email below.
  const sender = params.get("sender") ?? ""
  function setSender(next: string) {
    const p = new URLSearchParams(params.toString())
    if (next) p.set("sender", next)
    else p.delete("sender")
    const qs = p.toString()
    router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false })
  }
  // Grouped-by-sender vs the flat list (MH-600), persisted per browser so the
  // choice survives a reload; a fresh browser defaults to grouped, since a
  // sender writing several times is exactly what this view is for.
  const [grouped, setGroupedState] = useState(true)
  useEffect(() => {
    const stored = window.localStorage.getItem("inbox.email.groupedBySender")
    if (stored !== null) setGroupedState(stored === "true")
  }, [])
  function setGrouped(next: boolean) {
    setGroupedState(next)
    window.localStorage.setItem("inbox.email.groupedBySender", String(next))
    if (!next) setSender("")
  }
  const showSenderGroups = channel === "email" && grouped && !sender
  // The search term lives in the URL too (?q=, MH-556), the same reason `c` does:
  // a search someone is looking at survives a reload or a shared link.
  const [q, setQ] = useState(() => params.get("q") ?? "")
  useEffect(() => {
    const t = window.setTimeout(() => {
      const next = new URLSearchParams(params.toString())
      if (q.trim()) next.set("q", q.trim())
      else next.delete("q")
      const qs = next.toString()
      router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false })
      // eslint-disable-next-line react-hooks/exhaustive-deps
    }, 300)
    return () => window.clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q])

  // Threads opened while unread (MH-599). Opening one reads it, and under
  // unread_first a read row would drop below every unread one — the row a
  // person just left would move, and coming back to its slot opened a
  // different conversation. The API keeps these among the unread until reload.
  const keepRef = useRef<string[]>([])
  const pages = useConversationPages(
    ws.id,
    {
      status: status || undefined,
      unread,
      q: params.get("q") ?? undefined,
      channel: channel || undefined,
      // Only meaningful under Email (MH-555); dropped everywhere else so a
      // leftover value can never silently narrow another channel's list.
      mailbox_id: channel === "email" ? mailboxFilter || undefined : undefined,
      // MH-600: narrowed to one sender, drilled into from the grouped view.
      customer_email: channel === "email" ? sender || undefined : undefined,
      // Ten at a time, unread on top (MH-592): what needs an answer is what
      // opens first, and the rest arrives as the list is scrolled or searched.
      limit: 10,
      order: "unread_first",
    },
    () => keepRef.current,
  )
  const list = { error: pages.error, data: pages.data?.[0], mutate: pages.mutate }
  const items = (pages.data ?? []).flatMap((p) => p.conversations)
  const hasMore = !!pages.data?.[pages.data.length - 1]?.next_cursor
  if (
    selected &&
    !keepRef.current.includes(selected) &&
    items.some((c) => c.id === selected && c.unread)
  ) {
    keepRef.current = [...keepRef.current, selected].slice(-100)
  }

  // MH-600: the email channel grouped by sender. Only fetched on the Email
  // tab while the grouped view is showing — ws.id withheld otherwise, the
  // same conditional-fetch shape useConversation uses, so switching away
  // never leaves a stale request in flight.
  const senderGroups = useSenderGroupPages(showSenderGroups ? ws.id : null, {
    mailbox_id: mailboxFilter || undefined,
    q: params.get("q") ?? undefined,
    limit: 20,
  })
  const senderGroupItems = (senderGroups.data ?? []).flatMap((p) => p.groups)
  const senderGroupsHasMore = !!senderGroups.data?.[senderGroups.data.length - 1]?.next_cursor
  const senderGroupsLoadingMore = senderGroups.isValidating && senderGroups.size > 1
  const loadingMore = pages.isValidating && pages.size > 1
  const channelCounts: ChannelCount[] = list.data?.channel_counts ?? []

  // Mailboxes (MH-555): the sub-filter under Email only earns its place when
  // there is more than one to choose between.
  const mailboxes = useMailboxes(ws.id)
  const mailboxOpts = mailboxes.data?.mailboxes ?? []

  // No timer: the API tells this tab when a message actually arrives, and the
  // affected thread is revalidated by key so an open conversation updates too.
  const { mutate } = useSWRConfig()
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind !== "message" && e.kind !== "conversation") return
    void pages.mutate()
    if (e.conversation_id)
      void mutate([`/api/w/conversations/${encodeURIComponent(e.conversation_id)}`, ws.id])
  })

  // Virtualised conversation list (MH-556 cont'd): 1,000+ rows must not mean
  // 1,000+ live DOM nodes. The scroll container is the `<ul>` itself — rows
  // are absolutely-positioned `<li>` elements placed DIRECTLY under it (no
  // inner sizer wrapper), so `[data-conversation-list] > *` still enumerates
  // one element per row, in order, exactly as it did before virtualisation
  // (a live e2e check — inbox-dates-live.mjs — walks the list this way). A
  // position:relative + overflow-y-auto element's scrollable overflow already
  // includes its absolutely-positioned children, so no separate spacer div is
  // needed to make the scrollbar reflect the true (virtual) height.
  const listParentRef = useRef<HTMLUListElement>(null)
  const virtualizer = useVirtualizer({
    count: items.length,
    getScrollElement: () => listParentRef.current,
    estimateSize: () => 92,
    overscan: 10,
    getItemKey: (index) => items[index]!.id,
  })
  const virtualItems = virtualizer.getVirtualItems()
  const listBottom = virtualizer.getTotalSize()

  // Infinite scroll (MH-556): the sentinel below the list asks for the next
  // page as soon as it is in view, instead of a "Load more" button. Anchored
  // at the virtualised list's true bottom (not the last rendered row), so it
  // still fires correctly once most rows are windowed out.
  const sentinelRef = useRef<HTMLLIElement>(null)
  useEffect(() => {
    const el = sentinelRef.current
    if (!el) return
    const obs = new IntersectionObserver(
      (entries) => {
        if (entries[0]?.isIntersecting && hasMore && !loadingMore)
          void pages.setSize(pages.size + 1)
      },
      // The list scrolls inside its own <ul>, so that is the root: against the
      // viewport the margin would be clipped by the list and never reach ahead.
      { root: listParentRef.current, rootMargin: "400px" },
    )
    obs.observe(el)
    return () => obs.disconnect()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasMore, loadingMore, pages.size, items.length])

  // The observer alone can stall: a list already at its bottom when a page
  // lands (tall wrapped rows at 390px) gets a rebuilt observer while
  // loadingMore is still true, and nothing fires again. So also ask whenever
  // the last rendered row is near the end — re-checked on every render,
  // including the one where loadingMore turns false.
  const lastRendered = virtualItems[virtualItems.length - 1]?.index ?? -1
  useEffect(() => {
    if (lastRendered >= items.length - 5 && items.length > 0 && hasMore && !loadingMore)
      void pages.setSize(pages.size + 1)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lastRendered, items.length, hasMore, loadingMore, pages.size])

  // Keyboard navigation (MH-556): j/k or the arrows move the highlighted
  // conversation, Enter/n open it, Esc clears the search, / focuses it. Typing
  // in a field never triggers these — checked via the event's own target.
  const [cursorIdx, setCursorIdx] = useState(0)
  const rowRefs = useRef<Record<string, HTMLButtonElement | null>>({})
  useEffect(() => {
    function isTyping(t: EventTarget | null) {
      const el = t as HTMLElement | null
      return !!el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable)
    }
    function onKey(e: KeyboardEvent) {
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (e.key === "/" && !isTyping(e.target)) {
        e.preventDefault()
        document.getElementById("inbox-search")?.focus()
        return
      }
      if (isTyping(e.target)) return
      // Cycle channel tabs (MH-568): [ / ] step through All + whatever
      // channel_counts lists, same order the tab strip renders them in.
      if (e.key === "[" || e.key === "]") {
        e.preventDefault()
        const order = ["", ...channelCounts.map((c) => c.channel)]
        const i = order.indexOf(channel)
        const next =
          e.key === "]"
            ? order[(i + 1 + order.length) % order.length]
            : order[(i - 1 + order.length) % order.length]
        setChannel(next)
        return
      }
      if (items.length === 0) return
      if (e.key === "j" || e.key === "ArrowDown" || e.key === "n") {
        e.preventDefault()
        setCursorIdx((i) => Math.min(i + 1, items.length - 1))
      } else if (e.key === "k" || e.key === "ArrowUp" || e.key === "p") {
        e.preventDefault()
        setCursorIdx((i) => Math.max(i - 1, 0))
      } else if (e.key === "Enter") {
        const c = items[cursorIdx]
        if (c) select(c.id)
      } else if (e.key === "Escape") {
        select(null)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items, cursorIdx])
  // Follow the cursor only when the cursor moves (MH-597). `items` is a fresh
  // array on every render and scrolling the virtual list renders, so keying
  // this on `items` too snapped the list back to row 0 on every wheel tick —
  // the list could not be scrolled at all.
  useEffect(() => {
    if (items[cursorIdx]) virtualizer.scrollToIndex(cursorIdx, { align: "auto" })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cursorIdx])

  const statusOpts = [
    { value: "open", label: t("inbox.status.open") },
    { value: "snoozed", label: t("inbox.status.snoozed") },
    { value: "closed", label: t("inbox.status.closed") },
  ]

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.inbox")}
        description={t("inbox.hint")}
      />

      {/* Channel tabs (MH-568): data-driven from channel_counts, not a fixed
          list — a channel this workspace has never had a conversation on or a
          connection for gets no tab at all. overflow-x-auto + no wrap on the
          list so on a phone the strip scrolls sideways instead of wrapping. */}
      {channelCounts.length > 0 ? (
        <div className="overflow-x-auto px-6 pb-2">
          <Tabs
            value={channel || "all"}
            onValueChange={(v) => setChannel(v === "all" ? "" : String(v))}
          >
            <TabsList variant="line" className="w-max">
              <TabsTrigger value="all" data-channel-tab="all">
                {t("inbox.channel.all")}
              </TabsTrigger>
              {channelCounts.map((c) => {
                const Icon = channelIcon(c.channel)
                return (
                  <TabsTrigger key={c.channel} value={c.channel} data-channel-tab={c.channel}>
                    <Icon />
                    {channelLabel(c.channel, t)}
                    {c.unread ? <Badge variant="secondary">{c.unread}</Badge> : null}
                  </TabsTrigger>
                )
              })}
            </TabsList>
          </Tabs>
        </div>
      ) : null}

      <div
        className="flex flex-wrap items-center gap-2 px-6 pb-4"
        role="group"
        aria-label={t("inbox.filters")}
      >
        <div className="w-40">
          <PickSelect
            id="inbox-status"
            value={status}
            onChange={setStatus}
            options={statusOpts}
            noneLabel={t("inbox.allStatuses")}
          />
        </div>
        {channel === "email" && mailboxOpts.length > 1 ? (
          <div className="w-44">
            <PickSelect
              id="inbox-mailbox"
              value={mailboxFilter}
              onChange={setMailboxFilter}
              options={mailboxOpts.map((m) => ({ value: m.id, label: m.name }))}
              noneLabel={t("inbox.mailbox.all")}
            />
          </div>
        ) : null}
        <Button
          type="button"
          size="sm"
          variant={unread ? "default" : "outline"}
          aria-pressed={unread}
          onClick={() => setUnread((v) => !v)}
          data-filter="unread"
        >
          {t("inbox.filterUnread")}
          {list.data?.unread_count ? ` (${list.data.unread_count})` : ""}
        </Button>
        {channel === "email" ? (
          // MH-600: grouped-by-sender vs the flat list. Two buttons rather than
          // a single toggle switch — each state names itself, so it reads right
          // in both LTR and RTL without a direction-aware icon.
          <div
            className="ms-auto flex items-center gap-1"
            role="group"
            aria-label={t("inbox.senderGroups.toggleGrouped")}
          >
            <Button
              type="button"
              size="sm"
              variant={grouped ? "default" : "outline"}
              aria-pressed={grouped}
              onClick={() => setGrouped(true)}
              data-sender-toggle="grouped"
            >
              {t("inbox.senderGroups.toggleGrouped")}
            </Button>
            <Button
              type="button"
              size="sm"
              variant={!grouped ? "default" : "outline"}
              aria-pressed={!grouped}
              onClick={() => setGrouped(false)}
              data-sender-toggle="flat"
            >
              {t("inbox.senderGroups.toggleFlat")}
            </Button>
          </div>
        ) : null}
      </div>

      <div className="grid grid-cols-1 border-y border-line lg:grid-cols-[22rem_minmax(0,1fr)]">
        {/* The chat list column (MH-592): its own search on top, so what you
            search is right above what it finds, and the list below it. The
            search stays put while the list loads, errors or comes back empty,
            so a search with no hits can be changed where it was typed. */}
        <div
          className="flex min-w-0 flex-col lg:border-e lg:border-line"
          style={{ height: "min(70vh, 44rem)" }}
          data-conversation-column
        >
          <div className="shrink-0 border-b border-line p-2">
            <input
              id="inbox-search"
              type="search"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder={t("inbox.searchPlaceholder")}
              aria-label={t("inbox.searchPlaceholder")}
              dir="auto"
              data-search="conversations"
              className="h-8 w-full border border-line bg-grid-card px-3 text-sm placeholder:text-grid-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-grid-brand"
            />
          </div>
          {channel === "email" && sender ? (
            // MH-600: narrowed to one sender, either by clicking a group or via
            // a shared ?sender= link. The banner names who, and "back" only
            // makes sense when there is a grouped view underneath to return to.
            <div
              className="flex shrink-0 items-center justify-between gap-2 border-b border-line bg-grid-soft px-3 py-2 text-sm"
              data-sender-filter-banner
            >
              <span className="min-w-0 truncate text-grid-fg">
                {t("inbox.senderGroups.showingSender", {
                  who:
                    senderGroupItems.find((g) => g.sender_email === sender)?.sender_name || sender,
                })}
              </span>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => setSender("")}
                data-sender-clear
              >
                {grouped ? t("inbox.senderGroups.back") : t("inbox.senderGroups.clear")}
              </Button>
            </div>
          ) : null}
          {showSenderGroups ? (
            senderGroups.error ? (
              <ErrorState error={senderGroups.error} />
            ) : !senderGroups.data ? (
              <LoadingRows rows={5} />
            ) : senderGroupItems.length === 0 ? (
              <EmptyState title={t("inbox.senderGroups.empty")} />
            ) : (
              <ul
                className="relative min-h-0 min-w-0 flex-1 divide-y divide-line overflow-y-auto"
                data-sender-group-list
              >
                {senderGroupItems.map((g) => (
                  <SenderGroupRow
                    key={g.sender_email}
                    group={g}
                    onSelect={() => setSender(g.sender_email)}
                  />
                ))}
                {senderGroupsHasMore ? (
                  <li className="p-2">
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      className="w-full"
                      disabled={senderGroupsLoadingMore}
                      onClick={() => void senderGroups.setSize(senderGroups.size + 1)}
                      data-sender-groups-load-more
                    >
                      {senderGroupsLoadingMore ? t("common.working") : t("common.loadMore")}
                    </Button>
                  </li>
                ) : null}
              </ul>
            )
          ) : list.error ? (
            <ErrorState error={list.error} />
          ) : !list.data ? (
            <LoadingRows rows={5} />
          ) : items.length === 0 ? (
            <EmptyState
              title={t("inbox.empty")}
              body={canManage && !q.trim() ? t("inbox.emptyBody") : undefined}
            />
          ) : (
            // Virtualised (MH-556): position:relative + overflow-y-auto on this
            // same element, with every row (and the sentinel) absolutely
            // positioned directly inside it — see the virtualizer setup above
            // for why there is no separate inner sizer wrapper.
            <ul
              ref={listParentRef}
              className="relative min-h-0 min-w-0 flex-1 divide-y divide-line overflow-y-auto"
              data-conversation-list
            >
              {virtualItems.map((vi) => {
                const c = items[vi.index]!
                return (
                  <ConversationRow
                    key={c.id}
                    c={c}
                    active={c.id === selected}
                    highlighted={vi.index === cursorIdx}
                    onSelect={() => {
                      setCursorIdx(vi.index)
                      select(c.id)
                    }}
                    buttonRef={(el) => {
                      rowRefs.current[c.id] = el
                    }}
                    onChanged={() => void pages.mutate()}
                    measureRef={virtualizer.measureElement}
                    dataIndex={vi.index}
                    style={{
                      position: "absolute",
                      top: 0,
                      insetInlineStart: 0,
                      width: "100%",
                      transform: `translateY(${vi.start}px)`,
                    }}
                  />
                )
              })}
              <li
                ref={sentinelRef}
                aria-hidden
                className="h-1"
                data-scroll-sentinel
                style={{
                  position: "absolute",
                  top: listBottom,
                  insetInlineStart: 0,
                  width: "100%",
                }}
              />
              {loadingMore ? (
                <li
                  className="px-6 py-3 text-center text-xs text-grid-muted"
                  style={{
                    position: "absolute",
                    top: listBottom + 4,
                    insetInlineStart: 0,
                    width: "100%",
                  }}
                >
                  {t("common.working")}
                </li>
              ) : null}
            </ul>
          )}
        </div>
        <div className="min-w-0">
          {selected ? (
            <Thread key={selected} id={selected} onChanged={() => list.mutate()} />
          ) : (
            <p className="px-6 py-10 text-center text-sm text-grid-muted">{t("inbox.pickOne")}</p>
          )}
        </div>
      </div>
      <div className="pb-8" />
    </>
  )
}

function SenderGroupRow({ group, onSelect }: { group: SenderGroup; onSelect: () => void }) {
  const { t, formatDate } = useTranslations()
  const who = group.sender_name || group.sender_email || t("inbox.senderGroups.anonymous")
  return (
    <li className="relative">
      <button
        type="button"
        onClick={onSelect}
        data-sender-group={group.sender_email}
        data-sender-group-unread={group.unread_count > 0 ? "true" : "false"}
        className={`flex w-full min-w-0 flex-col gap-1 px-6 py-4 text-start transition-colors ${group.unread_count > 0 ? "bg-grid-card" : ""} hover:bg-grid-soft`}
      >
        <span className="flex min-w-0 items-center gap-2">
          {group.unread_count > 0 ? (
            <span
              className="size-2 shrink-0 rounded-full bg-grid-brand"
              aria-label={t("inbox.unread")}
            />
          ) : null}
          <BdiBlock className="truncate text-sm font-medium">{who}</BdiBlock>
          <span className="ms-auto shrink-0 text-xs text-grid-muted">
            {formatDate(group.latest_message_at)}
          </span>
        </span>
        <span className="min-w-0 truncate text-xs text-grid-muted" dir="auto">
          {group.sender_email}
        </span>
        {group.latest_subject ? (
          <BdiBlock className="truncate text-sm text-grid-fg">{group.latest_subject}</BdiBlock>
        ) : null}
        {group.latest_preview ? (
          <BdiBlock className="truncate text-xs text-grid-muted">{group.latest_preview}</BdiBlock>
        ) : null}
        <span className="flex items-center gap-3 text-xs text-grid-muted">
          <span data-sender-group-count>
            {t("inbox.senderGroups.conversationCount", { n: group.conversation_count })}
          </span>
          {group.unread_count > 0 ? (
            <Badge variant="default" data-sender-group-unread-badge>
              {t("inbox.senderGroups.unreadCount", { n: group.unread_count })}
            </Badge>
          ) : null}
        </span>
      </button>
    </li>
  )
}

function ConversationRow({
  c,
  active,
  highlighted,
  onSelect,
  buttonRef,
  onChanged,
  measureRef,
  dataIndex,
  style,
}: {
  c: Conversation
  active: boolean
  highlighted?: boolean
  onSelect: () => void
  buttonRef?: (el: HTMLButtonElement | null) => void
  onChanged?: () => void
  /** Virtualizer's own ref (MH-556): remeasures this row's real height so variable-height rows don't drift out of sync with the estimate. */
  measureRef?: (el: HTMLElement | null) => void
  dataIndex?: number
  style?: CSSProperties
}) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  // MH-602: a linked contact's own name wins over customer_name (which, for
  // an unlinked WhatsApp thread, is often just the raw phone) — never the
  // other way around.
  const who =
    c.contact?.name ||
    c.customer_name ||
    c.customer_email ||
    phoneLabel(c.customer_phone) ||
    t("inbox.anonymous")
  const [contactOpen, setContactOpen] = useState(false)

  // The row's menu (MH-556, MH-596): right-click, a touch long-press and
  // Shift+F10 / the keyboard Menu key on the focused row all open the SAME
  // ContextMenu — the browser fires the same "contextmenu" event for all
  // three, and Base UI's ContextMenu.Trigger listens for exactly that, so a
  // plain click on the row still just opens the conversation.
  async function act(patch: Record<string, unknown>, message: string) {
    try {
      await api(`/api/w/conversations/${encodeURIComponent(c.id)}`, {
        method: "PATCH",
        json: patch,
        workspaceId: ws.id,
      })
      toast.success(message)
      onChanged?.()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <ContextMenu>
      <ContextMenuTrigger
        render={
          <li
            ref={measureRef}
            data-index={dataIndex}
            className="group relative"
            style={style}
            data-row-menu="context"
          />
        }
      >
        <button
          ref={buttonRef}
          type="button"
          onClick={onSelect}
          aria-current={active ? "true" : undefined}
          data-conversation={c.id}
          data-conversation-unread={c.unread ? "true" : "false"}
          className={`flex w-full min-w-0 items-start gap-3 px-6 py-4 text-start transition-colors ${active ? "bg-grid-soft" : c.unread ? "bg-grid-card" : ""} ${highlighted ? "ring-2 ring-inset ring-grid-brand" : ""} hover:bg-grid-soft`}
        >
          <PersonAvatar
            id={c.contact_id || c.id}
            name={who}
            photoUrl={c.avatar_url}
            className="mt-0.5 shrink-0"
          />
          <span className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="flex min-w-0 items-center gap-2">
              {c.unread ? (
                <span
                  className="size-2 shrink-0 rounded-full bg-grid-brand"
                  aria-label={t("inbox.unread")}
                />
              ) : null}
              <BdiBlock className="truncate text-sm font-medium">{who}</BdiBlock>
              <Badge variant={statusVariant[c.status]} className="ms-auto shrink-0">
                {t(`inbox.status.${c.status}`)}
              </Badge>
            </span>
            <BdiBlock className="block truncate text-xs text-grid-muted" data-conversation-preview>
              {c.preview || c.subject}
            </BdiBlock>
            <span className="text-xs text-grid-muted">
              {t(`inbox.channel.${c.channel}`)} · <Ltr>{formatDate(c.last_message_at)}</Ltr> ·{" "}
              {t("inbox.messageCount", { n: c.message_count })}
            </span>
          </span>
        </button>
      </ContextMenuTrigger>

      <ContextMenuContent className="min-w-48">
        <ContextMenuItem
          onClick={() =>
            act({ unread: !c.unread }, c.unread ? t("inbox.markedRead") : t("inbox.markedUnread"))
          }
          data-action="toggle-unread"
        >
          {c.unread ? t("inbox.markRead") : t("inbox.markUnread")}
        </ContextMenuItem>
        <ContextMenuItem
          onClick={() =>
            act(
              { status: c.status === "closed" ? "open" : "closed" },
              c.status === "closed" ? t("inbox.reopened") : t("inbox.closed"),
            )
          }
          data-action="toggle-status"
        >
          {c.status === "closed" ? t("inbox.reopen") : t("inbox.close")}
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem onClick={() => setContactOpen(true)} data-contact-edit>
          {c.contact_id ? <PencilIcon /> : <UserPlusIcon />}
          {c.contact_id ? t("inbox.crm.editContact") : t("inbox.crm.addContact")}
        </ContextMenuItem>
      </ContextMenuContent>

      <ConversationContactDialog
        c={c}
        open={contactOpen}
        onOpenChange={setContactOpen}
        onDone={() => onChanged?.()}
      />
    </ContextMenu>
  )
}

/**
 * Where a chat started: the page, the language and the browser, recorded by the
 * API when the widget opened the conversation (MH-377). Rendered only when there
 * is something to show — email and WhatsApp threads have none of it.
 */
function VisitorContext({ c }: { c: Conversation }) {
  const { t, locale } = useTranslations()
  if (c.channel !== "chat" || !(c.page_url || c.locale || c.user_agent)) return null

  // The API keeps only http(s) URLs on the widget's allowed origin; checked
  // again here because this becomes a link someone clicks.
  const pageIsLink = /^https?:\/\//i.test(c.page_url)
  let language = c.locale
  try {
    language =
      (c.locale && new Intl.DisplayNames([locale], { type: "language" }).of(c.locale)) || c.locale
  } catch {
    // An unusual tag: show it as sent.
  }

  return (
    <dl
      aria-label={t("inbox.context")}
      // Its own full-width row of the header: inside the name column it was
      // squeezed to a few characters by the controls at phone width.
      className="grid basis-full grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-0.5 text-xs text-grid-muted"
      data-thread-context
    >
      {c.page_url ? (
        <>
          <dt>{t("inbox.context.page")}</dt>
          {/* dir="ltr" on the cell itself, not a span inside it: the ellipsis
              follows the cell's direction, and an RTL cell cut a URL from its
              start. rtl:text-right keeps a short value next to its label. */}
          <dd dir="ltr" className="min-w-0 truncate rtl:text-right" data-context="page">
            {pageIsLink ? (
              <a
                href={c.page_url}
                target="_blank"
                rel="noopener noreferrer nofollow"
                className="text-grid-fg underline-offset-2 hover:underline"
              >
                {c.page_url}
              </a>
            ) : (
              c.page_url
            )}
          </dd>
        </>
      ) : null}
      {c.locale ? (
        <>
          <dt>{t("inbox.context.language")}</dt>
          <dd className="min-w-0 truncate" data-context="locale" title={c.locale}>
            {language}
          </dd>
        </>
      ) : null}
      {c.user_agent ? (
        <>
          <dt>{t("inbox.context.browser")}</dt>
          <dd
            dir="ltr"
            className="min-w-0 truncate rtl:text-right"
            data-context="browser"
            title={c.user_agent}
          >
            {c.user_agent}
          </dd>
        </>
      ) : null}
    </dl>
  )
}

/**
 * Two runs of a thread as one, each message once. `older` ends where `newer`
 * begins or overlaps it, and each is already in the server's order, so the
 * order is kept rather than re-sorted: timestamps as strings do not sort (a
 * fractional second sorts before a whole one).
 */
function mergeMessages(older: InboxMessage[], newer: InboxMessage[]): InboxMessage[] {
  const seen = new Set(older.map((m) => m.id))
  return [...older, ...newer.filter((m) => !seen.has(m.id))]
}

function Thread({ id, onChanged }: { id: string; onChanged: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("inbox:write")
  const detail = useConversation(ws.id, id)
  const members = useMemberNames(ws.id)
  const [reply, setReply] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  // Earlier pages (MH-471). Opening a thread shows its newest page; "Load
  // earlier" walks back with the cursor. What was on screen when a page was
  // loaded is kept here too, so a new message arriving (which shifts the newest
  // page) can never open a gap between the two.
  const [earlier, setEarlier] = useState<{ messages: InboxMessage[]; cursor: string } | null>(null)
  const [loadingEarlier, setLoadingEarlier] = useState(false)

  if (detail.error) return <ErrorState error={detail.error} />
  if (!detail.data) return <LoadingRows rows={4} />
  const { conversation: c, visitor, reply_language: replyLang } = detail.data
  const total = detail.data.messages_total ?? detail.data.messages.length
  const messages = mergeMessages(earlier?.messages ?? [], detail.data.messages)
  const cursor = earlier ? earlier.cursor : detail.data.messages_next_cursor

  async function loadEarlier() {
    if (!cursor || !detail.data) return
    setLoadingEarlier(true)
    try {
      const page = await api<ThreadPage>(
        `/api/w/conversations/${encodeURIComponent(id)}/messages?cursor=${encodeURIComponent(cursor)}`,
        { workspaceId: ws.id },
      )
      setEarlier({ messages: mergeMessages(page.messages, messages), cursor: page.next_cursor })
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setLoadingEarlier(false)
    }
  }

  function refresh() {
    detail.mutate()
    onChanged()
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
        workspaceId: ws.id,
      })
      setReply("")
      refresh()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function patch(json: Record<string, unknown>, done: string) {
    try {
      await api(`/api/w/conversations/${encodeURIComponent(id)}`, {
        method: "PATCH",
        json,
        workspaceId: ws.id,
      })
      toast.success(done)
      refresh()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  // A canned reply is inserted, never sent: most want a sentence added first.
  function insert(text: string) {
    setReply((cur) => (cur.trim() ? `${cur.trimEnd()}\n${text}` : text))
    document.getElementById("reply-body")?.focus()
  }

  const assigneeOpts = members.options
  // MH-602: same precedence as the list row — the linked contact's name wins.
  const headerWho =
    c.contact?.name ||
    c.customer_name ||
    c.customer_email ||
    c.customer_phone ||
    t("inbox.anonymous")

  return (
    <section data-thread={id}>
      <header className="flex flex-wrap items-center gap-2 border-b border-line px-6 py-4">
        <PersonAvatar
          id={c.contact_id || c.id}
          name={headerWho}
          photoUrl={c.avatar_url}
          size="lg"
          className="shrink-0"
        />
        {/* Its own row on a phone: beside the assignee and snooze controls it was cut to one letter. */}
        <div className="min-w-0 flex-1 max-sm:basis-full">
          <BdiBlock as="p" className="truncate text-sm font-medium" data-thread-title>
            {headerWho}
          </BdiBlock>
          {/* On an email thread the SUBJECT is what the thread is about, and it
              was nowhere on this page — the e2e caught that. Chat has no
              subject worth showing, so it is only rendered when there is one. */}
          {c.subject ? (
            <BdiBlock as="p" className="truncate text-sm text-grid-fg" data-thread-subject>
              {c.subject}
            </BdiBlock>
          ) : null}
          <p className="truncate text-xs text-grid-muted">
            {t(`inbox.channel.${c.channel}`)}
            {c.customer_email ? (
              <>
                {" · "}
                <Ltr>{c.customer_email}</Ltr>
              </>
            ) : null}
            {c.customer_phone ? (
              <>
                {" · "}
                <Ltr>{c.customer_phone}</Ltr>
              </>
            ) : null}
          </p>
          <SnoozedNote c={c} />
        </div>
        {canWrite ? (
          <>
            <div className="w-44">
              <PickSelect
                id="assignee"
                value={c.assignee_user_id ?? ""}
                onChange={(v) => patch({ assignee_user_id: v }, t("inbox.assigned"))}
                options={assigneeOpts}
                noneLabel={t("inbox.unassigned")}
              />
            </div>
            <SnoozeControl c={c} onChanged={refresh} />
            <Button
              variant="outline"
              size="sm"
              data-action={c.status === "closed" ? "reopen" : "close"}
              onClick={() =>
                patch(
                  { status: c.status === "closed" ? "open" : "closed" },
                  c.status === "closed" ? t("inbox.reopened") : t("inbox.closed"),
                )
              }
            >
              {c.status === "closed" ? t("inbox.reopen") : t("inbox.close")}
            </Button>
          </>
        ) : null}
        <VisitorContext c={c} />
      </header>

      <ThreadCRM c={c} visitor={visitor} onChanged={refresh} />

      {total > detail.data.messages.length ? (
        <div className="flex flex-wrap items-center gap-3 px-6 pt-4" data-thread-paging>
          <p className="text-xs text-grid-muted" data-thread-count>
            {cursor
              ? t("inbox.shownOfTotal", { n: messages.length, total })
              : t("inbox.allShown", { total })}
          </p>
          {cursor ? (
            <Button
              variant="outline"
              size="sm"
              onClick={loadEarlier}
              disabled={loadingEarlier}
              data-load-earlier
            >
              <ChevronUpIcon />
              {loadingEarlier ? t("common.working") : t("inbox.loadEarlier")}
            </Button>
          ) : null}
        </div>
      ) : null}

      <ChatThread
        conversation={c}
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
      />
    </section>
  )
}
