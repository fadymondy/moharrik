"use client"

/*
A rule's runs (MH-427): the run log as a table, and one run in full — the event
it was given, each condition with the value it was compared against, and what
each action did (or, in a test, would have done).
*/

import { useMemo, useState } from "react"
import {
  BanIcon,
  CircleCheckIcon,
  CircleDashedIcon,
  CircleMinusIcon,
  CircleXIcon,
  FlaskConicalIcon,
  GaugeIcon,
  RepeatIcon,
  TriangleAlertIcon,
} from "lucide-react"

import { Bdi } from "@/components/bidi"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { actionErrorText, actionSummaryText, runReasonText } from "@/lib/rule-messages"
import type { RuleActionResult, RuleRun, RuleRunStatus } from "@/lib/rules"
import { cn } from "@/lib/utils"

import { actionIcon } from "./rule-model"

const STATUS_TONE: Record<string, string> = {
  ok: "text-nq-success",
  partial: "text-nq-warning-text",
  failed: "text-nq-danger-text",
  skipped: "text-muted-foreground",
  rate_limited: "text-nq-warning-text",
  loop_blocked: "text-nq-warning-text",
}

/** A test that would run says so, rather than "Ran". */
export function statusLabel(t: (k: string) => string, run: Pick<RuleRun, "status" | "dry_run">) {
  return run.dry_run && run.status === "ok"
    ? t("rules.status.would")
    : t(`rules.status.${run.status}`)
}

export function RunStatusIcon({
  status,
  className,
}: {
  status: RuleRunStatus | string
  className?: string
}) {
  const Icon =
    status === "ok"
      ? CircleCheckIcon
      : status === "partial"
        ? TriangleAlertIcon
        : status === "failed"
          ? CircleXIcon
          : status === "rate_limited"
            ? GaugeIcon
            : status === "loop_blocked"
              ? RepeatIcon
              : CircleMinusIcon
  return (
    <Icon
      className={cn("size-4 shrink-0", STATUS_TONE[status] ?? "text-muted-foreground", className)}
      aria-hidden
    />
  )
}

function ActionIconFor({ a }: { a: RuleActionResult }) {
  const Icon =
    a.status === "ok"
      ? CircleCheckIcon
      : a.status === "would"
        ? CircleDashedIcon
        : a.status === "failed"
          ? CircleXIcon
          : BanIcon
  const tone =
    a.status === "ok" || a.status === "would"
      ? "text-nq-success"
      : a.status === "failed"
        ? "text-nq-danger-text"
        : "text-muted-foreground"
  return <Icon className={cn("mt-0.5 size-4 shrink-0", tone)} aria-hidden />
}

