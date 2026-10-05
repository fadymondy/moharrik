"use client"

import { Suspense, useEffect, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { SettingsIcon } from "lucide-react"
import { Button, Tabs, TabsList, TabsTab, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Input, toast } from "@fadymondy/nasaq/web"
import { FieldError } from "@/components/form-layout"

import { DealForm, DealsTable, PipelineBoard } from "@/components/crm/deals"
import { SignalsPanel, signalTotal } from "@/components/opportunities/signals"
import { PickSelect, stageTone } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Deal, type Stage } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useDeals, useDealsList, useOpportunitySignals } from "@/lib/queries"
import { PageNav, PageSummary, useCursorStack } from "@/components/cursor-pager"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

type View = "board" | "list" | "signals"
const VIEWS: View[] = ["board", "list", "signals"]

const LIST_PAGE = 50

export default function DealsPage() {
  return (
    <Suspense fallback={<LoadingRows rows={4} />}>
      <Opportunities />
    </Suspense>
  )
}

function Opportunities() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.opportunities")} · ${ws.summary.name}`)
  const canWrite = ws.can("contacts:write")
  const canManagePipeline = ws.can("pipeline:manage")
  const deals = useDeals(ws.id)
  // Board stays the default; ?view=list|signals deep-links the other two.
  const router = useRouter()
  const pathname = usePathname()
  const search = useSearchParams()
  const asked = search.get("view")
  const view: View = VIEWS.includes(asked as View) ? (asked as View) : "board"
  const setView = (next: View) => router.replace(next === "board" ? pathname : `${pathname}?view=${next}`, { scroll: false })
  const signals = useOpportunitySignals(ws.id)
  const signalCount = signalTotal(signals.data)
  // Board: cards past each stage's first page, fetched per stage on demand (MH-467: the board
  // used to stop at 2,000 deals without saying so). The fresh first page wins over these by id.
  const [extra, setExtra] = useState<Record<string, { deals: Deal[]; cursor: string }>>({})
  const [loadingStage, setLoadingStage] = useState<string | null>(null)
  const firstIds = new Set((deals.data?.deals ?? []).map((d) => d.id))
  const boardDeals = [
    ...(deals.data?.deals ?? []),
    ...Object.values(extra).flatMap((e) => e.deals.filter((d) => !firstIds.has(d.id))),
  ]
  const cursorOf = (stageId: string) =>
    extra[stageId]?.cursor ?? deals.data?.stages.find((s) => s.id === stageId)?.next_cursor ?? ""
  async function loadMore(stageId: string) {
    const cursor = cursorOf(stageId)
    if (!cursor) return
    setLoadingStage(stageId)
    try {
      const page = await api<{ deals: Deal[]; next_cursor: string }>(
        `/api/w/deals?stage_id=${encodeURIComponent(stageId)}&cursor=${encodeURIComponent(cursor)}`,
        { workspaceId: ws.id },
      )
      setExtra((x) => ({
        ...x,
        [stageId]: {
          deals: [...(x[stageId]?.deals ?? []), ...page.deals],
          cursor: page.next_cursor,
        },
      }))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setLoadingStage(null)
    }
  }
  // List view: the whole pipeline a page at a time.
  const pager = useCursorStack(view)
  const list = useDealsList(ws.id, { cursor: pager.cursor, limit: LIST_PAGE }, view === "list")
  const [creating, setCreating] = useState(false)
  const [managing, setManaging] = useState(false)

  async function move(dealId: string, stageId: string, position?: number) {
    if (!deals.data) return
    // Optimistic: the board should not snap back while the request is in flight.
    const prev = deals.data
    const stage = prev.stages.find((s) => s.id === stageId)
    const moved = (d: Deal) =>
      d.id === dealId
        ? {
            ...d,
            stage_id: stageId,
            stage_name: stage?.name ?? d.stage_name,
            stage_kind: stage?.kind ?? d.stage_kind,
            position: position ?? d.position,
          }
        : d
    setExtra((x) =>
      Object.fromEntries(
        Object.entries(x).map(([k, v]) => [k, { ...v, deals: v.deals.map(moved) }]),
      ),
    )
    deals.mutate(
      {
        ...prev,
        deals: prev.deals.map((d) =>
          d.id === dealId
            ? {
                ...d,
                stage_id: stageId,
                stage_name: stage?.name ?? d.stage_name,
                stage_kind: stage?.kind ?? d.stage_kind,
                position: position ?? d.position,
              }
            : d,
        ),
      },
      { revalidate: false },
    )
    try {
      await api(`/api/w/deals/${encodeURIComponent(dealId)}`, {
        method: "PATCH",
        json: { stage_id: stageId, position },
        workspaceId: ws.id,
      })
      deals.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
      deals.mutate(prev)
    }
  }

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.opportunities")}
        description={t("opps.hint")}
        action={
          <div className="flex flex-wrap items-center gap-2">
            {canManagePipeline ? (
              <Button type="button" variant="secondary" size="sm" onClick={() => setManaging(true)}>
                <SettingsIcon />
                {t("pipeline.manage")}
              </Button>
            ) : null}
            {canWrite ? (
              <Button onClick={() => setCreating(true)}>{t("deals.newDeal")}</Button>
            ) : null}
          </div>
        }
      />

      <Tabs value={view} onValueChange={(v) => setView(String(v) as View)} className="gap-0">
        <div className="px-4 pb-3 sm:px-6">
          <TabsList variant="underline" aria-label={t("deals.viewLabel")}>
            {VIEWS.map((v) => (
              <TabsTab key={v} value={v} data-tab={v}>
                {t(`opps.view.${v}`)}
                {v === "signals" && signalCount ? (
                  <span className="ms-1 text-muted-foreground tabular-nums" data-signal-total>{signalCount}</span>
                ) : null}
              </TabsTab>
            ))}
          </TabsList>
        </div>
      </Tabs>

      {view === "signals" ? (
        <SignalsPanel onChanged={() => deals.mutate()} />
      ) : deals.error ? (
        <ErrorState error={deals.error} />
      ) : !deals.data ? (
        <LoadingRows rows={4} />
      ) : deals.data.total === 0 ? (
        <div className="border-y border-nq-line px-6 py-12 text-center">
          <p className="text-sm font-medium">{t("deals.empty")}</p>
          {canWrite ? <p className="mt-1 text-sm text-muted-foreground">{t("deals.emptyBody")}</p> : null}
        </div>
      ) : view === "board" ? (
        <PipelineBoard
          stages={deals.data.stages}
          deals={boardDeals}
          currency={deals.data.currency}
          onMove={move}
          onChanged={() => {
            deals.mutate()
            setExtra({})
          }}
          cursorOf={cursorOf}
          onLoadMore={loadMore}
          loadingStage={loadingStage}
        />
      ) : !list.data ? (
        <LoadingRows rows={4} />
      ) : (
        <>
          <PageSummary
            index={pager.index}
            pageSize={LIST_PAGE}
            shown={list.data.deals.length}
            total={list.data.total}
            name="deals"
          />
          <div
            className={list.current ? undefined : "opacity-60 transition-opacity"}
            aria-busy={!list.current}
          >
            <DealsTable deals={list.data.deals} />
          </div>
          <PageNav
            index={pager.index}
            pageSize={LIST_PAGE}
            total={list.data.total}
            nextCursor={list.data.next_cursor}
            busy={!list.current}
            onNext={pager.next}
            onPrev={pager.prev}
            name="deals"
          />
        </>
      )}
      <div className="pb-8" />

      {deals.data ? (
        <NewDealDialog
          open={creating}
          onOpenChange={setCreating}
          stages={deals.data.stages}
          currency={deals.data.currency}
          onCreated={() => deals.mutate()}
        />
      ) : null}
      {deals.data && canManagePipeline ? (
        <ManagePipelineDialog
          open={managing}
          onOpenChange={setManaging}
          stages={deals.data.stages}
          onSaved={() => deals.mutate()}
        />
      ) : null}
    </>
  )
}

