"use client"

/*
The Research home (MH-1066): describe who you are looking for, watch the run's stages, see how many
companies were found, how much evidence backs them, how many prospects were scored and how many are
high-fit, then accept one into the CRM, draft outreach or put the topic on a schedule.

Every number comes from GET /api/w/research-overview (counts of rows that exist). Stages are the
run's own stages as stored. Only actions the backend has are drawn: starting a run (POST
/api/w/research), accepting a prospect, drafting outreach (stored, never sent) and creating a
research schedule. Only discovery runs score prospects, so a manual run shows 0 there and says so.
Tones: info = in progress, success = done, danger = failed, violet = the product's own scoring.
*/

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useState, type FormEvent, type ReactNode } from "react"
import {
  ArrowRightIcon,
  BuildingIcon,
  CalendarClockIcon,
  CheckCircle2Icon,
  CircleDashedIcon,
  CircleIcon,
  CircleSlashIcon,
  FileTextIcon,
  LanguagesIcon,
  LoaderCircleIcon,
  SearchCheckIcon,
  SearchIcon,
  SparklesIcon,
  UserCheckIcon,
  XCircleIcon,
} from "lucide-react"
import { Badge, Button, Input, toast } from "@fadymondy/nasaq/web"

import { Bdi, isolate } from "@/components/bidi"
import { Empty, Panel, useFormat } from "@/components/overview/overview-cards"
import { AcceptDialog } from "@/components/prospects/decide"
import { ReasonList, ScoreBadge } from "@/components/prospects/shared"
import { MAX, MIN } from "@/components/research/tabs"
import { CapabilityNotes, isLive, ResearchStatusBadge } from "@/components/research/shared"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Prospect, type ResearchStage } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useProspects, useResearchOverview, useResearchRuns } from "@/lib/queries"
import type { FlowCounts, FlowRun } from "@/lib/research-flow-types"
import { cn } from "@/lib/utils"

const STAGE_TONE: Record<ResearchStage["status"], string> = {
  pending: "text-muted-foreground",
  running: "text-nq-info-text",
  done: "text-nq-success-text",
  skipped: "text-muted-foreground",
  failed: "text-nq-danger-text",
}

const STAGE_ICON: Record<ResearchStage["status"], ReactNode> = {
  pending: <CircleIcon />,
  running: <LoaderCircleIcon className="animate-spin" />,
  done: <CheckCircle2Icon />,
  skipped: <CircleSlashIcon />,
  failed: <XCircleIcon />,
}

export function ResearchFlagship({ base }: { base: string }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const ov = useResearchOverview(ws.id)
  const caps = useResearchRuns(ws.id)
  const refresh = () => {
    void ov.mutate()
    void caps.mutate()
  }
  if (ov.error) return <ErrorState error={ov.error} />
  const o = ov.data
  const canStart = !!caps.data && caps.data.can_search
  // The empty states point at the describe input only when it is drawn.
  const canDescribe = ws.can("research:run") && canStart
  return (
    <div className="space-y-4 sm:space-y-6" data-research-flagship>
      {ws.can("research:run") && canStart ? <Describe onStarted={refresh} /> : null}
      {ws.can("research:run") && caps.data && !canStart && caps.data.can_read ? (
        <p className="text-sm text-muted-foreground" data-flagship-cap>
          {t("rflag.pagesOnly", { runs: t("research.tabRuns") })}
        </p>
      ) : null}
      {!o ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          <Funnel counts={o.totals} threshold={o.thresholds.high_fit_score} />
          <Flow runs={o.runs} base={base} canDescribe={canDescribe} />
          <div className="grid grid-cols-1 gap-4 sm:gap-6 lg:grid-cols-2">
            <Waiting threshold={o.thresholds.high_fit_score} base={base} onChanged={refresh} />
            <Runs runs={o.runs} base={base} schedules={o.schedules} onChanged={refresh} canDescribe={canDescribe} />
          </div>
          {caps.data ? <CapabilityNotes caps={caps.data} /> : null}
        </>
      )}
    </div>
  )
}

// ── describe ─────────────────────────────────────────────────────────────

