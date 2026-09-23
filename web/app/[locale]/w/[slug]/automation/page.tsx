"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import {
  BookOpenIcon,
  CheckIcon,
  ChevronUpIcon,
  DownloadIcon,
  OctagonXIcon,
  PencilIcon,
  PlayIcon,
  PlusIcon,
  SettingsIcon,
  ShieldCheckIcon,
  XIcon,
} from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { ApproveForm } from "@/components/automation/approve-form"
import { ScheduleForm } from "@/components/automation/schedule-form"
import { StepEditor, fromEditorSteps, toEditorSteps, type EditorStep } from "@/components/automation/step-editor"
import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { CopyField, Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationRun, type AutomationSchedule, type AutomationWorkflow, type BrowserAgent } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWorkspaceEvents } from "@/lib/events"
import { useAutomationSettings, useBrowsers, useExtension, useRuns, useSchedules, useWorkflows } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

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
  const ext = useExtension()

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
  const active = list.filter((b) => !b.revoked_at)
  const pairings = browsers.data?.pairings ?? []
  const paused = settings.data?.paused ?? false
  const hasBrowser = active.length > 0
  const hasSites = active.some((b) => b.allowed_hosts.length > 0)
  const setupDone = hasBrowser && hasSites
  const [managing, setManaging] = useState(false)
  const [scheduling, setScheduling] = useState(false)
  const [runningOnce, setRunningOnce] = useState(false)
  const workflowList = workflows.data?.workflows ?? []

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

  const installHref = `/${locale}/w/${ws.slug}/automation/extension`

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.automation")}
        description={t("automation.hint")}
        action={
          canManage ? (
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
          ) : undefined
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

      {/* ---- setup: install, pair, allowed sites ----
          It leads the page until a browser is paired and may go somewhere;
          after that it folds into one status row, opened again on demand. */}
      {browsers.error ? (
        <ErrorState error={browsers.error} />
      ) : browsers.isLoading ? (
        <LoadingRows rows={3} />
      ) : setupDone && !managing ? (
        <BrowsersStatus browsers={active} onManage={() => setManaging(true)} installHref={installHref} />
      ) : (
        <section data-setup={setupDone ? "managing" : "open"}>
          <div className="flex flex-wrap items-end justify-between gap-3 px-6 pb-4">
            <div>
              <h2 className="text-base font-medium">{setupDone ? t("automation.browsers") : t("automation.flow.title")}</h2>
              <p className="text-sm text-grid-muted">{setupDone ? t("automation.browsersHint") : t("automation.flow.hint")}</p>
            </div>
            {setupDone ? (
              <Button variant="ghost" size="sm" onClick={() => setManaging(false)} data-manage-done>
                <ChevronUpIcon />
                {t("automation.flow.collapse")}
              </Button>
            ) : null}
          </div>
          <ol className="divide-y divide-line border-y border-line">
            <FlowStep n={1} state={hasBrowser ? "done" : "current"} title={t("automation.flow.install.title")} data="install">
              {hasBrowser ? (
                <p className="text-sm text-grid-muted">{t("automation.flow.install.done")}</p>
              ) : (
                <p className="text-sm text-grid-muted">{t("automation.flow.install.body")}</p>
              )}
              <div className="flex flex-wrap items-center gap-2">
                {ext.data?.available && ext.data.download_url ? (
                  <Button
                    size="sm"
                    variant={hasBrowser ? "outline" : "default"}
                    nativeButton={false}
                    render={<a href={ext.data.download_url} download={ext.data.file} data-flow-download />}
                  >
                    <DownloadIcon />
                    {t("automation.flow.install.download", { version: ext.data.version ?? "" })}
                  </Button>
                ) : null}
                <Button size="sm" variant="ghost" nativeButton={false} render={<Link href={installHref} />} data-install-link>
                  <BookOpenIcon />
                  {t("automation.flow.install.steps")}
                </Button>
              </div>
              {ext.data && !ext.data.available ? <p className="text-xs text-grid-muted">{ext.data.reason}</p> : null}
            </FlowStep>

            <FlowStep n={2} state={hasBrowser ? "done" : "current"} title={t("automation.flow.pair.title")} data="pair">
              <p className="text-sm text-grid-muted">
                {hasBrowser ? t("automation.flow.pair.done", { n: active.length }) : t("automation.flow.pair.body")}
              </p>
              {!hasBrowser || managing ? (
                <div className="max-w-md min-w-0 space-y-1">
                  <p className="text-xs text-grid-muted">{t("automation.ext.workspaceField")}</p>
                  <CopyField value={ws.slug} nowrap />
                </div>
              ) : null}
              {pairings.length > 0 ? (
                <ul className="space-y-1 text-sm text-grid-muted" data-waiting-pairings>
                  <li className="grid-micro">{t("automation.waiting", { n: pairings.length })}</li>
                  {pairings.map((p) => (
                    <li key={p.id} data-pairing={p.id}>
                      {p.requested_name || t("automation.unnamedBrowser")} · {p.platform || t("automation.unknownPlatform")}
                      {p.ip ? ` · ${p.ip}` : ""}
                    </li>
                  ))}
                </ul>
              ) : null}
              {!canManage ? <p className="text-sm text-grid-muted">{t("automation.ext.s7AskAdmin")}</p> : null}
              {canManage && (!hasBrowser || managing) ? <ApproveForm embedded onApproved={() => browsers.mutate()} /> : null}
            </FlowStep>

            <FlowStep n={3} state={hasSites ? "done" : hasBrowser ? "current" : "waiting"} title={t("automation.flow.sites.title")} data="sites">
              <p className="text-sm text-grid-muted">
                {!hasBrowser ? t("automation.flow.sites.waiting") : hasSites ? t("automation.flow.sites.done") : t("automation.flow.sites.body")}
              </p>
            </FlowStep>
            {hasBrowser ? (
              <li className="list-none">
                <ul className="divide-y divide-line border-t border-line" data-browser-list>
                  {list.map((b) => (
                    <BrowserRow key={b.id} browser={b} canManage={canManage} onChange={() => browsers.mutate()} />
                  ))}
                </ul>
              </li>
            ) : null}
          </ol>
        </section>
      )}

      {/* ---- workflows ---- */}
      <section className="mt-10">
        <SectionTitle
          title={t("automation.workflows")}
          hint={t("automation.workflowsHint")}
          action={
            canRun && workflowList.length > 0 ? (
              <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation/workflows/new`} />} data-new-workflow>
                <PlusIcon />
                {t("automation.editor.newWorkflow")}
              </Button>
            ) : undefined
          }
        />
        {workflows.error ? (
          <ErrorState error={workflows.error} />
        ) : workflows.isLoading ? (
          <LoadingRows rows={2} />
        ) : workflowList.length === 0 ? (
          <EmptyState
            title={t("automation.noWorkflows")}
            body={canRun ? t("automation.noWorkflowsBody") : undefined}
            action={
              canRun ? (
                <Button size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation/workflows/new`} />}>
                  <PlusIcon />
                  {t("automation.editor.newWorkflow")}
                </Button>
              ) : undefined
            }
          />
        ) : (
          <ul className="divide-y divide-line border-y border-line">
            {workflowList.map((w) => (
              <WorkflowRow
                key={w.id}
                workflow={w}
                browsers={active}
                canRun={canRun}
                onChange={() => {
                  workflows.mutate()
                  runs.mutate()
                }}
              />
            ))}
          </ul>
        )}
      </section>

      {/* ---- schedules ---- */}
      <section className="mt-10">
        <SectionTitle
          title={t("automation.schedules")}
          hint={t("automation.schedulesHint")}
          action={
            canRun && hasBrowser && (schedules.data?.schedules ?? []).length > 0 ? (
              <Button variant="outline" size="sm" onClick={() => setScheduling((v) => !v)} data-new-schedule>
                {scheduling ? <XIcon /> : <PlusIcon />}
                {scheduling ? t("common.cancel") : t("automation.sched.new")}
              </Button>
            ) : undefined
          }
        />
        {schedules.error ? (
          <ErrorState error={schedules.error} />
        ) : schedules.isLoading ? (
          <LoadingRows rows={2} />
        ) : (schedules.data?.schedules ?? []).length === 0 && !scheduling ? (
          // A schedule runs in a paired browser. With none paired, "add a
          // schedule" was a dead end: say what is missing and where to get it.
          !hasBrowser ? (
            <EmptyState
              title={t("automation.sched.needBrowserTitle")}
              body={t("automation.sched.needBrowserBody")}
              action={
                <Button size="sm" nativeButton={false} render={<Link href={installHref} />} data-schedule-install>
                  <DownloadIcon />
                  {t("automation.ext.link")}
                </Button>
              }
            />
          ) : (
            <EmptyState
              title={t("automation.noSchedules")}
              body={canRun ? t("automation.noSchedulesBody") : undefined}
              action={
                canRun ? (
                  <Button size="sm" onClick={() => setScheduling(true)} data-new-schedule>
                    <PlusIcon />
                    {t("automation.sched.new")}
                  </Button>
                ) : undefined
              }
            />
          )
        ) : (schedules.data?.schedules ?? []).length > 0 ? (
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
        ) : null}
        {canRun && hasBrowser && scheduling ? (
          <ScheduleForm
            browsers={active}
            workflows={workflowList}
            onAdded={() => {
              setScheduling(false)
              schedules.mutate()
            }}
          />
        ) : null}
      </section>

      {/* ---- runs ---- */}
      <section className="mt-10">
        <SectionTitle
          title={t("automation.runs")}
          hint={t("automation.runsHint")}
          action={
            canRun && hasBrowser ? (
              <Button variant="outline" size="sm" onClick={() => setRunningOnce((v) => !v)} data-run-once>
                {runningOnce ? <XIcon /> : <PlayIcon />}
                {runningOnce ? t("common.cancel") : t("automation.flow.runOnce")}
              </Button>
            ) : undefined
          }
        />
        {canRun && hasBrowser && runningOnce ? (
          <StartRun
            browsers={active}
            paused={paused}
            onStarted={() => {
              setRunningOnce(false)
              runs.mutate()
            }}
          />
        ) : null}
        {runs.error ? (
          <ErrorState error={runs.error} />
        ) : runs.isLoading ? (
          <LoadingRows rows={3} />
        ) : (runs.data?.runs ?? []).length === 0 ? (
          <EmptyState title={t("automation.noRuns")} body={hasBrowser ? undefined : t("automation.flow.runsNeedBrowser")} />
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

function SectionTitle({ title, hint, action }: { title: string; hint: string; action?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-3 px-6 pb-4">
      <div className="min-w-0">
        <h2 className="text-base font-medium">{title}</h2>
        <p className="max-w-2xl text-sm text-grid-muted">{hint}</p>
      </div>
      {action}
    </div>
  )
}

/** One step of the setup flow: a numbered marker, its state, and what to do. */
function FlowStep({
  n,
  state,
  title,
  data,
  children,
}: {
  n: number
  state: "done" | "current" | "waiting"
  title: string
  data: string
  children: React.ReactNode
}) {
  const { t } = useTranslations()
  return (
    <li className="flex gap-4 px-6 py-5" data-flow-step={data} data-flow-state={state}>
      <span
        className={`flex size-8 shrink-0 items-center justify-center rounded-full border text-sm tabular-nums ${
          state === "done"
            ? "border-primary bg-primary text-primary-foreground"
            : state === "current"
              ? "border-primary text-primary"
              : "border-line text-grid-muted"
        }`}
        aria-hidden
      >
        {state === "done" ? <CheckIcon className="size-4" /> : n}
      </span>
      <div className="min-w-0 flex-1 space-y-3">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          {title}
          <Badge variant={state === "done" ? "secondary" : state === "current" ? "default" : "outline"}>{t(`automation.flow.state.${state}`)}</Badge>
        </p>
        {children}
      </div>
    </li>
  )
}

/** Setup is done: one row that says so, with the way back in. */
function BrowsersStatus({ browsers, onManage, installHref }: { browsers: BrowserAgent[]; onManage: () => void; installHref: string }) {
  const { t } = useTranslations()
  const online = browsers.filter((b) => b.status === "online").length
  const outdated = browsers.filter((b) => b.update_available).length
  const hosts = [...new Set(browsers.flatMap((b) => b.allowed_hosts))]
  return (
    <section className="flex flex-wrap items-center gap-x-4 gap-y-2 border-y border-line bg-grid-card px-6 py-4" data-browser-status>
      <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground" aria-hidden>
        <CheckIcon className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          {t("automation.flow.statusRow", { n: browsers.length, online })}
          {outdated > 0 ? (
            <Link href={installHref} data-update-available>
              <Badge variant="secondary">{t("automation.flow.needUpdate", { n: outdated })}</Badge>
            </Link>
          ) : null}
        </p>
        <p className="flex items-start gap-1 text-xs text-grid-muted">
          <ShieldCheckIcon className="mt-0.5 size-3 shrink-0" />
          <span className="min-w-0 break-words">{t("automation.mayVisit", { hosts: hosts.join(", ") })}</span>
        </p>
      </div>
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={installHref} />} data-install-link>
          <DownloadIcon />
          {t("automation.ext.link")}
        </Button>
        <Button variant="outline" size="sm" onClick={onManage} data-manage-browsers>
          <SettingsIcon />
          {t("automation.flow.manage")}
        </Button>
      </div>
    </section>
  )
}

function BrowserRow({ browser, canManage, onChange }: { browser: BrowserAgent; canManage: boolean; onChange: () => void }) {
  const { t, formatDate, locale } = useTranslations()
  const ws = useWorkspace()
  // A browser that may go nowhere opens with its allow-list ready to fill in:
  // that is the setup step it is waiting on.
  const [editing, setEditing] = useState(canManage && !browser.revoked_at && browser.allowed_hosts.length === 0)
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
            <Bdi>{browser.name}</Bdi>
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
              description={t("automation.revokeBody", { name: isolate(browser.name) })}
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
            {run.agent_name ? <Bdi>{run.agent_name}</Bdi> : t("automation.unnamedBrowser")}
          </Link>
          <Badge variant={tone[run.status] ?? "outline"}>{t(`automation.status.${run.status}`)}</Badge>
        </p>
        <p className="text-xs text-grid-muted">
          {t("automation.stepsDone", { ok: run.ok_count, total: run.step_count })} · {t(`automation.trigger.${run.trigger}`)} · {formatDate(run.created_at)}
        </p>
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
          <Bdi>{schedule.name}</Bdi>
          <Badge variant={schedule.enabled ? "default" : "outline"}>{schedule.enabled ? t("automation.scheduleOn") : t("automation.scheduleOff")}</Badge>
          {schedule.last_status ? <Badge variant="secondary">{t(`automation.last.${schedule.last_status}`)}</Badge> : null}
        </p>
        {schedule.workflow_id ? (
          <p className="text-xs text-grid-muted" data-schedule-workflow>
            {t("automation.sched.runs", { name: schedule.workflow_name ? isolate(schedule.workflow_name) : t("automation.sched.deletedWorkflow") })}
            {Object.entries(schedule.values ?? {}).map(([k, v]) => ` · ${k}: ${v}`)}
            {(schedule.secret_params ?? []).map((k) => ` · ${k}: ${t("automation.sched.secretSet")}`)}
          </p>
        ) : null}
        <p className="text-xs text-grid-muted">
          <Ltr mono>{schedule.cron}</Ltr> · <Ltr>{schedule.timezone}</Ltr> · {schedule.agent_name ? <Bdi>{schedule.agent_name}</Bdi> : t("automation.unnamedBrowser")} · {t(`automation.catchUp.${schedule.catch_up}`)}
        </p>
        <p className="text-xs text-grid-muted">
          {schedule.next_run_at ? t("automation.nextRun", { date: formatDate(schedule.next_run_at) }) : ""}
          {schedule.last_run_at ? ` · ${t("automation.lastRun", { date: formatDate(schedule.last_run_at) })}` : ""}
        </p>
        {/* A miss is shown, not swallowed: "there is no data for Tuesday" has
            to have an answer on the page. */}
        {schedule.last_error ? (
          <p className="mt-1 text-xs text-grid-danger-text" data-schedule-error>
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
          <ConfirmButton
            label={t("common.delete")}
            title={t("automation.deleteScheduleTitle")}
            description={t("automation.deleteScheduleBody", { name: isolate(schedule.name) })}
            confirmLabel={t("common.delete")}
            onConfirm={remove}
          />
        </div>
      ) : null}
    </li>
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
  const { t, locale } = useTranslations()
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
          <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
            <Bdi>{workflow.name}</Bdi>
            <Badge variant="outline">{t("automation.editor.versionBadge", { n: workflow.version })}</Badge>
          </p>
          {workflow.description ? (
            <p className="text-sm text-grid-muted">
              <Bdi>{workflow.description}</Bdi>
            </p>
          ) : null}
          <p className="text-xs text-grid-muted">
            {t("automation.stepCount", { n: workflow.steps.length })}
            {workflow.placeholders.length > 0 ? ` · ${t("automation.asksFor", { names: isolate(workflow.placeholders.join(", ")) })}` : ""}
            {workflow.run_count > 0 ? ` · ${t("automation.ranTimes", { n: workflow.run_count })}` : ""}
          </p>
        </div>
        {canRun ? (
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation/workflows/${workflow.id}`} />} data-edit-workflow>
              <PencilIcon />
              {t("automation.editor.edit")}
            </Button>
            <Button variant="outline" size="sm" onClick={() => setOpen((v) => !v)} disabled={browsers.length === 0}>
              <PlayIcon />
              {t("automation.useIt")}
            </Button>
            <ConfirmButton
              label={t("common.delete")}
              title={t("automation.deleteWorkflowTitle")}
              description={t("automation.deleteWorkflowBody", { name: isolate(workflow.name) })}
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
