"use client"

/* Starting an ad-hoc run and one run's row, split out of the old combined
   Automation page (MH-526) so /automation/runs can render them on its own. */

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { PlayIcon } from "lucide-react"
import { toast } from "sonner"

import { PickSelect } from "@/components/crm/shared"
import { StepEditor, fromEditorSteps, toEditorSteps, type EditorStep } from "@/components/automation/step-editor"
import { RunNotes } from "@/components/automation/run-notes"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldGroup } from "@/components/ui/field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationRun, type BrowserAgent } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { Bdi } from "@/components/bidi"

export function StartRun({ browsers, paused, onStarted }: { browsers: BrowserAgent[]; paused: boolean; onStarted: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [agent, setAgent] = useState(browsers[0]?.id ?? "")
  const [steps, setSteps] = useState<EditorStep[]>(() => toEditorSteps([{ command: "navigate", args: { url: "https://" } }]))
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setErr(null)
    const parsed = fromEditorSteps(steps)
    if (parsed.length === 0) {
      setErr(t("automation.stepsNotArray"))
      return
    }
    setBusy(true)
    try {
      await api<{ run_id: string }>("/api/w/runs", { json: { agent_id: agent, steps: parsed }, workspaceId: ws.id })
      toast.success(t("automation.runStarted"))
      onStarted()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const chosen = browsers.find((b) => b.id === agent)
  const offline = chosen?.status !== "online"

  return (
    <form onSubmit={submit} noValidate className="px-6 pb-6" data-form="run">
      <FieldGroup>
        <div className="flex flex-wrap items-end gap-3">
          <div className="w-56">
            <PickSelect
              id="run-agent"
              value={agent}
              onChange={setAgent}
              options={browsers.map((b) => ({ value: b.id, label: `${b.name} (${b.status === "online" ? t("automation.online") : t("automation.offline")})` }))}
            />
          </div>
          <Button type="submit" disabled={busy || paused || offline}>
            <PlayIcon />
            {busy ? t("common.working") : t("automation.startRun")}
          </Button>
          {/* Say why the button is off rather than leaving a dead control. */}
          {paused ? (
            <span className="text-xs text-grid-muted">{t("automation.pausedTitle")}</span>
          ) : offline ? (
            <span className="text-xs text-grid-muted">{t("automation.offlineHint")}</span>
          ) : null}
        </div>
      </FieldGroup>
      <p className="pt-4 pb-2 text-sm text-grid-muted">{t("automation.stepsHint")}</p>
      <div className="-mx-6">
        <StepEditor steps={steps} onChange={setSteps} />
      </div>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}

export function RunRow({ run, href }: { run: AutomationRun; href: string }) {
  const { t, formatDate } = useTranslations()
  const tone: Record<string, "default" | "secondary" | "outline"> = {
    ok: "default",
    running: "secondary",
    queued: "secondary",
    failed: "outline",
    cancelled: "outline",
  }
  return (
    <li className="flex flex-wrap items-center gap-3 px-6 py-4" data-run={run.id} data-run-status={run.status}>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          <Link href={href} className="hover:underline">
            {run.agent_name ? <Bdi>{run.agent_name}</Bdi> : t("automation.unnamedBrowser")}
          </Link>
          <Badge variant={tone[run.status] ?? "outline"}>{t(`automation.status.${run.status}`)}</Badge>
        </p>
        <p className="text-xs text-grid-muted">
          {t("automation.stepsDone", { ok: run.ok_count, total: run.step_count })} · {t(`automation.trigger.${run.trigger}`)} · {formatDate(run.created_at)}
        </p>
        <RunNotes notes={run.notes} className="mt-1 space-y-0.5" />
        {run.error ? (
          <p className="mt-1 text-xs text-grid-danger-text">
            <Bdi>{run.error}</Bdi>
          </p>
        ) : null}
      </div>
      <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={href} />}>
        {t("automation.openRun")}
      </Button>
    </li>
  )
}
