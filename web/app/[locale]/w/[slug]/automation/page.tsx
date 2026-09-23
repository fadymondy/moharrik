"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { CheckIcon, DownloadIcon, OctagonXIcon, PlayIcon, PlusIcon, ShieldCheckIcon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { ApproveForm } from "@/components/automation/approve-form"
import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationRun, type AutomationSchedule, type AutomationWorkflow, type BrowserAgent } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWorkspaceEvents } from "@/lib/events"
import { useAutomationSettings, useBrowsers, useRuns, useSchedules, useWorkflows } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function AutomationPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.automation")} · ${ws.summary.name}`)
  const canManage = ws.can("automation:manage")
  const canRun = ws.can("automation:run")

  const browsers = useBrowsers(ws.id)
  const settings = useAutomationSettings(ws.id)
  const runs = useRuns(ws.id)
  const schedules = useSchedules(ws.id)
  const workflows = useWorkflows(ws.id)

  // A schedule fires with nobody watching, by definition. Without this a page
  // left open showed "waiting" long after the run had finished, because
  // nothing on this side had any reason to refetch.
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "browser") {
      // A browser connecting or going away is not something this page can
      // cause, so nothing here would otherwise refetch — it showed "Not
      // connected" indefinitely after a successful pairing.
      void browsers.mutate()
      return
    }
    if (e.kind !== "schedule") return
    void schedules.mutate()
    void runs.mutate()
  })

  const list = browsers.data?.browsers ?? []
  const pairings = browsers.data?.pairings ?? []
  const paused = settings.data?.paused ?? false

  async function setPaused(next: boolean) {
    try {
      await api("/api/w/automation/settings", { json: { paused: next }, workspaceId: ws.id })
      toast.success(next ? t("automation.stopped") : t("automation.resumed"))
      settings.mutate()
      browsers.mutate()
      runs.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <>
      <SectionHeader
        micro={ws.summary.name}
        title={t("nav.automation")}
        description={t("automation.hint")}
        action={
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation/extension`} />} data-install-link>
              <DownloadIcon />
              {t("automation.ext.link")}
            </Button>
            {canManage ? (
            paused ? (
              <Button size="sm" onClick={() => setPaused(false)}>
                <PlayIcon />
                {t("automation.resume")}
              </Button>
            ) : (
              <ConfirmButton
                label={
                  <span className="flex items-center gap-2">
                    <OctagonXIcon className="size-4" />
                    {t("automation.stopAll")}
                  </span>
                }
                title={t("automation.stopTitle")}
                description={t("automation.stopBody")}
                confirmLabel={t("automation.stopAll")}
                onConfirm={() => setPaused(true)}
              />
            )
          ) : null}
          </div>
        }
      />

      {paused ? (
        <div className="mx-6 mb-6 flex items-start gap-2 border border-grid-danger-text/40 bg-grid-danger-text/5 px-4 py-3 text-sm" role="status" data-paused="true">
          <OctagonXIcon className="mt-0.5 size-4 shrink-0 text-grid-danger-text" />
          <div>
            <p className="font-medium">{t("automation.pausedTitle")}</p>
            <p className="text-grid-muted">{settings.data?.reason || t("automation.pausedBody")}</p>
          </div>
        </div>
      ) : null}

      {/* ---- browsers ---- */}
      <section>
        <h2 className="px-6 pb-1 text-base font-medium">{t("automation.browsers")}</h2>
        <p className="px-6 pb-4 text-sm text-grid-muted">{t("automation.browsersHint")}</p>

        {browsers.error ? (
          <ErrorState error={browsers.error} />
        ) : browsers.isLoading ? (
          <LoadingRows rows={2} />
        ) : list.length === 0 ? (
          <EmptyState
            title={t("automation.noBrowsers")}
            body={t("automation.noBrowsersBody")}
            action={
              <Button size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation/extension`} />}>
                <DownloadIcon />
                {t("automation.ext.link")}
              </Button>
            }
          />
        ) : (
          <ul className="divide-y divide-line border-y border-line">
            {list.map((b) => (
              <BrowserRow key={b.id} browser={b} canManage={canManage} onChange={() => browsers.mutate()} />
            ))}
          </ul>
        )}

        {pairings.length > 0 ? (
          <div className="mt-6 px-6">
            <p className="grid-micro pb-2">{t("automation.waiting", { n: pairings.length })}</p>
            <ul className="space-y-1 text-sm text-grid-muted">
              {pairings.map((p) => (
                <li key={p.id} data-pairing={p.id}>
                  {p.requested_name || t("automation.unnamedBrowser")} · {p.platform || t("automation.unknownPlatform")}
                  {p.ip ? ` · ${p.ip}` : ""}
                </li>
              ))}
            </ul>
          </div>
        ) : null}

        {canManage ? <ApproveForm onApproved={() => browsers.mutate()} /> : null}
      </section>

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />

      {/* ---- workflows ---- */}
      <section>
        <h2 className="px-6 pb-1 text-base font-medium">{t("automation.workflows")}</h2>
        <p className="px-6 pb-4 text-sm text-grid-muted">{t("automation.workflowsHint")}</p>

        {workflows.error ? (
          <ErrorState error={workflows.error} />
        ) : workflows.isLoading ? (
          <LoadingRows rows={2} />
        ) : (workflows.data?.workflows ?? []).length === 0 ? (
          <EmptyState title={t("automation.noWorkflows")} body={canRun ? t("automation.noWorkflowsBody") : undefined} />
        ) : (
          <ul className="divide-y divide-line border-y border-line">
            {(workflows.data?.workflows ?? []).map((w) => (
              <WorkflowRow
                key={w.id}
                workflow={w}
                browsers={list.filter((b) => !b.revoked_at)}
                canRun={canRun}
                onChange={() => {
                  workflows.mutate()
                  runs.mutate()
                }}
              />
            ))}
          </ul>
        )}
        {canRun ? <AddWorkflow onAdded={() => workflows.mutate()} /> : null}
      </section>

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />

      {/* ---- schedules ---- */}
      <section>
        <h2 className="px-6 pb-1 text-base font-medium">{t("automation.schedules")}</h2>
        <p className="px-6 pb-4 text-sm text-grid-muted">{t("automation.schedulesHint")}</p>

        {schedules.error ? (
          <ErrorState error={schedules.error} />
        ) : schedules.isLoading ? (
          <LoadingRows rows={2} />
        ) : (schedules.data?.schedules ?? []).length === 0 ? (
          // A schedule runs in a paired browser. With none paired, "add a
          // schedule" was a dead end: say what is missing and where to get it.
          !list.some((b) => !b.revoked_at) ? (
            <EmptyState
              title={t("automation.sched.needBrowserTitle")}
              body={t("automation.sched.needBrowserBody")}
              action={
                <Button size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation/extension`} />} data-schedule-install>
                  <DownloadIcon />
                  {t("automation.ext.link")}
                </Button>
              }
            />
          ) : (
            <EmptyState title={t("automation.noSchedules")} body={canRun ? t("automation.noSchedulesBody") : undefined} />
          )
        ) : (
          <ul className="divide-y divide-line border-y border-line">
            {(schedules.data?.schedules ?? []).map((sc) => (
              <ScheduleRow
                key={sc.id}
                schedule={sc}
                canRun={canRun}
                onChange={() => {
                  schedules.mutate()
                  runs.mutate()
                }}
              />
            ))}
          </ul>
        )}

        {canRun && list.some((b) => !b.revoked_at) ? (
          <AddSchedule browsers={list.filter((b) => !b.revoked_at)} onAdded={() => schedules.mutate()} />
        ) : null}
      </section>

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />

      {/* ---- runs ---- */}
      <section>
        <h2 className="px-6 pb-1 text-base font-medium">{t("automation.runs")}</h2>
        <p className="px-6 pb-4 text-sm text-grid-muted">{t("automation.runsHint")}</p>

        {canRun && list.some((b) => !b.revoked_at) ? (
          <StartRun browsers={list.filter((b) => !b.revoked_at)} paused={paused} onStarted={() => runs.mutate()} />
        ) : null}

        {runs.error ? (
          <ErrorState error={runs.error} />
        ) : runs.isLoading ? (
          <LoadingRows rows={3} />
        ) : (runs.data?.runs ?? []).length === 0 ? (
          <EmptyState title={t("automation.noRuns")} />
        ) : (
          <ul className="divide-y divide-line border-y border-line">
            {(runs.data?.runs ?? []).map((r) => (
              <RunRow key={r.id} run={r} href={`/${locale}/w/${ws.slug}/automation/runs/${r.id}`} />
            ))}
          </ul>
        )}
      </section>
      <div className="pb-8" />
    </>
  )
}

function BrowserRow({ browser, canManage, onChange }: { browser: BrowserAgent; canManage: boolean; onChange: () => void }) {
  const { t, formatDate, locale } = useTranslations()
  const ws = useWorkspace()
  const [editing, setEditing] = useState(false)
  const [hosts, setHosts] = useState(browser.allowed_hosts.join(", "))
  const [busy, setBusy] = useState(false)
  const revoked = !!browser.revoked_at

  async function saveHosts(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api(`/api/w/browsers/${encodeURIComponent(browser.id)}`, {
        method: "PATCH",
        json: { allowed_hosts: hosts.split(",").map((h) => h.trim()).filter(Boolean) },
        workspaceId: ws.id,
      })
      toast.success(t("automation.hostsSaved"))
      setEditing(false)
      onChange()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function revoke() {
    await api(`/api/w/browsers/${encodeURIComponent(browser.id)}`, { method: "DELETE", workspaceId: ws.id })
    toast.success(t("automation.revoked"))
    onChange()
  }

  return (
    <li className="px-6 py-4" data-browser={browser.name} data-browser-status={browser.status}>
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
            {browser.name}
            {revoked ? (
              <Badge variant="outline">{t("automation.revokedBadge")}</Badge>
            ) : (
              <Badge variant={browser.status === "online" ? "default" : "outline"}>
                {browser.status === "online" ? t("automation.online") : t("automation.offline")}
              </Badge>
            )}
            {browser.update_available ? (
              // The extension reports its version on every connect, so this
              // clears by itself once the browser is updated (MH-399).
              <Link href={`/${locale}/w/${ws.slug}/automation/extension`} data-update-available>
                <Badge variant="secondary">{t("automation.ext.outdated", { version: browser.extension_version || "?" })}</Badge>
              </Link>
            ) : null}
          </p>
          <p className="text-xs text-grid-muted">
            {[browser.platform, browser.browser_version, browser.extension_version].filter(Boolean).join(" · ") || t("automation.unknownPlatform")}
            {" · "}
            {t("automation.lastSeen", { date: formatDate(browser.last_seen_at) })}
          </p>
          <p className="mt-1 flex items-start gap-1 text-xs" data-hosts={browser.allowed_hosts.join(",")}>
            <ShieldCheckIcon className="mt-0.5 size-3 shrink-0 text-grid-muted" />
            {browser.allowed_hosts.length > 0 ? (
              <span className="text-grid-muted">{t("automation.mayVisit", { hosts: browser.allowed_hosts.join(", ") })}</span>
            ) : (
              // Not an error — it is the safe default, and saying so stops
              // someone "fixing" it by pasting in a wildcard.
              <span className="text-grid-muted">{t("automation.noHostsYet")}</span>
            )}
          </p>
        </div>
        {canManage && !revoked ? (
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="sm" onClick={() => setEditing((v) => !v)}>
              {t("automation.editHosts")}
            </Button>
            <ConfirmButton
              label={t("automation.revoke")}
              title={t("automation.revokeTitle")}
              description={t("automation.revokeBody", { name: browser.name })}
              confirmLabel={t("automation.revoke")}
              onConfirm={revoke}
            />
          </div>
        ) : null}
      </div>

      {editing ? (
        <form onSubmit={saveHosts} className="mt-3" data-form="hosts">
          <FieldGroup className="sm:flex-row sm:items-end">
            <Field className="sm:flex-1">
              <FieldLabel htmlFor={`hosts-${browser.id}`}>{t("automation.allowedHosts")}</FieldLabel>
              <Input id={`hosts-${browser.id}`} dir="ltr" value={hosts} onChange={(e) => setHosts(e.target.value)} placeholder="example.com, mail.google.com" />
              <FieldDescription>{t("automation.allowedHostsHint")}</FieldDescription>
            </Field>
            <Button type="submit" disabled={busy}>
              <CheckIcon />
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </FieldGroup>
        </form>
      ) : null}
    </li>
  )
}

function StartRun({ browsers, paused, onStarted }: { browsers: BrowserAgent[]; paused: boolean; onStarted: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [agent, setAgent] = useState(browsers[0]?.id ?? "")
  const [steps, setSteps] = useState('[{"command":"goto","args":{"url":"https://example.com"}}]')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setErr(null)
    let parsed: unknown
    try {
      parsed = JSON.parse(steps)
    } catch {
      setErr(t("automation.stepsNotJson"))
      return
    }
    if (!Array.isArray(parsed) || parsed.length === 0) {
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
        <Field>
          <FieldLabel htmlFor="run-steps">{t("automation.steps")}</FieldLabel>
          <Textarea id="run-steps" dir="ltr" rows={4} className="font-mono text-xs" value={steps} onChange={(e) => setSteps(e.target.value)} />
          <FieldDescription>{t("automation.stepsHint")}</FieldDescription>
        </Field>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}

function RunRow({ run, href }: { run: AutomationRun; href: string }) {
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
            {run.agent_name || t("automation.unnamedBrowser")}
          </Link>
          <Badge variant={tone[run.status] ?? "outline"}>{t(`automation.status.${run.status}`)}</Badge>
        </p>
        <p className="text-xs text-grid-muted">
          {t("automation.stepsDone", { ok: run.ok_count, total: run.step_count })} · {t(`automation.trigger.${run.trigger}`)} · {formatDate(run.created_at)}
        </p>
        {run.error ? <p className="mt-1 text-xs text-grid-danger-text">{run.error}</p> : null}
      </div>
      <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={href} />}>
        {t("automation.openRun")}
      </Button>
    </li>
  )
}

function ScheduleRow({ schedule, canRun, onChange }: { schedule: AutomationSchedule; canRun: boolean; onChange: () => void }) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)

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
    <li className="flex flex-wrap items-start gap-3 px-6 py-4" data-schedule={schedule.name} data-schedule-status={schedule.last_status || "new"}>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          {schedule.name}
          <Badge variant={schedule.enabled ? "default" : "outline"}>{schedule.enabled ? t("automation.scheduleOn") : t("automation.scheduleOff")}</Badge>
          {schedule.last_status ? <Badge variant="secondary">{t(`automation.last.${schedule.last_status}`)}</Badge> : null}
        </p>
        <p className="text-xs text-grid-muted">
          <Ltr mono>{schedule.cron}</Ltr> · {schedule.timezone} · {schedule.agent_name || t("automation.unnamedBrowser")} · {t(`automation.catchUp.${schedule.catch_up}`)}
        </p>
        <p className="text-xs text-grid-muted">
          {schedule.next_run_at ? t("automation.nextRun", { date: formatDate(schedule.next_run_at) }) : ""}
          {schedule.last_run_at ? ` · ${t("automation.lastRun", { date: formatDate(schedule.last_run_at) })}` : ""}
        </p>
        {/* A miss is shown, not swallowed: "there is no data for Tuesday" has
            to have an answer on the page. */}
        {schedule.last_error ? (
          <p className="mt-1 text-xs text-grid-danger-text" data-schedule-error>
            {schedule.last_error}
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
          <ConfirmButton
            label={t("common.delete")}
            title={t("automation.deleteScheduleTitle")}
            description={t("automation.deleteScheduleBody", { name: schedule.name })}
            confirmLabel={t("common.delete")}
            onConfirm={remove}
          />
        </div>
      ) : null}
    </li>
  )
}

function AddSchedule({ browsers, onAdded }: { browsers: BrowserAgent[]; onAdded: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [agent, setAgent] = useState(browsers[0]?.id ?? "")
  const [cron, setCron] = useState("0 7 * * *")
  // The browser's own timezone: "07:00" means where the person is, and making
  // them work that out in UTC is how a schedule ends up two hours wrong.
  const [zone, setZone] = useState(typeof Intl !== "undefined" ? Intl.DateTimeFormat().resolvedOptions().timeZone : "UTC")
  const [catchUp, setCatchUp] = useState("skip")
  const [steps, setSteps] = useState('[{"command":"goto","args":{"url":"https://example.com"}}]')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setErr(null)
    let parsed: unknown
    try {
      parsed = JSON.parse(steps)
    } catch {
      setErr(t("automation.stepsNotJson"))
      return
    }
    if (!Array.isArray(parsed) || parsed.length === 0) {
      setErr(t("automation.stepsNotArray"))
      return
    }
    setBusy(true)
    try {
      await api("/api/w/schedules", {
        json: { name: name.trim(), agent_id: agent, cron: cron.trim(), timezone: zone, catch_up: catchUp, steps: parsed },
        workspaceId: ws.id,
      })
      toast.success(t("automation.scheduleCreated"))
      setName("")
      onAdded()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="px-6 pt-6" data-form="schedule">
      <FieldGroup>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="sc-name">{t("automation.scheduleName")}</FieldLabel>
            <Input id="sc-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
          </Field>
          <Field>
            <FieldLabel htmlFor="sc-agent">{t("automation.browsers")}</FieldLabel>
            <PickSelect id="sc-agent" value={agent} onChange={setAgent} options={browsers.map((b) => ({ value: b.id, label: b.name }))} />
          </Field>
          <Field>
            <FieldLabel htmlFor="sc-cron">{t("automation.cron")}</FieldLabel>
            <Input id="sc-cron" dir="ltr" className="font-mono" value={cron} onChange={(e) => setCron(e.target.value)} />
            <FieldDescription>{t("automation.cronHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="sc-zone">{t("automation.timezone")}</FieldLabel>
            <Input id="sc-zone" dir="ltr" value={zone} onChange={(e) => setZone(e.target.value)} />
            <FieldDescription>{t("automation.timezoneHint")}</FieldDescription>
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
        <Field>
          <FieldLabel htmlFor="sc-steps">{t("automation.steps")}</FieldLabel>
          <Textarea id="sc-steps" dir="ltr" rows={3} className="font-mono text-xs" value={steps} onChange={(e) => setSteps(e.target.value)} />
          <FieldDescription>{t("automation.stepsHint")}</FieldDescription>
        </Field>
        <div>
          <Button type="submit" disabled={busy || !name.trim()}>
            <PlusIcon />
            {busy ? t("common.working") : t("automation.addSchedule")}
          </Button>
        </div>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}

function WorkflowRow({
  workflow,
  browsers,
  canRun,
  onChange,
}: {
  workflow: AutomationWorkflow
  browsers: BrowserAgent[]
  canRun: boolean
  onChange: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [open, setOpen] = useState(false)
  const [agent, setAgent] = useState(browsers[0]?.id ?? "")
  const [values, setValues] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  // Every placeholder the steps use, whether or not someone remembered to
  // declare it: a workflow edited to add one should still be runnable.
  const fields = workflow.placeholders.map((name) => workflow.params.find((p) => p.name === name) ?? { name })

  async function run(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/workflows/${encodeURIComponent(workflow.id)}/run`, {
        json: { agent_id: agent, values },
        workspaceId: ws.id,
      })
      toast.success(t("automation.runStarted"))
      setOpen(false)
      onChange()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    await api(`/api/w/workflows/${encodeURIComponent(workflow.id)}`, { method: "DELETE", workspaceId: ws.id })
    toast.success(t("automation.workflowDeleted"))
    onChange()
  }

  return (
    <li className="px-6 py-4" data-workflow={workflow.name}>
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium">{workflow.name}</p>
          {workflow.description ? <p className="text-sm text-grid-muted">{workflow.description}</p> : null}
          <p className="text-xs text-grid-muted">
            {t("automation.stepCount", { n: workflow.steps.length })}
            {workflow.placeholders.length > 0 ? ` · ${t("automation.asksFor", { names: workflow.placeholders.join(", ") })}` : ""}
            {workflow.run_count > 0 ? ` · ${t("automation.ranTimes", { n: workflow.run_count })}` : ""}
          </p>
        </div>
        {canRun ? (
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => setOpen((v) => !v)} disabled={browsers.length === 0}>
              <PlayIcon />
              {t("automation.useIt")}
            </Button>
            <ConfirmButton
              label={t("common.delete")}
              title={t("automation.deleteWorkflowTitle")}
              description={t("automation.deleteWorkflowBody", { name: workflow.name })}
              confirmLabel={t("common.delete")}
              onConfirm={remove}
            />
          </div>
        ) : null}
      </div>

      {open ? (
        <form onSubmit={run} className="mt-3" data-form="run-workflow">
          <FieldGroup>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor={`wf-agent-${workflow.id}`}>{t("automation.browsers")}</FieldLabel>
                <PickSelect id={`wf-agent-${workflow.id}`} value={agent} onChange={setAgent} options={browsers.map((b) => ({ value: b.id, label: b.name }))} />
              </Field>
              {fields.map((f) => (
                <Field key={f.name}>
                  <FieldLabel htmlFor={`wf-${workflow.id}-${f.name}`}>
                    {("label" in f && f.label) || f.name}
                    {"required" in f && f.required ? "" : ` (${t("common.optional")})`}
                  </FieldLabel>
                  <Input
                    id={`wf-${workflow.id}-${f.name}`}
                    type={"secret" in f && f.secret ? "password" : "text"}
                    autoComplete={"secret" in f && f.secret ? "new-password" : undefined}
                    value={values[f.name] ?? ("default" in f ? (f.default ?? "") : "")}
                    onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}
                  />
                  {"secret" in f && f.secret ? <FieldDescription>{t("automation.secretHint")}</FieldDescription> : null}
                </Field>
              ))}
            </div>
            <div>
              <Button type="submit" disabled={busy || !agent}>
                {busy ? t("common.working") : t("automation.startRun")}
              </Button>
            </div>
          </FieldGroup>
          {err ? <FieldError className="mt-3">{err}</FieldError> : null}
        </form>
      ) : null}
    </li>
  )
}

