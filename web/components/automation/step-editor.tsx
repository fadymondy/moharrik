"use client"

/*
The visual step editor (MH-400): a form per command instead of a JSON textarea.

Each step is a row: a drag handle, its number, the command, the fields that
command takes, and "carry on if this fails". Rows reorder by dragging (dnd-kit,
the stack the registry's sortable lists use) or with the move buttons, which
are also what a keyboard or a screen reader uses.

The field set per command mirrors the extension's driver (extension/driver.js):
a field the driver does not read is not offered, and an argument the form does
not know — written by hand or over MCP — is kept as it was, not dropped.

Selector fields have a Pick button: the paired browser opens the page and the
person clicks the element (picker.tsx).
*/

import { useMemo, type ReactNode } from "react"
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core"
import { SortableContext, arrayMove, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable"
import { CSS } from "@dnd-kit/utilities"
import { ArrowDownIcon, ArrowUpIcon, GripVerticalIcon, PlusIcon, Trash2Icon } from "lucide-react"

import { PickSelect } from "@/components/crm/shared"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import type { WorkflowStep } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

export type EditorStep = WorkflowStep & { key: string; args: Record<string, unknown> }

type FieldKind = "url" | "selector" | "text" | "number" | "bool"
type FieldDef = { name: string; kind: FieldKind; required?: boolean }

/** The canonical commands, and the fields the driver reads for each. */
export const COMMANDS: Record<string, FieldDef[]> = {
  navigate: [{ name: "url", kind: "url", required: true }],
  click: [{ name: "selector", kind: "selector", required: true }],
  type: [
    { name: "selector", kind: "selector", required: true },
    { name: "text", kind: "text" },
  ],
  press: [
    { name: "key", kind: "text", required: true },
    { name: "selector", kind: "selector" },
  ],
  select: [
    { name: "selector", kind: "selector", required: true },
    { name: "value", kind: "text", required: true },
  ],
  scroll: [
    { name: "dy", kind: "number" },
    { name: "selector", kind: "selector" },
  ],
  wait: [
    { name: "ms", kind: "number" },
    { name: "selector", kind: "selector" },
  ],
  extract: [
    { name: "selector", kind: "selector", required: true },
    { name: "attribute", kind: "text" },
    { name: "all", kind: "bool" },
  ],
  close: [],
}

/** What the driver also accepts, and the form shows as its canonical name. */
const ALIASES: Record<string, string> = { goto: "navigate", open: "navigate", fill: "type", key: "press", read: "extract" }

export const canonical = (command: string) => {
  const c = command.trim().toLowerCase()
  return ALIASES[c] ?? c
}

let seq = 0
export const newKey = () => `s${Date.now().toString(36)}${(seq++).toString(36)}`

export function toEditorSteps(steps: WorkflowStep[]): EditorStep[] {
  return steps.map((s) => ({ ...s, key: newKey(), args: { ...(s.args ?? {}) } }))
}

/** Back to what the API stores: no client keys, no empty optional arguments. */
export function fromEditorSteps(steps: EditorStep[]): WorkflowStep[] {
  return steps.map(({ key: _key, command, args, continue_on_error }) => {
    const clean: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(args)) {
      if (v === "" || v === undefined || v === null || v === false) continue
      clean[k] = v
    }
    return { command, args: clean, ...(continue_on_error ? { continue_on_error: true } : {}) }
  })
}

/** The page a step acts on: the last navigate before it. */
export function pageFor(steps: EditorStep[], index: number): string {
  for (let i = index; i >= 0; i--) {
    if (canonical(steps[i].command) === "navigate") return String(steps[i].args.url ?? "")
  }
  return ""
}

export type StepStatus = "pending" | "running" | "ok" | "failed" | "skipped"

