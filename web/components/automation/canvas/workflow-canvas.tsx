"use client"

/*
The workflow canvas (MH-480): a workflow's steps drawn the way n8n draws one —
the trigger first, a node per step joined left to right, a "+" on every edge to
put a step between two, a "+" after the last one to add the next, and each
node lighting up as a run reaches it.

It draws what it is given and reports what the person does; it owns no
workflow state. The graph is a pure function of the steps (model.ts), so there
is nothing here that could drift from what the browser will run.

The React Flow surface is always laid out left-to-right internally (dir="ltr"
on its container — its geometry assumes it). An RTL page mirrors the positions
and swaps which side a node's handles are on, so the flow reads right-to-left,
while each node's own text keeps the page's direction.
*/

import "@xyflow/react/dist/style.css"

import {
  createContext,
  memo,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react"
import {
  Background,
  BackgroundVariant,
  Handle,
  NodeToolbar,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useNodesState,
  useReactFlow,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeProps,
} from "@xyflow/react"
import {
  ArrowDownIcon,
  ArrowLeftIcon,
  ArrowRightIcon,
  ArrowUpIcon,
  CheckIcon,
  CopyIcon,
  LoaderCircleIcon,
  PlayIcon,
  PlusIcon,
  Trash2Icon,
  TriangleAlertIcon,
  XIcon,
  ZapIcon,
} from "lucide-react"

import { BaseNode } from "@/components/flow/base-node"
import { ButtonEdge } from "@/components/flow/button-edge"
import { NodeStatusIndicator, type NodeStatus } from "@/components/flow/node-status-indicator"
import { ZoomSlider } from "@/components/flow/zoom-slider"
import { Button } from "@fadymondy/nasaq/web"
import type { WorkflowStep } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { cn } from "@/lib/utils"

import {
  GAP,
  NODE_SIZE,
  kindOf,
  mirror,
  missingField,
  specOf,
  subtitleOf,
  type Category,
  type Layout,
  type NodeRun,
  type Point,
} from "./model"

export type Selection = "trigger" | number | null

export type CanvasProps = {
  steps: WorkflowStep[]
  layout: Layout
  /** Run state per step position: a live test run, or a past one being replayed. */
  runs?: Record<number, NodeRun>
  /** Whether the trigger has fired in the run being shown. */
  triggerState?: NodeRun["state"]
  trigger: { title: string; subtitle: string; sites?: string[]; autoAdd?: boolean }
  selected: Selection
  readOnly?: boolean
  vertical?: boolean
  onSelect: (s: Selection) => void
  onMove?: (layout: Layout) => void
  onInsert?: (index: number) => void
  onDelete?: (index: number) => void
  onDuplicate?: (index: number) => void
  onReorder?: (index: number, to: number) => void
  onRunTo?: (index: number) => void
  /** Why "run to here" is unavailable right now, if it is. */
  runBlocked?: string
}

// ── what the nodes read ──────────────────────────────────────────────────

type Ctx = Omit<CanvasProps, "layout" | "steps" | "trigger" | "onMove"> & {
  rtl: boolean
  count: number
}
const CanvasCtx = createContext<Ctx | null>(null)
const useCanvas = () => {
  const c = useContext(CanvasCtx)
  if (!c) throw new Error("canvas node outside its canvas")
  return c
}

type StepData = { step: WorkflowStep; index: number }
type TriggerData = {
  title: string
  subtitle: string
  /** The sites the workflow needs, and whether a run adds them to its browser (MH-517). */
  sites?: string[]
  autoAdd?: boolean
}
type AddData = { index: number }
type InsertEdgeData = { index: number; items: number | null; state: string }

const CATEGORY_TONE: Record<Category, string> = {
  browser: "text-primary",
  interact: "text-nq-accent",
  wait: "text-nq-warning",
  data: "text-nq-success",
}

const statusOf = (state?: NodeRun["state"]): NodeStatus | undefined =>
  state === "running"
    ? "loading"
    : state === "ok"
      ? "success"
      : state === "failed"
        ? "error"
        : state === "skipped"
          ? "skipped"
          : undefined

function handles(rtl: boolean, vertical: boolean) {
  if (vertical) return { target: Position.Top, source: Position.Bottom }
  return rtl
    ? { target: Position.Right, source: Position.Left }
    : { target: Position.Left, source: Position.Right }
}

/** The corner badge: done, failed, or going. */
function StateBadge({ state }: { state?: NodeRun["state"] }) {
  if (state === "ok")
    return (
      <span
        className="absolute -end-2 -bottom-2 flex size-6 items-center justify-center rounded-full bg-nq-success text-background shadow-sm"
        data-node-badge="ok"
      >
        <CheckIcon className="size-3.5" strokeWidth={3} />
      </span>
    )
  if (state === "failed")
    return (
      <span
        className="absolute -end-2 -bottom-2 flex size-6 items-center justify-center rounded-full bg-nq-danger text-background shadow-sm"
        data-node-badge="failed"
      >
        <XIcon className="size-3.5" strokeWidth={3} />
      </span>
    )
  if (state === "running")
    return (
      <span
        className="absolute -end-2 -bottom-2 flex size-6 items-center justify-center rounded-full border border-nq-line bg-card text-primary shadow-sm"
        data-node-badge="running"
      >
        <LoaderCircleIcon className="size-3.5 animate-spin" />
      </span>
    )
  return null
}

// ── nodes ────────────────────────────────────────────────────────────────

const TriggerNode = memo(function TriggerNode({ data }: NodeProps<Node<TriggerData>>) {
  const c = useCanvas()
  const { t, dir } = useTranslations()
  const h = handles(c.rtl, !!c.vertical)
  // The trigger's rounded side faces away from the flow, as n8n draws it.
  const radius = c.vertical
    ? "rounded-t-[3rem] rounded-b-lg"
    : c.rtl
      ? "rounded-r-[3rem] rounded-l-lg"
      : "rounded-l-[3rem] rounded-r-lg"
  return (
    <div className="relative" data-canvas-node="trigger" data-node-status={c.triggerState ?? ""}>
      <NodeStatusIndicator status={statusOf(c.triggerState)} radius={radius}>
        <BaseNode
          className={cn("flex items-center justify-center", radius)}
          style={{ width: NODE_SIZE, height: NODE_SIZE }}
          aria-label={data.title}
        >
          <ZapIcon className="size-9 text-nq-accent" />
          <Handle
            type="source"
            position={h.source}
            isConnectable={false}
            className="!size-3 !border-nq-fg-muted !bg-card"
          />
          <StateBadge state={c.triggerState} />
        </BaseNode>
      </NodeStatusIndicator>
      <NodeLabel
        title={data.title}
        subtitle={data.subtitle}
        dir={dir}
        beside={c.vertical}
        note={
          data.sites?.length
            ? t(data.autoAdd ? "canvas.sites.nodeAuto" : "canvas.sites.nodeManual", {
                sites:
                  data.sites.length > 2
                    ? `${data.sites.slice(0, 2).join(", ")} +${data.sites.length - 2}`
                    : data.sites.join(", "),
              })
            : undefined
        }
      />
      <span className="sr-only">{t("canvas.trigger")}</span>
    </div>
  )
})

const StepNode = memo(function StepNode({ data, selected }: NodeProps<Node<StepData>>) {
  const c = useCanvas()
  const { t, dir } = useTranslations()
  const [hover, setHover] = useState(false)
  const { step, index } = data
  const kind = kindOf(step)
  const spec = specOf(kind)
  const Icon = spec.icon
  const run = c.runs?.[index]
  const h = handles(c.rtl, !!c.vertical)
  const missing = missingField(step)
  const title = kind === "unknown" ? step.command : t(`canvas.kind.${kind}`)
  // Earlier and later along the flow, drawn as arrows that point the way the
  // flow reads.
  const EarlierIcon = c.vertical ? ArrowUpIcon : c.rtl ? ArrowRightIcon : ArrowLeftIcon
  const LaterIcon = c.vertical ? ArrowDownIcon : c.rtl ? ArrowLeftIcon : ArrowRightIcon

  return (
    <div
      className="relative"
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      data-canvas-node="step"
      data-node-index={index}
      data-node-kind={kind}
      data-node-status={run?.state ?? ""}
    >
      {!c.readOnly ? (
        <NodeToolbar isVisible={hover || selected} position={Position.Top} offset={6}>
          <div
            className="flex items-center gap-0.5 rounded-md border border-nq-line bg-card p-0.5 shadow-sm"
            dir={dir}
          >
            {c.onRunTo ? (
              <Button
                variant="ghost"
                size="icon-sm"
                onClick={() => c.onRunTo?.(index)}
                disabled={!!c.runBlocked}
                title={c.runBlocked || t("canvas.runTo")}
                aria-label={t("canvas.runTo")}
                data-run-to={index}
              >
                <PlayIcon />
              </Button>
            ) : null}
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => c.onReorder?.(index, index - 1)}
              disabled={index === 0}
              title={t("canvas.moveEarlier")}
              aria-label={t("canvas.moveEarlier")}
            >
              <EarlierIcon />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => c.onReorder?.(index, index + 1)}
              disabled={index === c.count - 1}
              title={t("canvas.moveLater")}
              aria-label={t("canvas.moveLater")}
            >
              <LaterIcon />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => c.onDuplicate?.(index)}
              title={t("canvas.duplicate")}
              aria-label={t("canvas.duplicate")}
            >
              <CopyIcon />
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-nq-danger-text"
              onClick={() => c.onDelete?.(index)}
              title={t("canvas.delete")}
              aria-label={t("canvas.delete")}
              data-delete-node={index}
            >
              <Trash2Icon />
            </Button>
          </div>
        </NodeToolbar>
      ) : null}
      <NodeStatusIndicator status={statusOf(run?.state)} radius="rounded-lg">
        <BaseNode
          className={cn(
            "flex items-center justify-center rounded-lg",
            run?.state === "skipped" && "opacity-60",
          )}
          style={{ width: NODE_SIZE, height: NODE_SIZE }}
          aria-label={title}
        >
          <Icon
            className={cn(
              "size-9",
              CATEGORY_TONE[spec.category],
              kind === "unknown" && "text-nq-danger-text",
            )}
          />
          <Handle
            type="target"
            position={h.target}
            isConnectable={false}
            className="!size-3 !border-nq-fg-muted !bg-card"
          />
          <Handle
            type="source"
            position={h.source}
            isConnectable={false}
            className="!size-3 !border-nq-fg-muted !bg-card"
          />
          {missing && !run ? (
            <span
              className="absolute -end-2 -top-2 flex size-6 items-center justify-center rounded-full border border-nq-line bg-card text-nq-warning-text"
              title={t("canvas.needsField", { field: t(`automation.field.${missing}`) })}
              data-node-missing={missing}
            >
              <TriangleAlertIcon className="size-3.5" />
            </span>
          ) : null}
          {step.continue_on_error ? (
            <span className="absolute start-1.5 top-1 text-[10px] font-medium text-muted-foreground">
              {t("canvas.continues")}
            </span>
          ) : null}
          <StateBadge state={run?.state} />
        </BaseNode>
      </NodeStatusIndicator>
      <NodeLabel title={title} subtitle={subtitleOf(step)} dir={dir} beside={c.vertical} />
    </div>
  )
})