/** One run in full. */
export function RunResult({ run, showEvent = false }: { run: RuleRun; showEvent?: boolean }) {
  const { t } = useTranslations()
  return (
    <div
      className="space-y-4"
      data-run-result={run.status}
      data-dry-run={run.dry_run ? "true" : "false"}
    >
      <div className="flex flex-wrap items-center gap-2 rounded-md border border-nq-line bg-card px-3 py-2">
        <RunStatusIcon status={run.status} />
        <span className="text-sm font-medium" data-run-status>
          {statusLabel(t, run)}
        </span>
        {run.dry_run ? (
          <Badge variant="outline">
            <FlaskConicalIcon />
            {t("rules.testBadge")}
          </Badge>
        ) : null}
        {run.depth > 0 ? (
          <Badge variant="outline">{t("rules.depth", { n: run.depth })}</Badge>
        ) : null}
        {run.reason ? (
          <p className="w-full text-xs text-muted-foreground" data-run-reason>
            <Bdi>{runReasonText(t, run)}</Bdi>
          </p>
        ) : null}
      </div>

      <section className="space-y-2">
        <h3 className="text-xs font-medium text-muted-foreground">{t("rules.if")}</h3>
        {run.conditions.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("rules.noConditionsRan")}</p>
        ) : (
          <ul className="space-y-1.5" data-run-conditions>
            {run.conditions.map((c, i) => (
              <li
                key={i}
                className="flex items-start gap-2 text-sm"
                data-cond-passed={c.passed ? "true" : "false"}
              >
                {c.passed ? (
                  <CircleCheckIcon className="mt-0.5 size-4 shrink-0 text-nq-success" aria-hidden />
                ) : (
                  <CircleXIcon
                    className="mt-0.5 size-4 shrink-0 text-nq-danger-text"
                    aria-hidden
                  />
                )}
                <span className="min-w-0">
                  {t(`rules.field.${c.field}`)} {t(`rules.op.${c.op}`)}{" "}
                  {c.value ? <Bdi className="font-medium">{c.value}</Bdi> : null}
                  <span className="block text-xs text-muted-foreground">
                    {t("rules.itWas")} <Bdi>{c.actual === "" ? t("rules.empty") : c.actual}</Bdi>
                  </span>
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section className="space-y-2">
        <h3 className="text-xs font-medium text-muted-foreground">{t("rules.then")}</h3>
        {run.actions.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("rules.noActionsRan")}</p>
        ) : (
          <ol className="space-y-2" data-run-actions>
            {run.actions.map((a, i) => {
              const Icon = actionIcon(a.type)
              const summary = actionSummaryText(t, a)
              const error = actionErrorText(t, a)
              return (
                <li
                  key={i}
                  className="flex items-start gap-2 rounded-md border border-nq-line px-3 py-2 text-sm"
                  data-action-status={a.status}
                >
                  <ActionIconFor a={a} />
                  <div className="min-w-0 flex-1 space-y-0.5">
                    <p className="flex items-center gap-1.5 font-medium">
                      <Icon className="size-3.5 text-muted-foreground" aria-hidden />
                      {t(`rules.action.${a.type}`)}
                      <span className="text-xs font-normal text-muted-foreground">
                        · {t(`rules.actionStatus.${a.status}`)}
                      </span>
                    </p>
                    {summary ? (
                      <p className="text-muted-foreground" dir="auto">
                        {summary}
                      </p>
                    ) : null}
                    {error ? (
                      <p
                        className={cn(
                          "text-xs",
                          a.status === "failed" ? "text-nq-danger-text" : "text-nq-warning-text",
                        )}
                        dir="auto"
                      >
                        {error}
                      </p>
                    ) : null}
                  </div>
                </li>
              )
            })}
          </ol>
        )}
      </section>

      {showEvent ? (
        <section className="space-y-2">
          <h3 className="text-xs font-medium text-muted-foreground">{t("rules.eventGiven")}</h3>
          <dl
            className="grid grid-cols-[minmax(0,10rem)_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs"
            data-run-event
          >
            {Object.entries(run.event).map(([k, v]) => (
              <div key={k} className="contents">
                <dt className="truncate text-muted-foreground">{t(`rules.field.${k}`)}</dt>
                <dd className="break-words" dir="auto">
                  {v == null || v === "" ? "—" : String(v)}
                </dd>
              </div>
            ))}
          </dl>
        </section>
      ) : null}
    </div>
  )
}

/** The run log: newest first, one row per evaluation; a row opens the run. */
export function RunLog({
  runs,
  loading,
  error,
  more,
  onMore,
  showRule = true,
}: {
  runs: RuleRun[]
  loading: boolean
  error: unknown
  more: boolean
  onMore: () => void
  showRule?: boolean
}) {
  const { t, locale, isRtl } = useTranslations()
  const [open, setOpen] = useState<RuleRun | null>(null)
  const when = useMemo(
    () => new Intl.DateTimeFormat(intlTag(locale), { dateStyle: "medium", timeStyle: "short" }),
    [locale],
  )
  if (error) return <ErrorState error={error} />
  if (loading) return <LoadingRows rows={4} />
  if (runs.length === 0)
    return (
      <div className="pt-4">
        <EmptyState title={t("rules.runs.none")} body={t("rules.runs.noneBody")} />
      </div>
    )
  return (
    <section data-rule-runs>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[640px] text-sm">
          <thead className="text-xs text-muted-foreground">
            <tr className="border-y border-nq-line">
              {showRule ? (
                <th className="px-6 py-2 text-start font-normal">{t("rules.runs.rule")}</th>
              ) : null}
              <th className={cn("py-2 text-start font-normal", showRule ? "px-2" : "px-6")}>
                {t("rules.runs.status")}
              </th>
              <th className="px-2 py-2 text-start font-normal">{t("rules.runs.event")}</th>
              <th className="px-2 py-2 text-start font-normal">{t("rules.runs.when")}</th>
              <th className="px-6 py-2 text-start font-normal">{t("rules.runs.what")}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-nq-line border-b border-nq-line">
            {runs.map((r) => (
              <tr
                key={r.id}
                className="cursor-pointer hover:bg-nq-surface-soft"
                onClick={() => setOpen(r)}
                data-rule-run={r.id}
                data-run-row-status={r.status}
              >
                {showRule ? (
                  <td className="px-6 py-3 font-medium">
                    <Bdi>{r.rule_name}</Bdi>
                  </td>
                ) : null}
                <td className={cn("py-3", showRule ? "px-2" : "px-6")}>
                  <span className="flex items-center gap-1.5 whitespace-nowrap">
                    <RunStatusIcon status={r.status} />
                    {statusLabel(t, r)}
                    {r.dry_run ? <Badge variant="outline">{t("rules.testBadge")}</Badge> : null}
                  </span>
                </td>
                <td className="px-2 py-3 whitespace-nowrap text-muted-foreground">
                  {t(`rules.trigger.${r.trigger}`)}
                </td>
                <td className="px-2 py-3 whitespace-nowrap text-muted-foreground">
                  {when.format(new Date(r.started_at))}
                </td>
                <td className="max-w-md px-6 py-3 text-muted-foreground">
                  <span className="line-clamp-1" dir="auto">
                    {r.reason
                      ? runReasonText(t, r)
                      : r.actions
                          .map((a) => actionSummaryText(t, a))
                          .filter(Boolean)
                          .join(" · ")}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {more ? (
        <div className="px-6 pt-4">
          <Button variant="outline" size="sm" onClick={onMore}>
            {t("common.loadMore")}
          </Button>
        </div>
      ) : null}
      <Sheet open={!!open} onOpenChange={(v) => (!v ? setOpen(null) : undefined)}>
        <SheetContent
          side={isRtl ? "left" : "right"}
          className="w-full overflow-y-auto data-[side=left]:w-full data-[side=right]:w-full data-[side=left]:sm:max-w-lg data-[side=right]:sm:max-w-lg"
          data-run-sheet
        >
          <SheetHeader className="pe-12">
            <SheetTitle>
              <Bdi>{open?.rule_name}</Bdi>
            </SheetTitle>
            <SheetDescription>
              {open
                ? `${t(`rules.trigger.${open.trigger}`)} · ${when.format(new Date(open.started_at))}`
                : null}
            </SheetDescription>
          </SheetHeader>
          <div className="px-4 pb-6">{open ? <RunResult run={open} showEvent /> : null}</div>
        </SheetContent>
      </Sheet>
    </section>
  )
}
