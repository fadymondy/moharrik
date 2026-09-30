"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { Badge, Button, Field, FieldError, FieldLabel, Input, Textarea, toast } from "@fadymondy/nasaq/web"

import { MailIcon, MessageCircleIcon, MessageSquareIcon } from "lucide-react"

import { ActivityIcon, useMemberNames } from "@/components/crm/shared"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { EmptyState } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  ACTIVITY_KINDS,
  api,
  ApiError,
  type Activity,
  type ActivityKind,
  type ConversationRef,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMe } from "@/lib/queries"
import { Bdi } from "@/components/bidi"

type Target = { contact_id?: string; company_id?: string; deal_id?: string }

/**
 * A record's timeline: a composer (note / call / meeting / task) and the history,
 * open tasks first. Used by the contact, company and deal pages.
 *
 * `conversations` are the inbox threads linked to the record (MH-379). They sit
 * in the history by their latest message, among the activities, each linking
 * back to the thread in the inbox.
 */
export function Timeline({
  items,
  total,
  nextCursor,
  target,
  onChange,
  conversations = [],
}: {
  items: Activity[]
  /** Every entry on the timeline; more than items.length means older ones are behind nextCursor. */
  total: number
  nextCursor: string
  target: Target
  onChange: () => void
  /**
   * The linked inbox conversations in the first page's stretch of time (MH-379). Each older
   * page brings the ones in its own stretch, so a conversation never shows below activities
   * that have not been loaded yet, and "Showing N of M" stays a count of the activities.
   */
  conversations?: ConversationRef[]
}) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  // Older pages, fetched on demand (MH-467: the timeline used to stop at the newest 500). They
  // belong to one first page: when it is refetched (an entry added or changed), they are dropped.
  const [older, setOlder] = useState<{
    from: string
    items: Activity[]
    conversations: ConversationRef[]
    cursor: string
  }>({
    from: nextCursor,
    items: [],
    conversations: [],
    cursor: nextCursor,
  })
  const [loading, setLoading] = useState(false)
  const fresh =
    older.from === nextCursor
      ? older
      : { from: nextCursor, items: [], conversations: [], cursor: nextCursor }
  const all = [...items, ...fresh.items.filter((a) => !items.some((b) => b.id === a.id))]
  const allConversations = [
    ...conversations,
    ...fresh.conversations.filter((c) => !conversations.some((d) => d.id === c.id)),
  ]
  const open = all.filter((a) => a.kind === "task" && !a.done_at)
  const rest = all.filter((a) => !(a.kind === "task" && !a.done_at))
  // Newest first, activities and conversations together.
  const history = [
    ...rest.map((a) => ({
      at: a.created_at,
      activity: a,
      conversation: null as ConversationRef | null,
    })),
    ...allConversations.map((c) => ({
      at: c.last_message_at,
      activity: null as Activity | null,
      conversation: c,
    })),
  ].sort((x, y) => (x.at < y.at ? 1 : x.at > y.at ? -1 : 0))

  async function loadOlder() {
    if (!fresh.cursor) return
    setLoading(true)
    try {
      const qs = new URLSearchParams(
        Object.entries(target).filter(([, v]) => !!v) as [string, string][],
      )
      qs.set("cursor", fresh.cursor)
      const page = await api<{
        activities: Activity[]
        next_cursor: string
        conversations?: ConversationRef[]
      }>(`/api/w/activities?${qs}`, {
        workspaceId: ws.id,
      })
      setOlder({
        from: nextCursor,
        items: [...fresh.items, ...page.activities],
        conversations: [...fresh.conversations, ...(page.conversations ?? [])],
        cursor: page.next_cursor,
      })
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setLoading(false)
    }
  }

  return (
    <section data-timeline>
      <h2 className="px-6 pb-1 text-base font-medium">{t("timeline.title")}</h2>
      <p className="px-6 pb-4 text-sm text-muted-foreground">{t("timeline.hint")}</p>
      {canWrite ? <Composer target={target} onAdded={onChange} /> : null}
      {open.length > 0 ? (
        <>
          <p className="eyebrow px-6 pt-4 pb-2">{t("timeline.openTasks")}</p>
          <ul className="divide-y divide-nq-line border-y border-nq-line" data-open-tasks>
            {open.map((a) => (
              <Item key={a.id} a={a} target={target} onChange={onChange} />
            ))}
          </ul>
        </>
      ) : null}
      <p className="eyebrow px-6 pt-4 pb-2">{t("timeline.history")}</p>
      {history.length === 0 && !fresh.cursor ? (
        <EmptyState
          title={t("timeline.empty")}
          body={canWrite ? t("timeline.emptyBody") : undefined}
        />
      ) : (
        <ul className="divide-y divide-nq-line border-y border-nq-line">
          {history.map((h) =>
            h.activity ? (
              <Item key={h.activity.id} a={h.activity} target={target} onChange={onChange} />
            ) : h.conversation ? (
              <ConversationItem key={`c-${h.conversation.id}`} c={h.conversation} target={target} />
            ) : null,
          )}
        </ul>
      )}
      {total > items.length ? (
        <div className="flex flex-wrap items-center gap-3 px-6 pt-4" data-timeline-more>
          <span className="text-sm text-muted-foreground" data-timeline-shown={`${all.length}/${total}`}>
            {t("timeline.showing", { shown: formatNumber(all.length), total: formatNumber(total) })}
          </span>
          {fresh.cursor ? (
            <Button
              variant="secondary"
              size="sm"
              onClick={loadOlder}
              disabled={loading}
              data-timeline-load-older
            >
              {loading ? t("common.working") : t("timeline.loadOlder")}
            </Button>
          ) : null}
        </div>
      ) : null}
    </section>
  )
}

