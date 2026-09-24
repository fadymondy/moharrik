/*
The workflow canvas's model (MH-480).

A workflow IS its steps — the JSON array a browser runs. The canvas is only a
way of looking at and editing that array, so everything here is a pure
function of the steps:

  - the graph is the trigger, then one node per step in step order, each joined
    to the next. There is no other wiring to store, and none to get wrong;
  - a node's kind is read from its command (and, for extract, from the shape of
    its arguments), never stored beside it;
  - where the nodes sit is UI metadata kept apart from the steps (the layout),
    tagged with the signature of the commands it was drawn for. A layout whose
    signature no longer matches is ignored and the graph laid out afresh.

Positions are kept in LTR canvas units. An RTL page mirrors them for display
and un-mirrors what the person drags, so one stored layout reads the right way
in both directions.
*/

import type { LucideIcon } from "lucide-react"
import {
  ArrowDownUpIcon,
  BracesIcon,
  CameraIcon,
  CircleHelpIcon,
  CornerDownLeftIcon,
  GlobeIcon,
  HourglassIcon,
  KeyboardIcon,
  ListChecksIcon,
  LogOutIcon,
  MousePointerClickIcon,
  ScanSearchIcon,
  SquareXIcon,
  Table2Icon,
  TextSelectIcon,
  TimerIcon,
  UploadIcon,
} from "lucide-react"

import type { RunStep, WorkflowStep } from "@/lib/api"

export type Category = "browser" | "interact" | "wait" | "data"

export type NodeKind =
  | "navigate"
  | "click"
  | "type"
  | "press"
  | "select"
  | "scroll"
  | "upload"
  | "wait"
  | "wait_for"
  | "extract"
  | "extract_data"
  | "screenshot"
  | "eval_readonly"
  | "release"
  | "close"
  | "unknown"

export type FieldKind = "url" | "selector" | "text" | "textarea" | "number" | "bool" | "fields"
export type FieldDef = { name: string; kind: FieldKind; required?: boolean }

export type NodeSpec = {
  kind: NodeKind
  /** The command a new node of this kind is written with. */
  command: string
  category: Category
  icon: LucideIcon
  fields: FieldDef[]
  /** The arguments a new node starts with. */
  defaults: Record<string, unknown>
}

/*
The field set per kind mirrors the extension's driver (extension/driver.js and
driver/extract.js): a field the driver does not read is not offered. Anything
else a step carries — written by hand, over MCP, or by the recorder — is kept
exactly as it was and stays editable as JSON in the node's Settings.
*/
export const SPECS: Record<Exclude<NodeKind, "unknown">, NodeSpec> = {
  navigate: {
    kind: "navigate",
    command: "navigate",
    category: "browser",
    icon: GlobeIcon,
    fields: [{ name: "url", kind: "url", required: true }],
    defaults: { url: "https://" },
  },
  release: {
    kind: "release",
    command: "release",
    category: "browser",
    icon: LogOutIcon,
    fields: [],
    defaults: {},
  },
  close: {
    kind: "close",
    command: "close",
    category: "browser",
    icon: SquareXIcon,
    fields: [],
    defaults: {},
  },

  click: {
    kind: "click",
    command: "click",
    category: "interact",
    icon: MousePointerClickIcon,
    fields: [{ name: "selector", kind: "selector", required: true }],
    defaults: {},
  },
  type: {
    kind: "type",
    command: "type",
    category: "interact",
    icon: KeyboardIcon,
    fields: [
      { name: "selector", kind: "selector", required: true },
      { name: "text", kind: "text" },
    ],
    defaults: {},
  },
  press: {
    kind: "press",
    command: "press",
    category: "interact",
    icon: CornerDownLeftIcon,
    fields: [
      { name: "key", kind: "text", required: true },
      { name: "selector", kind: "selector" },
    ],
    defaults: { key: "Enter" },
  },
  select: {
    kind: "select",
    command: "select",
    category: "interact",
    icon: ListChecksIcon,
    fields: [
      { name: "selector", kind: "selector", required: true },
      { name: "value", kind: "text", required: true },
    ],
    defaults: {},
  },
  scroll: {
    kind: "scroll",
    command: "scroll",
    category: "interact",
    icon: ArrowDownUpIcon,
    fields: [
      { name: "dy", kind: "number" },
      { name: "selector", kind: "selector" },
    ],
    defaults: { dy: 600 },
  },
  upload: {
    kind: "upload",
    command: "upload",
    category: "interact",
    icon: UploadIcon,
    fields: [
      { name: "selector", kind: "selector" },
      { name: "file", kind: "text", required: true },
    ],
    defaults: {},
  },

  wait: {
    kind: "wait",
    command: "wait",
    category: "wait",
    icon: TimerIcon,
    fields: [
      { name: "ms", kind: "number" },
      { name: "selector", kind: "selector" },
    ],
    defaults: { ms: 1000 },
  },
  wait_for: {
    kind: "wait_for",
    command: "wait_for",
    category: "wait",
    icon: HourglassIcon,
    fields: [
      { name: "selector", kind: "selector", required: true },
      { name: "timeout_ms", kind: "number" },
    ],
    defaults: {},
  },

  extract: {
    kind: "extract",
    command: "extract",
    category: "data",
    icon: TextSelectIcon,
    fields: [
      { name: "selector", kind: "selector", required: true },
      { name: "attribute", kind: "text" },
      { name: "all", kind: "bool" },
    ],
    defaults: {},
  },
  extract_data: {
    kind: "extract_data",
    command: "extract",
    category: "data",
    icon: Table2Icon,
    fields: [
      { name: "list", kind: "selector" },
      { name: "fields", kind: "fields", required: true },
      { name: "limit", kind: "number" },
    ],
    defaults: { fields: { title: "h1" } },
  },
  screenshot: {
    kind: "screenshot",
    command: "screenshot",
    category: "data",
    icon: CameraIcon,
    fields: [{ name: "full_page", kind: "bool" }],
    defaults: {},
  },
  eval_readonly: {
    kind: "eval_readonly",
    command: "eval_readonly",
    category: "data",
    icon: BracesIcon,
    fields: [
      { name: "expression", kind: "textarea", required: true },
      { name: "timeout_ms", kind: "number" },
    ],
    defaults: {},
  },
}

