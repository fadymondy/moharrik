"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { useState, type ReactNode } from "react"
import { ArrowRightLeftIcon, Building2Icon, HandshakeIcon, UserIcon } from "lucide-react"
import { toast } from "sonner"

import { PickSelect } from "@/components/crm/shared"
import { Ltr } from "@/components/copy-field"
import { LeadStatusBadge, leadSource } from "@/components/leads/shared"
import { NotFoundState } from "@/components/not-found-state"
import { SectionHeader } from "@/components/public-frame"
import { ErrorState, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Lead, type LeadConversion, type LeadStatus } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"
import { useLead } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, BdiBlock, isolate } from "@/components/bidi"

export default function LeadDetailPage() {
  const { id } = useParams<{ id: string }>()
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const detail = useLead(ws.id, id)
  const lead = detail.data?.lead
  useDocumentTitle(lead ? `${lead.name || lead.email} · ${t("nav.leads")} · ${ws.summary.name}` : t("leads.detailTitle"))
  const base = `/${locale}/w/${ws.slug}`

  if (detail.error) {
    const notFound = detail.error instanceof ApiError && detail.error.status === 404
    if (notFound) return <NotFoundState title={t("leads.notFound")} body={t("leads.notFoundBody")} backHref={`${base}/leads`} backLabel={t("leads.backToList")} />
    return <ErrorState error={detail.error} />
  }
  if (!lead) return <LoadingRows rows={6} />

  return (
    <>
      <SectionHeader
        micro={
          <Link href={`${base}/leads`} className="hover:underline">
            {t("nav.leads")}
          </Link>
        }
        title={<bdi>{lead.name || lead.email}</bdi>}
        description={
          <span className="inline-flex flex-wrap items-center gap-2">
            <LeadStatusBadge status={lead.status} />
            {lead.name ? <Ltr>{lead.email}</Ltr> : null}
          </span>
        }
        action={<LeadActions lead={lead} onChange={() => detail.mutate()} />}
      />

      {lead.status === "converted" ? <Converted lead={lead} base={base} /> : null}

      <section className="border-t border-line">
        <h2 className="px-6 pt-5 pb-2 text-base font-medium">{t("leads.message")}</h2>
        <p className="px-6 pb-6 text-sm leading-6 break-words whitespace-pre-wrap text-grid-fg" data-lead-message dir="auto">
          {lead.message}
        </p>
      </section>

      <div className="grid border-t border-line md:grid-cols-2">
        <section className="border-b border-line md:border-e md:border-b-0">
          <h2 className="px-6 pt-5 pb-2 text-base font-medium">{t("leads.details")}</h2>
          <Facts
            rows={[
              [t("leads.field.email"), <Ltr key="e">{lead.email}</Ltr>],
              [t("leads.field.phone"), lead.phone ? <Ltr key="p">{lead.phone}</Ltr> : null],
              [t("leads.field.company"), lead.company ? <Bdi key="c">{lead.company}</Bdi> : null],
              [t("leads.field.budget"), lead.budget ? <Bdi key="b">{lead.budget}</Bdi> : null],
              [t("leads.field.form"), lead.form_name ? <Bdi key="f">{lead.form_name}</Bdi> : null],
              [t("leads.field.site"), lead.origin ? <Ltr key="o">{lead.origin}</Ltr> : null],
              [t("leads.field.received"), <Ltr key="r">{dateFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(lead.created_at))}</Ltr>],
            ]}
          />
        </section>
        <section data-lead-attribution>
          <h2 className="px-6 pt-5 pb-2 text-base font-medium">{t("leads.attribution")}</h2>
          <Attribution lead={lead} />
        </section>
      </div>
      <div className="border-t border-line pb-8" />
    </>
  )
}