/** The "+" after the last step (or alone, on an empty workflow). */
const AddNode = memo(function AddNode({ data }: NodeProps<Node<AddData>>) {
  const c = useCanvas()
  const { t } = useTranslations()
  const h = handles(c.rtl, !!c.vertical)
  return (
    <div className="relative" data-canvas-node="add">
      <button
        type="button"
        onClick={() => c.onInsert?.(data.index)}
        className="flex size-14 items-center justify-center rounded-lg border-2 border-dashed border-nq-fg-muted/60 bg-card text-muted-foreground transition-colors hover:border-primary hover:text-primary focus-visible:outline-2 focus-visible:outline-ring"
        aria-label={c.count === 0 ? t("canvas.addFirst") : t("canvas.addStep")}
        data-add-end
      >
        <PlusIcon className="size-6" />
        <Handle type="target" position={h.target} isConnectable={false} className="!opacity-0" />
      </button>
      {c.count === 0 ? (
        <p className="absolute top-full left-1/2 mt-2 w-40 -translate-x-1/2 text-center text-xs text-muted-foreground">
          {t("canvas.addFirst")}
        </p>
      ) : null}
    </div>
  )
})

function NodeLabel({
  title,
  subtitle,
  dir,
  beside,
  note,
}: {
  title: string
  subtitle: string
  dir: "ltr" | "rtl"
  beside?: boolean
  /** A third line: the trigger's sites (MH-517). */
  note?: string
}) {
  // Physical sides on purpose: the canvas is an LTR surface, and the label
  // goes to the page's end side.
  const side = dir === "rtl" ? "right-full mr-4" : "left-full ml-4"
  // Under the node when the flow runs across; beside it when it runs down, where
  // under it is where the edge to the next node goes.
  return (
    <div
      className={cn(
        "pointer-events-none absolute w-44",
        beside
          ? `top-1/2 ${side} -translate-y-1/2 text-start`
          : "top-full left-1/2 mt-2 -translate-x-1/2 text-center",
      )}
      dir={dir}
    >
      <p className="truncate text-sm font-medium text-foreground">{title}</p>
      {subtitle ? (
        <p dir="ltr" className="truncate font-mono text-[11px] text-muted-foreground">
          {subtitle}
        </p>
      ) : null}
      {note ? (
        <p className="truncate text-[11px] text-muted-foreground" data-trigger-sites>
          {note}
        </p>
      ) : null}
    </div>
  )
}

