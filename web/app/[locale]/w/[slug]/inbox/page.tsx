"use client"

import Link from "next/link"
import { useEffect, useRef, useState, type FormEvent } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { BarChart3Icon, ChevronUpIcon, EllipsisVerticalIcon, MailIcon, MessageCircleIcon, MessageSquareIcon, MessageSquareQuoteIcon, SettingsIcon } from "lucide-react"
import { toast } from "sonner"

import { PickSelect, useMemberNames } from "@/components/crm/shared"
import { CannedPicker } from "@/components/inbox/canned-picker"
import { AttachmentList, MessageBody } from "@/components/inbox/message-body"
import { SnoozeControl, SnoozedNote } from "@/components/inbox/snooze-control"
import { ThreadCRM } from "@/components/inbox/thread-crm"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { MarkdownEditor } from "@/components/campaigns/markdown-editor"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Conversation, type ConversationStatus, type InboxMessage, type ThreadPage } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { renderMarkdownPreview } from "@/lib/markdown"
import { useSWRConfig } from "swr"

import { useWorkspaceEvents } from "@/lib/events"
import { useConversation, useConversationPages } from "@/lib/queries"
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

  const pages = useConversationPages(ws.id, { status: status || undefined, unread, q: params.get("q") ?? undefined })
  const list = { error: pages.error, data: pages.data?.[0], mutate: pages.mutate }
  const items = (pages.data ?? []).flatMap((p) => p.conversations)
  const hasMore = !!pages.data?.[pages.data.length - 1]?.next_cursor
  const loadingMore = pages.isValidating && pages.size > 1

  // No timer: the API tells this tab when a message actually arrives, and the
  // affected thread is revalidated by key so an open conversation updates too.
  const { mutate } = useSWRConfig()
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind !== "message" && e.kind !== "conversation") return
    void pages.mutate()
    if (e.conversation_id) void mutate([`/api/w/conversations/${encodeURIComponent(e.conversation_id)}`, ws.id])
  })

  // Infinite scroll (MH-556): the sentinel below the list asks for the next
  // page as soon as it is in view, instead of a "Load more" button.
  const sentinelRef = useRef<HTMLLIElement>(null)
  useEffect(() => {
    const el = sentinelRef.current
    if (!el) return
    const obs = new IntersectionObserver(
      (entries) => {
        if (entries[0]?.isIntersecting && hasMore && !loadingMore) void pages.setSize(pages.size + 1)
      },
      { rootMargin: "400px" },
    )
    obs.observe(el)
    return () => obs.disconnect()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasMore, loadingMore, pages.size])

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
  useEffect(() => {
    const c = items[cursorIdx]
    if (c) rowRefs.current[c.id]?.scrollIntoView({ block: "nearest" })
  }, [cursorIdx, items])

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
        action={
          <div className="flex flex-wrap items-center gap-2">
            {/* Stats are everyone's (inbox:read); the settings are a manager's. */}
            <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/inbox/stats`} />}>
              <BarChart3Icon />
              {t("inbox.stats")}
            </Button>
            {canManage ? (
              <>
              <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/inbox/canned`} />}>
                <MessageSquareQuoteIcon />
                {t("inbox.canned")}
              </Button>
              <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/inbox/email`} />}>
                <MailIcon />
                {t("mailbox.title")}
              </Button>
              <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/inbox/whatsapp`} />}>
                <MessageCircleIcon />
                {t("whatsapp.title")}
              </Button>
              <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/inbox/widgets`} />}>
                <SettingsIcon />
                {t("inbox.widgets")}
              </Button>
              </>
            ) : null}
          </div>
        }
      />

      <div className="flex flex-wrap items-center gap-2 px-6 pb-4" role="group" aria-label={t("inbox.filters")}>
        <div className="w-40">
          <PickSelect id="inbox-status" value={status} onChange={setStatus} options={statusOpts} noneLabel={t("inbox.allStatuses")} />
        </div>
        <Button type="button" size="sm" variant={unread ? "default" : "outline"} aria-pressed={unread} onClick={() => setUnread((v) => !v)} data-filter="unread">
          {t("inbox.filterUnread")}
          {list.data?.unread_count ? ` (${list.data.unread_count})` : ""}
        </Button>
        <input
          id="inbox-search"
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("inbox.searchPlaceholder")}
          dir="auto"
          data-search="conversations"
          className="h-8 min-w-40 flex-1 border border-line bg-grid-card px-3 text-sm placeholder:text-grid-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-grid-brand"
        />
      </div>

      {list.error ? (
        <ErrorState error={list.error} />
      ) : !list.data ? (
        <LoadingRows rows={5} />
      ) : items.length === 0 ? (
        <EmptyState title={t("inbox.empty")} body={canManage ? t("inbox.emptyBody") : undefined} />
      ) : (
        <div className="grid grid-cols-1 border-y border-line lg:grid-cols-[22rem_minmax(0,1fr)]">
          <ul className="min-w-0 divide-y divide-line lg:border-e lg:border-line" data-conversation-list>
            {items.map((c, i) => (
              <ConversationRow
                key={c.id}
                c={c}
                active={c.id === selected}
                highlighted={i === cursorIdx}
                onSelect={() => {
                  setCursorIdx(i)
                  select(c.id)
                }}
                buttonRef={(el) => {
                  rowRefs.current[c.id] = el
                }}
                onChanged={() => void pages.mutate()}
              />
            ))}
            <li ref={sentinelRef} aria-hidden className="h-1" data-scroll-sentinel />
            {loadingMore ? <li className="px-6 py-3 text-center text-xs text-grid-muted">{t("common.working")}</li> : null}
          </ul>
          <div className="min-w-0">
            {selected ? (
              <Thread key={selected} id={selected} onChanged={() => list.mutate()} />
            ) : (
              <p className="px-6 py-10 text-center text-sm text-grid-muted">{t("inbox.pickOne")}</p>
            )}
          </div>
        </div>
      )}
      <div className="pb-8" />
    </>
  )
}

function ConversationRow({
  c,
  active,
  highlighted,
  onSelect,
  buttonRef,
  onChanged,
}: {
  c: Conversation
  active: boolean
  highlighted?: boolean
  onSelect: () => void
  buttonRef?: (el: HTMLButtonElement | null) => void
  onChanged?: () => void
}) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const who = c.customer_name || c.customer_email || t("inbox.anonymous")
  const [menuOpen, setMenuOpen] = useState(false)
  const longPress = useRef<number | null>(null)

  // The context menu (MH-556): a kebab button, right-click, and a touch
  // long-press all open the SAME menu — one set of actions, three ways in.
  async function act(patch: Record<string, unknown>, message: string) {
    try {
      await api(`/api/w/conversations/${encodeURIComponent(c.id)}`, { method: "PATCH", json: patch, workspaceId: ws.id })
      toast.success(message)
      onChanged?.()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <li
      className="group relative"
      onContextMenu={(e) => {
        e.preventDefault()
        setMenuOpen(true)
      }}
      onTouchStart={() => {
        longPress.current = window.setTimeout(() => setMenuOpen(true), 500)
      }}
      onTouchEnd={() => {
        if (longPress.current) window.clearTimeout(longPress.current)
      }}
    >
      <button
        ref={buttonRef}
        type="button"
        onClick={onSelect}
        aria-current={active ? "true" : undefined}
        data-conversation={c.id}
        data-conversation-unread={c.unread ? "true" : "false"}
        className={`flex w-full min-w-0 flex-col gap-1 px-6 py-4 text-start transition-colors ${active ? "bg-grid-soft" : c.unread ? "bg-grid-card" : ""} ${highlighted ? "ring-2 ring-inset ring-grid-brand" : ""} hover:bg-grid-soft`}
      >
        <span className="flex min-w-0 items-center gap-2">
          {c.unread ? <span className="size-2 shrink-0 rounded-full bg-grid-brand" aria-label={t("inbox.unread")} /> : null}
          <BdiBlock className="truncate text-sm font-medium">{who}</BdiBlock>
          <Badge variant={statusVariant[c.status]} className="ms-auto shrink-0">
            {t(`inbox.status.${c.status}`)}
          </Badge>
        </span>
        <BdiBlock className="block truncate text-xs text-grid-muted" data-conversation-preview>
          {c.preview || c.subject}
        </BdiBlock>
        <span className="text-xs text-grid-muted">
          {t(`inbox.channel.${c.channel}`)} · <Ltr>{formatDate(c.last_message_at)}</Ltr> · {t("inbox.messageCount", { n: c.message_count })}
        </span>
      </button>

      <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenuTrigger
          render={
            <button
              type="button"
              aria-label={t("inbox.moreActions")}
              data-context-menu-trigger
              onClick={(e) => e.stopPropagation()}
              className="absolute end-2 top-3 rounded-md p-1 text-grid-muted opacity-0 hover:bg-grid-card focus-visible:opacity-100 group-hover:opacity-100 group-focus-within:opacity-100"
            />
          }
        >
          <EllipsisVerticalIcon className="size-4" />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-44">
          <DropdownMenuItem onClick={() => act({ unread: !c.unread }, c.unread ? t("inbox.markedRead") : t("inbox.markedUnread"))} data-action="toggle-unread">
            {c.unread ? t("inbox.markRead") : t("inbox.markUnread")}
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => act({ status: c.status === "closed" ? "open" : "closed" }, c.status === "closed" ? t("inbox.reopened") : t("inbox.closed"))}
            data-action="toggle-status"
          >
            {c.status === "closed" ? t("inbox.reopen") : t("inbox.close")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </li>
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
    language = (c.locale && new Intl.DisplayNames([locale], { type: "language" }).of(c.locale)) || c.locale
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
              <a href={c.page_url} target="_blank" rel="noopener noreferrer nofollow" className="text-grid-fg underline-offset-2 hover:underline">
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
          <dd dir="ltr" className="min-w-0 truncate rtl:text-right" data-context="browser" title={c.user_agent}>
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
  const { t, formatDate } = useTranslations()
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
      await api(`/api/w/conversations/${encodeURIComponent(id)}/reply`, { json: { body: text }, workspaceId: ws.id })
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
      await api(`/api/w/conversations/${encodeURIComponent(id)}`, { method: "PATCH", json, workspaceId: ws.id })
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

  return (
    <section data-thread={id}>
      <header className="flex flex-wrap items-center gap-2 border-b border-line px-6 py-4">
        {/* Its own row on a phone: beside the assignee and snooze controls it was cut to one letter. */}
        <div className="min-w-0 flex-1 max-sm:basis-full">
          <BdiBlock as="p" className="truncate text-sm font-medium">
            {c.customer_name || c.customer_email || c.customer_phone || t("inbox.anonymous")}
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
              onClick={() => patch({ status: c.status === "closed" ? "open" : "closed" }, c.status === "closed" ? t("inbox.reopened") : t("inbox.closed"))}
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
            {cursor ? t("inbox.shownOfTotal", { n: messages.length, total }) : t("inbox.allShown", { total })}
          </p>
          {cursor ? (
            <Button variant="outline" size="sm" onClick={loadEarlier} disabled={loadingEarlier} data-load-earlier>
              <ChevronUpIcon />
              {loadingEarlier ? t("common.working") : t("inbox.loadEarlier")}
            </Button>
          ) : null}
        </div>
      ) : null}

      <ol className="flex flex-col gap-3 px-6 py-4" data-messages>
        {messages.map((m) => (
          <li
            key={m.id}
            data-message={m.direction}
            className={`max-w-[44rem] rounded-lg border border-line px-4 py-3 text-sm ${m.direction === "out" ? "ms-auto bg-grid-soft" : "bg-grid-card"}`}
          >
            <MessageBody body={m.body} bodyHtml={m.body_html} />
            <AttachmentList attachments={m.attachments} />
            <p className="mt-1 text-xs text-grid-muted">
              {m.direction === "out" ? (m.author_user_id ? members.name(m.author_user_id) : t("inbox.team")) : c.customer_name ? <Bdi>{c.customer_name}</Bdi> : t("inbox.customer")} · <Ltr>{formatDate(m.created_at)}</Ltr>
            </p>
          </li>
        ))}
      </ol>

      {canWrite ? (
        <form onSubmit={send} noValidate className="border-t border-line px-6 py-4" data-form="reply">
          <Field>
            <FieldLabel htmlFor="reply-body" className="sr-only">
              {t("inbox.replyLabel")}
            </FieldLabel>
            <Tabs defaultValue="write">
              <TabsList className="mb-2">
                <TabsTrigger value="write" data-tab="write">
                  {t("inbox.write")}
                </TabsTrigger>
                <TabsTrigger value="preview" data-tab="preview">
                  {t("inbox.preview")}
                </TabsTrigger>
              </TabsList>
              <TabsContent value="write">
                <MarkdownEditor id="reply-body" value={reply} onValueChange={setReply} rows={3} className="min-h-24" />
              </TabsContent>
              <TabsContent value="preview">
                <div
                  className="min-h-24 border border-line bg-grid-card px-3 py-2 text-sm [&_a]:underline [&_code]:rounded [&_code]:bg-grid-soft [&_code]:px-1"
                  dir="auto"
                  data-reply-preview
                  // Rendered through marked then DOMPurify (web/lib/markdown.ts) — the
                  // same two-pass rule as message-body.tsx: this is a preview of what
                  // the recipient will see, so it must be exactly as inert.
                  dangerouslySetInnerHTML={{ __html: renderMarkdownPreview(reply) || `<p class="text-grid-muted">${t("inbox.previewEmpty")}</p>` }}
                />
              </TabsContent>
            </Tabs>
          </Field>
          <div className="mt-3 flex flex-wrap items-center gap-3">
            <Button type="submit" disabled={busy || !reply.trim()}>
              <MessageSquareIcon />
              {busy ? t("common.working") : t("inbox.send")}
            </Button>
            <CannedPicker lang={replyLang} onInsert={insert} />
            {err ? <FieldError>{err}</FieldError> : null}
          </div>
        </form>
      ) : (
        <p className="border-t border-line px-6 py-4 text-sm text-grid-muted">{t("inbox.readOnly")}</p>
      )}
    </section>
  )
}
