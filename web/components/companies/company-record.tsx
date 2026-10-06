"use client"

// MH-1157: the company's record frame, after Mahaam's project page: cover, logo tile, name and
// actions, key facts, and tabs. The layout loads the company once; every tab reads it from here.
import Link from "next/link"
import { useParams, usePathname, useRouter } from "next/navigation"
import { createContext, useContext, useState, type FormEvent, type ReactNode } from "react"
import {
  ActivityIcon,
  BrainIcon,
  Building2Icon,
  CalendarIcon,
  FactoryIcon,
  GlobeIcon,
  HandshakeIcon,
  LayoutGridIcon,
  MapPinIcon,
  PencilIcon,
  StickyNoteIcon,
  UsersIcon,
} from "lucide-react"
import { Button, Field, FieldLabel, Input, Textarea, toast } from "@fadymondy/nasaq/web"

import { Bdi, isolate } from "@/components/bidi"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { ScoreMeter, ScoreReasons } from "@/components/crm/icp"
import { CompanyEditFields } from "@/components/crm/shared"
import { FieldError } from "@/components/form-layout"
import { NotFoundState } from "@/components/not-found-state"
import {
  RecordAvatar,
  RecordChip,
  RecordHeader,
  RecordSkeleton,
  type RecordTab,
} from "@/components/record/record"
import { ErrorState } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Company } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCompany } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

type CompanyData = NonNullable<ReturnType<typeof useCompany>["data"]>

export type CompanyRecordValue = {
  data: CompanyData
  company: Company
  canWrite: boolean
  base: string
  rec: string
  reload: () => void
}

const Ctx = createContext<CompanyRecordValue | null>(null)

export function useCompanyRecord(): CompanyRecordValue {
  const v = useContext(Ctx)
  if (!v) throw new Error("useCompanyRecord outside CompanyRecord")
  return v
}

export function CompanyRecord({ children }: { children: ReactNode }) {
  const { id } = useParams<{ id: string }>()
  const pathname = usePathname()
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const canWrite = ws.can("contacts:write")
  const detail = useCompany(ws.id, id)
  const base = `/${locale}/w/${ws.slug}`
  const rec = `${base}/companies/${id}`
  const company = detail.data?.company
  useDocumentTitle(company ? `${company.name} · ${ws.summary.name}` : t("companies.title"))

  if (detail.error) {
    const notFound = detail.error instanceof ApiError && detail.error.status === 404
    if (notFound)
      return (
        <NotFoundState
          title={t("companies.notFound")}
          body={t("companies.notFoundBody")}
          backHref={`${base}/companies`}
          backLabel={t("companies.backToList")}
        />
      )
    return <ErrorState error={detail.error} />
  }
  if (!detail.data || !company) return <RecordSkeleton />

  const data: CompanyData = detail.data
  const active = pathname.split(`/companies/${id}`)[1]?.split("/")[1] || "overview"
  const tabs: RecordTab[] = [
    { value: "overview", label: t("record.tab.overview"), icon: LayoutGridIcon, href: rec },
    {
      value: "people",
      label: t("record.tab.people"),
      icon: UsersIcon,
      href: `${rec}/people`,
      count: data.contacts.length || undefined,
    },
    {
      value: "deals",
      label: t("record.tab.deals"),
      icon: HandshakeIcon,
      href: `${rec}/deals`,
      count: data.deals_total || undefined,
    },
    {
      value: "activity",
      label: t("record.tab.activity"),
      icon: ActivityIcon,
      href: `${rec}/activity`,
      count: data.activities_total || undefined,
    },
    {
      value: "context",
      label: t("record.tab.context"),
      icon: BrainIcon,
      href: `${rec}/context`,
    },
    { value: "details", label: t("record.tab.details"), icon: PencilIcon, href: `${rec}/details` },
  ]

  return (
    <Ctx.Provider
      value={{ data, company, canWrite, base, rec, reload: () => void detail.mutate() }}
    >
      <div data-company-record={company.id}>
        <RecordHeader
          back={{ href: `${base}/companies`, label: t("nav.companies") }}
          avatar={<RecordAvatar name={company.name} icon={<Building2Icon />} />}
          title={<Bdi>{company.name}</Bdi>}
          subtitle={
            company.domain ? (
              <a
                href={`https://${company.domain}`}
                target="_blank"
                rel="noopener noreferrer nofollow"
                className="text-primary hover:underline"
                data-record-domain
              >
                <Ltr>{company.domain}</Ltr>
              </a>
            ) : null
          }
          description={company.notes ? <Bdi>{company.notes}</Bdi> : null}
          actions={
            canWrite ? (
              <>
                <Button
                  size="sm"
                  nativeButton={false}
                  render={<Link href={`${rec}/activity?focus=activity-body`} />}
                  data-action="record-log-activity"
                >
                  <StickyNoteIcon />
                  {t("c360.actions.logActivity")}
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  nativeButton={false}
                  render={<Link href={`${rec}/details`} />}
                  data-action="record-edit"
                >
                  <PencilIcon />
                  {t("common.edit")}
                </Button>
                <ConfirmButton
                  label={t("common.delete")}
                  title={t("companies.deleteTitle")}
                  description={t("companies.deleteBody", { name: isolate(company.name) })}
                  confirmLabel={t("common.delete")}
                  onConfirm={async () => {
                    await api(`/api/w/companies/${encodeURIComponent(company.id)}`, {
                      method: "DELETE",
                      workspaceId: ws.id,
                    })
                    router.push(`${base}/companies`)
                  }}
                />
              </>
            ) : null
          }
          chips={
            <>
              <RecordChip icon={<UsersIcon />}>
                {t("companies.people", { count: data.contacts.length })}
              </RecordChip>
              {company.industry ? (
                <RecordChip icon={<FactoryIcon />}>
                  <Bdi>{company.industry}</Bdi>
                </RecordChip>
              ) : null}
              {company.size ? (
                <RecordChip icon={<Building2Icon />}>
                  {t("companies.size")} · <Ltr>{company.size}</Ltr>
                </RecordChip>
              ) : null}
              {company.country ? (
                <RecordChip icon={<MapPinIcon />}>
                  <Bdi>{company.country}</Bdi>
                </RecordChip>
              ) : null}
              {company.domain ? (
                <RecordChip icon={<GlobeIcon />}>
                  <Ltr>{company.domain}</Ltr>
                </RecordChip>
              ) : null}
            </>
          }
          meta={
            <span className="inline-flex items-center gap-1.5">
              <CalendarIcon className="size-3.5" />
              {t("record.since", { when: formatDate(company.created_at) })}
            </span>
          }
          tabs={tabs}
          active={active}
        />
        {children}
      </div>
    </Ctx.Provider>
  )
}

