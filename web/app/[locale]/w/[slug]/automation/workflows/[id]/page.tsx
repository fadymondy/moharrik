"use client"

/*
The workflow canvas (MH-480): a workflow built and read the way n8n does it.

The trigger sits first; the steps follow as nodes joined by edges, a "+" on
every edge and after the last node; clicking a node opens its parameters in
the side panel; "Test workflow" runs what is on the canvas in a connected
browser and each node lights up as the run reaches it, with what it brought
back one click away; the Executions tab replays past runs node by node; and
version history previews and restores every save.

The steps JSON stays the one source of truth. The canvas edits that array and
nothing else; where the nodes sit is kept apart, as UI metadata
(/api/w/workflows/{id}/layout), and a save that only moved nodes is not a new
version. A workflow saved before the canvas existed opens as a graph — laid out
in a line — and runs exactly as it did.
*/

import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import {
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react"
import {
  ArrowLeftIcon,
  CircleAlertIcon,
  FlaskConicalIcon,
  HistoryIcon,
  LoaderCircleIcon,
  SaveIcon,
  SquareIcon,
} from "lucide-react"
import { toast } from "sonner"

import { validParamName } from "@/components/automation/params-editor"
import { ExecutionIcon, ExecutionsView } from "@/components/automation/canvas/executions-view"
import {
  autoLayout,
  canonicalCommand,
  insertAt,
  moveStep,
  newStep,
  nodeRuns,
  removeAt,
  resolveLayout,
  signatureOf,
  type Layout,
  type NodeKind,
} from "@/components/automation/canvas/model"
import { NodePicker } from "@/components/automation/canvas/node-picker"
import { StepPanel, TriggerPanel, type PanelTab } from "@/components/automation/canvas/node-panel"
import { VersionsPanel } from "@/components/automation/canvas/versions-panel"
import { WorkflowCanvas, type Selection } from "@/components/automation/canvas/workflow-canvas"
import { isolate } from "@/components/bidi"
import { PickSelect } from "@/components/crm/shared"
import { ErrorState, LoadingRows } from "@/components/states"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  type AutomationWorkflow,
  type WorkflowParam,
  type WorkflowStep,
  type WorkflowVersion,
} from "@/lib/api"
import { useWorkflowLayout, type WorkflowLayout } from "@/lib/canvas-queries"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { requiredSites } from "@/lib/required-sites"
import {
  useAutomationSettings,
  useBrowsers,
  useRun,
  useSchedules,
  useWorkflow,
  useWorkflowVersions,
} from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { cn } from "@/lib/utils"

/** Below md the canvas runs top to bottom and the panels cover it. */
function useNarrow() {
  return useSyncExternalStore(
    (cb) => {
      const m = window.matchMedia("(max-width: 767px)")
      m.addEventListener("change", cb)
      return () => m.removeEventListener("change", cb)
    },
    () => window.matchMedia("(max-width: 767px)").matches,
    () => false,
  )
}

export default function WorkflowCanvasPage() {
  return (
    <Suspense fallback={<LoadingRows rows={5} />}>
      <CanvasPage />
    </Suspense>
  )
}

function CanvasPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const params = useParams<{ id: string }>()
  const isNew = params.id === "new"
  const wf = useWorkflow(ws.id, isNew ? undefined : params.id)
  const versions = useWorkflowVersions(ws.id, isNew ? undefined : params.id)
  const layout = useWorkflowLayout(ws.id, isNew ? undefined : params.id)
  const workflow = wf.data?.workflow
  useDocumentTitle(`${workflow?.name || t("canvas.newTitle")} · ${ws.summary.name}`)

  if (!ws.can("automation:read"))
    return <ErrorState error={new ApiError(403, t("automation.editor.noAccess"))} />
  if (!isNew && wf.error) return <ErrorState error={wf.error} />
  // The layout is waited for too: drawing a line and then jumping to the saved
  // places a moment later reads as the graph rearranging itself.
  if (!isNew && (!workflow || (!layout.data && !layout.error))) return <LoadingRows rows={5} />

  return (
    <Editor
      // Remounted on every new version: a save or a restore resets the draft to
      // exactly what is now stored.
      key={workflow ? `${workflow.id}-${workflow.version}` : "new"}
      workflow={workflow}
      versions={versions.data?.versions ?? []}
      saved={layout.data}
      onSaved={() => {
        void wf.mutate()
        void versions.mutate()
        void layout.mutate()
      }}
      onLayoutSaved={() => void layout.mutate()}
    />
  )
}

