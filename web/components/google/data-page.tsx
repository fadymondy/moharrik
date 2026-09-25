"use client"

/*
  Pieces the Google data pages share (MH-444/445/441/442): the property / site / channel switcher,
  the window toggle, and the honest states — nothing connected, nothing chosen, Google failing,
  a stale answer. None of them ever shows a number that was not fetched.
*/

import Link from "next/link"
import type React from "react"
import {
  ArrowDownIcon,
  ArrowUpIcon,
  ClockIcon,
  MinusIcon,
  RefreshCwIcon,
  TriangleAlertIcon,
} from "lucide-react"

import { Ltr } from "@/components/copy-field"
import { KIND_ICON } from "@/components/google/resource-picker"
import { useFormat } from "@/components/overview/overview-cards"
import { EmptyState } from "@/components/states"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useWorkspace } from "@/components/workspace/workspace-context"
import type { GoogleKind, GoogleResource } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { useGoogleResources } from "@/lib/queries"
import { cn } from "@/lib/utils"

/** The enabled resources of a kind, and whether the workspace has any of that kind at all. */
export function useEnabled(kind: GoogleKind) {
  const ws = useWorkspace()
  const res = useGoogleResources(ws.id, kind, ws.can("analytics:read"))
  const list = (res.data?.resources ?? []).filter((r) => r.enabled && r.kind === kind)
  return { ...res, list, known: (res.data?.available ?? []).includes(kind) }
}

/** No resource of this kind to read: connect one, or choose one — or, for a reader, ask an admin. */
export function NoResource({ kind, known }: { kind: GoogleKind; known: boolean }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const manage = ws.can("integrations:manage")
  const href = `/${locale}/w/${ws.slug}/settings/integrations`
  return (
    <div data-no-resource={known ? "choose" : "connect"}>
      <EmptyState
        title={t(known ? `gdata.choose.${kind}` : `gdata.connect.${kind}`)}
        body={manage ? t(known ? "gdata.chooseBody" : "gdata.connectBody") : t("gdata.askAdmin")}
        action={
          manage ? (
            <Button nativeButton={false} render={<Link href={href} />}>
              {t(known ? "gdata.chooseAction" : "gdata.connectAction")}
            </Button>
          ) : undefined
        }
      />
    </div>
  )
}

