"use client"

/*
  The Overview's Google cards (MH-444/445/441): kept apart from overview-cards.tsx so the front
  page's own cards can change without touching these. Each is rendered only when the workspace has
  an enabled property, site or channel of its kind, and shows the stored snapshot — a figure never
  fetched is a dash with the reason, never a zero.
*/

import { ChartLineIcon } from "lucide-react"

import { Panel, useFormat } from "@/components/overview/overview-cards"
import type { OverviewTraffic } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

function Figure({ label, value, id }: { label: string; value: string; id: string }) {
  return (
    <div className="min-w-0" data-traffic-figure={id}>
      <p className="grid-micro truncate">{label}</p>
      <p className="mt-1 text-2xl font-medium text-grid-fg tabular-nums">{value}</p>
    </div>
  )
}

export function TrafficCard({ traffic, base }: { traffic: OverviewTraffic; base: string }) {
  const { t } = useTranslations()
  const f = useFormat()
  const props = traffic.properties ?? []
  const num = (v: number | null) => (v == null ? "—" : f.n(v))
  return (
    <Panel id="traffic" title={t("overview.traffic")} icon={<ChartLineIcon />} href={`${base}/analytics`} meta={<span className="text-xs text-grid-muted">{t("overview.last28")}</span>}>
      <ul className="divide-y divide-line">
        {props.map((p) => (
          <li key={p.resource_id} className="space-y-3 px-5 py-4" data-traffic-property={p.name}>
            {props.length > 1 ? (
              <p className="truncate text-sm font-medium text-grid-fg">
                <bdi>{p.name}</bdi>
              </p>
            ) : null}
            <div className="grid grid-cols-2 gap-4">
              <Figure id="users" label={t("analytics.users")} value={num(p.users)} />
              <Figure id="sessions" label={t("analytics.sessions")} value={num(p.sessions)} />
            </div>
            <p className="text-xs text-grid-muted">
              {p.users == null
                ? p.last_error || t("overview.trafficPending")
                : t("overview.trafficSource", { name: p.name, when: p.fetched_at ? f.ago(p.fetched_at) : "—" })}
            </p>
          </li>
        ))}
      </ul>
    </Panel>
  )
}
