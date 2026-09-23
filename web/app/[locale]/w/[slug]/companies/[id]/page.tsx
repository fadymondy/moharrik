"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { DealsTable } from "@/components/crm/deals"
import { CompanyEditFields } from "@/components/crm/shared"
import { Timeline } from "@/components/crm/timeline"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { NotFoundState } from "@/components/not-found-state"
import { SectionHeader } from "@/components/public-frame"
import { ErrorState, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldLabel } from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCompany } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

export default function CompanyDetailPage() {
  const { id } = useParams<{ id: string }>()
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  const detail = useCompany(ws.id, id)
  useDocumentTitle(detail.data ? `${detail.data.company.name} · ${ws.summary.name}` : t("companies.title"))
  const base = `/${locale}/w/${ws.slug}`

  if (detail.error) {
    const notFound = detail.error instanceof ApiError && detail.error.status === 404
    if (notFound) return <NotFoundState title={t("companies.notFound")} body={t("companies.notFoundBody")} backHref={`${base}/companies`} backLabel={t("companies.backToList")} />
    return <ErrorState error={detail.error} />
  }
  if (!detail.data) return <LoadingRows rows={6} />

  const { company, contacts, deals, activities } = detail.data

  return (
    <>
      <SectionHeader
        micro={t("nav.companies")}
        title={<Bdi>{company.name}</Bdi>}
        description={company.domain ? <Ltr>{company.domain}</Ltr> : undefined}
        action={
          canWrite ? (
            <ConfirmButton
              label={t("common.delete")}
              title={t("companies.deleteTitle")}
              description={t("companies.deleteBody", { name: isolate(company.name) })}
              confirmLabel={t("common.delete")}
              onConfirm={async () => {
                await api(`/api/w/companies/${encodeURIComponent(company.id)}`, { method: "DELETE", workspaceId: ws.id })
                window.location.href = `${base}/companies`
              }}
            />
          ) : undefined
        }
      />

      {canWrite ? <EditCompany company={company} onSaved={() => detail.mutate()} /> : null}

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />

      <section>
        <h2 className="px-6 pb-4 text-base font-medium">{t("companies.people", { count: contacts.length })}</h2>
        {contacts.length === 0 ? (
          <p className="px-6 pb-6 text-sm text-grid-muted">{t("companies.noPeople")}</p>
        ) : (
          <ul className="mb-6 divide-y divide-line border-y border-line">
            {contacts.map((c) => (
              <li key={c.id} className="px-6 py-3 text-sm">
                <Link className="font-medium underline decoration-grid-muted underline-offset-4 hover:decoration-current" href={`${base}/contacts/${c.id}`} data-person-link>
                  {c.name ? <Bdi>{c.name}</Bdi> : <Ltr>{c.email}</Ltr>}
                </Link>
                {c.title ? (
                  <span className="text-grid-muted">
                    {" · "}
                    <Bdi>{c.title}</Bdi>
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>

      <div className="grid-separator-contained hatch-band" aria-hidden />

      <section>
        <h2 className="px-6 py-4 text-base font-medium">{t("nav.deals")}</h2>
        <DealsTable deals={deals} total={detail.data.deals_total} empty={t("companies.noDeals")} />
      </section>

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />

      <Timeline items={activities} total={detail.data.activities_total} nextCursor={detail.data.activities_next_cursor} target={{ company_id: company.id }} onChange={() => detail.mutate()} />
      <div className="pb-8" />
    </>
  )
}

function EditCompany({ company, onSaved }: { company: { id: string; name: string; domain: string; industry: string; size: string; notes: string }; onSaved: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState(company.name)
  const [domain, setDomain] = useState(company.domain)
  const [industry, setIndustry] = useState(company.industry)
  const [size, setSize] = useState(company.size)
  const [notes, setNotes] = useState(company.notes)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/companies/${encodeURIComponent(company.id)}`, {
        method: "PATCH",
        json: { name: name.trim(), domain: domain.trim(), industry: industry.trim(), size, notes: notes.trim() },
        workspaceId: ws.id,
      })
      toast.success(t("companies.saved"))
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="px-6 pb-6" data-form="edit-company">
      <CompanyEditFields
        idPrefix="edit-company"
        name={name}
        setName={setName}
        domain={domain}
        setDomain={setDomain}
        industry={industry}
        setIndustry={setIndustry}
        size={size}
        setSize={setSize}
      />
      <Field className="mt-4">
        <FieldLabel htmlFor="edit-company-notes">{t("companies.notes")}</FieldLabel>
        <Textarea id="edit-company-notes" value={notes} onChange={(e) => setNotes(e.target.value)} maxLength={4000} rows={3} />
      </Field>
      <div className="mt-4 flex items-center gap-3">
        <Button type="submit" disabled={busy || !name.trim()}>
          {busy ? t("common.working") : t("common.save")}
        </Button>
        {err ? <FieldError>{err}</FieldError> : null}
      </div>
    </form>
  )
}
