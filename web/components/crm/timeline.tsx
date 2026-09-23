"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { ActivityIcon, useMemberNames } from "@/components/crm/shared"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { EmptyState } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { ACTIVITY_KINDS, api, ApiError, type Activity, type ActivityKind } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMe } from "@/lib/queries"
import { Bdi } from "@/components/bidi"

type Target = { contact_id?: string; company_id?: string; deal_id?: string }

/**
 * A record's timeline: a composer (note / call / meeting / task) and the history,
 * open tasks first. Used by the contact, company and deal pages.
 */
export function Timeline({ items, target, onChange }: { items: Activity[]; target: Target; onChange: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  const open = items.filter((a) => a.kind === "task" && !a.done_at)
  const rest = items.filter((a) => !(a.kind === "task" && !a.done_at))

  return (
    <section data-timeline>
      <h2 className="px-6 pb-1 text-base font-medium">{t("timeline.title")}</h2>
      <p className="px-6 pb-4 text-sm text-grid-muted">{t("timeline.hint")}</p>
      {canWrite ? <Composer target={target} onAdded={onChange} /> : null}
      {open.length > 0 ? (
        <>
          <p className="grid-micro px-6 pt-4 pb-2">{t("timeline.openTasks")}</p>
          <ul className="divide-y divide-line border-y border-line" data-open-tasks>
            {open.map((a) => (
              <Item key={a.id} a={a} target={target} onChange={onChange} />
            ))}
          </ul>
        </>
      ) : null}
      <p className="grid-micro px-6 pt-4 pb-2">{t("timeline.history")}</p>
      {rest.length === 0 ? (
        <EmptyState title={t("timeline.empty")} body={canWrite ? t("timeline.emptyBody") : undefined} />
      ) : (
        <ul className="divide-y divide-line border-y border-line">
          {rest.map((a) => (
            <Item key={a.id} a={a} target={target} onChange={onChange} />
          ))}
        </ul>
      )}
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
        json: { kind, body: body.trim(), due_at: kind === "task" && due ? due : undefined, ...target },
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
      <div role="radiogroup" aria-label={t("timeline.kind")} className="mb-3 flex flex-wrap gap-1.5">
        {ACTIVITY_KINDS.map((k) => (
          <Button
            key={k}
            type="button"
            size="sm"
            role="radio"
            aria-checked={kind === k}
            data-kind={k}
            variant={kind === k ? "default" : "outline"}
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
            <Input id="activity-due" type="date" dir="ltr" value={due} onChange={(e) => setDue(e.target.value)} />
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
  const canDelete = canWrite && (String(me.data?.id ?? "") === a.created_by || ws.can("pipeline:manage"))
  const base = `/${locale}/w/${ws.slug}`
  const overdue = a.kind === "task" && !a.done_at && a.due_at && new Date(a.due_at) < new Date(new Date().toDateString())

  async function toggle() {
    try {
      await api(`/api/w/activities/${encodeURIComponent(a.id)}`, { method: "PATCH", json: { done: !a.done_at }, workspaceId: ws.id })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function remove() {
    try {
      await api(`/api/w/activities/${encodeURIComponent(a.id)}`, { method: "DELETE", workspaceId: ws.id })
      toast.success(t("timeline.deleted"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  // Links to the other records this activity is on (not the page we are on).
  const refs: { href: string; label: string }[] = []
  if (a.contact && a.contact.id !== target.contact_id) refs.push({ href: `${base}/contacts/${a.contact.id}`, label: a.contact.name || a.contact.email })
  if (a.company && a.company.id !== target.company_id) refs.push({ href: `${base}/companies/${a.company.id}`, label: a.company.name })
  if (a.deal && a.deal.id !== target.deal_id) refs.push({ href: `${base}/deals/${a.deal.id}`, label: a.deal.name })

  return (
    <li className="flex items-start gap-3 px-6 py-4" data-activity={a.kind} data-activity-done={a.done_at ? "true" : "false"}>
      {a.kind === "task" ? (
        <input
          type="checkbox"
          className="mt-1 size-4 shrink-0 accent-[var(--color-grid-brand)]"
          checked={!!a.done_at}
          disabled={!canWrite}
          onChange={toggle}
          aria-label={a.done_at ? t("timeline.markOpen") : t("timeline.markDone")}
        />
      ) : (
        <span className="mt-0.5 text-grid-muted [&_svg]:size-4" aria-hidden>
          <ActivityIcon kind={a.kind} />
        </span>
      )}
      <div className="min-w-0 flex-1">
        <p dir="auto" className={`text-start text-sm break-words whitespace-pre-wrap ${a.done_at ? "text-grid-muted line-through" : ""}`}>{a.body}</p>
        <p className="mt-1 flex flex-wrap gap-x-2 gap-y-1 text-xs text-grid-muted">
          <span>{t(`activity.${a.kind}`)}</span>
          <span aria-hidden>·</span>
          <Ltr>{members.name(a.created_by)}</Ltr>
          <span aria-hidden>·</span>
          <Ltr>{formatDate(a.created_at)}</Ltr>
          {a.kind === "task" && a.due_at ? (
            <>
              <span aria-hidden>·</span>
              <span className={overdue ? "text-grid-danger-text" : ""}>
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

