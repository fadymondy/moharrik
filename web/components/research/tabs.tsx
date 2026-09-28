"use client"

/*
Research's three areas (MH-526): Runs, Evidence and Schedules, split out of
the old single /research page (which held them as in-page tabs) so each is
its own item in the sidebar's Research group. The shared bits — the ask box,
a run's row, the schedule form — stay together here since all three still
read the same "what can this deployment do" capabilities.
*/

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useEffect, useState, type FormEvent } from "react"
import { ArrowRightIcon, BookOpenIcon, LoaderCircleIcon, PlusIcon, SearchIcon } from "lucide-react"
import { toast } from "sonner"

import { Bdi, isolate } from "@/components/bidi"
import { ConfirmButton } from "@/components/confirm-button"
import { PickSelect } from "@/components/crm/shared"
import { EvidenceItem } from "@/components/research/evidence"
import { isLive, ResearchStatusBadge, useFormatDateTime } from "@/components/research/shared"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  SIGNAL_TYPES,
  type ResearchList,
  type ResearchRun,
  type ResearchSchedule,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useEvidence, useResearchRuns, useResearchSchedules } from "@/lib/queries"

export const MIN = 3
export const MAX = 500
export const MAX_URLS = 10

/** Typed text, settled: the list is asked once the reader stops typing. */
export function useSettled(value: string, ms = 300) {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setSettled(value.trim()), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return settled
}

/** http(s) addresses, one per line; null when any line is not one or there are too many. */
export function parseUrls(text: string): string[] | null {
  const lines = text
    .split(/\s*\n\s*/)
    .map((l) => l.trim())
    .filter(Boolean)
  if (lines.length === 0 || lines.length > MAX_URLS) return null
  for (const l of lines) {
    try {
      const u = new URL(l)
      if (u.protocol !== "http:" && u.protocol !== "https:") return null
    } catch {
      return null
    }
  }
  return lines
}

// ── runs ──────────────────────────────────────────────────────────────────

export function RunsTab({ list }: { list: ReturnType<typeof useResearchRuns> }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const canRun = ws.can("research:run")
  const runs = list.data?.runs ?? []
  const live = runs.find((r) => isLive(r.status))
  const canStart = !!list.data && (list.data.can_search || list.data.can_read)

  return (
    <>
      {canRun && canStart ? (
        live ? (
          <LiveNotice run={live} />
        ) : (
          <AskBox caps={list.data!} onStarted={() => list.mutate()} />
        )
      ) : null}

      <div className="grid-separator-contained hatch-band mt-2" aria-hidden />

      <h2 className="px-6 pt-6 pb-3 text-base font-medium">{t("research.runs")}</h2>
      {list.error ? (
        <ErrorState error={list.error} />
      ) : !list.data ? (
        <LoadingRows rows={4} />
      ) : runs.length === 0 ? (
        <EmptyState
          title={t("research.empty")}
          body={canRun && canStart ? t("research.emptyBody") : undefined}
        />
      ) : (
        <ul className="divide-y divide-line border-y border-line" data-research-runs>
          {runs.map((r) => (
            <RunRow key={r.id} run={r} />
          ))}
        </ul>
      )}
    </>
  )
}

