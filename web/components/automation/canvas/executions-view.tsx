"use client"

/*
A workflow's Executions tab (MH-480), as n8n has it: the runs down one side,
newest first, and the chosen one drawn on the canvas as it ran — every node in
the state it finished in, the item counts on the edges, and each node's output
a click away.

A past run is drawn from ITS OWN steps (what the run record kept), not from the
workflow as it reads today: a workflow edited since would otherwise show last
week's results against this week's steps.

Replay plays the run back — each node lights up in turn, for about as long as
it took (clamped, so a long wait does not stall the replay) — which is how a
run that failed half-way is best understood.
*/

import Link from "next/link"
import { useEffect, useMemo, useState } from "react"
import {
  ArrowLeftIcon,
  CircleCheckIcon,
  CircleDotIcon,
  CircleSlashIcon,
  CircleXIcon,
  ExternalLinkIcon,
  LoaderCircleIcon,
  PlayIcon,
  SquareIcon,
} from "lucide-react"

import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useExecutions, type WorkflowExecution, type WorkflowLayout } from "@/lib/canvas-queries"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { useRun } from "@/lib/queries"
import { cn } from "@/lib/utils"

import { autoLayout, nodeRuns, resolveLayout, stepsOfRun, type NodeRun } from "./model"
import { StepPanel, type PanelTab } from "./node-panel"
import { WorkflowCanvas, type Selection } from "./workflow-canvas"

export function ExecutionIcon({ status, className }: { status: string; className?: string }) {
  const c = cn("size-4 shrink-0", className)
  if (status === "ok") return <CircleCheckIcon className={cn(c, "text-nq-success-text")} />
  if (status === "failed") return <CircleXIcon className={cn(c, "text-nq-danger-text")} />
  if (status === "cancelled") return <CircleSlashIcon className={cn(c, "text-muted-foreground")} />
  if (status === "running")
    return <LoaderCircleIcon className={cn(c, "animate-spin text-primary")} />
  return <CircleDotIcon className={cn(c, "text-muted-foreground")} />
}

/** How long a run took, as a person reads it. */
export function useDuration() {
  const { t } = useTranslations()
  return (e: { started_at: string | null; finished_at: string | null }) => {
    if (!e.started_at || !e.finished_at) return ""
    const ms = new Date(e.finished_at).getTime() - new Date(e.started_at).getTime()
    if (!Number.isFinite(ms) || ms < 0) return ""
    return ms < 60_000
      ? t("canvas.exec.seconds", { n: (ms / 1000).toFixed(1) })
      : t("canvas.exec.minutes", { n: Math.round(ms / 60_000) })
  }
}