function Describe({ onStarted }: { onStarted: () => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const [topic, setTopic] = useState("")
  const [busy, setBusy] = useState(false)
  const trimmed = topic.trim()
  const valid = trimmed.length >= MIN && trimmed.length <= MAX

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!valid || busy) return
    setBusy(true)
    try {
      const res = await api<{ run_id: string }>("/api/w/research", {
        method: "POST",
        json: { topic: trimmed },
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
    <section className="border border-nq-line bg-card px-5 py-5" data-flagship-describe>
      <form onSubmit={submit} noValidate className="space-y-3" data-form="describe-research">
        <label htmlFor="rflag-topic" className="block text-base font-medium">
          {t("rflag.describe")}
        </label>
        <p className="text-sm text-muted-foreground">{t("rflag.describeHint")}</p>
        <div className="flex flex-col gap-2 sm:flex-row">
          <Input
            id="rflag-topic"
            dir="auto"
            maxLength={MAX}
            value={topic}
            onChange={(e) => setTopic(e.target.value)}
            placeholder={t("rflag.placeholder")}
            className="min-w-0 flex-1"
            data-input="describe"
          />
          <Button type="submit" disabled={!valid || busy} data-action="start-research">
            {busy ? <LoaderCircleIcon className="animate-spin" /> : <SearchIcon />}
            {t("rflag.start")}
          </Button>
        </div>
      </form>
    </section>
  )
}

// ── funnel ───────────────────────────────────────────────────────────────

function Funnel({ counts, threshold }: { counts: FlowCounts; threshold: number }) {
  const { t } = useTranslations()
  const f = useFormat()
  const steps: { key: "found" | "evidence" | "prospects" | "high_fit"; icon: ReactNode; tone: string; soft: string }[] = [
    { key: "found", icon: <BuildingIcon />, tone: "text-nq-info-text", soft: "bg-nq-info-soft" },
    { key: "evidence", icon: <FileTextIcon />, tone: "text-nq-info-text", soft: "bg-nq-info-soft" },
    { key: "prospects", icon: <SearchCheckIcon />, tone: "text-nq-tag-violet", soft: "bg-nq-tag-violet-soft" },
    { key: "high_fit", icon: <SparklesIcon />, tone: "text-nq-tag-violet", soft: "bg-nq-tag-violet-soft" },
  ]
  return (
    <section aria-label={t("rflag.funnel")} data-flagship-funnel>
      <ol className="grid grid-cols-2 gap-px border border-nq-line bg-nq-line lg:grid-cols-4">
        {steps.map((s) => (
          <li key={s.key} className="min-w-0 bg-card px-5 py-4" data-funnel={s.key} data-value={counts[s.key]}>
            <p className="flex items-center gap-2 text-xs font-medium text-muted-foreground">
              <span className={cn("flex size-6 shrink-0 items-center justify-center rounded-md [&>svg]:size-3.5", s.soft, s.tone)} aria-hidden>
                {s.icon}
              </span>
              <span className="min-w-0 break-words">{t(`rflag.funnel.${s.key}`)}</span>
            </p>
            <p className="mt-2 text-2xl font-semibold tabular-nums">{f.n(counts[s.key])}</p>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {s.key === "high_fit"
                ? t("rflag.funnelSub.high_fit", { score: f.n(threshold), accepted: f.n(counts.accepted) })
                : t(`rflag.funnelSub.${s.key}`)}
            </p>
          </li>
        ))}
      </ol>
    </section>
  )
}

// ── live flow ────────────────────────────────────────────────────────────

/** How long a stage took, only when the engine stored both times. */
function stageSeconds(s: ResearchStage): number | null {
  if (!s.started_at || !s.finished_at) return null
  const d = (Date.parse(s.finished_at) - Date.parse(s.started_at)) / 1000
  return Number.isFinite(d) && d >= 0 ? d : null
}

function Flow({ runs, base, canDescribe }: { runs: FlowRun[]; base: string; canDescribe: boolean }) {
  const { t } = useTranslations()
  const f = useFormat()
  const run = runs.find((r) => isLive(r.status)) ?? runs[0]
  return (
    <Panel
      id="research-flow"
      title={run && isLive(run.status) ? t("rflag.flowLive") : t("rflag.flowLatest")}
      icon={<SearchCheckIcon />}
      href={run ? `${base}/research/${run.id}` : undefined}
      meta={run ? <ResearchStatusBadge status={run.status} /> : undefined}
    >
      {!run ? (
        <Empty title={t("rflag.noRuns")} body={t(canDescribe ? "rflag.noRunsBody" : "rflag.noRunsBodyPast")} />
      ) : (
        <div className="space-y-3 px-5 py-4" data-flow-run={run.id} data-flow-status={run.status}>
          <p className="text-sm font-medium break-words" dir="auto">
            <Bdi>{run.topic}</Bdi>
          </p>
          {run.error ? (
            <p className="text-sm break-words text-nq-danger-text" dir="auto">
              {run.error}
            </p>
          ) : null}
          {run.stages.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("rflag.noStages")}</p>
          ) : (
            <ol className="grid grid-cols-1 gap-2 sm:grid-cols-3 xl:grid-cols-6" data-flow-stages>
              {run.stages.map((s) => {
                const secs = stageSeconds(s)
                return (
                  <li
                    key={s.stage}
                    className="min-w-0 border border-nq-line px-3 py-2"
                    data-stage={s.stage}
                    data-stage-status={s.status}
                  >
                    <p className={cn("flex items-center gap-1.5 text-sm font-medium [&>svg]:size-4 [&>svg]:shrink-0", STAGE_TONE[s.status])}>
                      {STAGE_ICON[s.status]}
                      <span className="min-w-0 break-words">{t(`research.stage.${s.stage}`)}</span>
                      {s.status === "done" ? <span className="ms-auto tabular-nums">{f.n(s.count)}</span> : null}
                    </p>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {t(`research.stageStatus.${s.status}`)}
                      {secs !== null ? ` · ${t("rflag.seconds", { n: f.n(Math.round(secs)) })}` : ""}
                    </p>
                    {s.note ? (
                      <p className="mt-1 text-xs break-words text-muted-foreground" dir="auto">
                        {s.note}
                      </p>
                    ) : null}
                  </li>
                )
              })}
            </ol>
          )}
          <RunCounts run={run} />
        </div>
      )}
    </Panel>
  )
}

