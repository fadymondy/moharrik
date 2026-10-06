"use client"

/*
A rule drawn the way the workflow canvas draws a workflow (MH-480, MH-427):
the trigger with its rounded side, an IF node holding the conditions, then a
node per action, joined in order, and a "+" to add the next action. A test or a
real run paints the nodes: what ran, what would have, what failed and what was
skipped.

Same surface and rules as the workflow canvas: React Flow laid out LTR
internally, mirrored for an RTL page, top to bottom on a narrow screen. The
graph is a pure function of the rule, so it cannot drift from what runs.
*/

import "@xyflow/react/dist/style.css"

import { createContext, memo, useContext, useEffect, useMemo, useRef } from "react"
import {
  Background,
  BackgroundVariant,
  Handle,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from "@xyflow/react"
import { CheckIcon, FilterIcon, MinusIcon, PlusIcon, XIcon } from "lucide-react"

import { NODE_SIZE, GAP } from "@/components/automation/canvas/model"
import { BaseNode } from "@/components/flow/base-node"
import { NodeStatusIndicator, type NodeStatus } from "@/components/flow/node-status-indicator"
import { ZoomSlider } from "@/components/flow/zoom-slider"
import { useTranslations } from "@/lib/i18n"
import { cn } from "@/lib/utils"

import { actionIcon, triggerIcon, type CanvasRun, type NodeState } from "./rule-model"

export type RuleSelection = "trigger" | "conditions" | "add" | number | null

type CanvasProps = {
  trigger: { type: string; title: string; subtitle: string }
  conditions: { title: string; subtitle: string }
  actions: { type: string; title: string; subtitle: string }[]
  run?: CanvasRun
  selected: RuleSelection
  readOnly?: boolean
  vertical?: boolean
  onSelect: (s: RuleSelection) => void
}

type Ctx = {
  rtl: boolean
  vertical: boolean
  readOnly: boolean
  onSelect: (s: RuleSelection) => void
  count: number
}
const CanvasCtx = createContext<Ctx | null>(null)
const useCanvas = () => {
  const c = useContext(CanvasCtx)
  if (!c) throw new Error("rule node outside its canvas")
  return c
}

const statusOf = (s: NodeState): NodeStatus | undefined =>
  s === "ok" || s === "would"
    ? "success"
    : s === "failed"
      ? "error"
      : s === "skipped"
        ? "skipped"
        : undefined

function handles(rtl: boolean, vertical: boolean) {
  if (vertical) return { target: Position.Top, source: Position.Bottom }
  return rtl
    ? { target: Position.Right, source: Position.Left }
    : { target: Position.Left, source: Position.Right }
}

function Badge({ state }: { state: NodeState }) {
  const { t } = useTranslations()
  if (!state) return null
  const tone =
    state === "ok"
      ? "bg-nq-success text-background"
      : state === "would"
        ? "border border-nq-success bg-card text-nq-success"
        : state === "failed"
          ? "bg-nq-danger text-background"
          : "border border-nq-line bg-card text-muted-foreground"
  const Icon = state === "failed" ? XIcon : state === "skipped" ? MinusIcon : CheckIcon
  return (
    <span
      className={cn(
        "absolute -end-2 -bottom-2 flex size-6 items-center justify-center rounded-full shadow-sm",
        tone,
      )}
      data-node-badge={state}
      title={t(`rules.nodeState.${state}`)}
    >
      <Icon className="size-3.5" strokeWidth={3} />
    </span>
  )
}

function Label({
  title,
  subtitle,
  beside,
  dir,
}: {
  title: string
  subtitle: string
  beside: boolean
  dir: "ltr" | "rtl"
}) {
  const side = dir === "rtl" ? "right-full mr-4" : "left-full ml-4"
  return (
    <div
      className={cn(
        "pointer-events-none absolute w-48",
        beside
          ? `top-1/2 ${side} -translate-y-1/2 text-start`
          : "top-full left-1/2 mt-2 -translate-x-1/2 text-center",
      )}
      dir={dir}
    >
      <p className="truncate text-sm font-medium text-foreground">{title}</p>
      {subtitle ? (
        <p className="line-clamp-2 text-[11px] text-muted-foreground">{subtitle}</p>
      ) : null}
    </div>
  )
}

type NodeData = {
  kind: "trigger" | "conditions" | "action"
  type: string
  title: string
  subtitle: string
  index: number
  state: NodeState
}

const RuleNode = memo(function RuleNode({ data, selected }: NodeProps<Node<NodeData>>) {
  const c = useCanvas()
  const { dir } = useTranslations()
  const h = handles(c.rtl, c.vertical)
  const Icon =
    data.kind === "trigger"
      ? triggerIcon(data.type)
      : data.kind === "conditions"
        ? FilterIcon
        : actionIcon(data.type)
  const radius =
    data.kind === "trigger"
      ? c.vertical
        ? "rounded-t-[3rem] rounded-b-lg"
        : c.rtl
          ? "rounded-r-[3rem] rounded-l-lg"
          : "rounded-l-[3rem] rounded-r-lg"
      : data.kind === "conditions"
        ? "rounded-[1.75rem]"
        : "rounded-lg"
  const tone =
    data.kind === "trigger"
      ? "text-nq-accent"
      : data.kind === "conditions"
        ? "text-nq-accent"
        : "text-primary"
  return (
    <div
      className="relative"
      data-rule-node={data.kind}
      data-node-index={data.kind === "action" ? data.index : undefined}
      data-node-state={data.state ?? ""}
      data-selected={selected ? "true" : "false"}
    >
      <NodeStatusIndicator status={statusOf(data.state)} radius={radius}>
        <BaseNode
          className={cn(
            "flex items-center justify-center",
            radius,
            data.state === "skipped" && "opacity-60",
          )}
          style={{ width: NODE_SIZE, height: NODE_SIZE }}
          aria-label={data.title}
        >
          <Icon className={cn("size-9", tone)} />
          {data.kind !== "trigger" ? (
            <Handle
              type="target"
              position={h.target}
              isConnectable={false}
              className="!size-3 !border-nq-fg-muted !bg-card"
            />
          ) : null}
          <Handle
            type="source"
            position={h.source}
            isConnectable={false}
            className="!size-3 !border-nq-fg-muted !bg-card"
          />
          <Badge state={data.state} />
        </BaseNode>
      </NodeStatusIndicator>
      <Label title={data.title} subtitle={data.subtitle} beside={c.vertical} dir={dir} />
    </div>
  )
})

const AddNode = memo(function AddNode() {
  const c = useCanvas()
  const { t } = useTranslations()
  const h = handles(c.rtl, c.vertical)
  return (
    <div className="relative" data-rule-node="add">
      <button
        type="button"
        onClick={() => c.onSelect("add")}
        className="flex size-14 items-center justify-center rounded-lg border-2 border-dashed border-nq-fg-muted/60 bg-card text-muted-foreground transition-colors hover:border-primary hover:text-primary focus-visible:outline-2 focus-visible:outline-ring"
        aria-label={t("rules.addAction")}
        title={t("rules.addAction")}
        data-add-action
      >
        <PlusIcon className="size-6" />
        <Handle type="target" position={h.target} isConnectable={false} className="!opacity-0" />
      </button>
      {c.count === 0 ? (
        <p className="absolute top-full left-1/2 mt-2 w-40 -translate-x-1/2 text-center text-xs text-muted-foreground">
          {t("rules.addFirstAction")}
        </p>
      ) : null}
    </div>
  )
})

const nodeTypes = { rule: RuleNode, add: AddNode }

const FIT = {
  padding: { top: "48px", bottom: "120px", left: "48px", right: "72px" },
  maxZoom: 1.05,
} as const
const FIT_DOWN = {
  padding: { top: "32px", bottom: "120px", left: "24px", right: "210px" },
  maxZoom: 0.95,
} as const
const FIT_DOWN_RTL = {
  ...FIT_DOWN,
  padding: { ...FIT_DOWN.padding, left: "210px", right: "24px" },
} as const

export function RuleCanvas(props: CanvasProps) {
  return (
    <ReactFlowProvider>
      <Canvas {...props} />
    </ReactFlowProvider>
  )
}

function Canvas({
  trigger,
  conditions,
  actions,
  run,
  selected,
  readOnly = false,
  vertical = false,
  onSelect,
}: CanvasProps) {
  const { t, isRtl } = useTranslations()
  const flow = useReactFlow()
  const rtl = isRtl && !vertical

  const nodes = useMemo<Node[]>(() => {
    const at = (i: number) => {
      const p = vertical ? { x: 0, y: i * (GAP - 40) } : { x: i * GAP, y: 0 }
      return rtl ? { x: -p.x - NODE_SIZE, y: p.y } : p
    }
    const out: Node[] = [
      {
        id: "trigger",
        type: "rule",
        position: at(0),
        data: {
          kind: "trigger",
          type: trigger.type,
          title: trigger.title,
          subtitle: trigger.subtitle,
          index: -1,
          state: run?.trigger,
        },
        selected: selected === "trigger",
        draggable: false,
      },
      {
        id: "conditions",
        type: "rule",
        position: at(1),
        data: {
          kind: "conditions",
          type: "",
          title: conditions.title,
          subtitle: conditions.subtitle,
          index: -1,
          state: run?.conditions,
        },
        selected: selected === "conditions",
        draggable: false,
      },
    ]
    actions.forEach((a, i) => {
      out.push({
        id: `a${i}`,
        type: "rule",
        position: at(i + 2),
        data: {
          kind: "action",
          type: a.type,
          title: a.title,
          subtitle: a.subtitle,
          index: i,
          state: run?.actions[i],
        },
        selected: selected === i,
        draggable: false,
      })
    })
    if (!readOnly) {
      const p = at(actions.length + 2)
      const off = (NODE_SIZE - 56) / 2
      out.push({
        id: "add",
        type: "add",
        // Where the next node would sit, pulled 40px back towards the last one;
        // mirrored on an RTL page like the nodes (the box is 56px wide).
        position: vertical
          ? { x: p.x + off, y: p.y }
          : { x: rtl ? p.x + NODE_SIZE - 16 : p.x - 40, y: p.y + off },
        data: {},
        draggable: false,
        selectable: false,
      })
    }
    return out
  }, [trigger, conditions, actions, run, selected, readOnly, vertical, rtl])

  const edges = useMemo<Edge[]>(() => {
    const ids = ["trigger", "conditions", ...actions.map((_, i) => `a${i}`)]
    const out: Edge[] = []
    const lit = (s: NodeState) =>
      s === "ok" || s === "would" ? "!stroke-nq-success" : s === "failed" ? "!stroke-nq-danger" : ""
    for (let i = 1; i < ids.length; i++) {
      const before = i === 1 ? run?.trigger : i === 2 ? run?.conditions : run?.actions[i - 3]
      out.push({
        id: `e${i}`,
        source: ids[i - 1],
        target: ids[i],
        type: "smoothstep",
        className: lit(before),
        selectable: false,
      })
    }
    if (!readOnly) {
      out.push({
        id: "e-add",
        source: ids[ids.length - 1],
        target: "add",
        selectable: false,
        style: { strokeDasharray: "4 4" },
      })
    }
    return out
  }, [actions, run, readOnly])

  const shape = `${actions.length}-${vertical ? "v" : "h"}-${isRtl ? "r" : "l"}-${readOnly ? "ro" : "rw"}`
  const fitted = useRef("")
  const box = useRef<HTMLDivElement>(null)
  const fit = vertical ? (isRtl ? FIT_DOWN_RTL : FIT_DOWN) : FIT
  const fitRef = useRef(fit)
  fitRef.current = fit
  useEffect(() => {
    if (fitted.current === shape) return
    fitted.current = shape
    const id = requestAnimationFrame(() => void flow.fitView({ ...fitRef.current, duration: 200 }))
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
      frame = requestAnimationFrame(() => void flow.fitView({ ...fitRef.current, duration: 200 }))
    })
    ro.observe(el)
    return () => {
      ro.disconnect()
      cancelAnimationFrame(frame)
    }
  }, [flow])

  return (
    <CanvasCtx.Provider value={{ rtl, vertical, readOnly, onSelect, count: actions.length }}>
      <div ref={box} dir="ltr" className="size-full" data-rule-canvas>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          nodesConnectable={false}
          nodesDraggable={false}
          elementsSelectable
          deleteKeyCode={null}
          onNodeClick={(_, n) => {
            if (n.id === "trigger") onSelect("trigger")
            else if (n.id === "conditions") onSelect("conditions")
            else if (n.type === "rule") onSelect((n.data as NodeData).index)
          }}
          onPaneClick={() => onSelect(null)}
          minZoom={0.2}
          maxZoom={2}
          fitView
          fitViewOptions={fit}
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