export function StepEditor({
  steps,
  onChange,
  statuses,
  renderPick,
}: {
  steps: EditorStep[]
  onChange: (next: EditorStep[]) => void
  /** Live progress of a test run, by position. */
  statuses?: Record<number, { status: StepStatus; summary?: string; error?: string }>
  /** The Pick control for a selector field. */
  renderPick?: (index: number, field: string) => ReactNode
}) {
  const { t } = useTranslations()
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )

  const update = (index: number, patch: Partial<EditorStep>) =>
    onChange(steps.map((s, i) => (i === index ? { ...s, ...patch } : s)))
  const move = (from: number, to: number) => {
    if (to < 0 || to >= steps.length) return
    onChange(arrayMove(steps, from, to))
  }
  const onDragEnd = (e: DragEndEvent) => {
    if (!e.over || e.active.id === e.over.id) return
    const from = steps.findIndex((s) => s.key === e.active.id)
    const to = steps.findIndex((s) => s.key === e.over!.id)
    if (from >= 0 && to >= 0) move(from, to)
  }

  const commandOptions = useMemo(
    () => Object.keys(COMMANDS).map((c) => ({ value: c, label: t(`automation.cmd.${c}`) })),
    [t],
  )

  return (
    <div data-step-editor>
      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={onDragEnd}>
        <SortableContext items={steps.map((s) => s.key)} strategy={verticalListSortingStrategy}>
          <ol className="divide-y divide-line border-y border-line">
            {steps.map((step, i) => (
              <StepRow
                key={step.key}
                step={step}
                index={i}
                count={steps.length}
                commandOptions={commandOptions}
                status={statuses?.[i]}
                onChange={(patch) => update(i, patch)}
                onMove={(to) => move(i, to)}
                onRemove={() => onChange(steps.filter((_, j) => j !== i))}
                renderPick={renderPick}
              />
            ))}
          </ol>
        </SortableContext>
      </DndContext>
      <div className="px-6 pt-3">
        <Button
          type="button"
          variant="outline"
          size="sm"
          data-add-step
          onClick={() => onChange([...steps, { key: newKey(), command: steps.length === 0 ? "navigate" : "click", args: {} }])}
        >
          <PlusIcon />
          {t("automation.editor.addStep")}
        </Button>
      </div>
    </div>
  )
}

