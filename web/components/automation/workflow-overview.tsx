"use client"

/*
The workflow page's Overview and Settings tabs (MH-1067).

Every number here is counted from real run rows by GET /api/w/workflows/{id}/overview.
A figure with nothing behind it reads "—", never an invented 0% or 100%.
*/

import Link from "next/link"
import {
  CalendarClockIcon,
  CircleAlertIcon,
  HistoryIcon,
  PencilRulerIcon,
} from "lucide-react"
import { useState } from "react"
import { Button, Switch, toast } from "@fadymondy/nasaq/web"

import { Bdi } from "@/components/bidi"
import { describeStep } from "@/components/automation/canvas/describe"
import { kindOf, specOf } from "@/components/automation/canvas/model"
import { api, ApiError, type AutomationWorkflow, type WorkflowStep } from "@/lib/api"
import type { WorkflowOverview } from "@/lib/canvas-queries"
import { dateFormat } from "@/lib/i18n-locale"
import { useTranslations } from "@/lib/i18n"
import { cn } from "@/lib/utils"

export type Tone = "ok" | "info" | "ai" | "attention" | "failed" | "idle"

const TONE: Record<Tone, string> = {
  ok: "bg-nq-success-soft text-nq-success-text",
  info: "bg-nq-info-soft text-nq-info-text",
  ai: "bg-primary/10 text-primary",
  attention: "bg-nq-warning-soft text-nq-warning-text",
  failed: "bg-nq-danger-soft text-nq-danger-text",
  idle: "bg-muted text-muted-foreground",
}

export function Pill({ tone, children, ...rest }: { tone: Tone; children: React.ReactNode }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap",
        TONE[tone],
      )}
      {...rest}
    >
      {children}
    </span>
  )
}

export const runTone = (status: string): Tone =>
  status === "ok"
    ? "ok"
    : status === "failed"
      ? "failed"
      : status === "running" || status === "queued"
        ? "info"
        : "idle"

export function useWhen() {
  const { locale } = useTranslations()
  return (iso: string | null | undefined) => {
    if (!iso) return "—"
    const d = new Date(iso)
    if (Number.isNaN(d.getTime())) return "—"
    return dateFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(d)
  }
}

export const ms = (n: number | null | undefined) =>
  n == null ? "" : n >= 1000 ? `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)} s` : `${n} ms`

function Metric({
  label,
  value,
  note,
  tone,
  id,
  raw,
  children,
}: {
  /** The figure itself, for tests: what the card shows, before it is formatted. */
  raw: string | number | null
  label: string
  value: React.ReactNode
  note?: React.ReactNode
  tone?: Tone
  id: string
  children?: React.ReactNode
}) {
  return (
    <div className="min-w-0 rounded-lg border border-nq-line bg-card p-4" data-metric={id} data-raw={raw ?? "none"}>
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <div className="mt-1.5 flex flex-wrap items-center gap-2 text-2xl font-semibold" data-metric-value>
        {tone ? <Pill tone={tone}>{value}</Pill> : value}
      </div>
      {note ? <p className="mt-1.5 text-xs text-muted-foreground" data-metric-note>
          {note}
        </p> : null}
      {children}
    </div>
  )
}