function Composer({ target, onAdded }: { target: Target; onAdded: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [kind, setKind] = useState<ActivityKind>("note")
  const [body, setBody] = useState("")
  const [due, setDue] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/activities", {
        json: {
          kind,
          body: body.trim(),
          due_at: kind === "task" && due ? due : undefined,
          ...target,
        },
        workspaceId: ws.id,
      })
      toast.success(t(`timeline.logged.${kind}`))
      setBody("")
      setDue("")
      onAdded()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="px-6 pb-4" data-form="activity">
      <div
        role="radiogroup"
        aria-label={t("timeline.kind")}
        className="mb-3 flex flex-wrap gap-1.5"
      >
        {ACTIVITY_KINDS.map((k) => (
          <Button
            key={k}
            type="button"
            size="sm"
            role="radio"
            aria-checked={kind === k}
            data-kind={k}
            variant={kind === k ? "primary" : "secondary"}
            onClick={() => setKind(k)}
          >
            <ActivityIcon kind={k} />
            {t(`activity.${k}`)}
          </Button>
        ))}
      </div>
      <Field>
        <FieldLabel htmlFor="activity-body" className="sr-only">
          {t(`timeline.placeholder.${kind}`)}
        </FieldLabel>
        <Textarea
          id="activity-body"
          value={body}
          onChange={(e) => setBody(e.target.value)}
          placeholder={t(`timeline.placeholder.${kind}`)}
          maxLength={5000}
          rows={3}
        />
      </Field>
      <div className="mt-3 flex flex-wrap items-end gap-3">
        {kind === "task" ? (
          <Field className="w-44">
            <FieldLabel htmlFor="activity-due">{t("timeline.due")}</FieldLabel>
            <Input
              id="activity-due"
              type="date"
              dir="ltr"
              value={due}
              onChange={(e) => setDue(e.target.value)}
            />
          </Field>
        ) : null}
        <Button type="submit" disabled={busy || !body.trim()}>
          {busy ? t("common.working") : t(`timeline.add.${kind}`)}
        </Button>
      </div>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}

