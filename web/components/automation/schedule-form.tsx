"use client"

/*
Adding a schedule (MH-403).

A schedule runs a SAVED WORKFLOW, filled with values chosen here. The form used
to post a raw JSON array of steps, while the API already took a workflow and
its values; it now offers the workflows by name and asks for exactly the values
each one needs. Secret values are typed hidden, sealed by the server, and never
shown again — the schedule list says "set".

"When" is a preset — every hour, every day at, weekdays at, every week on —
with a custom cron expression for anything else, and the next five run times
are shown before saving, in the time zone the schedule will run in (the
workspace's by default). "0 7 * * 1-5" is not something anyone should have to
check in their head.

The same form edits a schedule (MH-471): its time, workflow and values change
in place instead of deleting it and typing the password again. A secret value
already saved is not shown; left blank it is kept, typed over it is replaced.
*/

import Link from "next/link"
import { useEffect, useMemo, useState, type FormEvent } from "react"
import { CheckIcon, PlusIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { PickSelect } from "@/components/crm/shared"
import { Ltr } from "@/components/copy-field"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  type AutomationJob,
  type AutomationSchedule,
  type AutomationWorkflow,
  type BrowserAgent,
  type SchedulePreview,
  type WorkflowParam,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"

type Preset = "hourly" | "daily" | "weekdays" | "weekly" | "custom"

export function buildCron(
  preset: Preset,
  time: string,
  minute: string,
  day: string,
  custom: string,
): string {
  const [h, m] = (time || "07:00").split(":").map((x) => Number(x) || 0)
  switch (preset) {
    case "hourly":
      return `${Math.min(59, Math.max(0, Number(minute) || 0))} * * * *`
    case "daily":
      return `${m} ${h} * * *`
    case "weekdays":
      return `${m} ${h} * * 1-5`
    case "weekly":
      return `${m} ${h} * * ${day}`
    default:
      return custom.trim()
  }
}

type When = { preset: Preset; time: string; minute: string; day: string; custom: string }

const pad2 = (n: string) => n.padStart(2, "0")

/** Reads a saved cron expression back into the form's presets; anything else is "custom". */
export function parseCron(cron: string): When {
  const base: When = { preset: "custom", time: "07:00", minute: "0", day: "1", custom: cron }
  const f = cron.trim().split(/\s+/)
  if (f.length !== 5) return base
  const [m, h, dom, mon, dow] = f
  const num = (x: string, max: number) => /^\d{1,2}$/.test(x) && Number(x) <= max
  if (!num(m, 59) || dom !== "*" || mon !== "*") return base
  if (h === "*" && dow === "*") return { ...base, preset: "hourly", minute: String(Number(m)) }
  if (!num(h, 23)) return base
  const time = `${pad2(String(Number(h)))}:${pad2(String(Number(m)))}`
  if (dow === "*") return { ...base, preset: "daily", time }
  if (dow === "1-5") return { ...base, preset: "weekdays", time }
  if (/^[0-6]$/.test(dow)) return { ...base, preset: "weekly", time, day: dow }
  return base
}

export function ScheduleForm({
  browsers,
  workflows,
  jobs = [],
  onAdded,
  editing,
  onCancel,
}: {
  browsers: BrowserAgent[]
  workflows: AutomationWorkflow[]
  /** The server jobs this member may schedule (MH-478). */
  jobs?: AutomationJob[]
  onAdded: () => void
  /** The schedule being edited; without it the form adds a new one. */
  editing?: AutomationSchedule
  onCancel?: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const initial = editing ? parseCron(editing.cron) : null
  // A schedule of its own steps (from before MH-403) has no workflow to edit.
  const ownSteps = !!editing && !editing.workflow_id && !editing.job_key
  // Ids per form: an edit form can be open beside the add form.
  const idp = editing ? `sce-${editing.id}` : "sc"
  const [name, setName] = useState(editing?.name ?? "")
  // "" = any connected browser of the workspace (MH-478).
  const [agent, setAgent] = useState(editing?.agent_id ?? "")
  const allowedJobs = jobs.filter((j) => j.allowed)
  const [kind, setKind] = useState<"workflow" | "job">(
    editing
      ? editing.job_key
        ? "job"
        : "workflow"
      : workflows.length === 0 && allowedJobs.length > 0
        ? "job"
        : "workflow",
  )
  const [jobKey, setJobKey] = useState(editing?.job_key || allowedJobs[0]?.key || "")
  const [workflowId, setWorkflowId] = useState(
    editing ? editing.workflow_id : (workflows[0]?.id ?? ""),
  )
  const [values, setValues] = useState<Record<string, string>>(editing?.values ?? {})
  const [preset, setPreset] = useState<Preset>(initial?.preset ?? "weekdays")
  const [time, setTime] = useState(initial?.time ?? "07:00")
  const [minute, setMinute] = useState(initial?.minute ?? "0")
  const [day, setDay] = useState(initial?.day ?? "1")
  const [custom, setCustom] = useState(initial?.custom ?? "0 7 * * *")
  const workspaceZone = ws.current?.workspace.timezone || "UTC"
  const [zone, setZone] = useState(editing?.timezone ?? workspaceZone)
  const [catchUp, setCatchUp] = useState<string>(editing?.catch_up ?? "skip")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [preview, setPreview] = useState<SchedulePreview | null>(null)

  // The workspace's zone arrives after the first render; follow it until
  // someone types their own.
  const [zoneTouched, setZoneTouched] = useState(!!editing)
  useEffect(() => {
    if (!zoneTouched) setZone(workspaceZone)
  }, [workspaceZone, zoneTouched])

  const workflow = kind === "workflow" ? workflows.find((w) => w.id === workflowId) : undefined
  const job = kind === "job" ? allowedJobs.find((j) => j.key === jobKey) : undefined
  // Secrets this schedule already holds. Switching to another workflow keeps a
  // secret of the same name, as the API does.
  const sealed = new Set(editing?.secret_params ?? [])
  const fields: WorkflowParam[] = useMemo(() => {
    if (job) return job.params
    if (!workflow) return []
    return workflow.placeholders.map(
      (n) => workflow.params.find((p) => p.name === n) ?? { name: n, required: true },
    )
  }, [workflow, job])

  const cron = buildCron(preset, time, minute, day, custom)

  useEffect(() => {
    if (!cron) {
      setPreview(null)
      return
    }
    const ctl = new AbortController()
    const timer = setTimeout(() => {
      api<SchedulePreview>(
        `/api/w/schedules/preview?cron=${encodeURIComponent(cron)}&timezone=${encodeURIComponent(zone.trim())}`,
        {
          workspaceId: ws.id,
          signal: ctl.signal,
        },
      )
        .then(setPreview)
        .catch(() => {})
    }, 250)
    return () => {
      clearTimeout(timer)
      ctl.abort()
    }
  }, [cron, zone, ws.id])

  const fmt = useMemo(() => {
    try {
      return new Intl.DateTimeFormat(intlTag(locale), {
        weekday: "long",
        day: "numeric",
        month: "short",
        hour: "2-digit",
        minute: "2-digit",
        timeZone: preview?.timezone || "UTC",
      })
    } catch {
      return new Intl.DateTimeFormat(intlTag(locale), { dateStyle: "full", timeStyle: "short" })
    }
  }, [locale, preview?.timezone])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setErr(null)
    if (kind === "workflow" && !workflow && !ownSteps)
      return setErr(t("automation.sched.pickWorkflow"))
    if (kind === "job" && !job) return setErr(t("automation.sched.pickJob"))
    setBusy(true)
    try {
      const filled: Record<string, string> = {}
      for (const f of fields) {
        const v = values[f.name] ?? (f.secret ? "" : (f.default ?? ""))
        // A blank secret is left out: on an edit that keeps the sealed value.
        if (v !== "") filled[f.name] = v
      }
      const when = {
        name: name.trim(),
        agent_id: agent,
        cron,
        timezone: zone.trim(),
        catch_up: catchUp,
      }
      if (editing) {
        await api(`/api/w/schedules/${encodeURIComponent(editing.id)}`, {
          method: "PATCH",
          json:
            ownSteps || !workflow ? when : { ...when, workflow_id: workflow.id, values: filled },
          workspaceId: ws.id,
        })
        toast.success(t("automation.sched.updated"))
        onAdded()
        return
      }
      if (!workflow && !job) return
      await api("/api/w/schedules", {
        json: job
          ? {
              name: name.trim(),
              cron,
              timezone: zone.trim(),
              catch_up: catchUp,
              values: filled,
              job_key: job.key,
            }
          : { ...when, workflow_id: workflow!.id, values: filled },
        workspaceId: ws.id,
      })
      toast.success(t("automation.scheduleCreated"))
      setName("")
      setValues({})
      onAdded()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  if (workflows.length === 0 && allowedJobs.length === 0 && !ownSteps) {
    return (
      <p className="px-6 pt-6 text-sm text-grid-muted" data-schedule-needs-workflow>
        {t("automation.sched.noWorkflows")}{" "}
        <Link className="underline" href={`/${locale}/w/${ws.slug}/automation/workflows/new`}>
          {t("automation.editor.newWorkflow")}
        </Link>
      </p>
    )
  }

  const days = ["0", "1", "2", "3", "4", "5", "6"]

  return (
    <form
      onSubmit={submit}
      noValidate
      className={editing ? "px-6 pt-2 pb-6" : "px-6 pt-6"}
      data-form={editing ? "schedule-edit" : "schedule"}
      aria-label={editing ? t("automation.sched.editTitle", { name: editing.name }) : undefined}
    >
      <FieldGroup>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor={`${idp}-name`}>{t("automation.scheduleName")}</FieldLabel>
            <Input
              id={`${idp}-name`}
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={80}
            />
          </Field>
          {/* What a schedule runs is chosen when it is made; an edit keeps it. */}
          {editing ? null : (
            <Field>
              <FieldLabel htmlFor={`${idp}-kind`}>{t("automation.sched.kind")}</FieldLabel>
              <PickSelect
                id={`${idp}-kind`}
                value={kind}
                onChange={(v) => {
                  setKind(v as "workflow" | "job")
                  setValues({})
                }}
                options={[
                  ...(workflows.length > 0
                    ? [{ value: "workflow", label: t("automation.sched.kindWorkflow") }]
                    : []),
                  ...(allowedJobs.length > 0
                    ? [{ value: "job", label: t("automation.sched.kindJob") }]
                    : []),
                ]}
              />
              <FieldDescription>
                {kind === "job"
                  ? t("automation.sched.kindJobHint")
                  : t("automation.sched.kindWorkflowHint")}
              </FieldDescription>
            </Field>
          )}
          {ownSteps ? (
            <p className="text-sm text-grid-muted sm:col-span-2" data-schedule-own-steps>
              {t("automation.sched.ownSteps")}
            </p>
          ) : kind === "workflow" ? (
            <>
              <Field>
                <FieldLabel htmlFor={`${idp}-workflow`}>
                  {t("automation.sched.workflow")}
                </FieldLabel>
                <PickSelect
                  id={`${idp}-workflow`}
                  value={workflowId}
                  onChange={(v) => {
                    setWorkflowId(v)
                    setValues({})
                  }}
                  options={workflows.map((w) => ({
                    value: w.id,
                    label: w.enabled === false ? `${w.name} (${t("automation.wf.off")})` : w.name,
                  }))}
                />
                <FieldDescription>{t("automation.sched.workflowHint")}</FieldDescription>
              </Field>
            </>
          ) : (
            <Field>
              <FieldLabel htmlFor={`${idp}-job`}>{t("automation.sched.job")}</FieldLabel>
              <PickSelect
                id={`${idp}-job`}
                value={jobKey}
                onChange={(v) => {
                  setJobKey(v)
                  setValues({})
                }}
                options={allowedJobs.map((j) => ({
                  value: j.key,
                  label: t(`automation.job.${j.key}`),
                }))}
              />
              <FieldDescription>{job ? t(`automation.jobHint.${job.key}`) : ""}</FieldDescription>
            </Field>
          )}
          {kind === "workflow" ? (
            <Field>
              <FieldLabel htmlFor={`${idp}-agent`}>{t("automation.editor.browser")}</FieldLabel>
              <PickSelect
                id={`${idp}-agent`}
                value={agent}
                onChange={setAgent}
                noneLabel={t("automation.sched.anyBrowser")}
                options={browsers.map((b) => ({ value: b.id, label: b.name }))}
              />
              <FieldDescription>
                {agent
                  ? t("automation.sched.namedBrowserHint")
                  : t("automation.sched.anyBrowserHint")}
              </FieldDescription>
            </Field>
          ) : null}
          {fields.map((f) => (
            <Field key={`${kind}-${workflowId}-${jobKey}-${f.name}`}>
              <FieldLabel htmlFor={`${idp}-val-${f.name}`}>
                {job ? t(`automation.jobParam.${f.name}`) : f.label || f.name}
                {f.required ? "" : ` (${t("common.optional")})`}
              </FieldLabel>
              <Input
                id={`${idp}-val-${f.name}`}
                type={f.secret ? "password" : "text"}
                autoComplete={f.secret ? "new-password" : "off"}
                value={values[f.name] ?? (f.secret ? "" : (f.default ?? ""))}
                placeholder={
                  f.secret && sealed.has(f.name)
                    ? t("automation.sched.secretKeepPlaceholder")
                    : undefined
                }
                data-secret-kept={f.secret && sealed.has(f.name) ? "true" : undefined}
                onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}
              />
              {f.secret ? (
                <FieldDescription>
                  {sealed.has(f.name)
                    ? t("automation.sched.secretKeep")
                    : t("automation.sched.secretHint")}
                </FieldDescription>
              ) : f.help ? (
                <FieldDescription>{f.help}</FieldDescription>
              ) : null}
            </Field>
          ))}
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor={`${idp}-preset`}>{t("automation.cron")}</FieldLabel>
            <PickSelect
              id={`${idp}-preset`}
              value={preset}
              onChange={(v) => setPreset(v as Preset)}
              options={(["hourly", "daily", "weekdays", "weekly", "custom"] as Preset[]).map(
                (p) => ({ value: p, label: t(`automation.sched.preset.${p}`) }),
              )}
            />
          </Field>
          {preset === "hourly" ? (
            <Field>
              <FieldLabel htmlFor={`${idp}-minute`}>{t("automation.sched.minute")}</FieldLabel>
              <Input
                id={`${idp}-minute`}
                dir="ltr"
                inputMode="numeric"
                value={minute}
                onChange={(e) => setMinute(e.target.value.replace(/[^0-9]/g, "").slice(0, 2))}
              />
            </Field>
          ) : preset === "custom" ? (
            <Field>
              <FieldLabel htmlFor={`${idp}-cron`}>{t("automation.sched.expression")}</FieldLabel>
              <Input
                id={`${idp}-cron`}
                dir="ltr"
                className="font-mono"
                value={custom}
                onChange={(e) => setCustom(e.target.value)}
              />
              <FieldDescription>{t("automation.cronHint")}</FieldDescription>
            </Field>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2">
              {preset === "weekly" ? (
                <Field>
                  <FieldLabel htmlFor={`${idp}-day`}>{t("automation.sched.day")}</FieldLabel>
                  <PickSelect
                    id={`${idp}-day`}
                    value={day}
                    onChange={setDay}
                    options={days.map((d) => ({ value: d, label: t(`automation.sched.dow.${d}`) }))}
                  />
                </Field>
              ) : null}
              <Field>
                <FieldLabel htmlFor={`${idp}-time`}>{t("automation.sched.at")}</FieldLabel>
                <Input
                  id={`${idp}-time`}
                  type="time"
                  dir="ltr"
                  value={time}
                  onChange={(e) => setTime(e.target.value)}
                />
              </Field>
            </div>
          )}
          <Field>
            <FieldLabel htmlFor={`${idp}-zone`}>{t("automation.timezone")}</FieldLabel>
            <Input
              id={`${idp}-zone`}
              dir="ltr"
              value={zone}
              onChange={(e) => {
                setZoneTouched(true)
                setZone(e.target.value)
              }}
            />
            <FieldDescription>
              {t("automation.sched.zoneHint", { zone: workspaceZone })}
            </FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor={`${idp}-catchup`}>{t("automation.catchUpLabel")}</FieldLabel>
            <PickSelect
              id={`${idp}-catchup`}
              value={catchUp}
              onChange={setCatchUp}
              options={[
                { value: "skip", label: t("automation.catchUp.skip") },
                { value: "once", label: t("automation.catchUp.once") },
              ]}
            />
            <FieldDescription>{t("automation.catchUpHint")}</FieldDescription>
          </Field>
        </div>

        <div
          className="border border-line bg-grid-card px-4 py-3"
          data-preview={preview?.valid ? "valid" : "invalid"}
        >
          <p className="text-sm font-medium">
            {t("automation.sched.nextFive")}{" "}
            <Ltr mono className="text-xs text-grid-muted">
              {cron || "—"}
            </Ltr>
            {preview?.timezone ? (
              <span className="text-xs text-grid-muted"> · {preview.timezone}</span>
            ) : null}
          </p>
          {preview && !preview.valid ? (
            <p className="mt-1 text-sm text-grid-danger-text">{preview.error}</p>
          ) : (
            <ol className="mt-1 space-y-0.5 text-sm text-grid-muted" data-next-runs>
              {(preview?.times ?? []).map((at) => (
                <li key={at}>{fmt.format(new Date(at))}</li>
              ))}
            </ol>
          )}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" disabled={busy || !name.trim() || !preview?.valid}>
            {editing ? <CheckIcon /> : <PlusIcon />}
            {busy
              ? t("common.working")
              : editing
                ? t("automation.sched.saveChanges")
                : t("automation.addSchedule")}
          </Button>
          {onCancel ? (
            <Button type="button" variant="ghost" onClick={onCancel}>
              <XIcon />
              {t("common.cancel")}
            </Button>
          ) : null}
        </div>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}
