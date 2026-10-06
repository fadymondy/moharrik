"use client"

import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { useEffect, useState } from "react"
import { ArrowLeftIcon, CircleAlertIcon, RotateCcwIcon, SquareIcon } from "lucide-react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Badge,
  Button,
  toast,
} from "@fadymondy/nasaq/web"

import { SectionHeader } from "@/components/public-frame"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type RunStep } from "@/lib/api"
import { RunNotes } from "@/components/automation/run-notes"
import { useTranslations } from "@/lib/i18n"
import { useRun, useWorkflow } from "@/lib/queries"
import { Pill, ms, runTone, useWhen } from "@/components/automation/workflow-overview"
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

  const router = useRouter()
  const when = useWhen()
  const wf = useWorkflow(ws.id, run?.workflow_id || undefined)
  const [rerunning, setRerunning] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const canRun = ws.can("automation:run")
  const live = run?.status === "queued" || run?.status === "running"

  async function cancel() {
    try {
      await api(`/api/w/runs/${encodeURIComponent(id)}/cancel`, {
        method: "POST",
        workspaceId: ws.id,
      })
      toast.success(t("automation.cancelled"))
      detail.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  // The same stored steps again, recorded as a new run linked to this one (MH-1067).
  async function rerun() {
    setRerunning(true)
    try {
      const out = await api<{ run_id: string }>(`/api/w/runs/${encodeURIComponent(id)}/rerun`, {
        method: "POST",
        workspaceId: ws.id,
        json: { confirm: confirmOpen },
      })
      toast.success(t("wfo.rerun.started"))
      setConfirmOpen(false)
      router.push(`/${locale}/w/${ws.slug}/automation/runs/${out.run_id}`)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setRerunning(false)
    }
  }
  const writes = !!wf.data?.workflow.writes

  const failedStep = detail.data?.steps.find((s) => s.status === "failed")
  const nothingRan = !!detail.data && detail.data.steps.every((s) => s.status === "pending")

  return (
    <>
      <SectionHeader
        title={t("automation.run")}
        description={
          run
            ? t("automation.runOn", {
                name: isolate(run.agent_name) || t("automation.unnamedBrowser"),
                date: formatDate(run.created_at),
              })
            : undefined
        }
        action={
          <div className="flex flex-wrap items-center gap-2">
            {canRun && run ? (
              <Button
                variant="secondary"
                size="sm"
                disabled={live || rerunning}
                title={live ? t("wfo.rerun.live") : t("wfo.rerun.hint")}
                onClick={() => (writes ? setConfirmOpen(true) : void rerun())}
                data-rerun
              >
                <RotateCcwIcon />
                {rerunning ? t("wfo.rerun.working") : t("wfo.rerun")}
              </Button>
            ) : null}
            {canRun && live ? (
              <Button variant="secondary" size="sm" onClick={cancel}>
                <SquareIcon />
                {t("automation.cancelRun")}
              </Button>
            ) : null}
            <Button
              variant="ghost"
              size="sm"
              nativeButton={false}
              render={<Link href={`/${locale}/w/${ws.slug}/automation/runs`} />}
            >
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
          <div
            className="flex flex-wrap items-center gap-x-3 gap-y-2 px-6 pb-4 text-sm"
            data-run-status={run.status}
            data-run-summary
          >
            <Pill tone={runTone(run.status)}>{t(`automation.status.${run.status}`)}</Pill>
            <span className="text-muted-foreground">
              {t("automation.stepsDone", { ok: run.ok_count, total: run.step_count })}
            </span>
            <span className="text-muted-foreground">
              · {t(`automation.trigger.${run.trigger}`)}
            </span>
            {run.workflow_id ? (
              <span className="text-muted-foreground" data-run-workflow>
                ·{" "}
                <Link
                  href={`/${locale}/w/${ws.slug}/automation/workflows/${run.workflow_id}`}
                  className="text-foreground underline-offset-2 hover:underline"
                >
                  <Bdi>{run.workflow_name}</Bdi>
                </Link>{" "}
                {t("wfo.run.version", { n: run.workflow_version })}
              </span>
            ) : null}
            {run.started_at && run.finished_at ? (
              <span className="text-muted-foreground" data-run-duration>
                · {t("wfo.run.duration")}{" "}
                {ms(new Date(run.finished_at).getTime() - new Date(run.started_at).getTime())}
              </span>
            ) : null}
            <span className="text-muted-foreground">· {when(run.created_at)}</span>
            {run.rerun_of ? (
              <span className="text-muted-foreground" data-rerun-of={run.rerun_of}>
                · {t("wfo.run.rerunOf")}{" "}
                <Link
                  href={`/${locale}/w/${ws.slug}/automation/runs/${run.rerun_of}`}
                  className="text-primary underline-offset-2 hover:underline"
                >
                  {t("automation.run")}
                </Link>
              </span>
            ) : null}
          </div>
          {run.status === "failed" ? (
            <section
              className="mx-6 mb-4 rounded-lg border border-nq-danger/40 bg-nq-danger-soft p-4 text-sm"
              data-run-failure={failedStep ? failedStep.position : "run"}
            >
              <p className="flex items-center gap-2 font-medium text-nq-danger-text">
                <CircleAlertIcon className="size-4 shrink-0" />
                {failedStep ? (
                  <span>
                    {t("wfo.run.failed.title", {
                      n: failedStep.position + 1,
                      command: failedStep.command,
                    })}
                  </span>
                ) : (
                  <span>
                    {nothingRan ? t("wfo.run.failed.before") : t("automation.status.failed")}
                  </span>
                )}
              </p>
              {failedStep?.error || run.error ? (
                <div className="mt-2">
                  <p className="text-xs font-medium text-muted-foreground">
                    {t("wfo.run.failed.why")}
                  </p>
                  <p className="break-words text-foreground" dir="auto">
                    <Bdi>{failedStep?.error || run.error}</Bdi>
                  </p>
                </div>
              ) : null}
              {failedStep ? (
                <div className="mt-2">
                  <p className="text-xs font-medium text-muted-foreground">
                    {t("wfo.run.failed.input")}
                  </p>
                  {hasArgs(failedStep.args) ? (
                    <pre
                      dir="ltr"
                      className="mt-1 overflow-x-auto rounded bg-card px-3 py-2 font-mono text-xs"
                    >
                      {failedStep.args}
                    </pre>
                  ) : (
                    <p className="text-muted-foreground">{t("wfo.run.failed.noInput")}</p>
                  )}
                </div>
              ) : null}
            </section>
          ) : null}
          <RunNotes notes={run.notes} className="space-y-1 px-6 pb-4" />
          {run.error && run.status !== "failed" ? (
            <p className="px-6 pb-4 text-sm text-nq-danger-text">
              <Bdi>{run.error}</Bdi>
            </p>
          ) : null}

          <ol className="divide-y divide-nq-line border-y border-nq-line">
            {detail.data.steps.map((s) => (
              <StepRow key={s.position} step={s} runId={id} workspaceId={ws.id} />
            ))}
          </ol>
        </>
      )}
      <div className="pb-8" />
      {/* A workflow that publishes asks first: only this confirm re-runs it. */}
      <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <AlertDialogContent data-confirm-rerun-dialog>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("wfo.rerun.confirmTitle", { name: isolate(run?.workflow_name ?? "") })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("wfo.rerun.confirmBody", { hosts: (wf.data?.workflow.hosts ?? []).join(", ") })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction disabled={rerunning} onClick={() => void rerun()} data-confirm-rerun>
              {rerunning ? t("wfo.rerun.working") : t("wfo.rerun.confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

/**
 * A step's screenshot. Fetched with the workspace header and shown as a blob:
 * the route is in the workspace tier, which a plain <img src> cannot reach.
 */
function StepShot({
  runId,
  position,
  workspaceId,
}: {
  runId: string
  position: number
  workspaceId: string
}) {
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
  if (failed)
    return <p className="mt-2 text-xs text-muted-foreground">{t("automation.shotGone")}</p>
  if (!src) return null
  return (
    <a
      href={src}
      target="_blank"
      rel="noreferrer"
      className="mt-2 block max-w-xl border border-nq-line"
      data-step-shot={position}
    >
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src={src} alt={t("automation.shotAlt", { n: position + 1 })} className="block w-full" />
    </a>
  )
}

const hasArgs = (a: string) => !!a && a !== "{}" && a !== "null"

function StepRow({
  step,
  runId,
  workspaceId,
}: {
  step: RunStep
  runId: string
  workspaceId: string
}) {
  const { t } = useTranslations()
  return (
    <li className="px-6 py-4" data-step={step.position} data-step-status={step.status}>
      <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
        <span className="text-muted-foreground">{step.position + 1}.</span>
        <span dir="ltr" className="font-mono">
          {step.command}
        </span>
        <Badge
          variant={
            step.status === "ok" ? "brand" : step.status === "pending" ? "neutral" : "outline"
          }
        >
          {t(`automation.step.${step.status}`)}
        </Badge>
        {step.duration_ms > 0 ? (
          <span className="text-xs text-muted-foreground">{step.duration_ms} ms</span>
        ) : null}
      </p>
      {hasArgs(step.args) ? (
        <div className="mt-2">
          <p className="text-xs font-medium text-muted-foreground">{t("wfo.step.input")}</p>
          <pre
            dir="ltr"
            className="mt-1 overflow-x-auto bg-card px-3 py-2 font-mono text-xs text-muted-foreground"
            data-step-input
          >
            {step.args}
          </pre>
        </div>
      ) : null}
      {step.error ? (
        <p className="mt-2 text-sm text-nq-danger-text" data-step-error>
          <span className="text-xs font-medium">{t("wfo.step.error")}: </span>
          <Bdi>{step.error}</Bdi>
        </p>
      ) : null}
      {step.has_screenshot ? (
        <StepShot runId={runId} position={step.position} workspaceId={workspaceId} />
      ) : null}
      {step.result || step.summary ? (
        <p className="mt-2 text-xs font-medium text-muted-foreground">{t("wfo.step.output")}</p>
      ) : null}
      {step.result ? (
        // Shown exactly as the browser sent it. Pretty-printing it here would
        // be a second representation of the thing whose whole point is that it
        // is byte-for-byte what came back.
        <pre dir="ltr" className="mt-2 max-h-64 overflow-auto bg-card px-3 py-2 font-mono text-xs">
          {step.result}
        </pre>
      ) : step.summary ? (
        <p className="mt-2 text-sm text-muted-foreground" dir="auto">
          {step.summary}
        </p>
      ) : null}
    </li>
  )
}
