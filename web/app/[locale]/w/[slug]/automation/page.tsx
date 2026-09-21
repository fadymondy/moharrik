"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { CheckIcon, OctagonXIcon, PlayIcon, PlusIcon, ShieldCheckIcon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationRun, type BrowserAgent } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useAutomationSettings, useBrowsers, useRuns } from "@/lib/queries"
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

      {/* ---- browsers ---- */}
      <section>
        <h2 className="px-6 pb-1 text-base font-medium">{t("automation.browsers")}</h2>
        <p className="px-6 pb-4 text-sm text-grid-muted">{t("automation.browsersHint")}</p>

        {browsers.error ? (
          <ErrorState error={browsers.error} />
        ) : browsers.isLoading ? (
          <LoadingRows rows={2} />
        ) : list.length === 0 ? (
          <EmptyState title={t("automation.noBrowsers")} body={canManage ? t("automation.noBrowsersBody") : undefined} />
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
  const { t, formatDate } = useTranslations()
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

function ApproveForm({ onApproved }: { onApproved: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [code, setCode] = useState("")
  const [name, setName] = useState("")
  const [hosts, setHosts] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/browsers/approve", {
        json: {
          code: code.trim(),
          name: name.trim(),
          allowed_hosts: hosts.split(",").map((h) => h.trim()).filter(Boolean),
        },
        workspaceId: ws.id,
      })
      toast.success(t("automation.paired"))
      setCode("")
      setName("")
      setHosts("")
      onApproved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="px-6 pt-6" data-form="approve">
      <p className="pb-3 text-sm text-grid-muted">{t("automation.approveHint", { slug: ws.slug })}</p>
      <FieldGroup className="sm:flex-row sm:items-end">
        <Field className="sm:w-40">
          <FieldLabel htmlFor="pair-code">{t("automation.code")}</FieldLabel>
          <Input id="pair-code" dir="ltr" inputMode="numeric" value={code} onChange={(e) => setCode(e.target.value)} maxLength={12} />
        </Field>
        <Field className="sm:w-56">
          <FieldLabel htmlFor="pair-name">
            {t("automation.browserName")} <span className="text-grid-muted">({t("common.optional")})</span>
          </FieldLabel>
          <Input id="pair-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
        </Field>
        <Field className="sm:flex-1">
          <FieldLabel htmlFor="pair-hosts">
            {t("automation.allowedHosts")} <span className="text-grid-muted">({t("common.optional")})</span>
          </FieldLabel>
          <Input id="pair-hosts" dir="ltr" value={hosts} onChange={(e) => setHosts(e.target.value)} placeholder="example.com" />
          <FieldDescription>{t("automation.allowedHostsHint")}</FieldDescription>
        </Field>
        <Button type="submit" disabled={busy || !code.trim()}>
          <PlusIcon />
          {busy ? t("common.working") : t("automation.approve")}
        </Button>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
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