export function WorkflowOverviewTab({
  workflow,
  overview,
  error,
  base,
  onOpen,
}: {
  workflow: AutomationWorkflow
  overview?: WorkflowOverview
  error?: unknown
  base: string
  onOpen: (view: "editor" | "executions") => void
}) {
  const { t, formatNumber } = useTranslations()
  const when = useWhen()
  if (error)
    return (
      <p className="p-6 text-sm text-nq-danger-text" role="alert">
        {error instanceof Error ? error.message : t("common.networkError")}
      </p>
    )
  if (!overview) return <p className="p-6 text-sm text-muted-foreground">{t("common.working")}</p>

  const o = overview
  const last = o.last_run
  const days = o.window_days
  const statusTone: Tone = o.workspace_paused || o.status === "paused" ? "attention" : "ok"
  const stat = new Map(o.steps.map((s) => [s.position, s]))
  const failedStep = last?.status === "failed"

  return (
    <div className="min-h-0 flex-1 overflow-y-auto" data-wfo-overview>
      <div className="mx-auto w-full max-w-5xl space-y-6 p-4 sm:p-6">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Metric
            id="status"
            raw={o.status}
            label={t("wfo.status")}
            value={t(o.status === "paused" ? "wfo.status.paused" : "wfo.status.enabled")}
            tone={statusTone}
            note={o.workspace_paused ? t("wfo.status.wsPaused") : undefined}
          />
          <Metric
            id="last"
            raw={last ? `${last.id}:${last.status}` : null}
            label={t("wfo.last")}
            value={
              last ? (
                <Pill tone={runTone(last.status)}>{t(`automation.status.${last.status}`)}</Pill>
              ) : (
                <span className="text-base font-medium text-muted-foreground">
                  {t("wfo.last.never")}
                </span>
              )
            }
            note={
              last ? (
                <>
                  {when(last.created_at)} · {t(`automation.trigger.${last.trigger}`)} ·{" "}
                  {t("wfo.last.version", { n: last.version })}
                  {last.duration_ms != null ? ` · ${ms(last.duration_ms)}` : ""}
                </>
              ) : undefined
            }
          >
            {last ? (
              <Link
                href={`${base}/runs/${last.id}`}
                className="mt-2 inline-block text-xs font-medium text-primary underline-offset-2 hover:underline"
                data-last-run-link
              >
                {t("wfo.last.open")}
              </Link>
            ) : null}
          </Metric>
          <Metric
            id="rate"
            raw={o.success_rate.percent == null ? null : Math.round(o.success_rate.percent)}
            label={t("wfo.rate")}
            value={
              o.success_rate.percent == null
                ? "—"
                : `${formatNumber(Math.round(o.success_rate.percent))}%`
            }
            note={
              o.success_rate.finished > 0
                ? t("wfo.rate.basis", {
                    ok: o.success_rate.succeeded,
                    done: o.success_rate.finished,
                    days,
                  })
                : t("wfo.rate.empty", { days })
            }
          />
          <Metric
            id="items"
            raw={o.items.total}
            label={t("wfo.items")}
            value={o.items.total == null ? "—" : formatNumber(o.items.total)}
            note={
              o.items.total == null
                ? t("wfo.items.empty", { days })
                : t("wfo.items.basis", { runs: o.items.runs, days })
            }
          />
        </div>

        {failedStep && last ? (
          <div
            className="flex flex-wrap items-start gap-3 rounded-lg border border-nq-danger/40 bg-nq-danger-soft p-4 text-sm"
            role="status"
            data-wfo-failure
          >
            <CircleAlertIcon className="mt-0.5 size-4 shrink-0 text-nq-danger-text" />
            <div className="min-w-0 flex-1">
              <p className="font-medium text-nq-danger-text">{t("wfo.fail.title")}</p>
              {last.error ? (
                <p className="mt-1 break-words text-foreground" dir="auto">
                  <Bdi>{last.error}</Bdi>
                </p>
              ) : null}
            </div>
            <Button
              variant="secondary"
              size="sm"
              nativeButton={false}
              render={<Link href={`${base}/runs/${last.id}`} />}
            >
              {t("wfo.fail.open")}
            </Button>
          </div>
        ) : null}

        <section aria-labelledby="wfo-counts" data-wfo-counts>
          <h2 id="wfo-counts" className="text-sm font-semibold">
            {t("wfo.counts.title", { days })}
          </h2>
          <dl className="mt-2 flex flex-wrap gap-2 text-sm">
            {(
              [
                ["total", o.runs.total, "idle"],
                ["ok", o.runs.succeeded, "ok"],
                ["failed", o.runs.failed, "failed"],
                ["cancelled", o.runs.cancelled, "attention"],
                ["live", o.runs.in_progress, "info"],
              ] as const
            ).map(([k, n, tone]) => (
              <div
                key={k}
                className="flex items-center gap-2 rounded-lg border border-nq-line bg-card px-3 py-1.5"
                data-count={k}
              >
                <dt className="text-muted-foreground">{t(`wfo.counts.${k}`)}</dt>
                <dd>
                  <Pill tone={n > 0 ? tone : "idle"}>{formatNumber(n)}</Pill>
                </dd>
              </div>
            ))}
          </dl>
        </section>

        <section aria-labelledby="wfo-sched" className="flex flex-wrap items-center gap-3 text-sm">
          <CalendarClockIcon className="size-4 text-muted-foreground" />
          <h2 id="wfo-sched" className="sr-only">
            {t("wfo.sched")}
          </h2>
          <span data-wfo-schedules>
            {o.schedules.total === 0
              ? t("wfo.sched.none")
              : t("wfo.sched.some", { enabled: o.schedules.enabled, total: o.schedules.total })}
            {o.schedules.next_run_at
              ? ` ${t("wfo.sched.next", { when: when(o.schedules.next_run_at) })}`
              : ""}
          </span>
        </section>

        <section aria-labelledby="wfo-steps" data-wfo-steps>
          <h2 id="wfo-steps" className="text-sm font-semibold">
            {t("wfo.steps")}
          </h2>
          {workflow.steps.length === 0 ? (
            <p className="mt-2 text-sm text-muted-foreground">{t("wfo.steps.empty")}</p>
          ) : (
            <ol className="mt-2 divide-y divide-nq-line rounded-lg border border-nq-line bg-card">
              {workflow.steps.map((s: WorkflowStep, i: number) => {
                const spec = specOf(kindOf(s))
                const st = stat.get(i)
                const Icon = spec.icon
                return (
                  <li
                    key={i}
                    className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3 text-sm"
                    data-wfo-step={i}
                  >
                    <span className="w-6 text-muted-foreground">{i + 1}.</span>
                    <Icon className="size-4 shrink-0 text-muted-foreground" />
                    <span className="min-w-0 flex-1 basis-48" dir="auto">
                      <span className="block text-xs text-muted-foreground">
                        {t(`canvas.cat.${spec.category}`)}
                      </span>
                      <span className="block break-words">{describeStep(s, t)}</span>
                    </span>
                    {!st || st.ran === 0 ? (
                      <Pill tone="idle">{t("wfo.node.never")}</Pill>
                    ) : (
                      <span className="flex flex-wrap items-center gap-1.5">
                        <Pill tone={st.failed > 0 ? "failed" : "ok"}>
                          {t("wfo.node.record", { ok: st.ok, ran: st.ran })}
                        </Pill>
                        {st.last_items != null ? (
                          <Pill tone="info">{t("wfo.node.items", { n: st.last_items })}</Pill>
                        ) : null}
                      </span>
                    )}
                  </li>
                )
              })}
            </ol>
          )}
          <p className="mt-2 text-xs text-muted-foreground">
            {t("wfo.steps.note", { n: o.version })}
            {o.sampled ? ` ${t("wfo.steps.sampled")}` : ""}
          </p>
        </section>

        <div className="flex flex-wrap gap-2">
          <Button variant="secondary" size="sm" onClick={() => onOpen("editor")} data-open-builder>
            <PencilRulerIcon />
            {t("wfo.open.builder")}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => onOpen("executions")} data-open-runs>
            <HistoryIcon />
            {t("wfo.open.runs")}
          </Button>
        </div>
      </div>
    </div>
  )
}

