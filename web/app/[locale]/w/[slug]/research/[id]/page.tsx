"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { Fragment, useState, type ReactNode } from "react"
import { ArrowLeftIcon, ChevronDownIcon, ExternalLinkIcon, InfoIcon, LoaderCircleIcon, SquareIcon, TriangleAlertIcon } from "lucide-react"
import { toast } from "sonner"

import { SectionHeader } from "@/components/public-frame"
import { isLive, ResearchStatusBadge, useFormatDateTime } from "@/components/research/shared"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Skeleton } from "@/components/ui/skeleton"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type ResearchRun, type ResearchSource } from "@/lib/api"
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
  const textByPosition = new Map((content.data?.sources ?? []).map((s) => [s.position, s.content ?? ""] as const))

  async function cancel() {
    setCancelling(true)
    try {
      await api(`/api/w/research/${encodeURIComponent(id)}/cancel`, { method: "POST", workspaceId: ws.id })
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
        description={run ? t("research.startedAt", { date: fmt(run.started_at ?? run.created_at) }) : undefined}
        action={
          <div className="flex flex-wrap items-center gap-2">
            {canRun && live ? (
              <Button variant="outline" size="sm" onClick={cancel} disabled={cancelling} data-action="cancel-research">
                {cancelling ? <LoaderCircleIcon className="animate-spin" /> : <SquareIcon />}
                {t("research.cancel")}
              </Button>
            ) : null}
            <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/research`} />}>
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
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 px-6 pb-4 text-sm text-grid-muted" data-run-status={run.status}>
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

          <Outcome run={run} sources={sources} live={live} canSummarise={caps.data?.can_summarise} />

          <div className="grid-separator-contained hatch-band mt-2" aria-hidden />

          <h2 className="flex flex-wrap items-baseline gap-x-2 px-6 pt-6 pb-1 text-base font-medium">
            {t("research.sources")}
          </h2>
          <p className="px-6 pb-3 text-sm text-grid-muted">{t("research.sourcesHint")}</p>
          {sources.length === 0 ? (
            live ? (
              <LoadingRows rows={3} />
            ) : (
              <EmptyState title={t("research.noSources")} />
            )
          ) : (
            <ol className="divide-y divide-line border-y border-line" data-sources>
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
function Outcome({ run, sources, live, canSummarise }: { run: ResearchRun; sources: ResearchSource[]; live: boolean; canSummarise?: boolean }) {
  const { t } = useTranslations()

  if (live) {
    const read = sources.filter((s) => s.fetched).length
    return (
      <Panel icon={<LoaderCircleIcon className="animate-spin text-grid-brand" />} data="progress">
        <p className="font-medium" aria-live="polite">
          {run.status === "queued"
            ? t("research.progressQueued")
            : sources.length === 0
              ? t("research.progressSearching")
              : t("research.progressReading", { checked: sources.length, read })}
        </p>
        <p className="text-grid-muted">{t("research.progressHint")}</p>
      </Panel>
    )
  }

  if (run.status === "failed") {
    return (
      <Panel icon={<TriangleAlertIcon className="text-grid-danger" />} data="failed">
        <p className="font-medium">{t("research.failedTitle")}</p>
        {run.error ? <p className="text-grid-danger-text">{run.error}</p> : null}
      </Panel>
    )
  }

  if (run.status === "cancelled") {
    return (
      <Panel icon={<SquareIcon className="text-grid-muted" />} data="cancelled">
        <p className="font-medium">{t("research.cancelledTitle")}</p>
        <p className="text-grid-muted">{sources.length ? t("research.cancelledBody") : t("research.cancelledEmpty")}</p>
      </Panel>
    )
  }

  // ok, with nothing found: the search itself came back empty.
  if (sources.length === 0) {
    return (
      <Panel icon={<InfoIcon className="text-grid-muted" />} data="nothing-found">
        <p className="font-medium">{t("research.nothingFound")}</p>
      </Panel>
    )
  }

  // ok, and a model wrote an answer.
  if (run.summary && !run.error) {
    return (
      <section className="px-6 pb-6" data-outcome="summary">
        <h2 className="pb-2 text-base font-medium">{t("research.answer")}</h2>
        <div className="max-w-3xl space-y-3 text-sm leading-relaxed text-grid-body" dir="auto" data-summary>
          <Summary text={run.summary} sources={sources} />
        </div>
        <p className="mt-3 text-xs text-grid-muted">
          {run.model ? t("research.answerBy", { model: run.model }) : t("research.answerFrom")}
        </p>
      </section>
    )
  }

  // ok, no summary: the pages were collected and either there is no model, or
  // the model could not be used. Both are stated; neither is dressed up.
  const noModel = canSummarise === false
  return (
    <Panel icon={<InfoIcon className="text-grid-warn" />} data={noModel ? "no-model" : "not-summarised"}>
      <p className="font-medium">{t("research.notSummarisedTitle")}</p>
      <p className="text-grid-muted">{noModel ? t("research.noModelBody") : t("research.notSummarisedBody")}</p>
      {!noModel && run.error ? <p className="text-grid-muted">{run.error}</p> : null}
    </Panel>
  )
}

function Panel({ icon, children, data }: { icon: ReactNode; children: ReactNode; data: string }) {
  return (
    <section className="px-6 pb-6" data-outcome={data}>
      <div className="flex max-w-3xl items-start gap-3 border border-line bg-grid-card px-4 py-3 text-sm [&>svg]:mt-0.5 [&>svg]:size-4 [&>svg]:shrink-0">
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
            if (part === "," || part === "-" || part === "–") return <Fragment key={j}>{part === "," ? ", " : "–"}</Fragment>
            const n = Number(part)
            const src = byNumber.get(n)
            return src ? (
              <a
                key={j}
                href={`#source-${n}`}
                title={src.title || src.url}
                className="font-medium text-grid-brand underline-offset-4 hover:underline"
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
      className="scroll-mt-4 px-6 py-4 target:bg-grid-soft"
      data-source={n}
      data-source-read={source.fetched ? "true" : "false"}
    >
      <div className="flex items-start gap-3">
        <span className="grid-mono w-7 shrink-0 pt-0.5 text-xs text-grid-muted tabular-nums" dir="ltr" aria-label={t("research.rank", { n })}>
          #{n}
        </span>
        <div className="min-w-0 flex-1 space-y-1">
          <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
            <a className="min-w-0 break-words hover:underline" href={source.url} target="_blank" rel="noopener noreferrer">
              <bdi>{source.title || host}</bdi>
              <ExternalLinkIcon className="ms-1 inline size-3.5 align-[-2px] text-grid-muted" aria-hidden />
            </a>
            <Badge variant={source.fetched ? "secondary" : "outline"}>{source.fetched ? t("research.read") : t("research.unread")}</Badge>
          </p>
          {/* Aligned with the page, isolated inside: a dir="ltr" block would pin
              the link to the far left of an Arabic row, away from its title. */}
          <p className="truncate font-mono text-xs text-grid-muted" title={source.url}>
            <bdi dir="ltr">{source.url}</bdi>
          </p>
          {source.snippet ? (
            <p className="text-sm text-grid-muted break-words">
              <bdi>{source.snippet}</bdi>
            </p>
          ) : null}
          {!source.fetched ? (
            <p className="flex items-start gap-1.5 text-xs text-grid-warn-text" data-source-error>
              <TriangleAlertIcon className="mt-px size-3.5 shrink-0" aria-hidden />
              <span>{source.error ? t("research.couldNotRead", { reason: source.error }) : t("research.notOpened")}</span>
            </p>
          ) : canOpenText ? (
            <Collapsible
              onOpenChange={(open) => {
                if (open) onOpenText()
              }}
            >
              <CollapsibleTrigger
                render={<Button variant="ghost" size="sm" className="-ms-2 mt-1 text-grid-muted" data-action="page-text" />}
                className="group/trigger"
              >
                <ChevronDownIcon className="transition-transform group-data-[panel-open]/trigger:rotate-180" />
                <span className="group-data-[panel-open]/trigger:hidden">{t("research.showText")}</span>
                <span className="hidden group-data-[panel-open]/trigger:inline">{t("research.hideText")}</span>
              </CollapsibleTrigger>
              <CollapsibleContent className="mt-2" data-page-text>
                {textState === "error" ? (
                  <p className="text-xs text-grid-danger-text">
                    {textError instanceof ApiError ? textError.message : t("common.networkError")}
                  </p>
                ) : textState === "loading" || text === undefined ? (
                  <div className="space-y-2" aria-busy>
                    <Skeleton className="h-3 w-full" />
                    <Skeleton className="h-3 w-11/12" />
                    <Skeleton className="h-3 w-3/4" />
                  </div>
                ) : text ? (
                  <pre className="max-h-96 overflow-auto border border-line bg-grid-card px-3 py-2 font-sans text-xs leading-relaxed whitespace-pre-wrap break-words" dir="auto">
                    {text}
                  </pre>
                ) : (
                  <p className="text-xs text-grid-muted">{t("research.textEmpty")}</p>
                )}
              </CollapsibleContent>
            </Collapsible>
          ) : null}
        </div>
      </div>
    </li>
  )
}
