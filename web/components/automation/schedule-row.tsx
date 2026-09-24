"use client"

/* One schedule's row, split out of the old combined Automation page (MH-526)
   so /automation/schedules can render it on its own. */

import { Fragment, useState } from "react"
import { PencilIcon, PlayIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { ScheduleForm } from "@/components/automation/schedule-form"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationJob, type AutomationSchedule, type AutomationWorkflow, type BrowserAgent } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { Bdi, isolate } from "@/components/bidi"

export function ScheduleRow({
  schedule,
  canRun,
  browsers,
  workflows,
  jobs,
  onChange,
}: {
  schedule: AutomationSchedule
  canRun: boolean
  browsers: BrowserAgent[]
  workflows: AutomationWorkflow[]
  jobs: AutomationJob[]
  onChange: () => void
}) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)
  // Edited in place (MH-471): the form opens under the row it changes.
  const [editing, setEditing] = useState(false)
  // The schedule's own browser stays offerable even if it is not in the
  // active list (revoked since): the API refuses it with a reason, rather than
  // the form silently switching it to another.
  const browserOptions = !schedule.agent_id || browsers.some((b) => b.id === schedule.agent_id)
    ? browsers
    : [{ id: schedule.agent_id, name: schedule.agent_name || t("automation.unnamedBrowser") } as BrowserAgent, ...browsers]

  async function toggle() {
    try {
      await api(`/api/w/schedules/${encodeURIComponent(schedule.id)}`, { method: "PATCH", json: { enabled: !schedule.enabled }, workspaceId: ws.id })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function runNow() {
    setBusy(true)
    try {
      await api(`/api/w/schedules/${encodeURIComponent(schedule.id)}/run`, { method: "POST", workspaceId: ws.id })
      toast.success(t("automation.runStarted"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    await api(`/api/w/schedules/${encodeURIComponent(schedule.id)}`, { method: "DELETE", workspaceId: ws.id })
    toast.success(t("automation.scheduleDeleted"))
    onChange()
  }

  return (
    <li data-schedule={schedule.name} data-schedule-status={schedule.last_status || "new"} data-schedule-editing={editing ? "true" : undefined}>
    <div className="flex flex-wrap items-start gap-3 px-6 py-4">
      {/* basis-64: on a phone the actions wrap below instead of squeezing this column to one word a line. */}
      <div className="min-w-0 flex-1 basis-64">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          <Bdi>{schedule.name}</Bdi>
          <Badge variant={schedule.enabled ? "default" : "outline"}>{schedule.enabled ? t("automation.scheduleOn") : t("automation.scheduleOff")}</Badge>
          {schedule.last_status ? <Badge variant="secondary">{t(`automation.last.${schedule.last_status}`)}</Badge> : null}
          {/* Five failures in a row (MH-478): said on the row, not left for someone to count. */}
          {schedule.failing ? (
            <Badge variant="destructive" data-schedule-failing>
              {t("automation.sched.failing", { n: schedule.failure_count })}
            </Badge>
          ) : null}
        </p>
        {schedule.job_key ? (
          <p className="text-xs text-grid-muted" data-schedule-job={schedule.job_key}>
            {t("automation.sched.runsJob", { name: t(`automation.job.${schedule.job_key}`) })}
            {Object.entries(schedule.values ?? {}).map(([k, v]) => ` · ${t(`automation.jobParam.${k}`)}: ${v}`)}
          </p>
        ) : null}
        {schedule.workflow_id ? (
          <p className="text-xs text-grid-muted" data-schedule-workflow>
            {t("automation.sched.runs", { name: schedule.workflow_name ? isolate(schedule.workflow_name) : t("automation.sched.deletedWorkflow") })}
            {/* Each "name: value" pair is its own isolated run (MH-482). Bare in an RTL line, the
                Latin pairs and the Arabic words around them reorder into one another. */}
            {Object.entries(schedule.values ?? {}).map(([k, v]) => (
              <Fragment key={`v-${k}`}>
                {" · "}
                <Bdi data-schedule-value={k}>{`${k}: ${v}`}</Bdi>
              </Fragment>
            ))}
            {(schedule.secret_params ?? []).map((k) => (
              <Fragment key={`s-${k}`}>
                {" · "}
                <Bdi data-schedule-value={k}>{`${k}: ${t("automation.sched.secretSet")}`}</Bdi>
              </Fragment>
            ))}
          </p>
        ) : null}
        <p className="text-xs text-grid-muted">
          <Ltr mono>{schedule.cron}</Ltr> · <Ltr>{schedule.timezone}</Ltr>
          {schedule.job_key ? null : (
            <>
              {" · "}
              {schedule.agent_id ? (schedule.agent_name ? <Bdi>{schedule.agent_name}</Bdi> : t("automation.unnamedBrowser")) : t("automation.sched.anyBrowser")}
            </>
          )}
          {" · "}
          {t(`automation.catchUp.${schedule.catch_up}`)}
          {schedule.missed_count > 0 ? ` · ${t("automation.sched.missedCount", { n: schedule.missed_count })}` : ""}
        </p>
        <p className="text-xs text-grid-muted">
          {schedule.next_run_at ? t("automation.nextRun", { date: formatDate(schedule.next_run_at) }) : ""}
          {schedule.last_run_at ? ` · ${t("automation.lastRun", { date: formatDate(schedule.last_run_at) })}` : ""}
        </p>
        {/* A miss is shown, not swallowed: "there is no data for Tuesday" has
            to have an answer on the page. */}
        {schedule.last_error ? (
          <p
            className={schedule.last_status === "ok" || schedule.last_status === "skipped" ? "mt-1 text-xs text-grid-muted" : "mt-1 text-xs text-grid-danger-text"}
            data-schedule-error
          >
            <Bdi>{schedule.last_error}</Bdi>
          </p>
        ) : null}
      </div>
      {canRun ? (
        <div className="flex items-center gap-2">
          <Switch checked={schedule.enabled} onCheckedChange={toggle} aria-label={schedule.enabled ? t("automation.scheduleOn") : t("automation.scheduleOff")} />
          <Button variant="ghost" size="sm" onClick={runNow} disabled={busy}>
            <PlayIcon />
            {busy ? t("common.working") : t("automation.runNow")}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => setEditing((v) => !v)} aria-expanded={editing} data-edit-schedule>
            {editing ? <XIcon /> : <PencilIcon />}
            {editing ? t("common.cancel") : t("common.edit")}
          </Button>
          <ConfirmButton
            label={t("common.delete")}
            title={t("automation.deleteScheduleTitle")}
            description={t("automation.deleteScheduleBody", { name: isolate(schedule.name) })}
            confirmLabel={t("common.delete")}
            onConfirm={remove}
          />
        </div>
      ) : null}
    </div>
      {canRun && editing ? (
        <ScheduleForm
          editing={schedule}
          browsers={browserOptions}
          workflows={workflows}
          jobs={jobs}
          onCancel={() => setEditing(false)}
          onAdded={() => {
            setEditing(false)
            onChange()
          }}
        />
      ) : null}
    </li>
  )
}