function NewDealDialog({
  open,
  onOpenChange,
  stages,
  currency,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  stages: Stage[]
  currency: string
  onCreated: () => void
}) {
  const { t } = useTranslations()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("deals.newDeal")}</DialogTitle>
          <DialogDescription>{t("deals.newDealHint")}</DialogDescription>
        </DialogHeader>
        <DealForm
          formId="new-deal"
          stages={stages}
          currency={currency}
          onSaved={() => {
            onCreated()
            onOpenChange(false)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}

function ManagePipelineDialog({
  open,
  onOpenChange,
  stages,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  stages: Stage[]
  onSaved: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [rows, setRows] = useState<{ id?: string; name: string; kind: string }[]>([])
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  // onOpenChange only fires from Base UI's own interactions (Escape, overlay click) — not when
  // this dialog is opened externally by the "Edit pipeline" button — so the rows are seeded here,
  // on `open` itself, rather than in a handler the opener never calls.
  useEffect(() => {
    if (open) {
      setRows(stages.map((s) => ({ id: s.id, name: s.name, kind: s.kind })))
      setErr(null)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  function handleOpenChange(next: boolean) {
    onOpenChange(next)
  }

  function update(i: number, patch: Partial<{ name: string; kind: string }>) {
    setRows((r) => r.map((row, j) => (j === i ? { ...row, ...patch } : row)))
  }

  function remove(i: number) {
    setRows((r) => r.filter((_, j) => j !== i))
  }

  function add() {
    setRows((r) => [...r, { name: "", kind: "open" }])
  }

  function move(i: number, dir: -1 | 1) {
    setRows((r) => {
      const j = i + dir
      if (j < 0 || j >= r.length) return r
      const next = [...r]
      ;[next[i], next[j]] = [next[j], next[i]]
      return next
    })
  }

  async function submit() {
    const bad = rows.some((r) => !r.name.trim())
    if (bad) {
      setErr(t("pipeline.nameRequired"))
      return
    }
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/pipeline/stages", {
        method: "PUT",
        json: { stages: rows.map((r) => ({ id: r.id, name: r.name.trim(), kind: r.kind })) },
        workspaceId: ws.id,
      })
      toast.success(t("pipeline.saved"))
      onSaved()
      handleOpenChange(false)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const kindOpts = [
    { value: "open", label: t("pipeline.kindOpen") },
    { value: "won", label: t("pipeline.kindWon") },
    { value: "lost", label: t("pipeline.kindLost") },
  ]

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("pipeline.manage")}</DialogTitle>
          <DialogDescription>{t("pipeline.manageHint")}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3" data-pipeline-editor>
          {rows.map((r, i) => (
            <div key={i} className="flex items-center gap-2" data-stage-row={r.name || "new"}>
              <span
                className={`size-2 shrink-0 rounded-full ${stageTone[r.kind as Stage["kind"]]}`}
                aria-hidden
              />
              <Input
                aria-label={t("pipeline.stageName")}
                value={r.name}
                onChange={(e) => update(i, { name: e.target.value })}
                maxLength={40}
                className="flex-1"
              />
              <div className="w-32 shrink-0">
                <PickSelect
                  id={`stage-kind-${i}`}
                  value={r.kind}
                  onChange={(v) => update(i, { kind: v })}
                  options={kindOpts}
                />
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={t("pipeline.moveUp")}
                disabled={i === 0}
                onClick={() => move(i, -1)}
              >
                ↑
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                aria-label={t("pipeline.moveDown")}
                disabled={i === rows.length - 1}
                onClick={() => move(i, 1)}
              >
                ↓
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-sm"
                className="text-nq-danger-text"
                aria-label={t("common.delete")}
                disabled={rows.length <= 1}
                onClick={() => remove(i)}
              >
                ×
              </Button>
            </div>
          ))}
          <Button type="button" variant="secondary" size="sm" onClick={add}>
            {t("pipeline.addStage")}
          </Button>
        </div>
        {err ? <FieldError>{err}</FieldError> : null}
        <DialogFooter>
          <Button onClick={submit} disabled={busy}>
            {busy ? t("common.working") : t("common.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
