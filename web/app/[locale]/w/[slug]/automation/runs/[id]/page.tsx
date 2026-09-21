"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { ArrowLeftIcon, SquareIcon } from "lucide-react"
import { toast } from "sonner"

import { SectionHeader } from "@/components/public-frame"
import { ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type RunStep } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useRun } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function RunPage() {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const params = useParams<{ id: string }>()
  const id = params.id
  const detail = useRun(ws.id, id)
  const run = detail.data?.run
  useDocumentTitle(`${t("automation.run")} · ${ws.summary.name}`)

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
        micro={ws.summary.name}
        title={t("automation.run")}
        description={run ? t("automation.runOn", { name: run.agent_name || t("automation.unnamedBrowser"), date: formatDate(run.created_at) }) : undefined}
        action={
          <div className="flex items-center gap-2">
            {canRun && live ? (
              <Button variant="outline" size="sm" onClick={cancel}>
                <SquareIcon />
                {t("automation.cancelRun")}
              </Button>
            ) : null}
            <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation`} />}>
              <ArrowLeftIcon className="rtl:rotate-180" />
              {t("automation.backToAutomation")}
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
          {run.error ? <p className="px-6 pb-4 text-sm text-grid-danger-text">{run.error}</p> : null}

          <ol className="divide-y divide-line border-y border-line">
            {detail.data.steps.map((s) => (
              <StepRow key={s.position} step={s} />
            ))}
          </ol>
        </>
      )}
      <div className="pb-8" />
    </>
  )
}

function StepRow({ step }: { step: RunStep }) {
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
      {step.args && step.args !== "{}" ? (
        <pre dir="ltr" className="mt-2 overflow-x-auto bg-grid-card px-3 py-2 font-mono text-xs text-grid-muted">
          {step.args}
        </pre>
      ) : null}
      {step.error ? <p className="mt-2 text-sm text-grid-danger-text">{step.error}</p> : null}
      {step.result ? (
        // Shown exactly as the browser sent it. Pretty-printing it here would
        // be a second representation of the thing whose whole point is that it
        // is byte-for-byte what came back.
        <pre dir="ltr" className="mt-2 max-h-64 overflow-auto bg-grid-card px-3 py-2 font-mono text-xs">
          {step.result}
        </pre>
      ) : step.summary ? (
        <p className="mt-2 text-sm text-grid-muted">{step.summary}</p>
      ) : null}
    </li>
  )
}
