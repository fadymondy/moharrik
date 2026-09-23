"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { useState, type FormEvent } from "react"
import { LogOutIcon } from "lucide-react"
import { toast } from "sonner"

import { DealsTable } from "@/components/crm/deals"
import { LifecycleBadge, ScoreMeter, ScoreReasons } from "@/components/crm/icp"
import { PickSelect, useMemberNames } from "@/components/crm/shared"
import { Timeline } from "@/components/crm/timeline"
import { Ltr } from "@/components/copy-field"
import { ConfirmButton } from "@/components/confirm-button"
import { UnsubscribeDialog, type UnsubscribeTarget } from "@/components/mail/subscriber-admin"
import { NotFoundState } from "@/components/not-found-state"
import { SectionHeader } from "@/components/public-frame"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, LIFECYCLE_STAGES, type ContactDetail, type Lifecycle, type SubscriptionStatus } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useContact } from "@/lib/queries"
import { dateFormat } from "@/lib/i18n-locale"
import { CompanyPicker } from "@/components/crm/company-picker"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

const statusVariant: Record<SubscriptionStatus, "default" | "secondary" | "outline" | "destructive"> = {
  subscribed: "default",
  pending: "secondary",
  unsubscribed: "outline",
  bounced: "destructive",
}

export default function ContactDetailPage() {
  const { id } = useParams<{ id: string }>()
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  const detail = useContact(ws.id, id)
  const [leaving, setLeaving] = useState<UnsubscribeTarget | null>(null)
  useDocumentTitle(detail.data ? `${detail.data.contact.name || detail.data.contact.email} · ${ws.summary.name}` : t("contacts.detailTitle"))
  const base = `/${locale}/w/${ws.slug}`

  if (detail.error) {
    const notFound = detail.error instanceof ApiError && detail.error.status === 404
    if (notFound) return <NotFoundState title={t("contacts.notFound")} body={t("contacts.notFoundBody")} backHref={`${base}/contacts`} backLabel={t("contacts.backToList")} />
    return <ErrorState error={detail.error} />
  }
  if (!detail.data) return <LoadingRows rows={6} />

  const { contact, deals, activities } = detail.data

  return (
    <>
      <SectionHeader
        micro={t("nav.contacts")}
        title={contact.name ? <Bdi>{contact.name}</Bdi> : <Ltr>{contact.email}</Ltr>}
        description={contact.name ? <Ltr>{contact.email}</Ltr> : undefined}
        action={
          canWrite ? (
            <ConfirmButton
              label={t("common.delete")}
              title={t("contacts.deleteTitle")}
              description={t("contacts.deleteBody", { email: isolate(contact.email) })}
              confirmLabel={t("common.delete")}
              onConfirm={async () => {
                await api(`/api/w/contacts/${encodeURIComponent(contact.id)}`, { method: "DELETE", workspaceId: ws.id })
                window.location.href = `${base}/contacts`
              }}
            />
          ) : undefined
        }
      />

      {(contact.subscriptions ?? []).length > 0 ? (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-6 pb-6" data-contact-subscriptions>
          {contact.subscriptions.map((s) => (
            <span key={s.id} className="inline-flex items-center gap-1">
              <Badge variant={statusVariant[s.status]} data-sub={`${s.list_name}:${s.status}`}>
                <Bdi>{s.list_name}</Bdi> · {t(`sub.${s.status}`)}
              </Badge>
              {canWrite && s.status !== "unsubscribed" ? (
                <Button
                  variant="ghost"
                  size="xs"
                  data-unsubscribe-from={s.list_name}
                  onClick={() => setLeaving({ contactId: contact.id, who: contact.name || contact.email, listId: s.list_id, listName: s.list_name })}
                >
                  <LogOutIcon />
                  {t("contacts.unsubscribe")}
                </Button>
              ) : null}
            </span>
          ))}
        </div>
      ) : null}
      <UnsubscribeDialog target={leaving} onClose={() => setLeaving(null)} onDone={() => detail.mutate()} />

      <ContactFit contact={contact} canWrite={canWrite} onSaved={() => detail.mutate()} />

      {canWrite ? <EditContact contact={contact} onSaved={() => detail.mutate()} /> : null}

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />

      <section>
        <h2 className="px-6 py-4 text-base font-medium">{t("nav.deals")}</h2>
        <DealsTable deals={deals} total={detail.data.deals_total} empty={t("contacts.noDeals")} />
      </section>

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />

      <Timeline items={activities} total={detail.data.activities_total} nextCursor={detail.data.activities_next_cursor} conversations={detail.data.conversations ?? []} target={{ contact_id: contact.id }} onChange={() => detail.mutate()} />
      <div className="pb-8" />
    </>
  )
}

