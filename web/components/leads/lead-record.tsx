"use client"

// MH-1157: the lead's record frame, after Mahaam's project page: cover, avatar, name, the status
// and Convert, key facts, and tabs. The layout loads the lead once; every tab reads it from here.
import Link from "next/link"
import { useParams, usePathname } from "next/navigation"
import { createContext, useContext, useState, type ReactNode } from "react"
import {
  ArrowRightLeftIcon,
  Building2Icon,
  ClockIcon,
  FileTextIcon,
  HandshakeIcon,
  LayoutGridIcon,
  RouteIcon,
  UserIcon,
  WalletIcon,
} from "lucide-react"
import { Button, toast } from "@fadymondy/nasaq/web"

import { Bdi, BdiBlock, isolate } from "@/components/bidi"
import { Ltr } from "@/components/copy-field"
import { PickSelect } from "@/components/crm/shared"
import { LeadStatusBadge, leadSource } from "@/components/leads/shared"
import { NotFoundState } from "@/components/not-found-state"
import { useFormat } from "@/components/overview/overview-cards"
import {
  RecordAvatar,
  RecordChip,
  RecordHeader,
  RecordSkeleton,
  type RecordTab,
} from "@/components/record/record"
import { ErrorState } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Lead, type LeadConversion, type LeadStatus } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useLead } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export type LeadRecordValue = {
  lead: Lead
  base: string
  rec: string
  reload: () => void
}

const Ctx = createContext<LeadRecordValue | null>(null)

export function useLeadRecord(): LeadRecordValue {
  const v = useContext(Ctx)
  if (!v) throw new Error("useLeadRecord outside LeadRecord")
  return v
}

