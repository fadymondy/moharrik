"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useState, type FormEvent } from "react"
import { ArrowRightIcon, LoaderCircleIcon, SearchIcon } from "lucide-react"
import { toast } from "sonner"

import { SectionHeader } from "@/components/public-frame"
import { CapabilityNotes, isLive, ResearchStatusBadge, useFormatDateTime } from "@/components/research/shared"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Textarea } from "@/components/ui/textarea"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type ResearchRun } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useResearchRuns } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

const MIN = 3
const MAX = 500

export default function ResearchPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.research")} · ${ws.summary.name}`)
  const canRun = ws.can("research:run")

  const list = useResearchRuns(ws.id)
  const runs = list.data?.runs ?? []
  // One live run per workspace; the list is newest first, so it is near the top.
  const live = runs.find((r) => isLive(r.status))

  return (
    <>
      <SectionHeader micro={<Bdi>{ws.summary.name}</Bdi>} title={t("nav.research")} description={t("research.hint")} />

      {list.data ? <CapabilityNotes caps={list.data} /> : null}

      {canRun && list.data?.can_search ? (
        live ? <LiveNotice run={live} /> : <AskBox onStarted={() => list.mutate()} />
      ) : null}

      <div className="grid-separator-contained hatch-band mt-2" aria-hidden />

      <h2 className="px-6 pt-6 pb-3 text-base font-medium">{t("research.runs")}</h2>
      {list.error ? (
        <ErrorState error={list.error} />
      ) : !list.data ? (
        <LoadingRows rows={4} />
      ) : runs.length === 0 ? (
        <EmptyState title={t("research.empty")} body={canRun && list.data.can_search ? t("research.emptyBody") : undefined} />
      ) : (
        <ul className="divide-y divide-line border-y border-line" data-research-runs>
          {runs.map((r) => (
            <RunRow key={r.id} run={r} />
          ))}
        </ul>
      )}
      <div className="pb-8" />
    </>
  )
}

function AskBox({ onStarted }: { onStarted: () => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const [topic, setTopic] = useState("")
  const [busy, setBusy] = useState(false)
  const trimmed = topic.trim()
  const valid = trimmed.length >= MIN && trimmed.length <= MAX

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (!valid || busy) return
    setBusy(true)
    try {
      const res = await api<{ run_id: string }>("/api/w/research", { method: "POST", json: { topic: trimmed }, workspaceId: ws.id })
      toast.success(t("research.started"))
      onStarted()
      router.push(`/${locale}/w/${ws.slug}/research/${encodeURIComponent(res.run_id)}`)
    } catch (err) {
      // 409: somebody else started one between this page loading and the
      // click. Refreshing the list swaps this box for the "already going" note.
      if (err instanceof ApiError && err.status === 409) {
        toast.error(t("research.busy"))
        onStarted()
      } else toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
      setBusy(false)
    }
  }

  return (
    <section className="px-6 pb-6">
      <form onSubmit={onSubmit} noValidate className="max-w-3xl" data-form="ask-research">
        <Field>
          <FieldLabel htmlFor="research-topic">{t("research.ask")}</FieldLabel>
          <Textarea
            id="research-topic"
            dir="auto"
            rows={3}
            maxLength={MAX}
            value={topic}
            placeholder={t("research.askPlaceholder")}
            onChange={(e) => setTopic(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) e.currentTarget.form?.requestSubmit()
            }}
          />
          <FieldDescription className="flex flex-wrap justify-between gap-2">
            <span>{t("research.askHint")}</span>
            <span dir="ltr" className="tabular-nums">
              {trimmed.length}/{MAX}
            </span>
          </FieldDescription>
        </Field>
        <Button type="submit" className="mt-4" disabled={!valid || busy}>
          {busy ? <LoaderCircleIcon className="animate-spin" /> : <SearchIcon />}
          {t("research.start")}
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
        <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/research/${encodeURIComponent(run.id)}`} />}>
          {t("research.watch")}
          <ArrowRightIcon className="rtl:rotate-180" />
        </Button>
      </div>
    </section>
  )
}

function RunRow({ run }: { run: ResearchRun }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const fmt = useFormatDateTime()
  const href = `/${locale}/w/${ws.slug}/research/${encodeURIComponent(run.id)}`
  const live = isLive(run.status)
  return (
    <li className="flex flex-wrap items-center gap-3 px-6 py-4" data-research-run={run.id} data-run-status={run.status}>
      <div className="min-w-0 flex-1 space-y-1">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          <Link href={href} className="min-w-0 break-words hover:underline">
            <bdi>{run.topic}</bdi>
          </Link>
          <ResearchStatusBadge status={run.status} />
        </p>
        <p className="flex flex-wrap gap-x-2 text-xs text-grid-muted">
          {live ? (
            <span>{t("research.inProgress")}</span>
          ) : run.status === "cancelled" ? null : (
            // A stopped run never writes its counts, so none are shown rather than a false 0.
            <span data-counts>{t("research.counts", { found: run.found_count, read: run.read_count })}</span>
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