// ── edges ────────────────────────────────────────────────────────────────

const EDGE_TONE: Record<string, string> = {
  ok: "!stroke-nq-success",
  failed: "!stroke-nq-danger",
  running: "!stroke-primary flow-edge-running",
}

function InsertEdge(props: EdgeProps<Edge<InsertEdgeData>>) {
  const c = useCanvas()
  const { t } = useTranslations()
  const d = props.data!
  return (
    <ButtonEdge {...props} className={EDGE_TONE[d.state]}>
      <div className="flex flex-col items-center gap-1">
        {d.items != null ? (
          <span
            className="rounded bg-card px-1 text-[11px] whitespace-nowrap text-muted-foreground"
            data-edge-items={d.items}
          >
            {t("canvas.items", { n: d.items })}
          </span>
        ) : null}
        {!c.readOnly ? (
          <button
            type="button"
            onClick={() => c.onInsert?.(d.index)}
            className="flex size-6 items-center justify-center rounded-md border border-nq-line bg-card text-muted-foreground shadow-sm transition-colors hover:border-primary hover:text-primary focus-visible:outline-2 focus-visible:outline-ring"
            aria-label={t("canvas.insertHere", { n: d.index + 1 })}
            data-insert-at={d.index}
          >
            <PlusIcon className="size-3.5" />
          </button>
        ) : null}
      </div>
    </ButtonEdge>
  )
}