export function LeadRecord({ children }: { children: ReactNode }) {
  const { id } = useParams<{ id: string }>()
  const pathname = usePathname()
  const { t, locale } = useTranslations()
  const f = useFormat()
  const ws = useWorkspace()
  const detail = useLead(ws.id, id)
  const lead = detail.data?.lead
  const base = `/${locale}/w/${ws.slug}`
  const rec = `${base}/leads/${id}`
  useDocumentTitle(
    lead
      ? `${lead.name || lead.email} · ${t("nav.leads")} · ${ws.summary.name}`
      : t("leads.detailTitle"),
  )

  if (detail.error) {
    const notFound = detail.error instanceof ApiError && detail.error.status === 404
    if (notFound)
      return (
        <NotFoundState
          title={t("leads.notFound")}
          body={t("leads.notFoundBody")}
          backHref={`${base}/leads`}
          backLabel={t("leads.backToList")}
        />
      )
    return <ErrorState error={detail.error} />
  }
  if (!lead) return <RecordSkeleton />

  const reload = () => void detail.mutate()
  const active = pathname.split(`/leads/${id}`)[1]?.split("/")[1] || "overview"
  const tabs: RecordTab[] = [
    { value: "overview", label: t("record.tab.overview"), icon: LayoutGridIcon, href: rec },
    {
      value: "attribution",
      label: t("leads.attribution"),
      icon: RouteIcon,
      href: `${rec}/attribution`,
    },
  ]

  return (
    <Ctx.Provider value={{ lead, base, rec, reload }}>
      <div data-lead-record={lead.id}>
        <RecordHeader
          back={{ href: `${base}/leads`, label: t("nav.leads") }}
          avatar={<RecordAvatar name={lead.name || lead.email} />}
          status={<LeadStatusBadge status={lead.status} />}
          title={lead.name ? <Bdi>{lead.name}</Bdi> : <Ltr>{lead.email}</Ltr>}
          subtitle={
            <span className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
              {lead.name ? <Ltr>{lead.email}</Ltr> : null}
              {lead.phone ? <Ltr>{lead.phone}</Ltr> : null}
            </span>
          }
          actions={<LeadActions lead={lead} onChange={reload} />}
          chips={
            <>
              {lead.company ? (
                <RecordChip icon={<Building2Icon />}>
                  <Bdi>{lead.company}</Bdi>
                </RecordChip>
              ) : null}
              {lead.budget ? (
                <RecordChip icon={<WalletIcon />}>
                  <Bdi>{lead.budget}</Bdi>
                </RecordChip>
              ) : null}
              {lead.form_name ? (
                <RecordChip icon={<FileTextIcon />}>
                  <Bdi>{lead.form_name}</Bdi>
                </RecordChip>
              ) : null}
              <RecordChip icon={<RouteIcon />}>{leadSource(lead, t)}</RecordChip>
            </>
          }
          meta={
            <span className="inline-flex items-center gap-1.5" title={f.exact(lead.updated_at)}>
              <ClockIcon className="size-3.5" />
              {t("record.updated", { when: f.ago(lead.updated_at) })}
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

/** The status and Convert. Converting needs contacts:write too — it creates CRM rows. */
export function LeadActions({ lead, onChange }: { lead: Lead; onChange: () => void }) {
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
      await api(`/api/w/leads/${encodeURIComponent(lead.id)}`, {
        method: "PATCH",
        json: { status },
        workspaceId: ws.id,
      })
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
      const c = await api<LeadConversion>(`/api/w/leads/${encodeURIComponent(lead.id)}/convert`, {
        method: "POST",
        workspaceId: ws.id,
      })
      toast.success(t("leads.converted"), {
        description: c.free_mail
          ? t("leads.convertedFreeMail")
          : c.company_id
            ? t("leads.convertedWithCompany", {
                company: isolate(c.lead.converted_company?.name ?? ""),
              })
            : undefined,
      })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(null)
    }
  }

  const opts = (["new", "contacted", "qualified", "spam"] as LeadStatus[]).map((s) => ({
    value: s,
    label: t(`leads.status.${s}`),
  }))
  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="w-40 bg-card">
        <label htmlFor="lead-status" className="sr-only">
          {t("leads.statusLabel")}
        </label>
        <PickSelect
          id="lead-status"
          value={lead.status}
          onChange={setStatus}
          options={opts}
          disabled={busy !== null}
        />
      </div>
      {canConvert ? (
        lead.status === "spam" ? (
          <p className="text-xs text-muted-foreground">{t("leads.spamNoConvert")}</p>
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

export function Converted({ lead, base }: { lead: Lead; base: string }) {
  const { t, formatDate } = useTranslations()
  const link = (href: string, icon: ReactNode, label: string, name: string, attr: string) => (
    <Link
      href={href}
      className="flex min-w-0 items-center gap-2.5 px-4 py-4 transition-colors hover:bg-nq-surface-soft [&_svg]:size-4 [&_svg]:shrink-0"
      data-converted={attr}
    >
      {icon}
      <span className="min-w-0">
        <span className="block text-xs text-muted-foreground">{label}</span>
        <BdiBlock className="block truncate text-sm font-medium">{name}</BdiBlock>
      </span>
    </Link>
  )
  return (
    <section
      className="overflow-hidden rounded-xl border border-nq-line bg-card"
      data-lead-converted
    >
      <p className="px-4 pt-4 text-sm text-nq-fg-body">
        {t("leads.convertedOn", { date: formatDate(lead.converted_at) })}
      </p>
      <div className="grid sm:grid-cols-3">
        {lead.converted_contact
          ? link(
              `${base}/contacts/${lead.converted_contact.id}`,
              <UserIcon />,
              t("leads.contact"),
              lead.converted_contact.name,
              "contact",
            )
          : null}
        {lead.converted_company ? (
          link(
            `${base}/companies/${lead.converted_company.id}`,
            <Building2Icon />,
            t("leads.company"),
            lead.converted_company.name,
            "company",
          )
        ) : (
          <div
            className="flex items-center gap-2.5 px-4 py-4 text-muted-foreground [&_svg]:size-4 [&_svg]:shrink-0"
            data-converted="no-company"
          >
            <Building2Icon />
            <span className="min-w-0">
              <span className="block text-xs">{t("leads.company")}</span>
              <span className="block text-sm">{t("leads.noCompany")}</span>
            </span>
          </div>
        )}
        {lead.converted_deal
          ? link(
              `${base}/deals/${lead.converted_deal.id}`,
              <HandshakeIcon />,
              t("leads.deal"),
              lead.converted_deal.name,
              "deal",
            )
          : null}
      </div>
    </section>
  )
}

export function Facts({ rows }: { rows: [string, ReactNode | null][] }) {
  return (
    <dl className="grid grid-cols-[minmax(7rem,auto)_minmax(0,1fr)] gap-x-4 gap-y-2 px-4 pb-4 text-sm">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted-foreground">{k}</dt>
          <dd className="min-w-0 break-words">
            {v ?? <span className="text-muted-foreground">—</span>}
          </dd>
        </div>
      ))}
    </dl>
  )
}

export function Attribution({ lead }: { lead: Lead }) {
  const { t } = useTranslations()
  const a = lead.attribution
  const any = Object.values(a).some((v) => v)
  if (!any)
    return <p className="px-4 pb-4 text-sm text-muted-foreground">{t("leads.noAttribution")}</p>
  const url = (v: string) =>
    v ? (
      <span dir="ltr" className="ltr-isolate block truncate rtl:text-right" title={v}>
        {/^https?:\/\//i.test(v) ? (
          <a
            href={v}
            target="_blank"
            rel="noopener noreferrer nofollow"
            className="underline-offset-2 hover:underline"
          >
            {v}
          </a>
        ) : (
          v
        )}
      </span>
    ) : null
  const code = (v: string) =>
    v ? (
      <Ltr mono className="text-xs">
        {v}
      </Ltr>
    ) : null
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