function EditContact({
  contact,
  onSaved,
}: {
  contact: { id: string; name: string; phone: string; title: string; company: { id: string; name: string } | null; owner_user_id: string | null }
  onSaved: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const members = useMemberNames(ws.id)
  const [name, setName] = useState(contact.name)
  const [phone, setPhone] = useState(contact.phone)
  const [title, setTitle] = useState(contact.title)
  const [companyId, setCompanyId] = useState(contact.company?.id ?? "")
  const [ownerId, setOwnerId] = useState(contact.owner_user_id ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)


  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/contacts/${encodeURIComponent(contact.id)}`, {
        method: "PATCH",
        json: { name: name.trim(), phone: phone.trim(), title: title.trim(), company_id: companyId, owner_user_id: ownerId },
        workspaceId: ws.id,
      })
      toast.success(t("contacts.saved"))
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="px-6 pb-6" data-form="edit-contact">
      <FieldGroup className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Field>
          <FieldLabel htmlFor="edit-contact-name">{t("contacts.name")}</FieldLabel>
          <Input id="edit-contact-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={120} />
        </Field>
        <Field>
          <FieldLabel htmlFor="edit-contact-title">{t("contacts.jobTitle")}</FieldLabel>
          <Input id="edit-contact-title" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={120} />
        </Field>
        <Field>
          <FieldLabel htmlFor="edit-contact-phone">{t("contacts.phone")}</FieldLabel>
          <Input id="edit-contact-phone" dir="ltr" value={phone} onChange={(e) => setPhone(e.target.value)} maxLength={40} />
        </Field>
        <Field>
          <FieldLabel htmlFor="edit-contact-company">{t("companies.title")}</FieldLabel>
          <CompanyPicker id="edit-contact-company" value={companyId} selected={contact.company} onChange={setCompanyId} />
        </Field>
        <Field>
          <FieldLabel htmlFor="edit-contact-owner">{t("crm.owner")}</FieldLabel>
          <PickSelect id="edit-contact-owner" value={ownerId} onChange={setOwnerId} options={members.options} noneLabel={t("crm.unassigned")} />
        </Field>
      </FieldGroup>
      <div className="mt-4 flex items-center gap-3">
        <Button type="submit" disabled={busy}>
          {busy ? t("common.working") : t("common.save")}
        </Button>
        {err ? <FieldError>{err}</FieldError> : null}
      </div>
    </form>
  )
}

const NO_STAGE = "__none"

/**
 * Where the contact is in the lifecycle (MH-395), how it got there, and how well it fits the
 * workspace's ideal customer — the score with every reason behind it.
 */
function ContactFit({ contact, canWrite, onSaved }: { contact: ContactDetail; canWrite: boolean; onSaved: () => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const members = useMemberNames(ws.id)
  const [busy, setBusy] = useState(false)
  const history = contact.lifecycle_history ?? []
  const when = dateFormat(locale, { dateStyle: "medium", timeStyle: "short" })
  const stageOptions = [...LIFECYCLE_STAGES.map((st) => ({ value: st, label: t(`lifecycle.${st}`) }))]

  async function setStage(next: Lifecycle) {
    setBusy(true)
    try {
      await api(`/api/w/contacts/${encodeURIComponent(contact.id)}`, { method: "PATCH", json: { lifecycle: next }, workspaceId: ws.id })
      toast.success(t("lifecycle.saved"))
      onSaved()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="grid gap-4 px-6 pb-6 lg:grid-cols-2" data-contact-fit>
      <div className="border border-line bg-grid-card" data-lifecycle-panel>
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-5 py-4">
          <h2 className="text-base font-medium">{t("lifecycle.label")}</h2>
          <LifecycleBadge stage={contact.lifecycle} />
          {canWrite ? (
            <div className="ms-auto w-44">
              <PickSelect
                id="contact-lifecycle"
                value={contact.lifecycle || NO_STAGE}
                onChange={(v) => void setStage((v === NO_STAGE ? "" : v) as Lifecycle)}
                options={stageOptions}
                noneLabel={t("lifecycle.none")}
                disabled={busy}
              />
            </div>
          ) : null}
        </div>
        <p className="px-5 pt-3 text-xs text-grid-muted">{t("lifecycle.hint")}</p>
        {history.length === 0 ? (
          <p className="px-5 py-4 text-sm text-grid-muted">{t("lifecycle.noHistory")}</p>
        ) : (
          <ol className="divide-y divide-line px-5 pb-2" data-lifecycle-history>
            {history.map((e) => (
              <li key={e.id} className="flex flex-wrap items-center gap-x-2 gap-y-1 py-2.5 text-sm" data-lifecycle-event={e.source}>
                <span className="inline-flex items-center gap-1.5">
                  {e.from ? <LifecycleBadge stage={e.from} /> : <span className="text-grid-muted">{t("lifecycle.none")}</span>}
                  <span className="text-grid-muted rtl:-scale-x-100" aria-hidden>
                    →
                  </span>
                  <LifecycleBadge stage={e.to} />
                </span>
                <span className="text-grid-muted">
                  {e.source === "manual" ? t("lifecycle.source.manualBy", { who: members.name(e.actor_user_id) }) : t(`lifecycle.source.${e.source}`)}
                </span>
                <span className="ms-auto text-xs text-grid-muted">
                  <Ltr>{when.format(new Date(e.at))}</Ltr>
                </span>
              </li>
            ))}
            {contact.lifecycle_history_total > history.length ? (
              <li className="py-2 text-xs text-grid-muted">{t("lifecycle.more", { n: contact.lifecycle_history_total - history.length })}</li>
            ) : null}
          </ol>
        )}
      </div>

      <div className="border border-line bg-grid-card" data-score-panel>
        <div className="flex flex-wrap items-center gap-3 border-b border-line px-5 py-4">
          <h2 className="text-base font-medium">{t("icp.fit")}</h2>
          <ScoreMeter score={contact.score} />
          <Link className="ms-auto text-xs text-grid-muted hover:text-grid-fg hover:underline" href={`/${locale}/w/${ws.slug}/settings/icp`}>
            {t("icp.editProfile")}
          </Link>
        </div>
        <p className="px-5 pt-3 text-xs text-grid-muted">{t("icp.scoreHow")}</p>
        <div className="px-5 pb-2">
          <ScoreReasons reasons={contact.score_reasons ?? []} />
        </div>
      </div>
    </section>
  )
}
