"use client"

import Link from "next/link"
import { useEffect, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { ClipboardListIcon, InboxIcon } from "lucide-react"

import { SectionHeader } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import { LeadStatusBadge, leadSource } from "@/components/leads/shared"
import {
  Button,
  CardMeta,
  EmptyState as NqEmptyState,
  EntityIdentity,
  EntityList,
  type DataTableColumn,
  type EntityFacet,
} from "@fadymondy/nasaq/web"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { LEAD_STATUSES, type Lead, type LeadStatus } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useLeads } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

/**
 * Leads (MH-390): what visitors sent through the workspace's inquiry forms.
 * The list revalidates when the API says a lead arrived, so a new inquiry
 * appears without a reload. On Nasaq's EntityList in server mode (MH-1127):
 * the API filters by status and searches; the list shows what it was given.
 */
export default function LeadsPage() {
  const { t, locale, formatNumber, formatDate } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  useDocumentTitle(`${t("nav.leads")} · ${ws.summary.name}`)
  const canManageForms = ws.can("campaigns:write")

  // ?status=new opens the list already filtered — the Overview links its counts here.
  const initial = useSearchParams().get("status") as LeadStatus | null
  const [status, setStatus] = useState<LeadStatus | "">(
    initial && LEAD_STATUSES.includes(initial) ? initial : "",
  )
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
  const formsLink = `${base}/subscribe-forms`

  // The API takes one status: picking a second replaces the first.
  const facets: EntityFacet<Lead>[] = [
    {
      id: "status",
      title: t("leads.statusLabel"),
      options: LEAD_STATUSES.map((s) => ({
        value: s,
        label: counts
          ? `${t(`leads.status.${s}`)} · ${formatNumber(counts[s] ?? 0)}`
          : t(`leads.status.${s}`),
      })),
      getValues: () => [],
    },
  ]
  const facetValues: Record<string, string[]> = { status: status ? [status] : [] }
  const onFacetValuesChange = (next: Record<string, string[]>) => {
    const values = next.status ?? []
    const picked = values.find((v) => !facetValues.status.includes(v)) ?? values[0] ?? ""
    setStatus(picked as LeadStatus | "")
  }

  const who = (l: Lead) => l.name || l.email

  const columns: DataTableColumn<Lead>[] = [
    {
      id: "name",
      header: t("leads.contact"),
      hideable: false,
      className: "min-w-56",
      cell: (l) => (
        <span data-lead={l.email} data-lead-status={l.status} className="block min-w-0">
          <EntityIdentity
            name={who(l)}
            avatarName={who(l)}
            subtitle={l.name ? <Ltr>{l.email}</Ltr> : undefined}
          />
        </span>
      ),
    },
    {
      id: "company",
      header: t("leads.company"),
      cell: (l) =>
        l.company ? <Bdi>{l.company}</Bdi> : <span className="text-muted-foreground">—</span>,
    },
    {
      id: "message",
      header: t("leads.message"),
      className: "min-w-64 max-w-md",
      cell: (l) => (
        <span
          className="line-clamp-2 text-sm whitespace-normal break-words text-nq-fg-body"
          dir="auto"
        >
          {l.message}
        </span>
      ),
    },
    {
      id: "source",
      header: t("leads.sourceCol"),
      cell: (l) => (
        <bdi className="text-muted-foreground" data-lead-source>
          {leadSource(l, t)}
        </bdi>
      ),
    },
    {
      id: "status",
      header: t("leads.statusLabel"),
      cell: (l) => <LeadStatusBadge status={l.status} />,
    },
    {
      id: "received",
      header: t("leads.received"),
      align: "end",
      cell: (l) => (
        <span className="text-muted-foreground">
          <Ltr>{formatDate(l.created_at)}</Ltr>
        </span>
      ),
    },
  ]

  const filtered = !!q || !!status

  return (
    <>
      <SectionHeader
        title={t("nav.leads")}
        description={t("leads.hint")}
        action={
          canManageForms ? (
            <Button
              variant="secondary"
              size="sm"
              nativeButton={false}
              render={<Link href={formsLink} />}
            >
              <ClipboardListIcon />
              {t("leads.forms")}
            </Button>
          ) : undefined
        }
      />

      <section data-lead-list className="px-4 pt-6 sm:px-6">
        <EntityList<Lead>
          data={items}
          columns={columns}
          getRowId={(l) => l.id}
          rowLabel={who}
          label={t("nav.leads")}
          manual
          rowCount={items.length}
          selectable={false}
          query={{ value: query, onChange: setQuery }}
          searchPlaceholder={t("leads.search")}
          facets={facets}
          facetValues={facetValues}
          onFacetValuesChange={onFacetValuesChange}
          onRowClick={(l) => router.push(`${base}/leads/${l.id}`)}
          loading={!leads.data && !leads.error}
          error={
            leads.error
              ? leads.error instanceof Error
                ? leads.error.message
                : t("common.networkError")
              : undefined
          }
          onRetry={() => void leads.mutate()}
          empty={
            <NqEmptyState
              icon={InboxIcon}
              title={filtered && total > 0 ? t("leads.noMatch") : t("leads.empty")}
              description={
                filtered && total > 0
                  ? undefined
                  : canManageForms
                    ? t("leads.emptyBody")
                    : t("leads.emptyBodyMember")
              }
              actions={
                !filtered && canManageForms ? (
                  <Button
                    variant="secondary"
                    size="sm"
                    nativeButton={false}
                    render={<Link href={formsLink} />}
                  >
                    {t("forms.addInquiry")}
                  </Button>
                ) : undefined
              }
              className="border-0"
            />
          }
          renderCard={(l) => (
            <div
              className="flex min-w-0 flex-col gap-3"
              data-lead-card={l.email}
              data-lead-status={l.status}
            >
              <EntityIdentity
                className="pe-(--entity-card-controls)"
                name={who(l)}
                avatarName={who(l)}
                size="lg"
                subtitle={l.company || (l.name ? <Ltr>{l.email}</Ltr> : undefined)}
              />
              <p className="line-clamp-3 text-sm break-words text-nq-fg-body" dir="auto">
                {l.message}
              </p>
              <div className="flex flex-col gap-1.5">
                <CardMeta label={t("leads.statusLabel")}>
                  <LeadStatusBadge status={l.status} />
                </CardMeta>
                <CardMeta label={t("leads.sourceCol")}>
                  <bdi>{leadSource(l, t)}</bdi>
                </CardMeta>
                <CardMeta label={t("leads.received")}>
                  <Ltr>{formatDate(l.created_at)}</Ltr>
                </CardMeta>
              </div>
            </div>
          )}
          pagination={
            leads.data && items.length > 0 ? (
              <p className="text-sm text-muted-foreground" aria-live="polite" data-leads-summary>
                {t("leads.shown", {
                  shown: formatNumber(items.length),
                  total: formatNumber(total),
                })}
              </p>
            ) : (
              false
            )
          }
        />
      </section>
      <div className="pb-8" />
    </>
  )
}