/** found, evidence, prospects, high-fit for one run, with the honest note for runs that never score. */
function RunCounts({ run }: { run: FlowRun }) {
  const { t } = useTranslations()
  const f = useFormat()
  const c = run.counts
  return (
    <div data-run-counts={run.id}>
      <p className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm tabular-nums">
        <span data-count="found" data-value={c.found}>{t("rflag.run.found", { n: f.n(c.found) })}</span>
        <span data-count="evidence" data-value={c.evidence}>{t("rflag.run.evidence", { n: f.n(c.evidence) })}</span>
        <span data-count="prospects" data-value={c.prospects}>{t("rflag.run.prospects", { n: f.n(c.prospects) })}</span>
        <span data-count="high_fit" data-value={c.high_fit} className={c.high_fit > 0 ? "text-nq-tag-violet" : undefined}>
          {t("rflag.run.highFit", { n: f.n(c.high_fit) })}
        </span>
      </p>
      {run.origin !== "discovery" ? <p className="mt-1 text-xs text-muted-foreground">{t("rflag.notScored")}</p> : null}
    </div>
  )
}

// ── prospects waiting ────────────────────────────────────────────────────

function Waiting({ threshold, base, onChanged }: { threshold: number; base: string; onChanged: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const f = useFormat()
  const list = useProspects(ws.id, "new")
  const items = [...(list.data?.prospects ?? [])].sort((a, b) => b.score - a.score).slice(0, 5)
  const total = list.data?.counts.new ?? items.length
  const [accepting, setAccepting] = useState<Prospect | null>(null)
  return (
    <Panel
      id="research-waiting"
      title={t("rflag.waiting")}
      icon={<UserCheckIcon />}
      href={`${base}/prospects`}
      meta={total > 0 ? <Badge variant="brand">{f.n(total)}</Badge> : undefined}
    >
      {list.error ? (
        <ErrorState error={list.error} />
      ) : !list.data ? (
        <LoadingRows rows={3} />
      ) : items.length === 0 ? (
        <Empty title={t("rflag.waitingEmpty")} body={t("rflag.waitingEmptyBody")} />
      ) : (
        <ul className="divide-y divide-nq-line" data-waiting-list>
          {items.map((p) => (
            <WaitingRow key={p.id} p={p} base={base} threshold={threshold} onAccept={() => setAccepting(p)} />
          ))}
        </ul>
      )}
      {list.data && total > items.length ? (
        <p className="border-t border-nq-line px-5 py-3 text-xs text-muted-foreground">
          {t("opps.showing", { shown: f.n(items.length), total: f.n(total) })}
        </p>
      ) : null}
      {accepting ? (
        <AcceptDialog
          p={accepting}
          onClose={() => setAccepting(null)}
          onDone={() => {
            void list.mutate()
            onChanged()
          }}
        />
      ) : null}
    </Panel>
  )
}

function WaitingRow({ p, base, threshold, onAccept }: { p: Prospect; base: string; threshold: number; onAccept: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState<"" | "en" | "ar">("")
  const [made, setMade] = useState(false)
  const canDecide = ws.can("contacts:write")

  async function draft(locale: "en" | "ar") {
    setBusy(locale)
    try {
      await api(`/api/w/prospects/${encodeURIComponent(p.id)}/draft`, { json: { locale }, workspaceId: ws.id })
      setMade(true)
      toast.success(t("rflag.draftMade"))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy("")
    }
  }

  return (
    <li className="space-y-2 px-5 py-3" data-prospect={p.id} data-prospect-score={p.score}>
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <Link href={`${base}/prospects/${p.id}`} className="block text-sm font-medium break-words hover:underline" dir="auto">
            <Bdi>{p.name}</Bdi>
          </Link>
          {p.domain ? <p className="text-xs break-words text-muted-foreground">{isolate(p.domain)}</p> : null}
        </div>
        <ScoreBadge score={p.score} />
        {p.score >= threshold ? <Badge variant="brand">{t("rflag.highFit")}</Badge> : null}
      </div>
      <ReasonList reasons={p.reasons} compact />
      {canDecide ? (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" onClick={onAccept} data-action="accept-prospect">
            <UserCheckIcon />
            {t("rflag.accept")}
          </Button>
          {(["en", "ar"] as const).map((l) => (
            <Button key={l} size="sm" variant="secondary" disabled={!!busy} onClick={() => draft(l)} data-action={`draft-${l}`}>
              {busy === l ? <LoaderCircleIcon className="animate-spin" /> : <LanguagesIcon />}
              {l === "en" ? t("prospects.draftEn") : t("prospects.draftAr")}
            </Button>
          ))}
          {made ? (
            <Button size="sm" variant="ghost" nativeButton={false} render={<Link href={`${base}/prospects/${p.id}`} />}>
              {t("rflag.openDraft")}
              <ArrowRightIcon className="rtl:-scale-x-100" />
            </Button>
          ) : null}
        </div>
      ) : null}
    </li>
  )
}

// ── recent runs and schedules ────────────────────────────────────────────

function Runs({
  runs,
  base,
  schedules,
  onChanged,
  canDescribe,
}: {
  runs: FlowRun[]
  base: string
  schedules: { total: number; enabled: number; next_run_at: string | null }
  onChanged: () => void
  canDescribe: boolean
}) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <Panel id="research-recent" title={t("rflag.recent")} icon={<CircleDashedIcon />} href={`${base}/research/runs`}>
      <p className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-nq-line px-5 py-2 text-xs text-muted-foreground" data-schedules-line>
        <CalendarClockIcon className="size-3.5" aria-hidden />
        {schedules.total === 0
          ? t("rflag.schedules.none")
          : t("rflag.schedules.some", { enabled: f.n(schedules.enabled), total: f.n(schedules.total) })}
        {schedules.next_run_at ? <span>{t("rflag.schedules.next", { when: f.ago(schedules.next_run_at) })}</span> : null}
        <Link href={`${base}/research/schedules`} className="ms-auto hover:underline">
          {t("research.tabSchedules")}
        </Link>
      </p>
      {runs.length === 0 ? (
        <Empty title={t("rflag.noRuns")} body={t(canDescribe ? "rflag.noRunsBody" : "rflag.noRunsBodyPast")} />
      ) : (
        <ul className="divide-y divide-nq-line" data-recent-runs>
          {runs.slice(0, 6).map((r) => (
            <RunRow key={r.id} run={r} base={base} onChanged={onChanged} />
          ))}
        </ul>
      )}
    </Panel>
  )
}