function AskBox({ caps, onStarted }: { caps: ResearchList; onStarted: () => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  // Only the modes this deployment can do are offered: search needs SearXNG,
  // a page list needs the reader.
  const modes = [
    ...(caps.can_search ? (["search"] as const) : []),
    ...(caps.can_read ? (["urls"] as const) : []),
  ]
  const [mode, setMode] = useState<"search" | "urls">(modes[0] ?? "search")
  const [topic, setTopic] = useState("")
  const [urlText, setUrlText] = useState("")
  const [busy, setBusy] = useState(false)
  const trimmed = topic.trim()
  const urls = mode === "urls" ? parseUrls(urlText) : []
  const valid = trimmed.length >= MIN && trimmed.length <= MAX && urls !== null

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (!valid || busy) return
    setBusy(true)
    try {
      const res = await api<{ run_id: string }>("/api/w/research", {
        method: "POST",
        json: mode === "urls" ? { topic: trimmed, mode, urls } : { topic: trimmed },
        workspaceId: ws.id,
      })
      toast.success(t("research.started"))
      onStarted()
      router.push(`/${locale}/w/${ws.slug}/research/${encodeURIComponent(res.run_id)}`)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        toast.error(t("research.busy"))
        onStarted()
      } else toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
      setBusy(false)
    }
  }

  return (
    <section className="px-6 pb-6">
      <form
        onSubmit={onSubmit}
        noValidate
        className="max-w-3xl space-y-4"
        data-form="ask-research"
        data-mode={mode}
      >
        {modes.length > 1 ? (
          <div className="space-y-2">
            <p className="text-sm font-medium">{t("research.modeLabel")}</p>
            <ToggleGroup
              variant="outline"
              size="sm"
              spacing={0}
              value={[mode]}
              onValueChange={(v) => v[0] && setMode(v[0] as "search" | "urls")}
              aria-label={t("research.modeLabel")}
            >
              <ToggleGroupItem value="search" data-mode-option="search">
                <SearchIcon />
                {t("research.modeSearch")}
              </ToggleGroupItem>
              <ToggleGroupItem value="urls" data-mode-option="urls">
                <BookOpenIcon />
                {t("research.modeUrls")}
              </ToggleGroupItem>
            </ToggleGroup>
          </div>
        ) : null}

        {mode === "urls" ? (
          <Field>
            <FieldLabel htmlFor="research-urls">{t("research.urls")}</FieldLabel>
            <Textarea
              id="research-urls"
              dir="ltr"
              rows={4}
              value={urlText}
              placeholder={"https://example.com/careers\nhttps://example.com/news/…"}
              onChange={(e) => setUrlText(e.target.value)}
              data-field="urls"
            />
            <FieldDescription>{t("research.urlsHint", { max: MAX_URLS })}</FieldDescription>
            {urlText.trim() && urls === null ? (
              <FieldError>{t("research.urlsInvalid", { max: MAX_URLS })}</FieldError>
            ) : null}
          </Field>
        ) : null}

        <Field>
          <FieldLabel htmlFor="research-topic">
            {mode === "urls" ? t("research.lookFor") : t("research.ask")}
          </FieldLabel>
          <Textarea
            id="research-topic"
            dir="auto"
            rows={mode === "urls" ? 2 : 3}
            maxLength={MAX}
            value={topic}
            placeholder={
              mode === "urls" ? t("research.lookForPlaceholder") : t("research.askPlaceholder")
            }
            onChange={(e) => setTopic(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey))
                e.currentTarget.form?.requestSubmit()
            }}
          />
          <FieldDescription className="flex flex-wrap justify-between gap-2">
            <span>{mode === "urls" ? "" : t("research.askHint")}</span>
            <span dir="ltr" className="tabular-nums">
              {trimmed.length}/{MAX}
            </span>
          </FieldDescription>
        </Field>
        <Button type="submit" disabled={!valid || busy}>
          {busy ? (
            <LoaderCircleIcon className="animate-spin" />
          ) : mode === "urls" ? (
            <BookOpenIcon />
          ) : (
            <SearchIcon />
          )}
          {mode === "urls" ? t("research.readPages") : t("research.start")}
        </Button>
      </form>
    </section>
  )
}

/** Instead of a box that can only answer 409. */
function LiveNotice({ run }: { run: ResearchRun }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  return (
    <section className="px-6 pb-6" data-research-live={run.id}>
      <div className="flex max-w-3xl flex-wrap items-center gap-3 border border-line bg-grid-card px-4 py-3">
        <LoaderCircleIcon className="size-4 shrink-0 animate-spin text-grid-brand" aria-hidden />
        <div className="min-w-0 flex-1 text-sm">
          <p className="font-medium">{t("research.liveTitle")}</p>
          <p className="truncate text-grid-muted">
            <bdi>{run.topic}</bdi>
          </p>
        </div>
        <Button
          variant="outline"
          size="sm"
          nativeButton={false}
          render={<Link href={`/${locale}/w/${ws.slug}/research/${encodeURIComponent(run.id)}`} />}
        >
          {t("research.watch")}
          <ArrowRightIcon className="rtl:rotate-180" />
        </Button>
      </div>
    </section>
  )
}

