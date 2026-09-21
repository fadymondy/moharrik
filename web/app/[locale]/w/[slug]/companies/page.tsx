"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { useMemberNames } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Company } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCompanies } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function CompaniesPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.companies")} · ${ws.summary.name}`)
  const canWrite = ws.can("contacts:write")
  const companies = useCompanies(ws.id)
  const items = companies.data?.companies ?? []

  return (
    <>
      <SectionHeader micro={ws.summary.name} title={t("nav.companies")} description={t("companies.hint")} />

      {companies.error ? (
        <ErrorState error={companies.error} />
      ) : companies.isLoading ? (
        <LoadingRows />
      ) : items.length === 0 ? (
        <EmptyState title={t("companies.empty")} body={canWrite ? t("companies.emptyBody") : undefined} />
      ) : (
        <CompaniesTable items={items} canWrite={canWrite} onChange={() => companies.mutate()} />
      )}

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />

      {canWrite ? (
        <AddCompany onAdded={() => companies.mutate()} />
      ) : (
        <p className="px-6 py-6 text-sm text-grid-muted">{t("companies.readOnly")}</p>
      )}
      <div className="pb-8" />
    </>
  )
}

function AddCompany({ onAdded }: { onAdded: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [domain, setDomain] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/companies", { json: { name: name.trim(), domain: domain.trim() }, workspaceId: ws.id })
      toast.success(t("companies.created"))
      setName("")
      setDomain("")
      onAdded()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="px-6 py-6" data-form="add-company">
      <h2 className="text-base font-medium">{t("companies.addTitle")}</h2>
      <form onSubmit={submit} noValidate className="mt-5">
        <FieldGroup className="sm:flex-row sm:items-end">
          <Field className="sm:flex-1">
            <FieldLabel htmlFor="company-name">{t("companies.name")}</FieldLabel>
            <Input id="company-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={200} />
          </Field>
          <Field className="sm:flex-1">
            <FieldLabel htmlFor="company-domain">
              {t("companies.domain")} <span className="text-grid-muted">({t("common.optional")})</span>
            </FieldLabel>
            <Input id="company-domain" dir="ltr" value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="company.com" />
          </Field>
          <Button type="submit" disabled={busy || !name.trim()}>
            {busy ? t("common.working") : t("companies.create")}
          </Button>
        </FieldGroup>
        {err ? <FieldError className="mt-3">{err}</FieldError> : null}
      </form>
    </section>
  )
}

function CompaniesTable({ items, canWrite, onChange }: { items: Company[]; canWrite: boolean; onChange: () => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const members = useMemberNames(ws.id)
  const base = `/${locale}/w/${ws.slug}`

  async function remove(id: string) {
    try {
      await api(`/api/w/companies/${encodeURIComponent(id)}`, { method: "DELETE", workspaceId: ws.id })
      toast.success(t("companies.deleted"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <div className="border-y border-line">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="ps-6">{t("companies.name")}</TableHead>
            <TableHead>{t("companies.domain")}</TableHead>
            <TableHead>{t("companies.contacts")}</TableHead>
            <TableHead>{t("companies.openDeals")}</TableHead>
            <TableHead>{t("crm.owner")}</TableHead>
            <TableHead className="pe-6 text-end">
              <span className="sr-only">{t("common.actions")}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((c) => (
            <TableRow key={c.id} data-company-row={c.name}>
              <TableCell className="ps-6">
                <Link className="font-medium hover:underline" href={`${base}/companies/${c.id}`}>
                  {c.name}
                </Link>
                {c.industry ? <div className="text-xs text-grid-muted">{c.industry}</div> : null}
              </TableCell>
              <TableCell className="text-grid-muted">{c.domain ? <Ltr>{c.domain}</Ltr> : "—"}</TableCell>
              <TableCell>{c.contact_count}</TableCell>
              <TableCell>{c.open_deals}</TableCell>
              <TableCell className="text-grid-muted">
                <Ltr>{members.name(c.owner_user_id)}</Ltr>
              </TableCell>
              <TableCell className="pe-6 text-end">
                {canWrite ? (
                  <ConfirmButton
                    label={t("common.delete")}
                    title={t("companies.deleteTitle")}
                    description={t("companies.deleteBody", { name: c.name })}
                    confirmLabel={t("common.delete")}
                    onConfirm={() => remove(c.id)}
                  />
                ) : null}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

