"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { MessageSquareIcon, SettingsIcon, MailIcon } from "lucide-react"
import { toast } from "sonner"

import { PickSelect, useMemberNames } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Conversation, type ConversationStatus } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useSWRConfig } from "swr"

import { useWorkspaceEvents } from "@/lib/events"
import { useConversation, useConversations } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

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

  const [status, setStatus] = useState("open")
  const [unread, setUnread] = useState(false)
  const [selected, setSelected] = useState<string | null>(null)

  const list = useConversations(ws.id, { status: status || undefined, unread })

  // No timer: the API tells this tab when a message actually arrives, and the
  // affected thread is revalidated by key so an open conversation updates too.
  const { mutate } = useSWRConfig()
  useWorkspaceEvents(ws.id, (e) => {
    void list.mutate()
    if (e.conversation_id) void mutate([`/api/w/conversations/${encodeURIComponent(e.conversation_id)}`, ws.id])
  })
  const items = list.data?.conversations ?? []

  const statusOpts = [
    { value: "open", label: t("inbox.status.open") },
    { value: "snoozed", label: t("inbox.status.snoozed") },
    { value: "closed", label: t("inbox.status.closed") },
  ]

  return (
    <>
      <SectionHeader
        micro={ws.summary.name}
        title={t("nav.inbox")}
        description={t("inbox.hint")}
        action={
          canManage ? (
            <div className="flex items-center gap-2">
              <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/inbox/email`} />}>
                <MailIcon />
                {t("mailbox.title")}
              </Button>
              <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/inbox/widgets`} />}>
                <SettingsIcon />
                {t("inbox.widgets")}
              </Button>
            </div>
          ) : undefined
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
      </div>

      {list.error ? (
        <ErrorState error={list.error} />
      ) : !list.data ? (
        <LoadingRows rows={5} />
      ) : items.length === 0 ? (
        <EmptyState title={t("inbox.empty")} body={canManage ? t("inbox.emptyBody") : undefined} />
      ) : (
        <div className="grid border-y border-line lg:grid-cols-[22rem_minmax(0,1fr)]">
          <ul className="divide-y divide-line lg:border-e lg:border-line" data-conversation-list>
            {items.map((c) => (
              <ConversationRow key={c.id} c={c} active={c.id === selected} onSelect={() => setSelected(c.id)} />
            ))}
          </ul>
          <div className="min-w-0">
            {selected ? (
              <Thread id={selected} onChanged={() => list.mutate()} />
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

function ConversationRow({ c, active, onSelect }: { c: Conversation; active: boolean; onSelect: () => void }) {
  const { t, formatDate } = useTranslations()
  const who = c.customer_name || c.customer_email || t("inbox.anonymous")
  return (
    <li>
      <button
        type="button"
        onClick={onSelect}
        aria-current={active ? "true" : undefined}
        data-conversation={c.id}
        data-conversation-unread={c.unread ? "true" : "false"}
        className={`flex w-full flex-col gap-1 px-6 py-4 text-start transition-colors ${active ? "bg-grid-soft" : c.unread ? "bg-grid-card" : ""} hover:bg-grid-soft`}
      >
        <span className="flex items-center gap-2">
          {c.unread ? <span className="size-2 shrink-0 rounded-full bg-grid-brand" aria-label={t("inbox.unread")} /> : null}
          <span className="truncate text-sm font-medium">{who}</span>
          <Badge variant={statusVariant[c.status]} className="ms-auto shrink-0">
            {t(`inbox.status.${c.status}`)}
          </Badge>
        </span>
        <span className="truncate text-xs text-grid-muted">{c.preview || c.subject}</span>
        <span className="text-xs text-grid-muted">
          {t(`inbox.channel.${c.channel}`)} · <Ltr>{formatDate(c.last_message_at)}</Ltr> · {t("inbox.messageCount", { n: c.message_count })}
        </span>
      </button>
    </li>
  )
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

  if (detail.error) return <ErrorState error={detail.error} />
  if (!detail.data) return <LoadingRows rows={4} />
  const { conversation: c, messages } = detail.data

  async function send(e: FormEvent) {
    e.preventDefault()
    const text = reply.trim()
    if (!text) return
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/conversations/${encodeURIComponent(id)}/reply`, { json: { body: text }, workspaceId: ws.id })
      setReply("")
      detail.mutate()
      onChanged()
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
      detail.mutate()
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const assigneeOpts = members.options

  return (
    <section data-thread={id}>
      <header className="flex flex-wrap items-center gap-2 border-b border-line px-6 py-4">
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium">{c.customer_name || c.customer_email || t("inbox.anonymous")}</p>
          {/* On an email thread the SUBJECT is what the thread is about, and it
              was nowhere on this page — the e2e caught that. Chat has no
              subject worth showing, so it is only rendered when there is one. */}
          {c.subject ? (
            <p className="truncate text-sm text-grid-fg" data-thread-subject>
              {c.subject}
            </p>
          ) : null}
          <p className="truncate text-xs text-grid-muted">
            {t(`inbox.channel.${c.channel}`)}
            {c.customer_email ? (
              <>
                {" · "}
                <Ltr>{c.customer_email}</Ltr>
              </>
            ) : null}
          </p>
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
            <Button
              variant="outline"
              size="sm"
              onClick={() => patch({ status: c.status === "closed" ? "open" : "closed" }, c.status === "closed" ? t("inbox.reopened") : t("inbox.closed"))}
            >
              {c.status === "closed" ? t("inbox.reopen") : t("inbox.close")}
            </Button>
          </>
        ) : null}
      </header>

      <ol className="flex flex-col gap-3 px-6 py-4" data-messages>
        {messages.map((m) => (
          <li
            key={m.id}
            data-message={m.direction}
            className={`max-w-[44rem] rounded-lg border border-line px-4 py-3 text-sm ${m.direction === "out" ? "ms-auto bg-grid-soft" : "bg-grid-card"}`}
          >
            <p className="break-words whitespace-pre-wrap">{m.body}</p>
            <p className="mt-1 text-xs text-grid-muted">
              {m.direction === "out" ? (m.author_user_id ? members.name(m.author_user_id) : t("inbox.team")) : c.customer_name || t("inbox.customer")} · <Ltr>{formatDate(m.created_at)}</Ltr>
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
            <Textarea id="reply-body" value={reply} onChange={(e) => setReply(e.target.value)} rows={3} placeholder={t("inbox.replyPlaceholder")} maxLength={8000} />
          </Field>
          <div className="mt-3 flex items-center gap-3">
            <Button type="submit" disabled={busy || !reply.trim()}>
              <MessageSquareIcon />
              {busy ? t("common.working") : t("inbox.send")}
            </Button>
            {err ? <FieldError>{err}</FieldError> : null}
          </div>
        </form>
      ) : (
        <p className="border-t border-line px-6 py-4 text-sm text-grid-muted">{t("inbox.readOnly")}</p>
      )}
    </section>
  )
}