export function ResourceSwitcher({
  list,
  value,
  onChange,
  kind,
}: {
  list: GoogleResource[]
  value: string
  onChange: (id: string) => void
  kind: GoogleKind
}) {
  const { t } = useTranslations()
  if (list.length <= 1) {
    const r = list[0]
    return r ? (
      <p
        className="flex min-w-0 items-center gap-2 text-sm text-grid-body [&_svg]:size-4 [&_svg]:shrink-0 [&_svg]:text-grid-muted"
        data-resource-name
      >
        {KIND_ICON[kind]}
        <bdi className="truncate">{r.display_name}</bdi>
      </p>
    ) : null
  }
  const items = list.map((r) => ({ value: r.id, label: r.display_name }))
  return (
    <Select items={items} value={value} onValueChange={(v) => v && onChange(String(v))}>
      <SelectTrigger
        className="w-full max-w-72 sm:w-64"
        aria-label={t(`resources.kind.${kind}`)}
        data-resource-switcher
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {items.map((i) => (
          <SelectItem key={i.value} value={i.value}>
            {i.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

export function WindowToggle({
  value,
  onChange,
  options = [7, 28, 90],
}: {
  value: number
  onChange: (d: number) => void
  options?: number[]
}) {
  const { t } = useTranslations()
  return (
    <ToggleGroup
      variant="outline"
      size="sm"
      spacing={0}
      value={[String(value)]}
      onValueChange={(v) => {
        const next = v[0]
        if (next !== undefined) onChange(Number(next))
      }}
      aria-label={t("gdata.window")}
    >
      {options.map((d) => (
        <ToggleGroupItem key={d} value={String(d)} data-window={d}>
          {t("gdata.days", { n: d })}
        </ToggleGroupItem>
      ))}
    </ToggleGroup>
  )
}

/** Google could not be asked and nothing is stored: say why, and where to fix it. */
export function DataError({
  message,
  resource,
}: {
  message: string
  resource?: GoogleResource | null
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  return (
    <div
      role="alert"
      className="mx-6 my-6 flex items-start gap-3 border border-line bg-grid-card px-4 py-4 text-sm"
      data-data-error
    >
      <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-danger" />
      <div className="min-w-0 space-y-2">
        <p className="font-medium text-grid-fg">{t("gdata.errorTitle")}</p>
        <p className="break-words text-grid-danger-text">
          <Ltr>{message}</Ltr>
        </p>
        {resource?.last_error && resource.last_error !== message ? (
          <p className="break-words text-grid-muted">
            <Ltr>{resource.last_error}</Ltr>
          </p>
        ) : null}
        {ws.can("integrations:manage") ? (
          <Button
            variant="outline"
            size="sm"
            nativeButton={false}
            render={<Link href={`/${locale}/w/${ws.slug}/settings/integrations`} />}
          >
            {t("gdata.openIntegrations")}
          </Button>
        ) : (
          <p className="text-grid-muted">{t("gdata.askAdmin")}</p>
        )}
      </div>
    </div>
  )
}

/** "Updated 12 minutes ago" — and, when the answer is stale, why. */
export function Freshness({
  fetchedAt,
  stale,
  note,
  onRefresh,
  refreshing,
}: {
  fetchedAt: string
  stale?: boolean
  note?: string
  onRefresh?: () => void
  refreshing?: boolean
}) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <div className="space-y-2">
      {stale ? (
        <div
          role="status"
          className="flex items-start gap-3 border border-line bg-grid-card px-4 py-3 text-sm"
          data-stale
        >
          <ClockIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" />
          <p className="min-w-0 break-words text-grid-body">
            {t("gdata.stale", { when: f.ago(fetchedAt) })} <Ltr>{note}</Ltr>
          </p>
        </div>
      ) : null}
      <p className="flex flex-wrap items-center gap-2 text-xs text-grid-muted">
        <span title={f.exact(fetchedAt)} data-fetched-at={fetchedAt}>
          {t("gdata.updated", { when: f.ago(fetchedAt) })}
        </span>
        {onRefresh ? (
          <Button variant="ghost" size="xs" onClick={onRefresh} disabled={refreshing} data-refresh>
            <RefreshCwIcon className={refreshing ? "animate-spin" : undefined} />
            {refreshing ? t("gdata.refreshing") : t("gdata.refresh")}
          </Button>
        ) : null}
      </p>
    </div>
  )
}

/** One headline number, with an optional change against a previous period. */
export function Stat({
  label,
  value,
  sub,
  id,
  delta,
}: {
  label: string
  value: string
  sub?: string
  id: string
  delta?: React.ReactNode
}) {
  return (
    <div className="min-w-0 px-5 py-4" data-stat={id}>
      <p className="grid-micro truncate">{label}</p>
      <p className="mt-1.5 text-2xl font-medium text-grid-fg tabular-nums">{value}</p>
      {delta ? <div className="mt-1">{delta}</div> : null}
      {sub ? <p className="mt-0.5 truncate text-xs text-grid-muted">{sub}</p> : null}
    </div>
  )
}

/**
 * The change from `before` to `now`. Better or worse is said with an arrow, a sign and a word as
 * well as colour; `lowerIsBetter` for a search position. Nothing is shown when there is no base.
 */
export function Delta({
  now,
  before,
  lowerIsBetter = false,
  mode = "relative",
}: {
  now: number | null
  before: number | null
  lowerIsBetter?: boolean
  /** relative: a percent change; points: the difference itself (a position); pp: percentage points (a rate). */
  mode?: "relative" | "points" | "pp"
}) {
  const { t, locale } = useTranslations()
  if (now == null || before == null)
    return <span className="text-xs text-grid-muted">{t("gdata.noCompare")}</span>
  const diff = now - before
  if (mode === "relative" && before === 0)
    return <span className="text-xs text-grid-muted">{t("gdata.noBase")}</span>
  const change = mode === "relative" ? diff / before : mode === "pp" ? diff * 100 : diff
  const tag = intlTag(locale)
  const text =
    mode === "relative"
      ? new Intl.NumberFormat(tag, {
          style: "percent",
          maximumFractionDigits: 1,
          signDisplay: "always",
        }).format(change)
      : t(mode === "pp" ? "gdata.pp" : "gdata.pts", {
          n: new Intl.NumberFormat(tag, { maximumFractionDigits: 1, signDisplay: "always" }).format(
            change,
          ),
        })
  const flat = Math.abs(change) < (mode === "relative" ? 0.0005 : 0.05)
  const shown = flat
    ? mode === "relative"
      ? new Intl.NumberFormat(tag, { style: "percent" }).format(0)
      : t(mode === "pp" ? "gdata.pp" : "gdata.pts", { n: new Intl.NumberFormat(tag).format(0) })
    : text
  const good = flat ? null : lowerIsBetter ? diff < 0 : diff > 0
  const Icon = flat ? MinusIcon : diff > 0 ? ArrowUpIcon : ArrowDownIcon
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 text-xs font-medium tabular-nums",
        good == null ? "text-grid-muted" : good ? "text-grid-ok-text" : "text-grid-danger-text",
      )}
      data-delta={good == null ? "flat" : good ? "better" : "worse"}
    >
      <Icon className="size-3.5" aria-hidden />
      {shown}
      <span className="font-normal text-grid-muted">
        {t(good == null ? "gdata.flat" : good ? "gdata.better" : "gdata.worse")}
      </span>
    </span>
  )
}