export const CATEGORIES: { id: Category; kinds: Exclude<NodeKind, "unknown">[] }[] = [
  { id: "browser", kinds: ["navigate", "release", "close"] },
  { id: "interact", kinds: ["click", "type", "press", "select", "scroll", "upload"] },
  { id: "wait", kinds: ["wait", "wait_for"] },
  { id: "data", kinds: ["extract", "extract_data", "screenshot", "eval_readonly"] },
]

/** What the driver also accepts, and the canvas shows under its canonical name. */
const ALIASES: Record<string, string> = {
  goto: "navigate",
  open: "navigate",
  fill: "type",
  key: "press",
  read: "extract",
}

export const canonicalCommand = (command: string) => {
  const c = command.trim().toLowerCase()
  return ALIASES[c] ?? c
}

/** A step's node kind, read from its command and — for extract — its arguments. */
export function kindOf(step: WorkflowStep): NodeKind {
  const c = canonicalCommand(step.command)
  if (c === "extract") {
    const f = step.args?.fields
    return f && typeof f === "object" ? "extract_data" : "extract"
  }
  return c in SPECS ? (c as NodeKind) : "unknown"
}

export const specOf = (kind: NodeKind): NodeSpec =>
  kind === "unknown"
    ? { kind, command: "", category: "browser", icon: CircleHelpIcon, fields: [], defaults: {} }
    : SPECS[kind]

/** A new step of a kind, ready to edit. */
export function newStep(kind: Exclude<NodeKind, "unknown">): WorkflowStep {
  const spec = SPECS[kind]
  return { command: spec.command, args: structuredClone(spec.defaults) }
}