function AddWorkflow({ onAdded }: { onAdded: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [steps, setSteps] = useState('[{"command":"goto","args":{"url":"https://example.com/{{page}}"}}]')
  const [secrets, setSecrets] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setErr(null)
    let parsed: unknown
    try {
      parsed = JSON.parse(steps)
    } catch {
      setErr(t("automation.stepsNotJson"))
      return
    }
    if (!Array.isArray(parsed) || parsed.length === 0) {
      setErr(t("automation.stepsNotArray"))
      return
    }
    // Every placeholder becomes a parameter; the ones named here are marked
    // secret so their values never reach the run record.
    const secretNames = secrets.split(",").map((x) => x.trim()).filter(Boolean)
    const used = [...new Set((JSON.stringify(parsed).match(/\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}/g) ?? []).map((m) => m.replace(/[{}\s]/g, "")))]
    const params = used.map((n) => ({ name: n, required: true, secret: secretNames.includes(n) }))

    setBusy(true)
    try {
      await api("/api/w/workflows", {
        json: { name: name.trim(), description: description.trim(), steps: parsed, params },
        workspaceId: ws.id,
      })
      toast.success(t("automation.workflowSaved"))
      setName("")
      setDescription("")
      onAdded()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="px-6 pt-6" data-form="workflow">
      <FieldGroup>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="wf-name">{t("automation.workflowName")}</FieldLabel>
            <Input id="wf-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
          </Field>
          <Field>
            <FieldLabel htmlFor="wf-desc">
              {t("automation.workflowDescription")} <span className="text-grid-muted">({t("common.optional")})</span>
            </FieldLabel>
            <Input id="wf-desc" value={description} onChange={(e) => setDescription(e.target.value)} maxLength={400} />
          </Field>
        </div>
        <Field>
          <FieldLabel htmlFor="wf-steps">{t("automation.steps")}</FieldLabel>
          <Textarea id="wf-steps" dir="ltr" rows={3} className="font-mono text-xs" value={steps} onChange={(e) => setSteps(e.target.value)} />
          <FieldDescription>{t("automation.workflowStepsHint")}</FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor="wf-secrets">
            {t("automation.secretParams")} <span className="text-grid-muted">({t("common.optional")})</span>
          </FieldLabel>
          <Input id="wf-secrets" dir="ltr" value={secrets} onChange={(e) => setSecrets(e.target.value)} placeholder="password, token" />
          <FieldDescription>{t("automation.secretParamsHint")}</FieldDescription>
        </Field>
        <div>
          <Button type="submit" disabled={busy || !name.trim()}>
            <PlusIcon />
            {busy ? t("common.working") : t("automation.saveWorkflow")}
          </Button>
        </div>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}