/** The status and Convert. Converting needs contacts:write too — it creates CRM rows. */
function LeadActions({ lead, onChange }: { lead: Lead; onChange: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("leads:write")
  const canConvert = canWrite && ws.can("contacts:write")
  const [busy, setBusy] = useState<"status" | "convert" | null>(null)

  if (!canWrite || lead.status === "converted") return null

  async function setStatus(status: string) {
    if (!status || status === lead.status) return
    setBusy("status")
    try {
      await api(`/api/w/leads/${encodeURIComponent(lead.id)}`, { method: "PATCH", json: { status }, workspaceId: ws.id })
      toast.success(t("leads.statusSaved", { status: t(`leads.status.${status}`) }))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(null)
    }
  }

  async function convert() {
    setBusy("convert")
    try {
      const c = await api<LeadConversion>(`/api/w/leads/${encodeURIComponent(lead.id)}/convert`, { method: "POST", workspaceId: ws.id })
      toast.success(t("leads.converted"), {
        description: c.free_mail ? t("leads.convertedFreeMail") : c.company_id ? t("leads.convertedWithCompany", { company: isolate(c.lead.converted_company?.name ?? "") }) : undefined,
      })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(null)
    }
  }

  const opts = (["new", "contacted", "qualified", "spam"] as LeadStatus[]).map((s) => ({ value: s, label: t(`leads.status.${s}`) }))
  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="w-40">
        <label htmlFor="lead-status" className="sr-only">
          {t("leads.statusLabel")}
        </label>
        <PickSelect id="lead-status" value={lead.status} onChange={setStatus} options={opts} disabled={busy !== null} />
      </div>
      {canConvert ? (
        lead.status === "spam" ? (
          <p className="text-xs text-grid-muted">{t("leads.spamNoConvert")}</p>
        ) : (
          <Button onClick={convert} disabled={busy !== null} data-convert-lead>
            <ArrowRightLeftIcon />
            {busy === "convert" ? t("common.working") : t("leads.convert")}
          </Button>
        )
      ) : null}
    </div>
  )
}

function Converted({ lead, base }: { lead: Lead; base: string }) {
  const { t, formatDate } = useTranslations()
  const link = (href: string, icon: ReactNode, label: string, name: string, attr: string) => (
    <Link href={href} className="flex min-w-0 items-center gap-2.5 px-6 py-4 transition-colors hover:bg-grid-soft [&_svg]:size-4 [&_svg]:shrink-0" data-converted={attr}>
      {icon}
      <span className="min-w-0">
        <span className="block text-xs text-grid-muted">{label}</span>
        <BdiBlock className="block truncate text-sm font-medium">{name}</BdiBlock>
      </span>
    </Link>
  )
  return (
    <section className="border-t border-line bg-grid-card" data-lead-converted>
      <p className="px-6 pt-4 text-sm text-grid-body">{t("leads.convertedOn", { date: formatDate(lead.converted_at) })}</p>
      <div className="grid sm:grid-cols-3">
        {lead.converted_contact
          ? link(`${base}/contacts/${lead.converted_contact.id}`, <UserIcon />, t("leads.contact"), lead.converted_contact.name, "contact")
          : null}
        {lead.converted_company ? (
          link(`${base}/companies/${lead.converted_company.id}`, <Building2Icon />, t("leads.company"), lead.converted_company.name, "company")
        ) : (
          <div className="flex items-center gap-2.5 px-6 py-4 text-grid-muted [&_svg]:size-4 [&_svg]:shrink-0" data-converted="no-company">
            <Building2Icon />
            <span className="min-w-0">
              <span className="block text-xs">{t("leads.company")}</span>
              <span className="block text-sm">{t("leads.noCompany")}</span>
            </span>
          </div>
        )}
        {lead.converted_deal ? link(`${base}/deals/${lead.converted_deal.id}`, <HandshakeIcon />, t("leads.deal"), lead.converted_deal.name, "deal") : null}
      </div>
    </section>
  )
}

function Facts({ rows }: { rows: [string, ReactNode | null][] }) {
  return (
    <dl className="grid grid-cols-[minmax(7rem,auto)_minmax(0,1fr)] gap-x-4 gap-y-2 px-6 pb-6 text-sm">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-grid-muted">{k}</dt>
          <dd className="min-w-0 break-words">{v ?? <span className="text-grid-muted">—</span>}</dd>
        </div>
      ))}
    </dl>
  )
}

function Attribution({ lead }: { lead: Lead }) {
  const { t } = useTranslations()
  const a = lead.attribution
  const any = Object.values(a).some((v) => v)
  if (!any) return <p className="px-6 pb-6 text-sm text-grid-muted">{t("leads.noAttribution")}</p>
  const url = (v: string) =>
    v ? (
      <span dir="ltr" className="ltr-isolate block truncate rtl:text-right" title={v}>
        {/^https?:\/\//i.test(v) ? (
          <a href={v} target="_blank" rel="noopener noreferrer nofollow" className="underline-offset-2 hover:underline">
            {v}
          </a>
        ) : (
          v
        )}
      </span>
    ) : null
  const code = (v: string) => (v ? <Ltr mono className="text-xs">{v}</Ltr> : null)
  return (
    <Facts
      rows={[
        [t("leads.field.source"), leadSource(lead, t)],
        ["utm_source", code(a.utm_source)],
        ["utm_medium", code(a.utm_medium)],
        ["utm_campaign", code(a.utm_campaign)],
        ["utm_term", code(a.utm_term)],
        ["utm_content", code(a.utm_content)],
        ["gclid", code(a.gclid)],
        [t("leads.field.referrer"), url(a.referrer)],
        [t("leads.field.landing"), url(a.landing_page)],
      ]}
    />
  )
}