/** The one line under a node: the address, the element, the key — whatever says most. */
export function subtitleOf(step: WorkflowStep): string {
  const a = step.args ?? {}
  const pick = (...keys: string[]) => {
    for (const k of keys) {
      const v = a[k]
      if (v !== undefined && v !== null && v !== "") return String(v)
    }
    return ""
  }
  switch (kindOf(step)) {
    case "navigate":
      return pick("url").replace(/^https?:\/\//, "")
    case "press":
      return pick("key")
    case "type":
      return pick("selector")
    case "wait":
      return a.ms ? `${a.ms} ms` : pick("selector")
    case "scroll":
      return pick("selector") || (a.dy ? `${a.dy}px` : "")
    case "extract_data": {
      const n =
        a.fields && typeof a.fields === "object" ? Object.keys(a.fields as object).length : 0
      return pick("list") || (n ? Object.keys(a.fields as object).join(", ") : "")
    }
    case "eval_readonly":
      return pick("expression")
    case "upload":
      return pick("file", "selector")
    case "unknown":
      return step.command
    default:
      return pick("selector", "value", "url")
  }
}

/** Whether a step lacks something it cannot run without. */
export function missingField(step: WorkflowStep): string | null {
  const kind = kindOf(step)
  if (kind === "unknown") return null
  for (const f of SPECS[kind].fields) {
    if (!f.required) continue
    const v = step.args?.[f.name]
    if (f.kind === "fields") {
      if (!v || typeof v !== "object" || Object.keys(v as object).length === 0) return f.name
      continue
    }
    if (v === undefined || v === null || String(v).trim() === "" || String(v).trim() === "https://")
      return f.name
  }
  return null
}

// ── layout ───────────────────────────────────────────────────────────────

export type Point = { x: number; y: number }
export type Layout = { trigger: Point; nodes: Point[] }

/** A node's box, in canvas units. Every node is this size so mirroring is exact. */
export const NODE_SIZE = 96
export const GAP = 208

/** The commands a layout was drawn for. A layout for other commands is stale. */
export const signatureOf = (steps: WorkflowStep[]) =>
  steps.map((s) => canonicalCommand(s.command)).join("|")

/** The graph laid out in a line: across on a wide screen, down on a narrow one. */
export function autoLayout(count: number, vertical = false): Layout {
  const at = (i: number): Point => (vertical ? { x: 0, y: i * (GAP - 40) } : { x: i * GAP, y: 0 })
  return { trigger: at(0), nodes: Array.from({ length: count }, (_, i) => at(i + 1)) }
}

/**
 * The layout to draw: the saved one when it was drawn for these very steps,
 * otherwise a fresh line. A narrow screen always gets the fresh vertical line —
 * a stored layout is drawn for a wide canvas.
 */
export function resolveLayout(
  steps: WorkflowStep[],
  saved: { layout?: { trigger?: Point; nodes: Point[] }; signature: string } | undefined,
  vertical: boolean,
): Layout {
  if (vertical) return autoLayout(steps.length, true)
  if (
    saved &&
    saved.signature &&
    saved.signature === signatureOf(steps) &&
    saved.layout &&
    saved.layout.nodes.length === steps.length
  ) {
    return {
      trigger: saved.layout.trigger ?? {
        x: (saved.layout.nodes[0]?.x ?? GAP) - GAP,
        y: saved.layout.nodes[0]?.y ?? 0,
      },
      nodes: saved.layout.nodes,
    }
  }
  return autoLayout(steps.length)
}

/** Room for a node inserted at index: it takes the slot, and everything after it moves along. */
export function insertAt(layout: Layout, index: number, vertical = false): Layout {
  const before = index === 0 ? layout.trigger : layout.nodes[index - 1]
  const at = vertical
    ? { x: before.x, y: before.y + (GAP - 40) }
    : { x: before.x + GAP, y: before.y }
  const shifted = layout.nodes.map((p, i) =>
    i >= index ? (vertical ? { x: p.x, y: p.y + (GAP - 40) } : { x: p.x + GAP, y: p.y }) : p,
  )
  shifted.splice(index, 0, at)
  return { trigger: layout.trigger, nodes: shifted }
}

export function removeAt(layout: Layout, index: number): Layout {
  return { trigger: layout.trigger, nodes: layout.nodes.filter((_, i) => i !== index) }
}

/** Swap two nodes' steps but not their places, so the line keeps its shape. */
export function moveStep<T>(list: T[], from: number, to: number): T[] {
  const next = [...list]
  const [item] = next.splice(from, 1)
  next.splice(to, 0, item)
  return next
}

/** Mirror a point for an RTL canvas (and back — it is its own inverse). */
export const mirror = (p: Point): Point => ({ x: -p.x - NODE_SIZE, y: p.y })

// ── run results ──────────────────────────────────────────────────────────

export type NodeRunState = "pending" | "running" | "ok" | "failed" | "skipped"

export type NodeRun = {
  state: NodeRunState
  step?: RunStep
  /** How many items the step produced, when it produced a list or a record. */
  items?: number | null
}

/** A step's result as the browser sent it, parsed if it is JSON. */
export function parseResult(result: string): unknown {
  if (!result) return null
  try {
    return JSON.parse(result)
  } catch {
    return result
  }
}

/** Rows to show in a table, if the result has any: a list, a record, or values. */
export function rowsOf(data: unknown): Record<string, unknown>[] | null {
  if (!data || typeof data !== "object") return null
  const d = data as Record<string, unknown>
  if (Array.isArray(d.rows))
    return d.rows.filter((r) => r && typeof r === "object") as Record<string, unknown>[]
  if (d.fields && typeof d.fields === "object" && !Array.isArray(d.fields))
    return [d.fields as Record<string, unknown>]
  if (Array.isArray(d.values)) return d.values.map((value) => ({ value }))
  return null
}

/** The item count n8n-style edges show: rows, values, or a single record. */
export function itemsOf(result: string): number | null {
  const rows = rowsOf(parseResult(result))
  return rows ? rows.length : null
}

/**
 * The run's steps as node states, by position. A run that is going marks its
 * first pending step as the one running, since the server records a step only
 * once it has finished.
 */
export function nodeRuns(steps: RunStep[], runStatus: string): Record<number, NodeRun> {
  const out: Record<number, NodeRun> = {}
  let runningShown = false
  for (const s of steps) {
    let state: NodeRunState = s.status
    if (s.status === "pending" && runStatus === "running" && !runningShown) {
      state = "running"
      runningShown = true
    }
    out[s.position] = { state, step: s, items: s.status === "ok" ? itemsOf(s.result) : null }
  }
  return out
}

/** A run's own steps, as workflow steps — how a past execution is drawn. */
export function stepsOfRun(steps: RunStep[]): WorkflowStep[] {
  return steps.map((s) => {
    let args: Record<string, unknown> = {}
    try {
      const parsed = JSON.parse(s.args || "{}")
      if (parsed && typeof parsed === "object") args = parsed
    } catch {
      /* kept empty: the node still shows its command */
    }
    return { command: s.command, args }
  })
}