export function ExecutionsView({
  workflowId,
  runId,
  onSelectRun,
  saved,
  vertical,
}: {
  workflowId: string
  runId: string | null
  onSelectRun: (id: string | null) => void
  saved?: WorkflowLayout
  vertical: boolean
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const list = useExecutions(ws.id, workflowId)
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "run" || e.kind === "schedule") void list.mutate()
  })
  const executions = (list.data ?? []).flatMap((p) => p.executions)
  const more = !!list.data?.[list.data.length - 1]?.next_before
  const when = useMemo(
    () => new Intl.DateTimeFormat(intlTag(locale), { dateStyle: "medium", timeStyle: "medium" }),
    [locale],
  )
  const duration = useDuration()

  // The newest run is shown when none is chosen, as n8n opens the tab.
  const shown = runId ?? (vertical ? null : (executions[0]?.id ?? null))

  const listPane = (
    <aside
      className={cn(
        "flex min-h-0 flex-col border-nq-line md:w-80 md:shrink-0 md:border-e",
        shown && vertical ? "hidden" : "flex-1 md:flex-none",
      )}
      data-executions-list
    >
      <div className="border-b border-nq-line px-4 py-3">
        <h2 className="text-sm font-medium">{t("canvas.exec.title")}</h2>
        <p className="text-xs text-muted-foreground">{t("canvas.exec.hint")}</p>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {list.error ? (
          <ErrorState error={list.error} />
        ) : !list.data ? (
          <LoadingRows rows={4} />
        ) : executions.length === 0 ? (
          <EmptyState title={t("canvas.exec.none")} body={t("canvas.exec.noneBody")} />
        ) : (
          <ul className="divide-y divide-nq-line">
            {executions.map((e) => (
              <li key={e.id}>
                <button
                  type="button"
                  onClick={() => onSelectRun(e.id)}
                  className={cn(
                    "flex w-full items-start gap-3 px-4 py-3 text-start transition-colors hover:bg-nq-surface-soft",
                    e.id === shown && "bg-nq-surface-soft",
                  )}
                  aria-current={e.id === shown ? "true" : undefined}
                  data-execution={e.id}
                  data-execution-status={e.status}
                >
                  <ExecutionIcon status={e.status} className="mt-0.5" />
                  <span className="min-w-0 flex-1">
                    <span className="block text-sm font-medium">
                      {when.format(new Date(e.created_at))}
                    </span>
                    <span className="block text-xs text-muted-foreground">
                      {t(`automation.status.${e.status}`)}
                      {duration(e) ? ` · ${duration(e)}` : ""}
                      {` · ${t("automation.stepsDone", { ok: e.ok_count, total: e.step_count })}`}
                    </span>
                    <span className="mt-1 flex flex-wrap gap-1">
                      <Badge variant="outline">{t(`canvas.exec.trigger.${e.trigger}`)}</Badge>
                      {e.agent_name ? (
                        <Badge variant="secondary">
                          <span dir="auto">{e.agent_name}</span>
                        </Badge>
                      ) : null}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
        {more ? (
          <div className="p-3">
            <Button
              variant="outline"
              size="sm"
              className="w-full"
              onClick={() => list.setSize(list.size + 1)}
            >
              {t("common.loadMore")}
            </Button>
          </div>
        ) : null}
      </div>
    </aside>
  )

  return (
    <div className="flex min-h-0 flex-1 flex-col md:flex-row" data-executions-view>
      {listPane}
      {shown ? (
        <Replay
          key={shown}
          runId={shown}
          execution={executions.find((e) => e.id === shown)}
          saved={saved}
          vertical={vertical}
          onBack={() => onSelectRun(null)}
        />
      ) : !vertical ? (
        <div className="flex flex-1 items-center justify-center p-6 text-sm text-muted-foreground">
          {list.data ? t("canvas.exec.pick") : null}
        </div>
      ) : null}
    </div>
  )
}

function Replay({
  runId,
  execution,
  saved,
  vertical,
  onBack,
}: {
  runId: string
  execution?: WorkflowExecution
  saved?: WorkflowLayout
  vertical: boolean
  onBack: () => void
}) {
  const { t, locale, isRtl } = useTranslations()
  const ws = useWorkspace()
  const detail = useRun(ws.id, runId)
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "run") void detail.mutate()
  })
  const run = detail.data?.run
  const steps = useMemo(() => stepsOfRun(detail.data?.steps ?? []), [detail.data?.steps])
  const final = useMemo(
    () => nodeRuns(detail.data?.steps ?? [], run?.status ?? ""),
    [detail.data?.steps, run?.status],
  )
  const layout = useMemo(
    () => (steps.length ? resolveLayout(steps, saved, vertical) : autoLayout(0, vertical)),
    [steps, saved, vertical],
  )
  const [selected, setSelected] = useState<Selection>(null)
  const [tab, setTab] = useState<PanelTab>("output")
  const duration = useDuration()

  // ---- replay ----
  const [at, setAt] = useState<number | null>(null)
  const playing = at !== null
  useEffect(() => {
    if (at === null || !detail.data) return
    if (at >= detail.data.steps.length) {
      const done = setTimeout(() => setAt(null), 600)
      return () => clearTimeout(done)
    }
    const s = detail.data.steps[at]
    const ms = Math.min(Math.max(s?.duration_ms ?? 400, 350), 1400)
    const id = setTimeout(() => setAt((n) => (n === null ? null : n + 1)), ms)
    return () => clearTimeout(id)
  }, [at, detail.data])
  const runs = useMemo<Record<number, NodeRun>>(() => {
    if (at === null) return final
    const out: Record<number, NodeRun> = {}
    for (const [k, v] of Object.entries(final)) {
      const i = Number(k)
      if (i < at) out[i] = v
      else if (i === at && v.state !== "skipped" && v.state !== "pending")
        out[i] = { state: "running" }
    }
    return out
  }, [final, at])

  const output =
    typeof selected === "number"
      ? detail.data?.steps.find((s) => s.position === selected)
      : undefined

  return (
    <div
      className="relative flex min-h-0 min-w-0 flex-1 flex-col"
      data-replay={runId}
      data-replay-playing={playing ? "true" : "false"}
    >
      <div className="flex flex-wrap items-center gap-2 border-b border-nq-line px-4 py-2">
        {vertical ? (
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={onBack}
            aria-label={t("canvas.exec.back")}
          >
            <ArrowLeftIcon className="rtl:rotate-180" />
          </Button>
        ) : null}
        {run ? (
          <>
            <ExecutionIcon status={run.status} />
            <span className="text-sm font-medium">{t(`automation.status.${run.status}`)}</span>
            <span className="text-xs text-muted-foreground">
              {execution ? duration(execution) : ""}
              {run.agent_name ? ` · ${run.agent_name}` : ""}
            </span>
          </>
        ) : null}
        <div className="ms-auto flex items-center gap-1">
          <Button
            variant="outline"
            size="sm"
            onClick={() => (playing ? setAt(null) : setAt(0))}
            disabled={!detail.data || steps.length === 0}
            data-replay-button
          >
            {playing ? <SquareIcon /> : <PlayIcon />}
            {playing ? t("canvas.exec.stop") : t("canvas.exec.replay")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            nativeButton={false}
            render={<Link href={`/${locale}/w/${ws.slug}/automation/runs/${runId}`} />}
          >
            <ExternalLinkIcon />
            <span className="hidden sm:inline">{t("canvas.exec.details")}</span>
          </Button>
        </div>
      </div>
      {run?.error ? (
        <p className="border-b border-nq-line px-4 py-2 text-xs text-nq-danger-text" dir="auto">
          {run.error}
        </p>
      ) : null}
      <div className="relative flex min-h-0 flex-1">
        <div className="relative min-h-[320px] min-w-0 flex-1">
          {detail.error ? (
            <ErrorState error={detail.error} />
          ) : !detail.data ? (
            <LoadingRows rows={3} />
          ) : (
            <WorkflowCanvas
              steps={steps}
              layout={layout}
              runs={runs}
              triggerState={run && run.status !== "queued" ? "ok" : undefined}
              trigger={{
                title: t(`canvas.exec.trigger.${run?.trigger ?? "manual"}`),
                subtitle: t("canvas.exec.startedBy"),
              }}
              selected={selected}
              readOnly
              vertical={vertical}
              onSelect={(s) => {
                setSelected(s === "trigger" ? null : s)
                setTab("output")
              }}
            />
          )}
        </div>
        {typeof selected === "number" && steps[selected] ? (
          <div
            className={cn(
              "z-20 min-h-0 border-nq-line bg-background",
              vertical ? "absolute inset-0" : "w-[26rem] shrink-0 border-s",
            )}
            dir={isRtl ? "rtl" : "ltr"}
          >
            <StepPanel
              step={steps[selected]}
              index={selected}
              readOnly
              tab={tab}
              onTab={setTab}
              onChange={() => {}}
              onClose={() => setSelected(null)}
              output={output}
              runId={runId}
              agentId=""
              pageUrl=""
            />
          </div>
        ) : null}
      </div>
    </div>
  )
}
