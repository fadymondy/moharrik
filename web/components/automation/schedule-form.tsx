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
*/

import Link from "next/link"
import { useEffect, useMemo, useState, type FormEvent } from "react"
import { PlusIcon } from "lucide-react"
import { toast } from "sonner"

import { PickSelect } from "@/components/crm/shared"
import { Ltr } from "@/components/copy-field"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationWorkflow, type BrowserAgent, type SchedulePreview, type WorkflowParam } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"

type Preset = "hourly" | "daily" | "weekdays" | "weekly" | "custom"

export function buildCron(preset: Preset, time: string, minute: string, day: string, custom: string): string {
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

export function ScheduleForm({
  browsers,
  workflows,
  onAdded,
}: {
  browsers: BrowserAgent[]
  workflows: AutomationWorkflow[]
  onAdded: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [agent, setAgent] = useState(browsers[0]?.id ?? "")
  const [workflowId, setWorkflowId] = useState(workflows[0]?.id ?? "")
  const [values, setValues] = useState<Record<string, string>>({})
  const [preset, setPreset] = useState<Preset>("weekdays")
  const [time, setTime] = useState("07:00")
  const [minute, setMinute] = useState("0")
  const [day, setDay] = useState("1")
  const [custom, setCustom] = useState("0 7 * * *")
  const workspaceZone = ws.current?.workspace.timezone || "UTC"
  const [zone, setZone] = useState(workspaceZone)
  const [catchUp, setCatchUp] = useState("skip")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [preview, setPreview] = useState<SchedulePreview | null>(null)

  // The workspace's zone arrives after the first render; follow it until
  // someone types their own.
  const [zoneTouched, setZoneTouched] = useState(false)
  useEffect(() => {
    if (!zoneTouched) setZone(workspaceZone)
  }, [workspaceZone, zoneTouched])

  const workflow = workflows.find((w) => w.id === workflowId)
  const fields: WorkflowParam[] = useMemo(() => {
    if (!workflow) return []
    return workflow.placeholders.map((n) => workflow.params.find((p) => p.name === n) ?? { name: n, required: true })
  }, [workflow])

  const cron = buildCron(preset, time, minute, day, custom)

  useEffect(() => {
    if (!cron) {
      setPreview(null)
      return
    }
    const ctl = new AbortController()
    const timer = setTimeout(() => {
      api<SchedulePreview>(`/api/w/schedules/preview?cron=${encodeURIComponent(cron)}&timezone=${encodeURIComponent(zone.trim())}`, {
        workspaceId: ws.id,
        signal: ctl.signal,
      })
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
      return new Intl.DateTimeFormat(intlTag(locale), { weekday: "long", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", timeZone: preview?.timezone || "UTC" })
    } catch {
      return new Intl.DateTimeFormat(intlTag(locale), { dateStyle: "full", timeStyle: "short" })
    }
  }, [locale, preview?.timezone])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setErr(null)
    if (!workflow) return setErr(t("automation.sched.pickWorkflow"))
    setBusy(true)
    try {
      const filled: Record<string, string> = {}
      for (const f of fields) {
        const v = values[f.name] ?? (f.secret ? "" : (f.default ?? ""))
        if (v !== "") filled[f.name] = v
      }
      await api("/api/w/schedules", {
        json: { name: name.trim(), agent_id: agent, cron, timezone: zone.trim(), catch_up: catchUp, workflow_id: workflow.id, values: filled },
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

  if (workflows.length === 0) {
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
    <form onSubmit={submit} noValidate className="px-6 pt-6" data-form="schedule">
      <FieldGroup>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="sc-name">{t("automation.scheduleName")}</FieldLabel>
            <Input id="sc-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
          </Field>
          <Field>
            <FieldLabel htmlFor="sc-agent">{t("automation.editor.browser")}</FieldLabel>
            <PickSelect id="sc-agent" value={agent} onChange={setAgent} options={browsers.map((b) => ({ value: b.id, label: b.name }))} />
          </Field>
          <Field>
            <FieldLabel htmlFor="sc-workflow">{t("automation.sched.workflow")}</FieldLabel>
            <PickSelect
              id="sc-workflow"
              value={workflowId}
              onChange={(v) => {
                setWorkflowId(v)
                setValues({})
              }}
              options={workflows.map((w) => ({ value: w.id, label: w.name }))}
            />
            <FieldDescription>{t("automation.sched.workflowHint")}</FieldDescription>
          </Field>
          {fields.map((f) => (
            <Field key={`${workflowId}-${f.name}`}>
              <FieldLabel htmlFor={`sc-val-${f.name}`}>
                {f.label || f.name}
                {f.required ? "" : ` (${t("common.optional")})`}
              </FieldLabel>
              <Input
                id={`sc-val-${f.name}`}
                type={f.secret ? "password" : "text"}
                autoComplete={f.secret ? "new-password" : "off"}
                value={values[f.name] ?? (f.secret ? "" : (f.default ?? ""))}
                onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}
              />
              {f.secret ? <FieldDescription>{t("automation.sched.secretHint")}</FieldDescription> : null}
            </Field>
          ))}
        </div>

        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="sc-preset">{t("automation.cron")}</FieldLabel>
            <PickSelect
              id="sc-preset"
              value={preset}
              onChange={(v) => setPreset(v as Preset)}
              options={(["hourly", "daily", "weekdays", "weekly", "custom"] as Preset[]).map((p) => ({ value: p, label: t(`automation.sched.preset.${p}`) }))}
            />
          </Field>
          {preset === "hourly" ? (
            <Field>
              <FieldLabel htmlFor="sc-minute">{t("automation.sched.minute")}</FieldLabel>
              <Input id="sc-minute" dir="ltr" inputMode="numeric" value={minute} onChange={(e) => setMinute(e.target.value.replace(/[^0-9]/g, "").slice(0, 2))} />
            </Field>
          ) : preset === "custom" ? (
            <Field>
              <FieldLabel htmlFor="sc-cron">{t("automation.sched.expression")}</FieldLabel>
              <Input id="sc-cron" dir="ltr" className="font-mono" value={custom} onChange={(e) => setCustom(e.target.value)} />
              <FieldDescription>{t("automation.cronHint")}</FieldDescription>
            </Field>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2">
              {preset === "weekly" ? (
                <Field>
                  <FieldLabel htmlFor="sc-day">{t("automation.sched.day")}</FieldLabel>
                  <PickSelect id="sc-day" value={day} onChange={setDay} options={days.map((d) => ({ value: d, label: t(`automation.sched.dow.${d}`) }))} />
                </Field>
              ) : null}
              <Field>
                <FieldLabel htmlFor="sc-time">{t("automation.sched.at")}</FieldLabel>
                <Input id="sc-time" type="time" dir="ltr" value={time} onChange={(e) => setTime(e.target.value)} />
              </Field>
            </div>
          )}
          <Field>
            <FieldLabel htmlFor="sc-zone">{t("automation.timezone")}</FieldLabel>
            <Input
              id="sc-zone"
              dir="ltr"
              value={zone}
              onChange={(e) => {
                setZoneTouched(true)
                setZone(e.target.value)
              }}
            />
            <FieldDescription>{t("automation.sched.zoneHint", { zone: workspaceZone })}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="sc-catchup">{t("automation.catchUpLabel")}</FieldLabel>
            <PickSelect
              id="sc-catchup"
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

        <div className="border border-line bg-grid-card px-4 py-3" data-preview={preview?.valid ? "valid" : "invalid"}>
          <p className="text-sm font-medium">
            {t("automation.sched.nextFive")} <Ltr mono className="text-xs text-grid-muted">{cron || "—"}</Ltr>
            {preview?.timezone ? <span className="text-xs text-grid-muted"> · {preview.timezone}</span> : null}
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

        <div>
          <Button type="submit" disabled={busy || !name.trim() || !preview?.valid}>
            <PlusIcon />
            {busy ? t("common.working") : t("automation.addSchedule")}
          </Button>
        </div>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}
