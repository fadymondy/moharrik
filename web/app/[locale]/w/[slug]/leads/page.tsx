"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import { useSearchParams } from "next/navigation"
import { ClipboardListIcon, SearchIcon } from "lucide-react"

import { SectionHeader } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { LeadStatusBadge, leadSource } from "@/components/leads/shared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { LEAD_STATUSES, type Lead, type LeadStatus } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useLeads } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, BdiBlock } from "@/components/bidi"

/**
 * Leads (MH-390): what visitors sent through the workspace's inquiry forms.
 * The list revalidates when the API says a lead arrived, so a new inquiry
 * appears without a reload.
 */
export default function LeadsPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.leads")} · ${ws.summary.name}`)
  const canManageForms = ws.can("campaigns:write")

  // ?status=new opens the list already filtered — the Overview links its counts here.
  const initial = useSearchParams().get("status") as LeadStatus | null
  const [status, setStatus] = useState<LeadStatus | "">(initial && LEAD_STATUSES.includes(initial) ? initial : "")
  const [query, setQuery] = useState("")
  const [q, setQ] = useState("")
  // A short debounce, so typing a name is one request rather than one a key.
  useEffect(() => {
    const id = setTimeout(() => setQ(query), 250)
    return () => clearTimeout(id)
  }, [query])

  const leads = useLeads(ws.id, { status, q })
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "lead") void leads.mutate()
  })
  const items = leads.data?.leads ?? []
  const counts = leads.data?.counts
  const total = counts ? LEAD_STATUSES.reduce((n, s) => n + (counts[s] ?? 0), 0) : 0
  const base = `/${locale}/w/${ws.slug}`

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.leads")}
        description={t("leads.hint")}
        action={
          canManageForms ? (
            <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`${base}/subscribe-forms`} />}>
              <ClipboardListIcon />
              {t("leads.forms")}
            </Button>
          ) : undefined
        }
      />

      <div className="flex flex-col gap-3 px-6 pb-4 lg:flex-row lg:items-center">
        <div className="flex flex-wrap items-center gap-2" role="group" aria-label={t("leads.filterStatus")}>
          <FilterButton active={status === ""} onClick={() => setStatus("")} label={t("leads.all")} count={counts ? total : undefined} filter="all" />
          {LEAD_STATUSES.map((s) => (
            <FilterButton key={s} active={status === s} onClick={() => setStatus(s)} label={t(`leads.status.${s}`)} count={counts?.[s]} filter={s} />
          ))}
        </div>
        <div className="relative lg:ms-auto lg:w-72">
          <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-grid-muted" aria-hidden />
          <Input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("leads.search")}
            aria-label={t("leads.search")}
            className="ps-8"
            maxLength={200}
          />
        </div>
      </div>

      {leads.error ? (
        <ErrorState error={leads.error} />
      ) : !leads.data ? (
        <LoadingRows rows={5} />
      ) : items.length === 0 ? (
        total === 0 ? (
          <EmptyState
            title={t("leads.empty")}
            body={canManageForms ? t("leads.emptyBody") : t("leads.emptyBodyMember")}
            action={
              canManageForms ? (
                <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`${base}/subscribe-forms`} />}>
                  {t("forms.addInquiry")}
                </Button>
              ) : undefined
            }
          />
        ) : (
          <EmptyState title={t("leads.noMatch")} />
        )
      ) : (
        <ul className="divide-y divide-line border-y border-line" data-lead-list>
          {items.map((l) => (
            <LeadRow key={l.id} lead={l} href={`${base}/leads/${l.id}`} />
          ))}
        </ul>
      )}
      <div className="pb-8" />
    </>
  )
}

function FilterButton({ active, onClick, label, count, filter }: { active: boolean; onClick: () => void; label: string; count?: number; filter: string }) {
  return (
    <Button type="button" size="sm" variant={active ? "default" : "outline"} aria-pressed={active} onClick={onClick} data-filter={filter}>
      {label}
      {count !== undefined ? <span className={active ? "opacity-80" : "text-grid-muted"}>{count}</span> : null}
    </Button>
  )
}

function LeadRow({ lead, href }: { lead: Lead; href: string }) {
  const { t, formatDate } = useTranslations()
  const who = lead.name || lead.email
  const source = leadSource(lead, t)
  return (
    <li>
      <Link
        href={href}
        className={`flex flex-col gap-1.5 px-6 py-4 transition-colors hover:bg-grid-soft ${lead.status === "new" ? "bg-grid-card" : ""}`}
        data-lead={lead.email}
        data-lead-status={lead.status}
      >
        <span className="flex min-w-0 items-center gap-2">
          <BdiBlock className="truncate text-sm font-medium">{who}</BdiBlock>
          {lead.company ? <span className="hidden truncate text-sm text-grid-muted sm:inline">·&nbsp;<bdi>{lead.company}</bdi></span> : null}
          <LeadStatusBadge status={lead.status} className="ms-auto shrink-0" />
        </span>
        <span className="line-clamp-2 text-sm text-grid-body break-words" dir="auto">{lead.message}</span>
        <span className="flex flex-wrap gap-x-2 gap-y-1 text-xs text-grid-muted">
          {lead.name ? <Ltr>{lead.email}</Ltr> : null}
          {lead.name ? <span aria-hidden>·</span> : null}
          <bdi data-lead-source>{source}</bdi>
          <span aria-hidden>·</span>
          <Ltr>{formatDate(lead.created_at)}</Ltr>
        </span>
      </Link>
    </li>
  )
}
