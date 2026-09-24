"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { useEffect, useState } from "react"
import { ArrowLeftIcon, SquareIcon } from "lucide-react"
import { toast } from "sonner"

import { SectionHeader } from "@/components/public-frame"
import { ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type RunStep } from "@/lib/api"
import { RunNotes } from "@/components/automation/run-notes"
import { useTranslations } from "@/lib/i18n"
import { useRun } from "@/lib/queries"
import { useWorkspaceEvents } from "@/lib/events"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

export default function RunPage() {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const params = useParams<{ id: string }>()
  const id = params.id
  const detail = useRun(ws.id, id)
  const run = detail.data?.run
  useDocumentTitle(`${t("automation.run")} · ${ws.summary.name}`)

  // Each step lands as it happens (MH-478): the server says a run moved.
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "run" || e.kind === "schedule") void detail.mutate()
  })

  const canRun = ws.can("automation:run")
  const live = run?.status === "queued" || run?.status === "running"

  async function cancel() {
    try {
      await api(`/api/w/runs/${encodeURIComponent(id)}/cancel`, { method: "POST", workspaceId: ws.id })
      toast.success(t("automation.cancelled"))
      detail.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("automation.run")}
        description={run ? t("automation.runOn", { name: isolate(run.agent_name) || t("automation.unnamedBrowser"), date: formatDate(run.created_at) }) : undefined}
        action={
          <div className="flex flex-wrap items-center gap-2">
            {canRun && live ? (
              <Button variant="outline" size="sm" onClick={cancel}>
                <SquareIcon />
                {t("automation.cancelRun")}
              </Button>
            ) : null}
            <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation/runs`} />}>
              <ArrowLeftIcon className="rtl:rotate-180" />
              {t("automation.backToRuns")}
            </Button>
          </div>
        }
      />

      {detail.error ? (
        <ErrorState error={detail.error} />
      ) : !detail.data || !run ? (
        <LoadingRows rows={4} />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2 px-6 pb-4 text-sm" data-run-status={run.status}>
            <Badge variant={run.status === "ok" ? "default" : run.status === "running" || run.status === "queued" ? "secondary" : "outline"}>
              {t(`automation.status.${run.status}`)}
            </Badge>
            <span className="text-grid-muted">{t("automation.stepsDone", { ok: run.ok_count, total: run.step_count })}</span>
            <span className="text-grid-muted">· {t(`automation.trigger.${run.trigger}`)}</span>
          </div>
          <RunNotes notes={run.notes} className="space-y-1 px-6 pb-4" />
          {run.error ? (
            <p className="px-6 pb-4 text-sm text-grid-danger-text">
              <Bdi>{run.error}</Bdi>
            </p>
          ) : null}

          <ol className="divide-y divide-line border-y border-line">
            {detail.data.steps.map((s) => (
              <StepRow key={s.position} step={s} runId={id} workspaceId={ws.id} />
            ))}
          </ol>
        </>
      )}
      <div className="pb-8" />
    </>
  )
}

/**
 * A step's screenshot. Fetched with the workspace header and shown as a blob:
 * the route is in the workspace tier, which a plain <img src> cannot reach.
 */
function StepShot({ runId, position, workspaceId }: { runId: string; position: number; workspaceId: string }) {
  const { t } = useTranslations()
  const [src, setSrc] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let url: string | null = null
    let alive = true
    fetch(`/api/w/runs/${encodeURIComponent(runId)}/steps/${position}/screenshot`, {
      headers: { "X-Workspace-ID": workspaceId },
      credentials: "same-origin",
      cache: "no-store",
    })
      .then((r) => (r.ok ? r.blob() : Promise.reject(new Error(String(r.status)))))
      .then((b) => {
        url = URL.createObjectURL(b)
        if (alive) setSrc(url)
      })
      .catch(() => alive && setFailed(true))
    return () => {
      alive = false
      if (url) URL.revokeObjectURL(url)
    }
  }, [runId, position, workspaceId])
  if (failed) return <p className="mt-2 text-xs text-grid-muted">{t("automation.shotGone")}</p>
  if (!src) return null
  return (
    <a href={src} target="_blank" rel="noreferrer" className="mt-2 block max-w-xl border border-line" data-step-shot={position}>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src={src} alt={t("automation.shotAlt", { n: position + 1 })} className="block w-full" />
    </a>
  )
}

function StepRow({ step, runId, workspaceId }: { step: RunStep; runId: string; workspaceId: string }) {
  const { t } = useTranslations()
  return (
    <li className="px-6 py-4" data-step={step.position} data-step-status={step.status}>
      <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
        <span className="text-grid-muted">{step.position + 1}.</span>
        <span dir="ltr" className="font-mono">
          {step.command}
        </span>
        <Badge variant={step.status === "ok" ? "default" : step.status === "pending" ? "secondary" : "outline"}>
          {t(`automation.step.${step.status}`)}
        </Badge>
        {step.duration_ms > 0 ? <span className="text-xs text-grid-muted">{step.duration_ms} ms</span> : null}
      </p>
      {step.args && step.args !== "{}" && step.args !== "null" ? (
        <pre dir="ltr" className="mt-2 overflow-x-auto bg-grid-card px-3 py-2 font-mono text-xs text-grid-muted">
          {step.args}
        </pre>
      ) : null}
      {step.error ? (
        <p className="mt-2 text-sm text-grid-danger-text">
          <Bdi>{step.error}</Bdi>
        </p>
      ) : null}
      {step.has_screenshot ? <StepShot runId={runId} position={step.position} workspaceId={workspaceId} /> : null}
      {step.result ? (
        // Shown exactly as the browser sent it. Pretty-printing it here would
        // be a second representation of the thing whose whole point is that it
        // is byte-for-byte what came back.
        <pre dir="ltr" className="mt-2 max-h-64 overflow-auto bg-grid-card px-3 py-2 font-mono text-xs">
          {step.result}
        </pre>
      ) : step.summary ? (
        <p className="mt-2 text-sm text-grid-muted" dir="auto">
          {step.summary}
        </p>
      ) : null}
    </li>
  )
}
