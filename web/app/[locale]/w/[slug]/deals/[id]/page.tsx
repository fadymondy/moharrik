"use client"

import Link from "next/link"
import {
  ActivityIcon,
  Building2Icon,
  CalendarClockIcon,
  ChevronRightIcon,
  CoinsIcon,
  RouteIcon,
  UserRoundIcon,
} from "lucide-react"

import { Bdi } from "@/components/bidi"
import { DealValue } from "@/components/crm/deals"
import { ClosedOnEdit, StageBadge, StagePath, useDealRecord } from "@/components/deals/deal-record"
import { Facts } from "@/components/leads/lead-record"
import { useFormat } from "@/components/overview/overview-cards"
import { RecordBody, RecordCard, RecordStat } from "@/components/record/record"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"

/** MH-1161: where the deal stands: its value, its stage on the pipeline, who it is with. */
export default function DealOverviewPage() {
  const { t, locale } = useTranslations()
  const f = useFormat()
  const { data, deal, stages, canWrite, base, rec, reload } = useDealRecord()
  const day = (s: string) => dateFormat(locale).format(new Date(s))
  const at = stages.findIndex((s) => s.id === deal.stage_id)
  const close = deal.closed_at ?? deal.expected_close
  const recent = data.activities.slice(0, 4)

  return (
    <RecordBody>
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <RecordStat
          icon={<CoinsIcon />}
          label={t("deals.value")}
          value={<DealValue deal={deal} />}
          href={canWrite ? `${rec}/details` : undefined}
        />
        <RecordStat
          icon={<RouteIcon />}
          label={t("deals.stage")}
          value={<Bdi>{deal.stage_name}</Bdi>}
          hint={at >= 0 ? t("record.stageOf", { n: at + 1, total: stages.length }) : undefined}
        />
        <RecordStat
          icon={<CalendarClockIcon />}
          label={
            deal.closed_at
              ? t(`deals.closed.${deal.stage_kind === "lost" ? "lost" : "won"}`)
              : t("deals.expectedClose")
          }
          value={
            close ? (
              day(close)
            ) : (
              <span className="text-muted-foreground">{t("record.noClose")}</span>
            )
          }
        />
        <RecordStat
          icon={<ActivityIcon />}
          label={t("record.tab.activity")}
          value={f.n(data.activities_total)}
          href={`${rec}/activity`}
        />
      </div>

      <RecordCard title={t("record.stagePath")} data-deal-pipeline>
        <StagePath />
      </RecordCard>

      <div className="grid gap-4 lg:grid-cols-3">
        <RecordCard
          title={t("record.recentActivity")}
          className="lg:col-span-2"
          action={
            <Link href={`${rec}/activity`} className="text-primary hover:underline">
              {t("record.viewAll")}
            </Link>
          }
          data-deal-recent
        >
          {recent.length ? (
            <ul className="divide-y divide-border border-t border-border">
              {recent.map((a) => (
                <li key={a.id} className="flex items-start gap-3 px-4 py-3 text-sm">
                  <span className="mt-0.5 shrink-0 rounded-md bg-nq-surface-soft px-1.5 py-0.5 text-xs text-muted-foreground">
                    {t(`activity.${a.kind}`)}
                  </span>
                  <p
                    className={`line-clamp-2 min-w-0 flex-1 break-words ${a.done_at ? "text-muted-foreground line-through" : ""}`}
                    dir="auto"
                  >
                    {a.body}
                  </p>
                  <span
                    className="shrink-0 text-xs text-muted-foreground"
                    title={f.exact(a.created_at)}
                  >
                    {f.ago(a.created_at)}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="px-4 pb-4 text-sm text-muted-foreground">{t("record.noActivity")}</p>
          )}
        </RecordCard>

        <div className="flex flex-col gap-4">
          <RecordCard title={t("record.related")} data-deal-related>
            {deal.contact || deal.company ? (
              <ul className="flex flex-col gap-1 px-2 pb-2">
                {deal.contact ? (
                  <Related
                    href={`${base}/contacts/${deal.contact.id}`}
                    icon={<UserRoundIcon />}
                    label={deal.contact.name || deal.contact.email}
                    hint={deal.contact.title || t("deals.contact")}
                  />
                ) : null}
                {deal.company ? (
                  <Related
                    href={`${base}/companies/${deal.company.id}`}
                    icon={<Building2Icon />}
                    label={deal.company.name}
                    hint={t("deals.company")}
                  />
                ) : null}
              </ul>
            ) : (
              <p className="px-4 pb-4 text-sm text-muted-foreground">{t("record.noRelated")}</p>
            )}
          </RecordCard>

          <RecordCard title={t("record.details")} data-deal-facts>
            <Facts
              rows={[
                [t("deals.stage"), <StageBadge key="s" deal={deal} />],
                [t("deals.value"), <DealValue key="v" deal={deal} />],
                [t("deals.expectedClose"), deal.expected_close ? day(deal.expected_close) : null],
                [
                  t(`deals.closed.${deal.stage_kind === "lost" ? "lost" : "won"}`),
                  deal.closed_at ? (
                    <span key="c" className="inline-flex flex-wrap items-center gap-1">
                      {day(deal.closed_at)}
                      {canWrite ? (
                        <ClosedOnEdit dealId={deal.id} closedAt={deal.closed_at} onSaved={reload} />
                      ) : null}
                    </span>
                  ) : null,
                ],
                [t("record.added"), day(deal.created_at)],
              ]}
            />
          </RecordCard>
        </div>
      </div>
    </RecordBody>
  )
}

function Related({
  href,
  icon,
  label,
  hint,
}: {
  href: string
  icon: React.ReactNode
  label: string
  hint: string
}) {
  return (
    <li>
      <Link
        href={href}
        className="flex items-center gap-3 rounded-lg px-2 py-2 transition-colors hover:bg-nq-surface-soft"
      >
        <span
          className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/12 text-primary [&_svg]:size-4"
          aria-hidden
        >
          {icon}
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">
            <Bdi>{label}</Bdi>
          </span>
          <span className="block truncate text-xs text-muted-foreground">
            <Bdi>{hint}</Bdi>
          </span>
        </span>
        <ChevronRightIcon className="size-4 text-muted-foreground rtl:rotate-180" />
      </Link>
    </li>
  )
}