function RunRow({ run, base, onChanged }: { run: FlowRun; base: string; onChanged: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const f = useFormat()
  const [busy, setBusy] = useState(false)
  // A page-list run cannot be repeated from its topic alone (the pages are not in the overview).
  const canSchedule = ws.can("research:run") && run.mode === "search" && run.origin !== "discovery"

  async function schedule(cadence: "daily" | "weekly") {
    setBusy(true)
    try {
      await api("/api/w/research-schedules", {
        json: { name: run.topic.slice(0, 120), topic: run.topic, mode: "search", cadence },
        workspaceId: ws.id,
      })
      toast.success(t("research.scheduleCreated"))
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <li className="space-y-1.5 px-5 py-3" data-run={run.id} data-run-status={run.status} data-run-origin={run.origin}>
      <div className="flex items-start gap-2">
        <Link href={`${base}/research/${run.id}`} className="min-w-0 flex-1 text-sm font-medium break-words hover:underline" dir="auto">
          <Bdi>{run.topic}</Bdi>
        </Link>
        <ResearchStatusBadge status={run.status} />
      </div>
      <p className="text-xs text-muted-foreground">
        {t(`rflag.origin.${run.origin}`)} · <span title={f.exact(run.created_at)}>{f.ago(run.created_at)}</span>
      </p>
      <RunCounts run={run} />
      {canSchedule ? (
        <div className="flex flex-wrap items-center gap-2 pt-1">
          <span className="text-xs text-muted-foreground">{t("rflag.scheduleThis")}</span>
          {(["daily", "weekly"] as const).map((c) => (
            <Button key={c} size="sm" variant="secondary" disabled={busy} onClick={() => schedule(c)} data-action={`schedule-${c}`}>
              <CalendarClockIcon />
              {t(`research.cadence.${c}`)}
            </Button>
          ))}
        </div>
      ) : null}
    </li>
  )
}