/** The company's ideal-customer fit and every reason behind it. */
export function CompanyScore({ company, base }: { company: Company; base: string }) {
  const { t } = useTranslations()
  return (
    <section className="overflow-hidden rounded-xl border border-nq-line bg-card" data-score-panel>
      <div className="flex flex-wrap items-center gap-3 border-b border-nq-line px-5 py-4">
        <h2 className="text-base font-medium">{t("icp.fit")}</h2>
        <ScoreMeter score={company.score} />
        <Link
          className="ms-auto text-xs text-muted-foreground hover:text-foreground hover:underline"
          href={`${base}/settings/icp`}
        >
          {t("icp.editProfile")}
        </Link>
      </div>
      <p className="px-5 pt-3 text-xs text-muted-foreground">{t("icp.companyHow")}</p>
      <div className="px-5 pb-2">
        <ScoreReasons reasons={company.score_reasons ?? []} />
      </div>
    </section>
  )
}

export function EditCompany({ company, onSaved }: { company: Company; onSaved: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState(company.name)
  const [domain, setDomain] = useState(company.domain)
  const [industry, setIndustry] = useState(company.industry)
  const [size, setSize] = useState<string>(company.size)
  const [country, setCountry] = useState(company.country ?? "")
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
        json: {
          name: name.trim(),
          domain: domain.trim(),
          industry: industry.trim(),
          size,
          country: country.trim(),
          notes: notes.trim(),
        },
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
    <form onSubmit={submit} noValidate data-form="edit-company">
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
      <Field className="mt-4 sm:max-w-xs">
        <FieldLabel htmlFor="edit-company-country">{t("companies.country")}</FieldLabel>
        <Input
          id="edit-company-country"
          value={country}
          onChange={(e) => setCountry(e.target.value)}
          maxLength={80}
          placeholder={t("companies.countryPlaceholder")}
        />
      </Field>
      <Field className="mt-4">
        <FieldLabel htmlFor="edit-company-notes">{t("companies.notes")}</FieldLabel>
        <Textarea
          id="edit-company-notes"
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          maxLength={4000}
          rows={3}
        />
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

/** The people at the company, each a link to their record. */
export function CompanyPeople({
  contacts,
  base,
  limit,
}: {
  contacts: CompanyData["contacts"]
  base: string
  limit?: number
}) {
  const { t } = useTranslations()
  const rows = limit ? contacts.slice(0, limit) : contacts
  if (contacts.length === 0)
    return <p className="px-4 pb-4 text-sm text-muted-foreground">{t("companies.noPeople")}</p>
  return (
    <ul className="divide-y divide-nq-line border-t border-nq-line">
      {rows.map((c) => (
        <li key={c.id}>
          <Link
            className="flex min-w-0 items-center gap-3 px-4 py-3 text-sm transition-colors hover:bg-nq-surface-soft"
            href={`${base}/contacts/${c.id}`}
            data-person-link
          >
            <RecordAvatar
              name={c.name || c.email}
              className="size-8 rounded-full text-xs shadow-none ring-0 sm:size-8 sm:text-xs"
            />
            <span className="min-w-0 flex-1">
              <span className="block truncate font-medium">
                {c.name ? <Bdi>{c.name}</Bdi> : <Ltr>{c.email}</Ltr>}
              </span>
              {c.title || c.name ? (
                <span className="block truncate text-xs text-muted-foreground">
                  {c.title ? <Bdi>{c.title}</Bdi> : <Ltr>{c.email}</Ltr>}
                </span>
              ) : null}
            </span>
          </Link>
        </li>
      ))}
    </ul>
  )
}
