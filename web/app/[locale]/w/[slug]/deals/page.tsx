"use client"

import { useEffect, useState } from "react"
import { LayoutGridIcon, ListIcon, SettingsIcon } from "lucide-react"
import { toast } from "sonner"

import { DealForm, DealsTable, PipelineBoard } from "@/components/crm/deals"
import { PickSelect, stageTone } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { ErrorState, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { FieldError } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Deal, type Stage } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useDeals } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

type View = "board" | "list"

export default function DealsPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.deals")} · ${ws.summary.name}`)
  const canWrite = ws.can("contacts:write")
  const canManagePipeline = ws.can("pipeline:manage")
  const deals = useDeals(ws.id)
  const [view, setView] = useState<View>("board")
  const [creating, setCreating] = useState(false)
  const [managing, setManaging] = useState(false)

  async function move(dealId: string, stageId: string, position?: number) {
    if (!deals.data) return
    // Optimistic: the board should not snap back while the request is in flight.
    const prev = deals.data
    const stage = prev.stages.find((s) => s.id === stageId)
    deals.mutate(
      { ...prev, deals: prev.deals.map((d) => (d.id === dealId ? { ...d, stage_id: stageId, stage_name: stage?.name ?? d.stage_name, stage_kind: stage?.kind ?? d.stage_kind, position: position ?? d.position } : d)) },
      { revalidate: false },
    )
    try {
      await api(`/api/w/deals/${encodeURIComponent(dealId)}`, { method: "PATCH", json: { stage_id: stageId, position }, workspaceId: ws.id })
      deals.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
      deals.mutate(prev)
    }
  }

  return (
    <>
      <SectionHeader
        micro={ws.summary.name}
        title={t("nav.deals")}
        description={t("deals.hint")}
        action={
          <div className="flex flex-wrap items-center gap-2">
            <div role="radiogroup" aria-label={t("deals.viewLabel")} className="flex gap-1">
              <Button type="button" size="icon-sm" variant={view === "board" ? "default" : "outline"} role="radio" aria-checked={view === "board"} aria-label={t("deals.viewBoard")} onClick={() => setView("board")}>
                <LayoutGridIcon />
              </Button>
              <Button type="button" size="icon-sm" variant={view === "list" ? "default" : "outline"} role="radio" aria-checked={view === "list"} aria-label={t("deals.viewList")} onClick={() => setView("list")}>
                <ListIcon />
              </Button>
            </div>
            {canManagePipeline ? (
              <Button type="button" variant="outline" size="sm" onClick={() => setManaging(true)}>
                <SettingsIcon />
                {t("pipeline.manage")}
              </Button>
            ) : null}
            {canWrite ? <Button onClick={() => setCreating(true)}>{t("deals.newDeal")}</Button> : null}
          </div>
        }
      />

      {deals.error ? (
        <ErrorState error={deals.error} />
      ) : !deals.data ? (
        <LoadingRows rows={4} />
      ) : deals.data.deals.length === 0 ? (
        <div className="border-y border-line px-6 py-12 text-center">
          <p className="text-sm font-medium">{t("deals.empty")}</p>
          {canWrite ? (
            <p className="mt-1 text-sm text-grid-muted">{t("deals.emptyBody")}</p>
          ) : null}
        </div>
      ) : view === "board" ? (
        <PipelineBoard stages={deals.data.stages} deals={deals.data.deals} currency={deals.data.currency} onMove={move} />
      ) : (
        <DealsTable deals={deals.data.deals} />
      )}
      <div className="pb-8" />

      {deals.data ? (
        <NewDealDialog open={creating} onOpenChange={setCreating} stages={deals.data.stages} currency={deals.data.currency} onCreated={() => deals.mutate()} />
      ) : null}
      {deals.data && canManagePipeline ? (
        <ManagePipelineDialog open={managing} onOpenChange={setManaging} stages={deals.data.stages} onSaved={() => deals.mutate()} />
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
              <span className={`size-2 shrink-0 rounded-full ${stageTone[r.kind as Stage["kind"]]}`} aria-hidden />
              <Input
                aria-label={t("pipeline.stageName")}
                value={r.name}
                onChange={(e) => update(i, { name: e.target.value })}
                maxLength={40}
                className="flex-1"
              />
              <div className="w-32 shrink-0">
                <PickSelect id={`stage-kind-${i}`} value={r.kind} onChange={(v) => update(i, { kind: v })} options={kindOpts} />
              </div>
              <Button type="button" variant="ghost" size="icon-xs" aria-label={t("pipeline.moveUp")} disabled={i === 0} onClick={() => move(i, -1)}>
                ↑
              </Button>
              <Button type="button" variant="ghost" size="icon-xs" aria-label={t("pipeline.moveDown")} disabled={i === rows.length - 1} onClick={() => move(i, 1)}>
                ↓
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                className="text-grid-danger-text"
                aria-label={t("common.delete")}
                disabled={rows.length <= 1}
                onClick={() => remove(i)}
              >
                ×
              </Button>
            </div>
          ))}
          <Button type="button" variant="outline" size="sm" onClick={add}>
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