function Item({ a, target, onChange }: { a: Activity; target: Target; onChange: () => void }) {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const members = useMemberNames(ws.id)
  const canWrite = ws.can("contacts:write")
  const me = useMe()
  // The API lets the author or a manager (pipeline:manage is manager+) delete an activity.
  const canDelete =
    canWrite && (String(me.data?.id ?? "") === a.created_by || ws.can("pipeline:manage"))
  const base = `/${locale}/w/${ws.slug}`
  const overdue =
    a.kind === "task" &&
    !a.done_at &&
    a.due_at &&
    new Date(a.due_at) < new Date(new Date().toDateString())

  async function toggle() {
    try {
      await api(`/api/w/activities/${encodeURIComponent(a.id)}`, {
        method: "PATCH",
        json: { done: !a.done_at },
        workspaceId: ws.id,
      })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function remove() {
    try {
      await api(`/api/w/activities/${encodeURIComponent(a.id)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      toast.success(t("timeline.deleted"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  // Links to the other records this activity is on (not the page we are on).
  const refs: { href: string; label: string }[] = []
  if (a.contact && a.contact.id !== target.contact_id)
    refs.push({
      href: `${base}/contacts/${a.contact.id}`,
      label: a.contact.name || a.contact.email,
    })
  if (a.company && a.company.id !== target.company_id)
    refs.push({ href: `${base}/companies/${a.company.id}`, label: a.company.name })
  if (a.deal && a.deal.id !== target.deal_id)
    refs.push({ href: `${base}/deals/${a.deal.id}`, label: a.deal.name })

  return (
    <li
      className="flex items-start gap-3 px-6 py-4"
      data-activity={a.kind}
      data-activity-done={a.done_at ? "true" : "false"}
    >
      {a.kind === "task" ? (
        <input
          type="checkbox"
          className="mt-1 size-4 shrink-0 accent-[var(--nq-brand)]"
          checked={!!a.done_at}
          disabled={!canWrite}
          onChange={toggle}
          aria-label={a.done_at ? t("timeline.markOpen") : t("timeline.markDone")}
        />
      ) : (
        <span className="mt-0.5 text-muted-foreground [&_svg]:size-4" aria-hidden>
          <ActivityIcon kind={a.kind} />
        </span>
      )}
      <div className="min-w-0 flex-1">
        <ActivityBody body={a.body} done={!!a.done_at} />
        <p className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-xs text-muted-foreground">
          <span>{t(`activity.${a.kind}`)}</span>
          <span aria-hidden>·</span>
          <Ltr>{members.name(a.created_by)}</Ltr>
          <span aria-hidden>·</span>
          <Ltr>{formatDate(a.created_at)}</Ltr>
          {a.kind === "task" && a.due_at ? (
            <>
              <span aria-hidden>·</span>
              <span className={overdue ? "text-nq-danger-text" : ""}>
                {t("timeline.dueOn", { date: formatDate(a.due_at) })}
              </span>
            </>
          ) : null}
          {refs.map((r) => (
            <span key={r.href} className="inline-flex gap-2">
              <span aria-hidden>·</span>
              <Link className="underline underline-offset-4" href={r.href}>
                <Bdi>{r.label}</Bdi>
              </Link>
            </span>
          ))}
        </p>
      </div>
      {canDelete ? (
        <ConfirmButton
          label={t("common.delete")}
          title={t("timeline.deleteTitle")}
          description={t("timeline.deleteBody")}
          confirmLabel={t("common.delete")}
          onConfirm={remove}
        />
      ) : null}
    </li>
  )
}

// Record<string, ...> on purpose (MH-568): ConversationChannel is no longer a
// closed union, so a channel this map doesn't name (telegram, slack, a
// workflow feed) falls through to the MessageSquareIcon default below rather
// than failing to typecheck.
const channelIcon: Record<string, typeof MessageSquareIcon> = {
  chat: MessageSquareIcon,
  email: MailIcon,
  whatsapp: MessageCircleIcon,
}

/** An inbox conversation on a CRM timeline (MH-379): who, where, how much, and the way back to the thread. */
function ConversationItem({ c, target }: { c: ConversationRef; target: Target }) {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const base = `/${locale}/w/${ws.slug}`
  const Icon = channelIcon[c.channel] ?? MessageSquareIcon
  // On a company page the conversation names which of its people wrote.
  const person = c.contact && c.contact.id !== target.contact_id ? c.contact : null

  return (
    <li className="flex items-start gap-3 px-6 py-4" data-timeline-conversation={c.id}>
      <span className="mt-0.5 text-muted-foreground [&_svg]:size-4" aria-hidden>
        <Icon />
      </span>
      <div className="min-w-0 flex-1">
        <p className="text-sm">
          <span className="font-medium">
            {t("timeline.conversation")} · {t(`inbox.channel.${c.channel}`)}
          </span>
          {c.channel === "email" && c.subject ? (
            <span className="text-muted-foreground">
              {" · "}
              <Bdi>{c.subject}</Bdi>
            </span>
          ) : null}
        </p>
        {c.preview ? (
          <p
            dir="auto"
            className="mt-0.5 line-clamp-2 text-start text-sm break-words text-muted-foreground"
          >
            {c.preview}
          </p>
        ) : null}
        <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
          <Badge
            variant={
              c.status === "open" ? "brand" : c.status === "snoozed" ? "neutral" : "outline"
            }
          >
            {t(`inbox.status.${c.status}`)}
          </Badge>
          <span>{t("inbox.messageCount", { n: c.message_count })}</span>
          <span aria-hidden>·</span>
          <Ltr>{formatDate(c.last_message_at)}</Ltr>
          {person ? (
            <>
              <span aria-hidden>·</span>
              <Link className="underline underline-offset-4" href={`${base}/contacts/${person.id}`}>
                <Bdi>{person.name || person.email}</Bdi>
              </Link>
            </>
          ) : null}
          <span aria-hidden>·</span>
          <Link
            className="underline underline-offset-4"
            href={`${base}/inbox?c=${encodeURIComponent(c.id)}`}
            data-open-in-inbox
          >
            {t("timeline.openInInbox")}
          </Link>
        </p>
      </div>
    </li>
  )
}

// A chat transcript can be long (MH-379): past this many lines the body is
// clamped, with a button to read the rest in place.
const CLAMP_LINES = 12

function ActivityBody({ body, done }: { body: string; done: boolean }) {
  const { t } = useTranslations()
  const [all, setAll] = useState(false)
  const long = body.split("\n").length > CLAMP_LINES || body.length > 1200
  return (
    <>
      <p
        dir="auto"
        className={`text-start text-sm break-words whitespace-pre-wrap ${done ? "text-muted-foreground line-through" : ""} ${long && !all ? "line-clamp-[12]" : ""}`}
      >
        {body}
      </p>
      {long ? (
        <Button
          variant="link"
          size="sm"
          className="px-0"
          onClick={() => setAll((v) => !v)}
          aria-expanded={all}
          data-activity-more
        >
          {all ? t("timeline.showLess") : t("timeline.showAll")}
        </Button>
      ) : null}
    </>
  )
}
