"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { Fragment, useState, type ReactNode } from "react"
import {
  ArrowLeftIcon,
  ArrowRightIcon,
  ChevronDownIcon,
  BotIcon,
  ExternalLinkIcon,
  InfoIcon,
  LoaderCircleIcon,
  SquareIcon,
  TriangleAlertIcon,
} from "lucide-react"
import {
  Badge,
  Button,
  Collapsible,
  CollapsiblePanel,
  CollapsibleTrigger,
  Skeleton,
  toast,
} from "@fadymondy/nasaq/web"

import { SectionHeader } from "@/components/public-frame"
import { AgentPanel } from "@/components/research/agent-panel"
import { CostLine, EvidenceItem, SignalBadge, StageList } from "@/components/research/evidence"
import { isLive, ResearchStatusBadge, useFormatDateTime } from "@/components/research/shared"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  type CompanySuggestion,
  type Evidence,
  type ResearchRun,
  type ResearchSource,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useResearchContent, useResearchRun, useResearchRuns } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

export default function ResearchRunPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const fmt = useFormatDateTime()
  const { id } = useParams<{ id: string }>()
  const detail = useResearchRun(ws.id, id)
  // For can_summarise: an empty summary means something different on a
  // deployment with no model than on one whose model failed.
  const caps = useResearchRuns(ws.id)
  const run = detail.data?.run
  const sources = detail.data?.sources ?? []
  useDocumentTitle(`${run?.topic || t("research.run")} · ${ws.summary.name}`)

  const canRun = ws.can("research:run")
  const live = isLive(run?.status)
  const [cancelling, setCancelling] = useState(false)

  // Page text is withheld by the API unless asked for; it is asked for the
  // first time somebody opens a source's text, and then kept.
  const [wantText, setWantText] = useState(false)
  const content = useResearchContent(ws.id, id, wantText && !live)
  const textByPosition = new Map(
    (content.data?.sources ?? []).map((s) => [s.position, s.content ?? ""] as const),
  )

  async function cancel() {
    setCancelling(true)
    try {
      await api(`/api/w/research/${encodeURIComponent(id)}/cancel`, {
        method: "POST",
        workspaceId: ws.id,
      })
      toast.success(t("research.cancelled"))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setCancelling(false)
      void detail.mutate()
      void caps.mutate()
    }
  }

  const read = sources.filter((s) => s.fetched).length

  return (
    <>
      <SectionHeader
        micro={t("nav.research")}
        title={run ? <Bdi>{run.topic}</Bdi> : <Skeleton className="h-7 w-72 max-w-full" />}
        description={
          run ? t("research.startedAt", { date: fmt(run.started_at ?? run.created_at) }) : undefined
        }
        action={
          <div className="flex flex-wrap items-center gap-2">
            {canRun && live ? (
              <Button
                variant="secondary"
                size="sm"
                onClick={cancel}
                disabled={cancelling}
                data-action="cancel-research"
              >
                {cancelling ? <LoaderCircleIcon className="animate-spin" /> : <SquareIcon />}
                {t("research.cancel")}
              </Button>
            ) : null}
            <Button
              variant="ghost"
              size="sm"
              nativeButton={false}
              render={<Link href={`/${locale}/w/${ws.slug}/research/runs`} />}
            >
              <ArrowLeftIcon className="rtl:rotate-180" />
              {t("research.back")}
            </Button>
          </div>
        }
      />

      {detail.error ? (
        <ErrorState error={detail.error} />
      ) : !run ? (
        <LoadingRows rows={4} />
      ) : (
        <>
          <div
            className="flex flex-wrap items-center gap-x-2 gap-y-1 px-6 pb-4 text-sm text-muted-foreground"
            data-run-status={run.status}
          >
            <ResearchStatusBadge status={run.status} />
            <span data-counts>
              {live
                ? t("research.countsSoFar", { found: sources.length, read })
                : // A stopped run never wrote its counts; what it kept is the sources.
                  run.status === "cancelled"
                  ? t("research.counts", { found: sources.length, read })
                  : t("research.counts", { found: run.found_count, read: run.read_count })}
            </span>
            {run.finished_at && !live ? (
              <>
                <span aria-hidden>·</span>
                <span>{t("research.finishedAt", { date: fmt(run.finished_at) })}</span>
              </>
            ) : null}
          </div>

          <StageList stages={run.stages ?? []} />
          {run.cost ? <CostLine cost={run.cost} /> : null}
          {run.mode === "urls" && run.urls?.length ? (
            <div className="px-6 pt-3 text-xs text-muted-foreground" data-urls-given>
              <p className="font-medium text-foreground">{t("research.pagesGiven")}</p>
              <ul>
                {run.urls.map((u) => (
                  <li key={u} className="truncate">
                    <bdi dir="ltr">{u}</bdi>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          <div className="pb-6" />

          <Outcome
            run={run}
            sources={sources}
            live={live}
            canSummarise={caps.data?.can_summarise}
            onChanged={() => void detail.mutate()}
          />

          {!live ? (
            <RunEvidence
              evidence={detail.data?.evidence ?? []}
              companies={detail.data?.companies ?? []}
            />
          ) : null}

          <div className="hatch h-8 border-y border-border mt-2" aria-hidden />

          <h2 className="flex flex-wrap items-baseline gap-x-2 px-6 pt-6 pb-1 text-base font-medium">
            {t("research.sources")}
          </h2>
          <p className="px-6 pb-3 text-sm text-muted-foreground">{t("research.sourcesHint")}</p>
          {sources.length === 0 ? (
            live ? (
              <LoadingRows rows={3} />
            ) : (
              <EmptyState title={t("research.noSources")} />
            )
          ) : (
            <ol className="divide-y divide-nq-line border-y border-nq-line" data-sources>
              {sources.map((s) => (
                <SourceRow
                  key={s.position}
                  source={s}
                  text={textByPosition.get(s.position)}
                  textState={content.error ? "error" : content.data ? "ready" : "loading"}
                  textError={content.error}
                  canOpenText={!live}
                  onOpenText={() => setWantText(true)}
                />
              ))}
            </ol>
          )}
        </>
      )}
      <div className="pb-8" />
    </>
  )
}

/** What the run came to: live progress, an answer, or an honest account of why there is none. */
function Outcome({
  run,
  sources,
  live,
  canSummarise,
  onChanged,
}: {
  run: ResearchRun
  sources: ResearchSource[]
  live: boolean
  canSummarise?: boolean
  onChanged: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [queueing, setQueueing] = useState(false)

  async function summariseViaAgent() {
    setQueueing(true)
    try {
      await api(`/api/w/research/${encodeURIComponent(run.id)}/summarise-via-agent`, {
        method: "POST",
        workspaceId: ws.id,
      })
      toast.success(t("agent.summariseQueued"))
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setQueueing(false)
    }
  }

  if (live) {
    const read = sources.filter((s) => s.fetched).length
    return (
      <Panel icon={<LoaderCircleIcon className="animate-spin text-nq-brand" />} data="progress">
        <p className="font-medium" aria-live="polite">
          {run.status === "queued"
            ? t("research.progressQueued")
            : sources.length === 0
              ? t("research.progressSearching")
              : t("research.progressReading", { checked: sources.length, read })}
        </p>
        <p className="text-muted-foreground">{t("research.progressHint")}</p>
      </Panel>
    )
  }

  if (run.status === "failed") {
    return (
      <Panel icon={<TriangleAlertIcon className="text-nq-danger" />} data="failed">
        <p className="font-medium">{t("research.failedTitle")}</p>
        {run.error ? <p className="text-nq-danger-text">{run.error}</p> : null}
      </Panel>
    )
  }

  if (run.status === "cancelled") {
    return (
      <Panel icon={<SquareIcon className="text-muted-foreground" />} data="cancelled">
        <p className="font-medium">{t("research.cancelledTitle")}</p>
        <p className="text-muted-foreground">
          {sources.length ? t("research.cancelledBody") : t("research.cancelledEmpty")}
        </p>
      </Panel>
    )
  }

  // ok, with nothing found: the search itself came back empty.
  if (sources.length === 0) {
    return (
      <Panel icon={<InfoIcon className="text-muted-foreground" />} data="nothing-found">
        <p className="font-medium">{t("research.nothingFound")}</p>
      </Panel>
    )
  }

  // ok, and a model wrote an answer.
  if (run.summary && !run.error) {
    return (
      <section className="px-6 pb-6" data-outcome="summary">
        <h2 className="pb-2 text-base font-medium">{t("research.answer")}</h2>
        <div
          className="max-w-3xl space-y-3 text-sm leading-relaxed text-nq-fg-body"
          dir="auto"
          data-summary
        >
          <Summary text={run.summary} sources={sources} />
        </div>
        <p className="mt-3 text-xs text-muted-foreground">
          {run.model?.startsWith("agent:")
            ? t("agent.summaryBy", { model: run.model.slice("agent:".length) })
            : run.model
              ? t("research.answerBy", { model: run.model })
              : t("research.answerFrom")}
        </p>
      </section>
    )
  }

  // ok, no summary: the pages were collected and either there is no model, or
  // the model could not be used. Both are stated; neither is dressed up.
  // The summary was handed to the workspace's AI assistant and is waiting.
  if (run.stages?.some((st) => st.stage === "summarise" && st.status === "pending")) {
    return (
      <>
        <Panel icon={<BotIcon className="text-nq-brand" />} data="agent-summary">
          <p className="font-medium">{t("agent.summaryWaitingTitle")}</p>
          <p className="text-muted-foreground">{t("agent.summaryWaitingBody")}</p>
        </Panel>
        <AgentPanel compact />
      </>
    )
  }

  const noModel = canSummarise === false
  return (
    <Panel
      icon={<InfoIcon className="text-nq-warning" />}
      data={noModel ? "no-model" : "not-summarised"}
    >
      <p className="font-medium">{t("research.notSummarisedTitle")}</p>
      <p className="text-muted-foreground">
        {noModel ? t("research.noModelBody") : t("research.notSummarisedBody")}
      </p>
      {!noModel && run.error ? <p className="text-muted-foreground">{run.error}</p> : null}
      {ws.can("research:run") ? (
        <div className="pt-2">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => void summariseViaAgent()}
            disabled={queueing}
            data-action="summarise-via-agent"
          >
            {queueing ? <LoaderCircleIcon className="animate-spin" /> : <BotIcon />}
            {t("agent.summarise")}
          </Button>
        </div>
      ) : null}
    </Panel>
  )
}

function Panel({ icon, children, data }: { icon: ReactNode; children: ReactNode; data: string }) {
  return (
    <section className="px-6 pb-6" data-outcome={data}>
      <div className="flex max-w-3xl items-start gap-3 border border-nq-line bg-card px-4 py-3 text-sm [&>svg]:mt-0.5 [&>svg]:size-4 [&>svg]:shrink-0">
        {icon}
        <div className="min-w-0 flex-1 space-y-1">{children}</div>
      </div>
    </section>
  )
}

/**
 * The model's answer, with its [n] citations turned into links to source n
 * below. A number that matches no source stays plain text: a link to nothing
 * would suggest evidence that is not there.
 */
function Summary({ text, sources }: { text: string; sources: ResearchSource[] }) {
  const byNumber = new Map(sources.map((s) => [s.position + 1, s] as const))
  const paragraphs = text.split(/\n{2,}/).filter((p) => p.trim())
  return (
    <>
      {paragraphs.map((p, i) => (
        <p key={i} className="whitespace-pre-line">
          {renderInline(p, byNumber)}
        </p>
      ))}
    </>
  )
}

function renderInline(text: string, byNumber: Map<number, ResearchSource>): ReactNode[] {
  const out: ReactNode[] = []
  // [1], [1, 3], [2-4] citations and **bold** — the two things these answers use.
  const re = /\[(\d+(?:\s*[,–-]\s*\d+)*)\]|\*\*([^*]+)\*\*/g
  let last = 0
  let m: RegExpExecArray | null
  let k = 0
  while ((m = re.exec(text))) {
    if (m.index > last) out.push(text.slice(last, m.index))
    if (m[2] !== undefined) {
      out.push(<strong key={k++}>{m[2]}</strong>)
    } else {
      const parts = m[1].split(/\s*([,–-])\s*/)
      out.push(
        <span key={k++} className="whitespace-nowrap" dir="ltr">
          [
          {parts.map((part, j) => {
            if (part === "," || part === "-" || part === "–")
              return <Fragment key={j}>{part === "," ? ", " : "–"}</Fragment>
            const n = Number(part)
            const src = byNumber.get(n)
            return src ? (
              <a
                key={j}
                href={`#source-${n}`}
                title={src.title || src.url}
                className="font-medium text-nq-brand underline-offset-4 hover:underline"
                data-citation={n}
              >
                {n}
              </a>
            ) : (
              <Fragment key={j}>{part}</Fragment>
            )
          })}
          ]
        </span>,
      )
    }
    last = re.lastIndex
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}

function SourceRow({
  source,
  text,
  textState,
  textError,
  canOpenText,
  onOpenText,
}: {
  source: ResearchSource
  text?: string
  textState: "loading" | "ready" | "error"
  textError: unknown
  canOpenText: boolean
  onOpenText: () => void
}) {
  const { t } = useTranslations()
  const n = source.position + 1
  let host = ""
  try {
    host = new URL(source.url).host
  } catch {
    host = source.url
  }

  return (
    <li
      id={`source-${n}`}
      className="scroll-mt-4 px-6 py-4 target:bg-nq-surface-soft"
      data-source={n}
      data-source-read={source.fetched ? "true" : "false"}
    >
      <div className="flex items-start gap-3">
        <span
          className="font-mono w-7 shrink-0 pt-0.5 text-xs text-muted-foreground tabular-nums"
          dir="ltr"
          aria-label={t("research.rank", { n })}
        >
          #{n}
        </span>
        <div className="min-w-0 flex-1 space-y-1">
          <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
            <a
              className="min-w-0 break-words hover:underline"
              href={source.url}
              target="_blank"
              rel="noopener noreferrer"
            >
              <bdi>{source.title || host}</bdi>
              <ExternalLinkIcon
                className="ms-1 inline size-3.5 align-[-2px] text-muted-foreground"
                aria-hidden
              />
            </a>
            <Badge variant={source.fetched ? "neutral" : "outline"}>
              {source.fetched ? t("research.read") : t("research.unread")}
            </Badge>
          </p>
          {/* Aligned with the page, isolated inside: a dir="ltr" block would pin
              the link to the far left of an Arabic row, away from its title. */}
          <p className="truncate font-mono text-xs text-muted-foreground" title={source.url}>
            <bdi dir="ltr">{source.url}</bdi>
          </p>
          {source.snippet ? (
            <p className="text-sm text-muted-foreground break-words">
              <bdi>{source.snippet}</bdi>
            </p>
          ) : null}
          {!source.fetched ? (
            <p className="flex items-start gap-1.5 text-xs text-nq-warning-text" data-source-error>
              <TriangleAlertIcon className="mt-px size-3.5 shrink-0" aria-hidden />
              <span>
                {source.error
                  ? t("research.couldNotRead", { reason: source.error })
                  : t("research.notOpened")}
              </span>
            </p>
          ) : canOpenText ? (
            <Collapsible
              onOpenChange={(open) => {
                if (open) onOpenText()
              }}
            >
              <CollapsibleTrigger
                render={
                  <Button
                    variant="ghost"
                    size="sm"
                    className="-ms-2 mt-1 text-muted-foreground"
                    data-action="page-text"
                  />
                }
                className="group/trigger"
              >
                <ChevronDownIcon className="transition-transform group-data-[panel-open]/trigger:rotate-180" />
                <span className="group-data-[panel-open]/trigger:hidden">
                  {t("research.showText")}
                </span>
                <span className="hidden group-data-[panel-open]/trigger:inline">
                  {t("research.hideText")}
                </span>
              </CollapsibleTrigger>
              <CollapsiblePanel className="mt-2" data-page-text>
                {textState === "error" ? (
                  <p className="text-xs text-nq-danger-text">
                    {textError instanceof ApiError ? textError.message : t("common.networkError")}
                  </p>
                ) : textState === "loading" || text === undefined ? (
                  <div className="space-y-2" aria-busy>
                    <Skeleton className="h-3 w-full" />
                    <Skeleton className="h-3 w-11/12" />
                    <Skeleton className="h-3 w-3/4" />
                  </div>
                ) : text ? (
                  <pre
                    className="max-h-96 overflow-auto border border-nq-line bg-card px-3 py-2 font-sans text-xs leading-relaxed whitespace-pre-wrap break-words"
                    dir="auto"
                  >
                    {text}
                  </pre>
                ) : (
                  <p className="text-xs text-muted-foreground">{t("research.textEmpty")}</p>
                )}
              </CollapsiblePanel>
            </Collapsible>
          ) : null}
        </div>
      </div>
    </li>
  )
}

/** What the run kept: its evidence, and the companies that evidence names. */
function RunEvidence({
  evidence,
  companies,
}: {
  evidence: Evidence[]
  companies: CompanySuggestion[]
}) {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  return (
    <>
      <div className="hatch h-8 border-y border-border mt-2" aria-hidden />
      <h2
        className="flex flex-wrap items-baseline gap-x-2 px-6 pt-6 pb-1 text-base font-medium"
        data-run-evidence={evidence.length}
      >
        {t("research.evidence")}
        <span className="text-sm font-normal text-muted-foreground tabular-nums">
          {formatNumber(evidence.length)}
        </span>
      </h2>
      <p className="px-6 pb-3 text-sm text-muted-foreground">{t("research.evidenceHint")}</p>
      {evidence.length === 0 ? (
        <p className="border-y border-nq-line px-6 py-6 text-sm text-muted-foreground">
          {t("research.noEvidence")}
        </p>
      ) : (
        <ul className="divide-y divide-nq-line border-y border-nq-line">
          {evidence.map((e) => (
            <EvidenceItem key={e.id} e={e} />
          ))}
        </ul>
      )}

      {companies.length ? (
        <section className="px-6 pt-6" data-run-companies={companies.length}>
          <h2 className="text-base font-medium">{t("research.companiesFound")}</h2>
          <p className="pb-3 text-sm text-muted-foreground">{t("research.companiesFoundHint")}</p>
          <ul className="flex flex-wrap gap-2">
            {companies.map((c) => (
              <li
                key={c.id}
                className="flex flex-wrap items-center gap-2 border border-nq-line bg-card px-3 py-2 text-sm"
                data-run-company={c.name}
                data-company-status={c.status}
              >
                {c.crm_company_id ? (
                  <Link
                    href={`/${locale}/w/${ws.slug}/companies/${encodeURIComponent(c.crm_company_id)}`}
                    className="font-medium hover:underline"
                  >
                    <Bdi>{c.name}</Bdi>
                  </Link>
                ) : (
                  <span className="font-medium">
                    <Bdi>{c.name}</Bdi>
                  </span>
                )}
                {c.signal_types.slice(0, 2).map((s) => (
                  <SignalBadge key={s} signal={s} />
                ))}
                <span className="text-xs text-muted-foreground">
                  {t(`research.suggestionStatus.${c.status}`)}
                </span>
              </li>
            ))}
          </ul>
          {companies.some((c) => c.status === "suggested") ? (
            <Button
              variant="secondary"
              size="sm"
              className="mt-3"
              nativeButton={false}
              render={<Link href={`/${locale}/w/${ws.slug}/companies#suggestions`} />}
            >
              {t("research.reviewOnCompanies")}
              <ArrowRightIcon className="rtl:rotate-180" />
            </Button>
          ) : null}
        </section>
      ) : null}
    </>
  )
}