/**
 * Settings: only controls that work. The switch is the same PATCH the workflow
 * list uses; everything else is a fact with a link to where it is changed.
 */
export function WorkflowSettingsTab({
  workflow,
  overview,
  workspaceId,
  base,
  canEdit,
  sites,
  autoAdd,
  onChanged,
  onOpenHistory,
}: {
  workflow: AutomationWorkflow
  overview?: WorkflowOverview
  workspaceId: string
  base: string
  canEdit: boolean
  sites: string[]
  autoAdd: boolean
  onChanged: () => void
  onOpenHistory: () => void
}) {
  const { t } = useTranslations()
  const [busy, setBusy] = useState(false)
  const on = workflow.enabled

  async function toggle(next: boolean) {
    setBusy(true)
    try {
      await api(`/api/w/workflows/${encodeURIComponent(workflow.id)}`, {
        method: "PATCH",
        workspaceId,
        json: { enabled: next },
      })
      toast.success(t("wfo.set.saved"))
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="min-h-0 flex-1 overflow-y-auto" data-wfo-settings>
      <div className="mx-auto w-full max-w-3xl space-y-4 p-4 sm:p-6">
        <h2 className="text-sm font-semibold">{t("wfo.set.title")}</h2>
        {!canEdit ? <p className="text-xs text-muted-foreground">{t("wfo.set.readonly")}</p> : null}

        <div className="flex items-center justify-between gap-4 rounded-lg border border-nq-line bg-card p-4">
          <div className="min-w-0">
            <p className="text-sm font-medium">{t("wfo.set.enabled")}</p>
            <p className="text-xs text-muted-foreground">{t("wfo.set.enabledHint")}</p>
          </div>
          <Switch
            checked={on}
            disabled={!canEdit || busy}
            onCheckedChange={(v) => void toggle(v)}
            aria-label={t("wfo.set.enabled")}
            data-wfo-enabled
          />
        </div>

        <div className="rounded-lg border border-nq-line bg-card p-4 text-sm">
          <p className="font-medium">{t("wfo.set.schedules")}</p>
          <p className="mt-1 text-xs text-muted-foreground">
            {!overview || overview.schedules.total === 0
              ? t("wfo.sched.none")
              : t("wfo.sched.some", {
                  enabled: overview.schedules.enabled,
                  total: overview.schedules.total,
                })}
          </p>
          <Button
            className="mt-3"
            variant="secondary"
            size="sm"
            nativeButton={false}
            render={<Link href={`${base}/schedules`} />}
          >
            {t("wfo.set.schedules.manage")}
          </Button>
        </div>

        <div className="rounded-lg border border-nq-line bg-card p-4 text-sm" data-wfo-sites>
          <p className="font-medium">{t("wfo.set.sites")}</p>
          {sites.length === 0 ? (
            <p className="mt-1 text-xs text-muted-foreground">{t("wfo.set.sites.none")}</p>
          ) : (
            <ul className="mt-2 flex flex-wrap gap-1.5">
              {sites.map((h) => (
                <li key={h}>
                  <Pill tone="info">
                    <bdi dir="ltr">{h}</bdi>
                  </Pill>
                </li>
              ))}
            </ul>
          )}
          {sites.length > 0 ? (
            <p className="mt-2 text-xs text-muted-foreground">
              {autoAdd ? t("wfo.set.autoOn") : t("wfo.set.autoOff")}
            </p>
          ) : null}
        </div>

        <div className="rounded-lg border border-nq-line bg-card p-4 text-sm">
          <p className="font-medium">{t("wfo.set.versions")}</p>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("wfo.set.versions.body", { n: workflow.version })}
          </p>
          <Button className="mt-3" variant="secondary" size="sm" onClick={onOpenHistory}>
            <HistoryIcon />
            {t("wfo.set.versions.open")}
          </Button>
        </div>
      </div>
    </div>
  )
}