type Draft = { name: string; description: string; steps: WorkflowStep[]; params: WorkflowParam[] }
const snapshot = (d: Draft) =>
  JSON.stringify({
    name: d.name.trim(),
    description: d.description.trim(),
    steps: d.steps,
    params: d.params,
  })

function Editor({
  workflow,
  versions,
  saved,
  onSaved,
  onLayoutSaved,
}: {
  workflow?: AutomationWorkflow
  versions: WorkflowVersion[]
  saved?: WorkflowLayout
  onSaved: () => void
  onLayoutSaved: () => void
}) {
  const { t, locale, isRtl } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const search = useSearchParams()
  const canEdit = ws.can("automation:run")
  const narrow = useNarrow()
  const base = `/${locale}/w/${ws.slug}/automation`

  // ---- the draft: the steps, and nothing but ----
  const initial = useMemo<Draft>(
    () => ({
      name: workflow?.name ?? "",
      description: workflow?.description ?? "",
      steps: workflow?.steps ?? [],
      params: workflow?.params ?? [],
    }),
    [workflow],
  )
  const [draft, setDraft] = useState<Draft>(initial)
  const dirty = snapshot(draft) !== snapshot(initial) || !workflow
  const steps = draft.steps

  // ---- where the nodes sit: kept apart from the steps ----
  const initialLayout = useMemo(
    () => resolveLayout(initial.steps, saved, false),
    [initial.steps, saved],
  )
  const [layout, setLayout] = useState<Layout>(initialLayout)
  const layoutDirty = JSON.stringify(layout) !== JSON.stringify(initialLayout)
  const shown = narrow ? autoLayout(steps.length, true) : layout

  // ---- selection and panels ----
  const [selected, setSelected] = useState<Selection>(null)
  const [panel, setPanel] = useState<"node" | "picker" | "versions" | null>(null)
  const [insertIndex, setInsertIndex] = useState(0)
  const [tab, setTab] = useState<PanelTab>("parameters")
  const [preview, setPreview] = useState<WorkflowVersion | null>(null)
  const view = search.get("view") === "executions" && workflow ? "executions" : "editor"
  const runParam = search.get("run")
  const go = (next: { view?: string; run?: string | null }) => {
    const q = new URLSearchParams(search.toString())
    if (next.view !== undefined)
      next.view === "editor" ? q.delete("view") : q.set("view", next.view)
    if (next.run !== undefined) next.run ? q.set("run", next.run) : q.delete("run")
    const s = q.toString()
    router.replace(`${base}/workflows/${workflow?.id ?? "new"}${s ? `?${s}` : ""}`, {
      scroll: false,
    })
  }

  const select = useCallback((s: Selection) => {
    setSelected(s)
    setPanel(s === null ? null : "node")
  }, [])

  // ---- browsers ----
  const browsers = useBrowsers(ws.id)
  const settings = useAutomationSettings(ws.id)
  const schedules = useSchedules(ws.id)
  const usable = (browsers.data?.browsers ?? []).filter((b) => !b.revoked_at)
  const [agentId, setAgentId] = useState("")
  const agent =
    usable.find((b) => b.id === agentId) ?? usable.find((b) => b.status === "online") ?? usable[0]
  const paused = settings.data?.paused ?? false
  const [values, setValues] = useState<Record<string, string>>({})
  // Why a run cannot start right now — said beside the button, never a dead one.
  const browserBlocked = !agent
    ? t("automation.editor.noBrowser")
    : paused
      ? t("automation.pausedTitle")
      : agent.status !== "online"
        ? t("automation.editor.browserOffline", { name: agent.name })
        : ""

  // ---- editing ----
  const setSteps = (next: WorkflowStep[]) => setDraft((d) => ({ ...d, steps: next }))
  function insert(index: number, kind: Exclude<NodeKind, "unknown">) {
    setSteps([...steps.slice(0, index), newStep(kind), ...steps.slice(index)])
    setLayout((l) => insertAt(l, index))
    setSelected(index)
    setTab("parameters")
    setPanel("node")
  }
  function remove(index: number) {
    setSteps(steps.filter((_, i) => i !== index))
    setLayout((l) => removeAt(l, index))
    setSelected(null)
    setPanel(null)
  }
  function duplicate(index: number) {
    setSteps([
      ...steps.slice(0, index + 1),
      structuredClone(steps[index]),
      ...steps.slice(index + 1),
    ])
    setLayout((l) => insertAt(l, index + 1))
    setSelected(index + 1)
  }
  function reorder(index: number, to: number) {
    if (to < 0 || to >= steps.length) return
    setSteps(moveStep(steps, index, to))
    if (selected === index) setSelected(to)
  }

  // ---- test runs ----
  const [test, setTest] = useState<{ runId: string; steps: string; upTo: number } | null>(null)
  const [starting, setStarting] = useState(false)
  const [runErr, setRunErr] = useState<string | null>(null)
  // A publishing workflow (MH-558) asks first: the run control opens this
  // instead of starting straight away, the same as the old /automation page
  // did before the canvas replaced it.
  // The steps a confirmed run goes up to; null while no confirmation is open.
  const [confirmRun, setConfirmRun] = useState<number | null>(null)
  // A publishing workflow asks first (MH-558), for a whole run and for "run to here" alike.
  const askThenStart = (upTo: number) => (workflow?.writes ? setConfirmRun(upTo) : void start(upTo))
  // Test runs of a draft not saved yet: they become its first executions once
  // it is (POST …/executions/adopt).
  const draftRuns = useRef<string[]>([])
  const run = useRun(ws.id, test?.runId)
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "run" && test) void run.mutate()
    if (e.kind === "browser") void browsers.mutate()
  })
  const runStatus = run.data?.run.status
  const live = runStatus === "queued" || runStatus === "running"
  // Results are drawn only while the canvas still holds the steps that ran: a
  // node moved or edited since would otherwise wear another step's result.
  const current = !!test && test.steps === JSON.stringify(steps.slice(0, test.upTo))
  const runs = useMemo(
    () => (run.data && current ? nodeRuns(run.data.steps, run.data.run.status) : undefined),
    [run.data, current],
  )

  async function start(upTo: number) {
    if (!agent || browserBlocked) return
    const run = steps.slice(0, upTo)
    const need = draft.params
      .filter((p) => p.required && !p.secret && !(values[p.name] ?? p.default ?? "").trim())
      .map((p) => p.name)
    const needSecret = draft.params
      .filter((p) => p.required && p.secret && !(values[p.name] ?? "").trim())
      .map((p) => p.name)
    if (need.length || needSecret.length) {
      setRunErr(t("canvas.needValues", { names: [...need, ...needSecret].join(", ") }))
      select("trigger")
      return
    }
    setStarting(true)
    setRunErr(null)
    try {
      const { run_id } = await api<{ run_id: string }>("/api/w/workflows/test", {
        json: {
          agent_id: agent.id,
          workflow_id: workflow?.id,
          steps: run,
          params: draft.params.filter((p) => p.name),
          values,
        },
        workspaceId: ws.id,
      })
      setTest({ runId: run_id, steps: JSON.stringify(run), upTo })
      if (!workflow) draftRuns.current.push(run_id)
    } catch (e) {
      setRunErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setStarting(false)
    }
  }
  async function stop() {
    if (!test) return
    try {
      await api(`/api/w/runs/${encodeURIComponent(test.runId)}/cancel`, {
        method: "POST",
        workspaceId: ws.id,
      })
      void run.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  // ---- save ----
  const [saving, setSaving] = useState(false)
  const [saveErr, setSaveErr] = useState<string | null>(null)
  const nameInput = useRef<HTMLInputElement>(null)
  const saveLayout = async (id: string, l: Layout, s: WorkflowStep[]) =>
    api(`/api/w/workflows/${encodeURIComponent(id)}/layout`, {
      method: "PUT",
      json: { trigger: l.trigger, nodes: l.nodes, signature: signatureOf(s) },
      workspaceId: ws.id,
    })

  const save = useCallback(async () => {
    setSaveErr(null)
    if (!draft.name.trim()) {
      setSaveErr(t("automation.editor.needName"))
      nameInput.current?.focus()
      return
    }
    if (steps.length === 0) return setSaveErr(t("automation.editor.needStep"))
    const badParam = draft.params.find((p) => !validParamName(p.name))
    if (badParam) {
      select("trigger")
      return setSaveErr(t("automation.params.badName", { name: badParam.name || "…" }))
    }
    setSaving(true)
    try {
      const body = {
        name: draft.name.trim(),
        description: draft.description.trim(),
        steps,
        params: draft.params,
      }
      if (!workflow) {
        const { workflow: created } = await api<{ workflow: AutomationWorkflow }>(
          "/api/w/workflows",
          { json: body, workspaceId: ws.id },
        )
        await saveLayout(created.id, layout, steps)
        if (draftRuns.current.length) {
          // Best-effort: the workflow is saved either way.
          await api(`/api/w/workflows/${encodeURIComponent(created.id)}/executions/adopt`, {
            json: { run_ids: draftRuns.current },
            workspaceId: ws.id,
          }).catch(() => {})
        }
        toast.success(t("automation.editor.created"))
        router.replace(`${base}/workflows/${created.id}`)
        return
      }
      if (snapshot(draft) === snapshot(initial)) {
        // Only nodes moved: nothing that runs changed, so no new version.
        await saveLayout(workflow.id, layout, steps)
        toast.success(t("canvas.layoutSaved"))
        onLayoutSaved()
        return
      }
      const { workflow: next } = await api<{ workflow: AutomationWorkflow }>(
        `/api/w/workflows/${encodeURIComponent(workflow.id)}`,
        {
          method: "PUT",
          json: { ...body, base_version: workflow.version },
          workspaceId: ws.id,
        },
      )
      await saveLayout(workflow.id, layout, steps)
      toast.success(t("automation.editor.savedAs", { n: next.version }))
      onSaved()
    } catch (e) {
      setSaveErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setSaving(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [draft, steps, layout, workflow, initial])

  // Ctrl/Cmd+S saves, as it does in n8n.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
        e.preventDefault()
        if (canEdit && (dirty || layoutDirty) && !saving) void save()
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [save, canEdit, dirty, layoutDirty, saving])

  // Leaving with unsaved edits asks first.
  useEffect(() => {
    if (!workflow || !(dirty || layoutDirty)) return
    const warn = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener("beforeunload", warn)
    return () => window.removeEventListener("beforeunload", warn)
  }, [dirty, layoutDirty, workflow])

  // ---- what the canvas shows ----
  const readOnly = !canEdit || !!preview
  const canvasSteps = preview ? preview.steps : steps
  const canvasLayout = preview
    ? narrow
      ? autoLayout(preview.steps.length, true)
      : resolveLayout(preview.steps, saved, false)
    : shown
  const mine = (schedules.data?.schedules ?? []).filter(
    (s) => workflow && s.workflow_id === workflow.id,
  )
  // The sites it needs, as the server works them out (MH-517), for the steps
  // as they are on the canvas now, saved or not.
  const requiredHere = requiredSites(workflow?.hosts, steps)
  const autoAdd = settings.data?.auto_add_sites ?? true
  const triggerSub = [
    t("canvas.start.manual"),
    mine.length ? t("canvas.nSchedules", { n: mine.length }) : "",
    "MCP",
  ]
    .filter(Boolean)
    .join(" · ")
  const pageUrl = (index: number) => {
    for (let i = index; i >= 0; i--) {
      const s = steps[i]
      if (s && canonicalCommand(s.command) === "navigate") {
        return String(s.args?.url ?? "").replace(
          /\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}/g,
          (m, name: string) => {
            const p = draft.params.find((x) => x.name === name)
            return p?.secret ? m : values[name] || p?.default || m
          },
        )
      }
    }
    return ""
  }

  const done = run.data && !live ? run.data.run : null
  const panelOpen =
    view === "editor" &&
    (panel === "picker" || panel === "versions" || (panel === "node" && selected !== null))

  return (
    <div
      className="flex h-[calc(100dvh-3.5rem)] min-h-[480px] flex-col"
      data-canvas-page
      data-version={workflow?.version ?? 0}
      data-dirty={dirty || layoutDirty ? "true" : "false"}
    >
      {/* ---- the top bar ---- */}
      <header className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line px-3 py-2 sm:px-4">
        <Button
          variant="ghost"
          size="icon-sm"
          nativeButton={false}
          render={<Link href={`${base}/workflows`} />}
          aria-label={t("canvas.backToList")}
          title={t("canvas.backToList")}
        >
          <ArrowLeftIcon className="rtl:rotate-180" />
        </Button>
        <div className="flex min-w-0 flex-1 items-center gap-2 md:flex-none">
          <Input
            ref={nameInput}
            value={draft.name}
            maxLength={80}
            readOnly={!canEdit}
            dir="auto"
            placeholder={t("canvas.namePlaceholder")}
            aria-label={t("automation.workflowName")}
            onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
            className="h-8 min-w-0 border-transparent bg-transparent px-2 text-base font-medium shadow-none hover:border-line focus-visible:border-ring md:w-64"
            data-workflow-name
          />
          {workflow ? (
            <Badge variant="outline">
              {t("automation.editor.versionBadge", { n: workflow.version })}
            </Badge>
          ) : null}
          {dirty || layoutDirty ? (
            <span className="text-xs whitespace-nowrap text-grid-muted" data-unsaved>
              {t("canvas.unsaved")}
            </span>
          ) : null}
        </div>

        <nav
          className="order-last flex w-full items-center justify-center gap-1 md:order-none md:w-auto md:flex-1"
          aria-label={t("canvas.views")}
        >
          <div className="inline-flex rounded-lg bg-muted p-[3px]">
            <button
              type="button"
              onClick={() => go({ view: "editor", run: null })}
              className={cn(
                "rounded-md px-3 py-1 text-sm font-medium transition-colors",
                view === "editor"
                  ? "bg-background text-foreground shadow-sm"
                  : "text-foreground/60 hover:text-foreground",
              )}
              aria-current={view === "editor" ? "page" : undefined}
              data-view-tab="editor"
            >
              {t("canvas.view.editor")}
            </button>
            {workflow ? (
              <button
                type="button"
                onClick={() => go({ view: "executions" })}
                className={cn(
                  "rounded-md px-3 py-1 text-sm font-medium transition-colors",
                  view === "executions"
                    ? "bg-background text-foreground shadow-sm"
                    : "text-foreground/60 hover:text-foreground",
                )}
                aria-current={view === "executions" ? "page" : undefined}
                data-view-tab="executions"
              >
                {t("canvas.view.executions")}
              </button>
            ) : null}
          </div>
        </nav>

        <div className="flex items-center gap-2">
          {usable.length > 0 ? (
            <div className="hidden w-60 lg:block" title={t("automation.editor.browser")}>
              <PickSelect
                id="canvas-agent"
                value={agent?.id ?? ""}
                onChange={setAgentId}
                options={usable.map((b) => ({
                  value: b.id,
                  label: `${b.name} (${b.status === "online" ? t("automation.online") : t("automation.offline")})`,
                }))}
              />
            </div>
          ) : null}
          {workflow ? (
            <Button
              variant={panel === "versions" ? "secondary" : "ghost"}
              size="sm"
              onClick={() => {
                setPanel(panel === "versions" ? null : "versions")
                if (view !== "editor") go({ view: "editor", run: null })
              }}
              data-open-versions
            >
              <HistoryIcon />
              <span className="hidden sm:inline">{t("automation.versions.title")}</span>
            </Button>
          ) : null}
          {canEdit ? (
            <Button
              size="sm"
              onClick={() => void save()}
              disabled={saving || !(dirty || layoutDirty) || !!preview}
              data-save
            >
              {saving ? <LoaderCircleIcon className="animate-spin" /> : <SaveIcon />}
              {saving ? t("common.working") : t("common.save")}
            </Button>
          ) : null}
        </div>
      </header>
      {saveErr ? (
        <p
          className="flex items-center gap-2 border-b border-line bg-grid-card px-4 py-2 text-sm text-grid-danger-text"
          role="alert"
          data-save-error
        >
          <CircleAlertIcon className="size-4 shrink-0" />
          {saveErr}
        </p>
      ) : null}

      {view === "executions" && workflow ? (
        <ExecutionsView
          workflowId={workflow.id}
          runId={runParam}
          onSelectRun={(id) => go({ run: id })}
          saved={saved}
          vertical={narrow}
        />
      ) : (
        <div className="relative flex min-h-0 flex-1">
          <div className="relative min-w-0 flex-1" data-canvas-area>
            {preview ? (
              <div
                className="absolute inset-x-0 top-0 z-10 flex flex-wrap items-center justify-center gap-2 border-b border-line bg-grid-card px-4 py-2 text-sm"
                data-preview-banner={preview.version}
              >
                <HistoryIcon className="size-4 text-grid-muted" />
                {t("canvas.previewBanner", { n: preview.version })}
                <Button size="xs" variant="outline" onClick={() => setPreview(null)}>
                  {t("canvas.backToCurrent")}
                </Button>
              </div>
            ) : null}
            <WorkflowCanvas
              steps={canvasSteps}
              layout={canvasLayout}
              runs={preview ? undefined : runs}
              triggerState={
                !preview && runs && run.data && run.data.run.status !== "queued" ? "ok" : undefined
              }
              trigger={{
                title: t("canvas.triggerTitle"),
                subtitle: triggerSub,
                sites: requiredHere,
                autoAdd,
              }}
              selected={preview ? null : selected}
              readOnly={readOnly}
              vertical={narrow}
              onSelect={(s) => {
                if (preview) return
                select(s)
              }}
              onMove={setLayout}
              onInsert={(i) => {
                setInsertIndex(i)
                setSelected(null)
                setPanel("picker")
              }}
              onDelete={remove}
              onDuplicate={duplicate}
              onReorder={reorder}
              onRunTo={(i) => askThenStart(i + 1)}
              runBlocked={browserBlocked || (live ? t("automation.editor.testing") : "")}
            />

            {/* ---- run it ---- */}
            {!preview && canEdit ? (
              <div className="pointer-events-none absolute inset-x-0 bottom-16 z-10 flex flex-col items-center gap-2 px-4 md:bottom-6">
                <div className="pointer-events-auto flex flex-wrap items-center justify-center gap-2">
                  {live ? (
                    <Button size="lg" variant="outline" onClick={stop} data-stop-run>
                      <SquareIcon />
                      {t("canvas.stop")}
                    </Button>
                  ) : (
                    <Button
                      size="lg"
                      onClick={() => askThenStart(steps.length)}
                      disabled={starting || steps.length === 0 || !!browserBlocked}
                      data-test-run
                    >
                      {starting ? (
                        <LoaderCircleIcon className="animate-spin" />
                      ) : (
                        <FlaskConicalIcon />
                      )}
                      {t("canvas.test")}
                    </Button>
                  )}
                </div>
                {live && run.data ? (
                  <p
                    className="pointer-events-auto flex items-center gap-2 rounded-full border border-line bg-grid-card px-3 py-1 text-xs shadow-sm"
                    data-run-live
                  >
                    <LoaderCircleIcon className="size-3.5 animate-spin text-primary" />
                    {t("canvas.running", {
                      ok: run.data.run.ok_count,
                      total: run.data.run.step_count,
                    })}
                  </p>
                ) : done ? (
                  <p
                    className="pointer-events-auto flex flex-wrap items-center justify-center gap-2 rounded-full border border-line bg-grid-card px-3 py-1 text-xs shadow-sm"
                    data-test-status={done.status}
                  >
                    <ExecutionIcon status={done.status} className="size-3.5" />
                    {t(`canvas.finished.${done.status}`, {
                      ok: done.ok_count,
                      total: done.step_count,
                    })}
                    {!current ? (
                      <span className="text-grid-muted">· {t("automation.editor.testStale")}</span>
                    ) : null}
                    {workflow ? (
                      <button
                        type="button"
                        className="underline"
                        onClick={() => go({ view: "executions", run: done.id })}
                        data-open-execution
                      >
                        {t("canvas.openExecution")}
                      </button>
                    ) : (
                      <Link className="underline" href={`${base}/runs/${done.id}`}>
                        {t("automation.openRun")}
                      </Link>
                    )}
                  </p>
                ) : steps.length === 0 ? (
                  <p className="pointer-events-auto rounded-full border border-line bg-grid-card px-3 py-1 text-xs text-grid-muted">
                    {t("canvas.emptyHint")}
                  </p>
                ) : browserBlocked ? (
                  <p
                    className="pointer-events-auto rounded-full border border-line bg-grid-card px-3 py-1 text-xs text-grid-muted"
                    data-run-blocked
                  >
                    {browserBlocked}
                    {!agent ? (
                      <>
                        {" "}
                        <Link className="underline" href={`${base}/extension`}>
                          {t("automation.ext.link")}
                        </Link>
                      </>
                    ) : null}
                  </p>
                ) : null}
                {runErr ? (
                  <p
                    className="pointer-events-auto max-w-lg rounded-md border border-line bg-grid-card px-3 py-1.5 text-center text-xs text-grid-danger-text"
                    role="alert"
                    data-test-error
                  >
                    {runErr}
                  </p>
                ) : null}
              </div>
            ) : null}
          </div>

          {panelOpen ? (
            <div
              className={cn(
                "z-20 min-h-0 border-line bg-background",
                narrow ? "absolute inset-0" : "w-[26rem] shrink-0 border-s",
              )}
              dir={isRtl ? "rtl" : "ltr"}
              data-side-panel={panel}
            >
              {panel === "picker" ? (
                <NodePicker
                  position={insertIndex}
                  onPick={(k) => insert(insertIndex, k)}
                  onClose={() => setPanel(null)}
                />
              ) : panel === "versions" && workflow ? (
                <VersionsPanel
                  workflow={workflow}
                  versions={versions}
                  previewing={preview?.version ?? null}
                  dirty={dirty}
                  canEdit={canEdit}
                  onPreview={setPreview}
                  onRestored={onSaved}
                  onClose={() => {
                    setPreview(null)
                    setPanel(null)
                  }}
                />
              ) : selected === "trigger" ? (
                <TriggerPanel
                  workflowId={workflow?.id}
                  steps={steps}
                  params={draft.params}
                  values={values}
                  schedules={schedules.data?.schedules ?? []}
                  readOnly={!canEdit}
                  description={draft.description}
                  onDescription={(description) => setDraft((d) => ({ ...d, description }))}
                  onParams={(p) => setDraft((d) => ({ ...d, params: p }))}
                  onValues={setValues}
                  onClose={() => select(null)}
                  sites={requiredHere}
                  autoAdd={autoAdd}
                  canManage={ws.can("automation:manage")}
                />
              ) : typeof selected === "number" && steps[selected] ? (
                <StepPanel
                  key={selected}
                  step={steps[selected]}
                  index={selected}
                  readOnly={!canEdit}
                  tab={tab}
                  onTab={setTab}
                  onChange={(s) => setSteps(steps.map((x, i) => (i === selected ? s : x)))}
                  onClose={() => select(null)}
                  onDelete={() => remove(selected)}
                  onRunTo={() => askThenStart(selected + 1)}
                  runBlocked={browserBlocked || (live ? t("automation.editor.testing") : "")}
                  output={runs ? run.data?.steps.find((s) => s.position === selected) : undefined}
                  runId={runs ? test?.runId : undefined}
                  live={live}
                  agentId={agent?.id ?? ""}
                  pickBlocked={browserBlocked || undefined}
                  pageUrl={pageUrl(selected)}
                />
              ) : null}
            </div>
          ) : null}
        </div>
      )}
      <span className="sr-only" aria-live="polite">
        {live
          ? t("canvas.running", {
              ok: run.data?.run.ok_count ?? 0,
              total: run.data?.run.step_count ?? 0,
            })
          : ""}
      </span>

      {/* A publishing workflow asks first (MH-558): only this confirm starts the run. */}
      <AlertDialog open={confirmRun != null} onOpenChange={(open) => !open && setConfirmRun(null)}>
        <AlertDialogContent data-confirm-run-dialog>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("presets.confirmTitle", { name: isolate(draft.name || t("canvas.newTitle")) })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("presets.confirmBody", {
                hosts: (requiredHere.length ? requiredHere : (workflow?.hosts ?? [])).join(", "),
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              disabled={starting}
              onClick={async () => {
                await start(confirmRun ?? steps.length)
                setConfirmRun(null)
              }}
              data-confirm-run
            >
              {starting ? t("common.working") : t("presets.runAndPublish")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