const nodeTypes = { trigger: TriggerNode, step: StepNode, add: AddNode }

/** Room round the graph: the labels hang below the nodes, and the run controls float at the bottom. */
const FIT = {
  padding: { top: "56px", bottom: "150px", left: "48px", right: "72px" },
  maxZoom: 1.1,
} as const
/** Down a narrow screen the labels sit beside the nodes, so the room goes there. */
const FIT_DOWN = {
  padding: { top: "32px", bottom: "150px", left: "24px", right: "200px" },
  maxZoom: 1,
} as const
const FIT_DOWN_RTL = {
  ...FIT_DOWN,
  padding: { ...FIT_DOWN.padding, left: "200px", right: "24px" },
} as const
const edgeTypes = { insert: InsertEdge }

// ── the canvas ───────────────────────────────────────────────────────────

export function WorkflowCanvas(props: CanvasProps) {
  return (
    <ReactFlowProvider>
      <Canvas {...props} />
    </ReactFlowProvider>
  )
}

function Canvas(props: CanvasProps) {
  const { steps, layout, runs, trigger, selected, readOnly, vertical, onSelect, onMove } = props
  const { t, isRtl } = useTranslations()
  const flow = useReactFlow()

  const place = useCallback((p: Point) => (isRtl && !vertical ? mirror(p) : p), [isRtl, vertical])

  const built = useMemo<Node[]>(() => {
    const out: Node[] = [
      {
        id: "trigger",
        type: "trigger",
        position: place(layout.trigger),
        data: trigger,
        selected: selected === "trigger",
        draggable: !readOnly && !vertical,
        deletable: false,
      },
    ]
    steps.forEach((step, index) => {
      out.push({
        id: `s${index}`,
        type: "step",
        position: place(layout.nodes[index] ?? { x: (index + 1) * GAP, y: 0 }),
        data: { step, index },
        selected: selected === index,
        draggable: !readOnly && !vertical,
        deletable: !readOnly,
      })
    })
    if (!readOnly) {
      const last =
        steps.length === 0 ? layout.trigger : (layout.nodes[steps.length - 1] ?? layout.trigger)
      // Centred on the last node's line: the add box is smaller than a node.
      const off = (NODE_SIZE - 56) / 2
      const at = vertical
        ? { x: last.x + off, y: last.y + GAP - 40 }
        : { x: last.x + GAP - 40, y: last.y + off }
      out.push({
        id: "add",
        type: "add",
        position: isRtl && !vertical ? { x: -at.x - 56, y: at.y } : at,
        data: { index: steps.length },
        draggable: false,
        selectable: false,
        deletable: false,
      })
    }
    return out
  }, [steps, layout, trigger, selected, readOnly, vertical, place, isRtl])

  const [nodes, setNodes, onNodesChange] = useNodesState<Node>(built)
  // Follow the props, but never yank a node out from under the pointer.
  useEffect(() => {
    setNodes((current) => {
      const dragging = new Map(current.filter((n) => n.dragging).map((n) => [n.id, n.position]))
      return built.map((n) =>
        dragging.has(n.id) ? { ...n, position: dragging.get(n.id)!, dragging: true } : n,
      )
    })
  }, [built, setNodes])

  const edges = useMemo<Edge[]>(() => {
    const ids = ["trigger", ...steps.map((_, i) => `s${i}`)]
    const out: Edge[] = []
    for (let i = 1; i < ids.length; i++) {
      const before = i - 2 >= 0 ? runs?.[i - 2] : undefined
      const next = runs?.[i - 1]
      // An edge is lit once the step before it finished, and pulses while the
      // step after it is the one going.
      const state =
        next?.state === "running"
          ? "running"
          : i === 1
            ? props.triggerState === "ok"
              ? "ok"
              : ""
            : before?.state === "ok"
              ? "ok"
              : before?.state === "failed"
                ? "failed"
                : ""
      out.push({
        id: `e${i}`,
        source: ids[i - 1],
        target: ids[i],
        type: "insert",
        data: {
          index: i - 1,
          items: before?.state === "ok" ? (before.items ?? null) : null,
          state,
        },
        selectable: false,
        deletable: false,
      })
    }
    if (!readOnly) {
      out.push({
        id: "e-add",
        source: ids[ids.length - 1],
        target: "add",
        selectable: false,
        deletable: false,
        style: { strokeDasharray: "4 4" },
      })
    }
    return out
  }, [steps, runs, readOnly, props.triggerState])

  // Fit the graph whenever its shape changes (a node added, a graph opened) and
  // whenever the canvas itself changes size — a side panel opening would
  // otherwise leave the nodes it covers out of sight.
  const shape = `${steps.length}-${vertical ? "v" : "h"}-${isRtl ? "r" : "l"}`
  const fitted = useRef("")
  const box = useRef<HTMLDivElement>(null)
  const verticalRef = useRef(!!vertical)
  verticalRef.current = !!vertical
  const rtlRef = useRef(isRtl)
  rtlRef.current = isRtl
  useEffect(() => {
    if (fitted.current === shape) return
    fitted.current = shape
    const id = requestAnimationFrame(
      () =>
        void flow.fitView({
          ...(verticalRef.current ? (rtlRef.current ? FIT_DOWN_RTL : FIT_DOWN) : FIT),
          duration: 200,
        }),
    )
    return () => cancelAnimationFrame(id)
  }, [shape, flow])
  useEffect(() => {
    const el = box.current
    if (!el) return
    let last = el.clientWidth
    let frame = 0
    const ro = new ResizeObserver(() => {
      if (Math.abs(el.clientWidth - last) < 8) return
      last = el.clientWidth
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(
        () =>
          void flow.fitView({
            ...(verticalRef.current ? (rtlRef.current ? FIT_DOWN_RTL : FIT_DOWN) : FIT),
            duration: 200,
          }),
      )
    })
    ro.observe(el)
    return () => {
      ro.disconnect()
      cancelAnimationFrame(frame)
    }
  }, [flow])

  const ctx: Ctx = { ...props, rtl: isRtl && !vertical, count: steps.length }

  return (
    <CanvasCtx.Provider value={ctx}>
      <div
        ref={box}
        dir="ltr"
        className="size-full"
        data-workflow-canvas
        data-read-only={readOnly ? "true" : "false"}
      >
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          onNodesChange={onNodesChange}
          nodesConnectable={false}
          elementsSelectable
          deleteKeyCode={readOnly ? null : ["Delete", "Backspace"]}
          onNodesDelete={(deleted) => {
            const i = deleted
              .map((n) => n.data as StepData)
              .find((d) => typeof d?.index === "number")
            if (i) props.onDelete?.(i.index)
          }}
          onNodeClick={(_, n) => {
            if (n.id === "trigger") onSelect("trigger")
            else if (n.type === "step") onSelect((n.data as StepData).index)
          }}
          onPaneClick={() => onSelect(null)}
          onNodeDragStop={(_, n) => {
            if (!onMove) return
            const p = isRtl && !vertical ? mirror(n.position) : n.position
            if (n.id === "trigger") onMove({ ...layout, trigger: p })
            else if (n.type === "step") {
              const i = (n.data as StepData).index
              onMove({ ...layout, nodes: layout.nodes.map((q, j) => (j === i ? p : q)) })
            }
          }}
          minZoom={0.2}
          maxZoom={2}
          fitView
          fitViewOptions={vertical ? (isRtl ? FIT_DOWN_RTL : FIT_DOWN) : FIT}
          attributionPosition={isRtl ? "bottom-left" : "bottom-right"}
        >
          <Background variant={BackgroundVariant.Dots} gap={20} size={1.4} />
          <ZoomSlider
            position={isRtl ? "bottom-right" : "bottom-left"}
            labels={{
              zoomIn: t("canvas.zoomIn"),
              zoomOut: t("canvas.zoomOut"),
              reset: t("canvas.zoomReset"),
              fit: t("canvas.fit"),
              zoom: t("canvas.zoom"),
            }}
          />
        </ReactFlow>
      </div>
    </CanvasCtx.Provider>
  )
}