export function RunRow({ run }: { run: ResearchRun }) {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const fmt = useFormatDateTime()
  const href = `/${locale}/w/${ws.slug}/research/${encodeURIComponent(run.id)}`
  const live = isLive(run.status)
  return (
    <li
      className="flex flex-wrap items-center gap-3 px-6 py-4"
      data-research-run={run.id}
      data-run-status={run.status}
    >
      <div className="min-w-0 flex-1 space-y-1">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          <Link href={href} className="min-w-0 break-words hover:underline">
            <bdi>{run.topic}</bdi>
          </Link>
          <ResearchStatusBadge status={run.status} />
          {run.mode === "urls" ? <Badge variant="outline">{t("research.mode.urls")}</Badge> : null}
          {run.origin !== "manual" ? (
            <Badge variant="outline">{t(`research.origin.${run.origin}`)}</Badge>
          ) : null}
        </p>
        <p className="flex flex-wrap gap-x-2 text-xs text-grid-muted">
          {live ? (
            <span>{run.stage ? t(`research.stage.${run.stage}`) : t("research.inProgress")}</span>
          ) : run.status === "cancelled" ? null : (
            <>
              <span data-counts>
                {t("research.counts", { found: run.found_count, read: run.read_count })}
              </span>
              <span aria-hidden>·</span>
              <span data-evidence-count>
                {t("research.evidenceCount", { n: formatNumber(run.evidence_count) })}
              </span>
            </>
          )}
          {run.status === "cancelled" ? null : <span aria-hidden>·</span>}
          <span>{fmt(run.started_at ?? run.created_at)}</span>
        </p>
        {run.status === "failed" && run.error ? (
          <p className="text-xs text-grid-danger-text">
            <Bdi>{run.error}</Bdi>
          </p>
        ) : null}
      </div>
      <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={href} />}>
        {t("research.open")}
      </Button>
    </li>
  )
}

// ── evidence ──────────────────────────────────────────────────────────────

const PAGE = 25