function StepRow({
  step,
  index,
  count,
  commandOptions,
  status,
  onChange,
  onMove,
  onRemove,
  renderPick,
}: {
  step: EditorStep
  index: number
  count: number
  commandOptions: { value: string; label: string }[]
  status?: { status: StepStatus; summary?: string; error?: string }
  onChange: (patch: Partial<EditorStep>) => void
  onMove: (to: number) => void
  onRemove: () => void
  renderPick?: (index: number, field: string) => ReactNode
}) {
  const { t } = useTranslations()
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({ id: step.key })
  const command = canonical(step.command)
  const fields = COMMANDS[command]
  const known = !!fields
  const options = known ? commandOptions : [{ value: step.command, label: step.command }, ...commandOptions]
  const id = (f: string) => `step-${step.key}-${f}`

  const setArg = (name: string, value: unknown) => onChange({ args: { ...step.args, [name]: value } })

  const tone: Record<StepStatus, "default" | "secondary" | "outline"> = {
    pending: "outline",
    running: "secondary",
    ok: "default",
    failed: "outline",
    skipped: "outline",
  }

  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={`relative bg-background px-6 py-4 ${isDragging ? "z-10 shadow-lg" : ""} ${status?.status === "running" ? "bg-grid-card" : ""}`}
      data-step-row={index}
      data-step-command={command}
      data-step-status={status?.status ?? ""}
    >
      <div className="flex flex-wrap items-start gap-3">
        <div className="flex items-center gap-1 pt-1">
          <button
            type="button"
            ref={setActivatorNodeRef}
            {...attributes}
            {...listeners}
            className="cursor-grab touch-none rounded p-1 text-grid-muted hover:text-grid-fg focus-visible:outline-2 focus-visible:outline-ring active:cursor-grabbing"
            aria-label={t("automation.editor.dragStep", { n: index + 1 })}
            data-drag-handle
          >
            <GripVerticalIcon className="size-4" />
          </button>
          <span className="w-6 text-sm tabular-nums text-grid-muted">{index + 1}.</span>
        </div>

        <div className="min-w-0 flex-1 space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <div className="w-44">
              <PickSelect
                id={id("command")}
                value={known ? command : step.command}
                onChange={(c) => onChange({ command: c })}
                options={options}
              />
            </div>
            {status ? (
              <Badge variant={tone[status.status]} data-step-badge>
                {t(`automation.editor.status.${status.status}`)}
              </Badge>
            ) : null}
            {!known ? <span className="text-xs text-grid-danger-text">{t("automation.editor.unknownCommand")}</span> : null}
          </div>

          {fields && fields.length > 0 ? (
            <div className="grid gap-3 sm:grid-cols-2">
              {fields.map((f) => (
                <FieldInput
                  key={f.name}
                  id={id(f.name)}
                  def={f}
                  value={step.args[f.name]}
                  onChange={(v) => setArg(f.name, v)}
                  pick={f.kind === "selector" && renderPick ? renderPick(index, f.name) : null}
                />
              ))}
            </div>
          ) : null}

          <label className="flex w-fit items-center gap-2 text-xs text-grid-muted">
            <Checkbox checked={!!step.continue_on_error} onCheckedChange={(v) => onChange({ continue_on_error: !!v })} />
            {t("automation.editor.continueOnError")}
          </label>

          {status?.error ? <p className="text-xs text-grid-danger-text">{status.error}</p> : null}
          {status?.status === "ok" && status.summary ? <p className="text-xs text-grid-muted">{status.summary}</p> : null}
        </div>

        <div className="flex items-center gap-1">
          <Button type="button" variant="ghost" size="icon-sm" disabled={index === 0} onClick={() => onMove(index - 1)} aria-label={t("automation.editor.moveUp")}>
            <ArrowUpIcon />
          </Button>
          <Button type="button" variant="ghost" size="icon-sm" disabled={index === count - 1} onClick={() => onMove(index + 1)} aria-label={t("automation.editor.moveDown")}>
            <ArrowDownIcon />
          </Button>
          <Button type="button" variant="ghost" size="icon-sm" className="text-grid-danger-text" onClick={onRemove} aria-label={t("automation.editor.removeStep")}>
            <Trash2Icon />
          </Button>
        </div>
      </div>
    </li>
  )
}

function FieldInput({
  id,
  def,
  value,
  onChange,
  pick,
}: {
  id: string
  def: FieldDef
  value: unknown
  onChange: (v: unknown) => void
  pick: ReactNode
}) {
  const { t } = useTranslations()
  const label = (
    <Label htmlFor={id} className="text-xs">
      {t(`automation.field.${def.name}`)}
      {def.required ? null : <span className="text-grid-muted"> ({t("common.optional")})</span>}
    </Label>
  )
  if (def.kind === "bool") {
    return (
      <label className="flex items-center gap-2 self-end pb-2 text-sm">
        <Checkbox id={id} checked={!!value} onCheckedChange={(v) => onChange(!!v)} />
        {t(`automation.field.${def.name}`)}
      </label>
    )
  }
  const ltr = def.kind !== "text" || def.name === "key" || def.name === "attribute"
  return (
    // A selector or an address is read whole, so it gets the whole row.
    <div className={`min-w-0 space-y-1.5 ${def.kind === "selector" || def.kind === "url" ? "sm:col-span-2" : ""}`} data-field={def.name}>
      {label}
      <div className="flex items-center gap-2">
        <Input
          id={id}
          dir={ltr ? "ltr" : undefined}
          className={def.kind === "selector" ? "font-mono text-xs" : undefined}
          inputMode={def.kind === "number" ? "numeric" : undefined}
          value={value == null ? "" : String(value)}
          placeholder={t(`automation.placeholder.${def.name}`)}
          onChange={(e) => {
            const raw = e.target.value
            if (def.kind === "number") {
              onChange(raw === "" ? "" : Number.isFinite(Number(raw)) ? Number(raw) : raw)
              return
            }
            onChange(raw)
          }}
        />
        {pick}
      </div>
    </div>
  )
}
