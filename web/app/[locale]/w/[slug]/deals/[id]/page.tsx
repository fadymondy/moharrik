"use client"

import { useParams } from "next/navigation"

import { DealForm } from "@/components/crm/deals"
import { formatMoney, stageTone } from "@/components/crm/shared"
import { Timeline } from "@/components/crm/timeline"
import { Ltr } from "@/components/copy-field"
import { ConfirmButton } from "@/components/confirm-button"
import { NotFoundState } from "@/components/not-found-state"
import { SectionHeader } from "@/components/public-frame"
import { Badge } from "@/components/ui/badge"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type StageKind } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useDeal, useDeals } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

import { Bdi, isolate } from "@/components/bidi"
export default function DealDetailPage() {
  const { id } = useParams<{ id: string }>()
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  const detail = useDeal(ws.id, id)
  const pipeline = useDeals(ws.id)
  useDocumentTitle(detail.data ? `${detail.data.deal.title} · ${ws.summary.name}` : t("deals.dealTitle"))
  const base = `/${locale}/w/${ws.slug}`

  if (detail.error) {
    const notFound = detail.error instanceof ApiError && detail.error.status === 404
    if (notFound) return <NotFoundState title={t("deals.notFound")} body={t("deals.notFoundBody")} backHref={`${base}/deals`} backLabel={t("deals.backToList")} />
    return <ErrorState error={detail.error} />
  }
  if (!detail.data || !pipeline.data) return <LoadingRows rows={6} />

  const { deal, activities } = detail.data
  const closedTone: Record<StageKind, "default" | "secondary" | "outline"> = { open: "outline", won: "default", lost: "secondary" }

  return (
    <>
      <SectionHeader
        micro={t("nav.deals")}
        title={<Bdi>{deal.title}</Bdi>}
        description={
          <span className="inline-flex items-center gap-2">
            <span className={`size-2 rounded-full ${stageTone[deal.stage_kind]}`} aria-hidden />
            <Bdi>{deal.stage_name}</Bdi>
            <span aria-hidden>·</span>
            <Ltr>{formatMoney(deal.value_minor, deal.currency, locale)}</Ltr>
          </span>
        }
        action={
          canWrite ? (
            <ConfirmButton
              label={t("common.delete")}
              title={t("deals.deleteTitle")}
              description={t("deals.deleteBody", { title: isolate(deal.title) })}
              confirmLabel={t("common.delete")}
              onConfirm={async () => {
                await api(`/api/w/deals/${encodeURIComponent(deal.id)}`, { method: "DELETE", workspaceId: ws.id })
                window.location.href = `${base}/deals`
              }}
            />
          ) : undefined
        }
      />

      {deal.closed_at ? (
        <p className="px-6 pb-4 -mt-2 text-sm text-grid-muted">
          <Badge variant={closedTone[deal.stage_kind]}>{t(`deals.closed.${deal.stage_kind === "lost" ? "lost" : "won"}`)}</Badge>{" "}
          {t("deals.closedOn", { date: new Intl.DateTimeFormat(locale).format(new Date(deal.closed_at)) })}
        </p>
      ) : null}

      {canWrite ? (
        <section className="px-6 pb-6">
          <DealForm formId="edit-deal" stages={pipeline.data.stages} currency={pipeline.data.currency} deal={deal} onSaved={() => detail.mutate()} />
        </section>
      ) : null}

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />

      <Timeline items={activities} target={{ deal_id: deal.id }} onChange={() => detail.mutate()} />
      <div className="pb-8" />
    </>
  )
}