export function EvidenceTab() {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [q, setQ] = useState("")
  const [company, setCompany] = useState("")
  const [signal, setSignal] = useState("")
  const [limit, setLimit] = useState(PAGE)
  const sq = useSettled(q)
  const sc = useSettled(company)
  useEffect(() => setLimit(PAGE), [sq, sc, signal])
  const ev = useEvidence(ws.id, { q: sq, company: sc, signal, limit })
  const items = ev.data?.evidence ?? []
  const total = ev.data?.total ?? 0
  const filtered = !!(sq || sc || signal)

  return (
    <section data-evidence-tab>
      <p className="px-6 pb-4 text-sm text-grid-muted">{t("research.evidenceTabHint")}</p>
      <div className="grid gap-3 px-6 pb-4 sm:grid-cols-3">
        <Field>
          <FieldLabel htmlFor="ev-q">{t("research.evidenceSearch")}</FieldLabel>
          <Input
            id="ev-q"
            type="search"
            dir="auto"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            data-evidence-q
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="ev-company">{t("research.evidenceCompany")}</FieldLabel>
          <Input
            id="ev-company"
            type="search"
            dir="auto"
            value={company}
            placeholder={t("research.evidenceCompanyPlaceholder")}
            onChange={(e) => setCompany(e.target.value)}
            data-evidence-company-filter
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="ev-signal">{t("research.evidenceSignal")}</FieldLabel>
          <PickSelect
            id="ev-signal"
            value={signal}
            onChange={setSignal}
            noneLabel={t("research.signalAny")}
            options={SIGNAL_TYPES.map((s) => ({ value: s, label: t(`research.signal.${s}`) }))}
          />
        </Field>
      </div>
      {ev.error ? (
        <ErrorState error={ev.error} />
      ) : !ev.data ? (
        <LoadingRows rows={4} />
      ) : total === 0 ? (
        filtered ? (
          <p className="border-y border-line px-6 py-6 text-sm text-grid-muted">
            {t("research.evidenceNoMatch")}
          </p>
        ) : (
          <EmptyState title={t("research.evidenceEmpty")} body={t("research.evidenceEmptyBody")} />
        )
      ) : (
        <>
          <p className="px-6 pb-2 text-xs text-grid-muted" data-evidence-total={total}>
            {t("research.evidenceShowing", {
              shown: formatNumber(items.length),
              total: formatNumber(total),
            })}
          </p>
          <ul className="divide-y divide-line border-y border-line" data-evidence-list>
            {items.map((e) => (
              <EvidenceItem key={e.id} e={e} showRun />
            ))}
          </ul>
          {items.length < total && limit < 100 ? (
            <div className="px-6 pt-4">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setLimit((l) => Math.min(l + PAGE, 100))}
              >
                {t("research.loadMore")}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </section>
  )
}

// ── schedules and budget ──────────────────────────────────────────────────

export function SchedulesTab({ caps }: { caps?: ResearchList }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const canRun = ws.can("research:run")
  const data = useResearchSchedules(ws.id)
  const schedules = data.data?.schedules ?? []
  const budget = data.data?.budget
  const possible = !!caps && (caps.can_search || caps.can_read)

  return (
    <section data-schedules-tab>
      {budget ? (
        <BudgetPanel budget={budget} canEdit={canRun} onSaved={() => data.mutate()} />
      ) : null}

      <div className="grid-separator-contained hatch-band mt-2" aria-hidden />

      {data.error ? (
        <ErrorState error={data.error} />
      ) : !data.data ? (
        <LoadingRows rows={3} />
      ) : schedules.length === 0 ? (
        <EmptyState
          title={t("research.schedulesEmpty")}
          body={possible && canRun ? t("research.schedulesEmptyBody") : undefined}
        />
      ) : (
        <ul className="divide-y divide-line border-y border-line" data-research-schedules>
          {schedules.map((s) => (
            <ScheduleRow key={s.id} schedule={s} canRun={canRun} onChange={() => data.mutate()} />
          ))}
        </ul>
      )}

      {!canRun ? (
        <p className="px-6 py-6 text-sm text-grid-muted">{t("research.schedulesReadOnly")}</p>
      ) : !possible ? (
        caps ? (
          <p className="px-6 py-6 text-sm text-grid-muted" data-schedules-not-configured>
            {t("research.schedulesNotConfigured")}
          </p>
        ) : null
      ) : (
        <NewSchedule caps={caps!} onCreated={() => data.mutate()} />
      )}
    </section>
  )
}

function BudgetPanel({
  budget,
  canEdit,
  onSaved,
}: {
  budget: { daily_run_budget: number; runs_today: number }
  canEdit: boolean
  onSaved: () => void
}) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [value, setValue] = useState(String(budget.daily_run_budget))
  const [busy, setBusy] = useState(false)
  useEffect(() => setValue(String(budget.daily_run_budget)), [budget.daily_run_budget])
  const n = Number(value)
  const valid = value.trim() !== "" && Number.isInteger(n) && n >= 0 && n <= 50

  async function save(e: FormEvent) {
    e.preventDefault()
    if (!valid) return
    setBusy(true)
    try {
      await api("/api/w/research-schedules/budget", {
        method: "PUT",
        json: { daily_run_budget: n },
        workspaceId: ws.id,
      })
      toast.success(t("research.budgetSaved"))
      onSaved()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section
      className="px-6 pb-6"
      data-budget={budget.daily_run_budget}
      data-runs-today={budget.runs_today}
    >
      <h2 className="text-base font-medium">{t("research.budget")}</h2>
      <p className="mt-1 text-sm">
        {t("research.budgetLine", {
          used: formatNumber(budget.runs_today),
          budget: formatNumber(budget.daily_run_budget),
        })}
      </p>
      <p className="mt-1 max-w-3xl text-sm text-grid-muted">{t("research.budgetHint")}</p>
      {canEdit ? (
        <form onSubmit={save} className="mt-4 flex flex-wrap items-end gap-3" noValidate>
          <Field className="w-40">
            <FieldLabel htmlFor="research-budget">{t("research.budgetRuns")}</FieldLabel>
            <Input
              id="research-budget"
              type="number"
              min={0}
              max={50}
              dir="ltr"
              value={value}
              onChange={(e) => setValue(e.target.value)}
            />
          </Field>
          <Button
            type="submit"
            variant="outline"
            disabled={!valid || busy || n === budget.daily_run_budget}
          >
            {busy ? t("common.working") : t("common.save")}
          </Button>
        </form>
      ) : null}
    </section>
  )
}

function ScheduleRow({
  schedule,
  canRun,
  onChange,
}: {
  schedule: ResearchSchedule
  canRun: boolean
  onChange: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const fmt = useFormatDateTime()

  async function toggle(enabled: boolean) {
    try {
      await api(`/api/w/research-schedules/${encodeURIComponent(schedule.id)}`, {
        method: "PATCH",
        json: { enabled },
        workspaceId: ws.id,
      })
      onChange()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    }
  }

  async function remove() {
    await api(`/api/w/research-schedules/${encodeURIComponent(schedule.id)}`, {
      method: "DELETE",
      workspaceId: ws.id,
    })
    toast.success(t("research.scheduleDeleted"))
    onChange()
  }

  return (
    <li
      className="flex flex-wrap items-start gap-3 px-6 py-4"
      data-research-schedule={schedule.name}
      data-schedule-status={schedule.last_status || "new"}
    >
      <div className="min-w-0 flex-1 space-y-1">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          <Bdi>{schedule.name}</Bdi>
          <Badge variant={schedule.enabled ? "default" : "outline"}>
            {schedule.enabled ? t("research.scheduleOn") : t("research.scheduleOff")}
          </Badge>
          <Badge variant="outline">{t(`research.cadence.${schedule.cadence}`)}</Badge>
          {schedule.mode === "urls" ? (
            <Badge variant="outline">{t("research.mode.urls")}</Badge>
          ) : null}
          {schedule.last_status ? (
            <Badge
              variant={schedule.last_status === "started" ? "secondary" : "outline"}
              data-last-status={schedule.last_status}
            >
              {t(`research.last.${schedule.last_status}`)}
            </Badge>
          ) : null}
        </p>
        <p className="text-sm text-grid-muted" dir="auto">
          {schedule.topic}
        </p>
        {schedule.mode === "urls" ? (
          <ul className="text-xs text-grid-muted">
            {schedule.urls.map((u) => (
              <li key={u} className="truncate">
                <bdi dir="ltr">{u}</bdi>
              </li>
            ))}
          </ul>
        ) : null}
        <p className="text-xs text-grid-muted">
          {schedule.enabled ? t("research.nextRun", { date: fmt(schedule.next_run_at) }) : null}
          {schedule.last_run_at
            ? `${schedule.enabled ? " · " : ""}${t("research.lastRun", { date: fmt(schedule.last_run_at) })}`
            : ""}
        </p>
        {schedule.last_note ? (
          <p className="text-xs text-grid-warn-text" data-schedule-note>
            <Bdi>{schedule.last_note}</Bdi>
          </p>
        ) : null}
        {schedule.last_run_id ? (
          <Link
            className="text-xs text-grid-brand hover:underline"
            href={`/${locale}/w/${ws.slug}/research/${encodeURIComponent(schedule.last_run_id)}`}
          >
            {t("research.openLastRun")}
          </Link>
        ) : null}
      </div>
      {canRun ? (
        <div className="flex items-center gap-2">
          <Switch
            checked={schedule.enabled}
            onCheckedChange={(v) => toggle(!!v)}
            aria-label={schedule.enabled ? t("research.scheduleOn") : t("research.scheduleOff")}
          />
          <ConfirmButton
            label={t("common.delete")}
            title={t("research.scheduleDeleteTitle")}
            description={t("research.scheduleDeleteBody", { name: isolate(schedule.name) })}
            confirmLabel={t("common.delete")}
            onConfirm={remove}
          />
        </div>
      ) : null}
    </li>
  )
}

function NewSchedule({ caps, onCreated }: { caps: ResearchList; onCreated: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const modes = [
    ...(caps.can_search ? (["search"] as const) : []),
    ...(caps.can_read ? (["urls"] as const) : []),
  ]
  const [mode, setMode] = useState<"search" | "urls">(modes[0] ?? "search")
  const [name, setName] = useState("")
  const [topic, setTopic] = useState("")
  const [urlText, setUrlText] = useState("")
  const [cadence, setCadence] = useState("daily")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const urls = mode === "urls" ? parseUrls(urlText) : []
  const valid = name.trim() !== "" && topic.trim().length >= MIN && urls !== null

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!valid) return
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/research-schedules", {
        json: {
          name: name.trim(),
          topic: topic.trim(),
          mode,
          cadence,
          ...(mode === "urls" ? { urls } : {}),
        },
        workspaceId: ws.id,
      })
      toast.success(t("research.scheduleCreated"))
      setName("")
      setTopic("")
      setUrlText("")
      onCreated()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="px-6 py-6" data-form="new-research-schedule">
      <h2 className="text-base font-medium">{t("research.scheduleNew")}</h2>
      <form onSubmit={submit} noValidate className="mt-4 max-w-3xl space-y-4">
        <FieldGroup className="sm:flex-row sm:items-end">
          <Field className="sm:flex-1">
            <FieldLabel htmlFor="rs-name">{t("research.scheduleName")}</FieldLabel>
            <Input
              id="rs-name"
              dir="auto"
              maxLength={120}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field className="sm:w-44">
            <FieldLabel htmlFor="rs-cadence">{t("research.cadence")}</FieldLabel>
            <PickSelect
              id="rs-cadence"
              value={cadence}
              onChange={setCadence}
              options={[
                { value: "daily", label: t("research.cadence.daily") },
                { value: "weekly", label: t("research.cadence.weekly") },
              ]}
            />
          </Field>
        </FieldGroup>
        {modes.length > 1 ? (
          <ToggleGroup
            variant="outline"
            size="sm"
            spacing={0}
            value={[mode]}
            onValueChange={(v) => v[0] && setMode(v[0] as "search" | "urls")}
            aria-label={t("research.modeLabel")}
          >
            <ToggleGroupItem value="search">
              <SearchIcon />
              {t("research.modeSearch")}
            </ToggleGroupItem>
            <ToggleGroupItem value="urls">
              <BookOpenIcon />
              {t("research.modeUrls")}
            </ToggleGroupItem>
          </ToggleGroup>
        ) : null}
        {mode === "urls" ? (
          <Field>
            <FieldLabel htmlFor="rs-urls">{t("research.urls")}</FieldLabel>
            <Textarea
              id="rs-urls"
              dir="ltr"
              rows={3}
              value={urlText}
              onChange={(e) => setUrlText(e.target.value)}
            />
            <FieldDescription>{t("research.urlsHint", { max: MAX_URLS })}</FieldDescription>
            {urlText.trim() && urls === null ? (
              <FieldError>{t("research.urlsInvalid", { max: MAX_URLS })}</FieldError>
            ) : null}
          </Field>
        ) : null}
        <Field>
          <FieldLabel htmlFor="rs-topic">
            {mode === "urls" ? t("research.lookFor") : t("research.ask")}
          </FieldLabel>
          <Textarea
            id="rs-topic"
            dir="auto"
            rows={2}
            maxLength={MAX}
            value={topic}
            onChange={(e) => setTopic(e.target.value)}
          />
        </Field>
        {err ? <FieldError>{err}</FieldError> : null}
        <Button type="submit" disabled={!valid || busy}>
          {busy ? <LoaderCircleIcon className="animate-spin" /> : <PlusIcon />}
          {t("research.scheduleCreate")}
        </Button>
      </form>
    </section>
  )
}
