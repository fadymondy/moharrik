"use client"

/*
The workspace's own limits on its automation (MH-234, MH-478): how many runs
may go at once across all its browsers, how many one browser may be asked for
in an hour, and how long runs — and their screenshots — are kept. They sit
beside the kill switch because the person who needs to change them is the
person looking at the runs, not whoever can restart the server.

Read-only for anyone who cannot manage automation: they see the numbers, and
no control that would fail.
*/

import { useEffect, useState, type FormEvent } from "react"
import { CheckIcon, GaugeIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationSettings } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

export function LimitsPanel({ settings, canManage, onSaved }: { settings?: AutomationSettings; canManage: boolean; onSaved: () => void }) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [parallel, setParallel] = useState("")
  const [hourly, setHourly] = useState("")
  const [retention, setRetention] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    if (!settings) return
    setParallel(String(settings.max_parallel_runs))
    setHourly(String(settings.runs_per_hour))
    setRetention(String(settings.retention_days))
  }, [settings])

  if (!settings) return null

  const dirty =
    parallel !== String(settings.max_parallel_runs) || hourly !== String(settings.runs_per_hour) || retention !== String(settings.retention_days)

  async function save(e: FormEvent) {
    e.preventDefault()
    setErr(null)
    const p = Number(parallel)
    const h = Number(hourly)
    const r = Number(retention)
    if (!Number.isInteger(p) || p < 1 || p > 50) return setErr(t("automation.limits.parallelRange"))
    if (!Number.isInteger(h) || h < 1 || h > 1000) return setErr(t("automation.limits.hourlyRange"))
    if (!Number.isInteger(r) || r < 0 || r > 3650) return setErr(t("automation.limits.retentionRange"))
    setBusy(true)
    try {
      await api("/api/w/automation/settings", {
        json: { max_parallel_runs: p, runs_per_hour: h, retention_days: r },
        workspaceId: ws.id,
      })
      toast.success(t("automation.limits.saved"))
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const num = (id: string, value: string, set: (v: string) => void) => (
    <Input
      id={id}
      dir="ltr"
      inputMode="numeric"
      className="tabular-nums"
      value={value}
      disabled={!canManage}
      onChange={(e) => set(e.target.value.replace(/[^0-9]/g, "").slice(0, 4))}
    />
  )

  return (
    <section className="mt-10" data-limits>
      <div className="flex flex-wrap items-end justify-between gap-3 px-6 pb-4">
        <div className="min-w-0">
          <h2 className="flex items-center gap-2 text-base font-medium">
            <GaugeIcon className="size-4" />
            {t("automation.limits.title")}
          </h2>
          <p className="max-w-2xl text-sm text-grid-muted">{t("automation.limits.hint")}</p>
        </div>
        <p className="text-sm text-grid-muted" data-live-runs={settings.live_runs}>
          {t("automation.limits.live", { n: formatNumber(settings.live_runs), max: formatNumber(settings.max_parallel_runs) })}
        </p>
      </div>
      <form onSubmit={save} noValidate className="border-y border-line px-6 py-5" data-form="limits">
        <FieldGroup>
          <div className="grid gap-4 sm:grid-cols-3">
            <Field>
              <FieldLabel htmlFor="lim-parallel">{t("automation.limits.parallel")}</FieldLabel>
              {num("lim-parallel", parallel, setParallel)}
              <FieldDescription>{t("automation.limits.parallelHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="lim-hourly">{t("automation.limits.hourly")}</FieldLabel>
              {num("lim-hourly", hourly, setHourly)}
              <FieldDescription>{t("automation.limits.hourlyHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="lim-retention">{t("automation.limits.retention")}</FieldLabel>
              {num("lim-retention", retention, setRetention)}
              <FieldDescription>{retention === "0" ? t("automation.limits.retentionForever") : t("automation.limits.retentionHint")}</FieldDescription>
            </Field>
          </div>
          {canManage ? (
            <div>
              <Button type="submit" disabled={busy || !dirty} data-save-limits>
                <CheckIcon />
                {busy ? t("common.working") : t("common.save")}
              </Button>
            </div>
          ) : (
            <p className="text-xs text-grid-muted">{t("automation.limits.adminOnly")}</p>
          )}
        </FieldGroup>
        {err ? <FieldError className="mt-3">{err}</FieldError> : null}
      </form>
    </section>
  )
}
