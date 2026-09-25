"use client"

/*
The side panel (MH-480): what n8n opens when a node is clicked.

A step's panel has three tabs:
  Parameters — the fields the driver reads for this kind of step, each selector
               with a Pick button that has the connected browser point at the
               element;
  Settings   — what happens when the step fails, and the step's arguments as
               JSON, so anything the form does not model (a click's button, a
               select by label, a recorded extra) is still editable rather than
               invisible;
  Output     — what this step brought back in the run being shown.

The trigger's panel says how the workflow starts — by hand, on a schedule, over
MCP — and holds its parameters: the {{placeholders}} a run is asked for, and
the values a test run from the canvas uses.

Every edit goes straight back to the caller as a new step; the steps stay the
only copy of the workflow.
*/

import Link from "next/link"
import { useEffect, useMemo, useState, type ReactNode } from "react"
import {
  CalendarClockIcon,
  CircleHelpIcon,
  GlobeIcon,
  HandIcon,
  PlayIcon,
  PlusIcon,
  Trash2Icon,
  WorkflowIcon,
  XIcon,
  ZapIcon,
} from "lucide-react"

import { PickControl } from "@/components/automation/pick-control"
import { placeholdersIn, validParamName } from "@/components/automation/params-editor"
import { PickSelect } from "@/components/crm/shared"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"
import { useWorkspace } from "@/components/workspace/workspace-context"
import type { AutomationSchedule, RunStep, WorkflowParam, WorkflowStep } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

import { NodeOutput } from "./node-output"
import { kindOf, specOf, type FieldDef } from "./model"

export type PanelTab = "parameters" | "settings" | "output"

// ── the frame every panel shares ─────────────────────────────────────────

function PanelHeader({
  icon,
  title,
  sub,
  actions,
  onClose,
}: {
  icon: ReactNode
  title: ReactNode
  sub?: ReactNode
  actions?: ReactNode
  onClose: () => void
}) {
  const { t } = useTranslations()
  return (
    <div className="flex items-start gap-3 border-b border-line px-4 py-3">
      <span className="flex size-9 shrink-0 items-center justify-center rounded-md border border-line bg-grid-card">
        {icon}
      </span>
      <div className="min-w-0 flex-1">
        <h2 className="truncate text-base font-medium">{title}</h2>
        {sub ? <p className="truncate text-xs text-grid-muted">{sub}</p> : null}
      </div>
      <div className="flex items-center gap-1">
        {actions}
        <Button
          variant="ghost"
          size="icon-sm"
          onClick={onClose}
          aria-label={t("canvas.close")}
          data-panel-close
        >
          <XIcon />
        </Button>
      </div>
    </div>
  )
}

// ── a step ───────────────────────────────────────────────────────────────

export function StepPanel({
  step,
  index,
  readOnly,
  tab,
  onTab,
  onChange,
  onClose,
  onDelete,
  onRunTo,
  runBlocked,
  output,
  runId,
  live,
  agentId,
  pickBlocked,
  pageUrl,
}: {
  step: WorkflowStep
  index: number
  readOnly?: boolean
  tab: PanelTab
  onTab: (t: PanelTab) => void
  onChange: (next: WorkflowStep) => void
  onClose: () => void
  onDelete?: () => void
  onRunTo?: () => void
  runBlocked?: string
  output?: RunStep
  runId?: string
  live?: boolean
  agentId: string
  pickBlocked?: string
  pageUrl: string
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const kind = kindOf(step)
  const spec = specOf(kind)
  const Icon = spec.icon
  const args = step.args ?? {}

  // Setting a field to nothing removes it, so an emptied optional field does
  // not travel to the browser as "".
  const setArg = (name: string, value: unknown) => {
    const next = { ...args }
    if (value === "" || value === undefined || value === null || value === false) delete next[name]
    else next[name] = value
    onChange({ ...step, args: next })
  }

  return (
    <div className="flex h-full flex-col" data-step-panel={index} data-step-kind={kind}>
      <PanelHeader
        icon={<Icon className="size-4.5" />}
        title={kind === "unknown" ? step.command : t(`canvas.kind.${kind}`)}
        sub={t("canvas.stepN", { n: index + 1 })}
        onClose={onClose}
        actions={
          readOnly ? null : (
            <>
              {onRunTo ? (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  onClick={onRunTo}
                  disabled={!!runBlocked}
                  title={runBlocked || t("canvas.runTo")}
                  aria-label={t("canvas.runTo")}
                >
                  <PlayIcon />
                </Button>
              ) : null}
              {onDelete ? (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-grid-danger-text"
                  onClick={onDelete}
                  aria-label={t("canvas.delete")}
                >
                  <Trash2Icon />
                </Button>
              ) : null}
            </>
          )
        }
      />
      <Tabs
        value={tab}
        onValueChange={(v) => onTab(v as PanelTab)}
        className="min-h-0 flex-1 gap-0"
      >
        <TabsList variant="line" className="w-full justify-start border-b border-line px-2">
          <TabsTrigger value="parameters" className="flex-none" data-panel-tab="parameters">
            {t("canvas.tab.parameters")}
          </TabsTrigger>
          <TabsTrigger value="settings" className="flex-none" data-panel-tab="settings">
            {t("canvas.tab.settings")}
          </TabsTrigger>
          <TabsTrigger value="output" className="flex-none" data-panel-tab="output">
            {t("canvas.tab.output")}
            {output && output.status !== "pending" ? (
              <span
                className={`size-1.5 rounded-full ${output.status === "ok" ? "bg-grid-ok" : output.status === "failed" ? "bg-grid-danger" : "bg-grid-muted"}`}
                aria-hidden
              />
            ) : null}
          </TabsTrigger>
        </TabsList>

        <TabsContent value="parameters" className="min-h-0 overflow-y-auto">
          <div className="space-y-4 px-4 py-4">
            <p className="text-sm text-grid-muted">
              {kind === "unknown"
                ? t("canvas.unknownHint", { command: step.command })
                : t(`canvas.desc.${kind}`)}
            </p>
            {spec.fields.length === 0 && kind !== "unknown" ? (
              <p className="text-sm text-grid-muted">{t("canvas.noFields")}</p>
            ) : null}
            {spec.fields.map((f) =>
              f.kind === "fields" ? (
                <FieldsMap
                  key={f.name}
                  value={(args.fields as Record<string, string>) ?? {}}
                  readOnly={readOnly}
                  onChange={(v) => setArg("fields", v)}
                  pick={(onPicked, key) =>
                    readOnly ? null : (
                      <PickControl
                        id={`f-${index}-${key}`}
                        agentId={agentId}
                        unavailable={pickBlocked}
                        pageUrl={pageUrl}
                        onPicked={(sel) => onPicked(sel)}
                      />
                    )
                  }
                />
              ) : (
                <FieldInput
                  key={`${index}-${f.name}`}
                  id={`node-${index}-${f.name}`}
                  def={f}
                  value={args[f.name]}
                  readOnly={readOnly}
                  onChange={(v) => setArg(f.name, v)}
                  pick={
                    f.kind === "selector" && !readOnly ? (
                      <PickControl
                        id={`p-${index}-${f.name}`}
                        agentId={agentId}
                        unavailable={pickBlocked}
                        pageUrl={pageUrl}
                        onPicked={(sel) => setArg(f.name, sel)}
                      />
                    ) : null
                  }
                />
              ),
            )}
            {kind === "extract_data" ? (
              <p className="text-xs text-grid-muted">{t("canvas.fieldsHint")}</p>
            ) : null}
          </div>
        </TabsContent>

        <TabsContent value="settings" className="min-h-0 overflow-y-auto">
          <div className="space-y-5 px-4 py-4">
            <Field>
              <FieldLabel htmlFor={`onerr-${index}`}>{t("canvas.onError")}</FieldLabel>
              {readOnly ? (
                <p className="text-sm">
                  {step.continue_on_error ? t("canvas.onError.continue") : t("canvas.onError.stop")}
                </p>
              ) : (
                <PickSelect
                  id={`onerr-${index}`}
                  value={step.continue_on_error ? "continue" : "stop"}
                  onChange={(v) => {
                    const next = { ...step }
                    if (v === "continue") next.continue_on_error = true
                    else delete next.continue_on_error
                    onChange(next)
                  }}
                  options={[
                    { value: "stop", label: t("canvas.onError.stop") },
                    { value: "continue", label: t("canvas.onError.continue") },
                  ]}
                />
              )}
              <FieldDescription>{t("canvas.onErrorHint")}</FieldDescription>
            </Field>
            <ArgsJson
              key={JSON.stringify(args)}
              args={args}
              readOnly={readOnly}
              onChange={(a) => onChange({ ...step, args: a })}
            />
            <p className="text-xs text-grid-muted">
              {t("canvas.command")}{" "}
              <span dir="ltr" className="ltr-isolate font-mono">
                {step.command}
              </span>
            </p>
          </div>
        </TabsContent>

        <TabsContent value="output" className="min-h-0 overflow-y-auto">
          <NodeOutput step={output} runId={runId} workspaceId={ws.id} live={live} />
        </TabsContent>
      </Tabs>
    </div>
  )
}

function FieldInput({
  id,
  def,
  value,
  readOnly,
  onChange,
  pick,
}: {
  id: string
  def: FieldDef
  value: unknown
  readOnly?: boolean
  onChange: (v: unknown) => void
  pick: ReactNode
}) {
  const { t } = useTranslations()
  if (def.kind === "bool") {
    return (
      <label className="flex w-fit items-center gap-2 text-sm" data-field={def.name}>
        <Checkbox
          id={id}
          checked={!!value}
          disabled={readOnly}
          onCheckedChange={(v) => onChange(!!v)}
        />
        {t(`automation.field.${def.name}`)}
      </label>
    )
  }
  const ltr =
    def.kind !== "text" && def.kind !== "textarea"
      ? true
      : def.name === "key" || def.name === "file"
  const label = (
    <Label htmlFor={id} className="text-xs">
      {t(`automation.field.${def.name}`)}
      {def.required ? null : <span className="text-grid-muted"> ({t("common.optional")})</span>}
    </Label>
  )
  if (def.kind === "textarea") {
    return (
      <div className="space-y-1.5" data-field={def.name}>
        {label}
        <Textarea
          id={id}
          dir="ltr"
          rows={4}
          readOnly={readOnly}
          className="font-mono text-xs"
          value={value == null ? "" : String(value)}
          placeholder={t(`automation.placeholder.${def.name}`)}
          onChange={(e) => onChange(e.target.value)}
        />
      </div>
    )
  }
  return (
    <div className="min-w-0 space-y-1.5" data-field={def.name}>
      {label}
      <div className="flex items-center gap-2">
        <Input
          id={id}
          dir={ltr ? "ltr" : "auto"}
          readOnly={readOnly}
          className={def.kind === "selector" ? "font-mono text-xs" : undefined}
          inputMode={def.kind === "number" ? "numeric" : undefined}
          value={value == null ? "" : String(value)}
          placeholder={t(`automation.placeholder.${def.name}`)}
          onChange={(e) => {
            const raw = e.target.value
            if (def.kind === "number")
              return onChange(raw === "" ? "" : Number.isFinite(Number(raw)) ? Number(raw) : raw)
            onChange(raw)
          }}
        />
        {pick}
      </div>
    </div>
  )
}

/** A structured extract's fields: a name for each value and where it is read from. */
function FieldsMap({
  value,
  readOnly,
  onChange,
  pick,
}: {
  value: Record<string, string>
  readOnly?: boolean
  onChange: (v: Record<string, string>) => void
  pick: (onPicked: (selector: string) => void, key: string) => ReactNode
}) {
  const { t } = useTranslations()
  // Edited as rows so a name can be typed letter by letter without the
  // object's keys colliding half-way.
  const [rows, setRows] = useState(() =>
    Object.entries(value).map(([k, v]) => ({ k, v: String(v ?? "") })),
  )
  useEffect(() => {
    setRows((cur) => {
      const same =
        JSON.stringify(Object.fromEntries(cur.filter((r) => r.k).map((r) => [r.k, r.v]))) ===
        JSON.stringify(value)
      return same ? cur : Object.entries(value).map(([k, v]) => ({ k, v: String(v ?? "") }))
    })
  }, [value])
  const commit = (next: { k: string; v: string }[]) => {
    setRows(next)
    onChange(Object.fromEntries(next.filter((r) => r.k.trim()).map((r) => [r.k.trim(), r.v])))
  }
  return (
    <div className="space-y-2" data-field="fields">
      <p className="text-xs font-medium">{t("automation.field.fields")}</p>
      {rows.map((r, i) => (
        <div key={i} className="flex items-center gap-2" data-fields-row={i}>
          <Input
            dir="ltr"
            className="w-28 shrink-0 font-mono text-xs"
            value={r.k}
            readOnly={readOnly}
            aria-label={t("canvas.fieldName")}
            placeholder={t("canvas.fieldName")}
            onChange={(e) =>
              commit(rows.map((x, j) => (j === i ? { ...x, k: e.target.value } : x)))
            }
          />
          <Input
            dir="ltr"
            className="min-w-0 flex-1 font-mono text-xs"
            value={r.v}
            readOnly={readOnly}
            aria-label={t("canvas.fieldSelector")}
            placeholder="a|@href"
            onChange={(e) =>
              commit(rows.map((x, j) => (j === i ? { ...x, v: e.target.value } : x)))
            }
          />
          {pick((sel) => commit(rows.map((x, j) => (j === i ? { ...x, v: sel } : x))), String(i))}
          {!readOnly ? (
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="text-grid-danger-text"
              aria-label={t("canvas.removeField")}
              onClick={() => commit(rows.filter((_, j) => j !== i))}
            >
              <Trash2Icon />
            </Button>
          ) : null}
        </div>
      ))}
      {!readOnly ? (
        <Button
          type="button"
          variant="outline"
          size="xs"
          onClick={() => setRows([...rows, { k: "", v: "" }])}
          data-add-field
        >
          <PlusIcon />
          {t("canvas.addField")}
        </Button>
      ) : null}
    </div>
  )
}

/** The step's arguments as JSON: the escape hatch for anything the form does not model. */
function ArgsJson({
  args,
  readOnly,
  onChange,
}: {
  args: Record<string, unknown>
  readOnly?: boolean
  onChange: (a: Record<string, unknown>) => void
}) {
  const { t } = useTranslations()
  const [text, setText] = useState(() => JSON.stringify(args, null, 2))
  const [err, setErr] = useState("")
  return (
    <Field>
      <FieldLabel htmlFor="args-json">{t("canvas.argsJson")}</FieldLabel>
      <Textarea
        id="args-json"
        dir="ltr"
        rows={8}
        readOnly={readOnly}
        className="font-mono text-xs"
        value={text}
        aria-invalid={!!err || undefined}
        onChange={(e) => setText(e.target.value)}
        onBlur={() => {
          if (readOnly) return
          try {
            const parsed = JSON.parse(text || "{}")
            if (!parsed || typeof parsed !== "object" || Array.isArray(parsed))
              throw new Error("not an object")
            setErr("")
            if (JSON.stringify(parsed) !== JSON.stringify(args)) onChange(parsed)
          } catch {
            setErr(t("canvas.argsInvalid"))
          }
        }}
        data-args-json
      />
      {err ? (
        <p className="text-xs text-grid-danger-text">{err}</p>
      ) : (
        <FieldDescription>{t("canvas.argsHint")}</FieldDescription>
      )}
    </Field>
  )
}

// ── the trigger ──────────────────────────────────────────────────────────

export function TriggerPanel({
  workflowId,
  steps,
  params,
  values,
  schedules,
  readOnly,
  description,
  onDescription,
  onParams,
  onValues,
  onClose,
  sites = [],
  autoAdd = true,
  canManage = false,
}: {
  /** The sites the workflow needs, and whether a run adds them (MH-517). */
  sites?: string[]
  autoAdd?: boolean
  canManage?: boolean
  workflowId?: string
  description: string
  onDescription: (v: string) => void
  steps: WorkflowStep[]
  params: WorkflowParam[]
  values: Record<string, string>
  schedules: AutomationSchedule[]
  readOnly?: boolean
  onParams: (next: WorkflowParam[]) => void
  onValues: (next: Record<string, string>) => void
  onClose: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const used = useMemo(() => placeholdersIn(steps), [steps])
  const declared = new Set(params.map((p) => p.name))
  const undeclared = used.filter((n) => !declared.has(n))
  const mine = schedules.filter((s) => workflowId && s.workflow_id === workflowId)
  const set = (i: number, patch: Partial<WorkflowParam>) =>
    onParams(params.map((p, j) => (j === i ? { ...p, ...patch } : p)))
  const fields = [
    ...params.filter((p) => p.name),
    ...undeclared.map((name) => ({ name }) as WorkflowParam),
  ]

  return (
    <div className="flex h-full flex-col" data-trigger-panel>
      <PanelHeader
        icon={<ZapIcon className="size-4.5 text-grid-gold" />}
        title={t("canvas.trigger")}
        sub={t("canvas.triggerSub")}
        onClose={onClose}
      />
      <div className="min-h-0 flex-1 space-y-6 overflow-y-auto px-4 py-4">
        <Field>
          <FieldLabel htmlFor="wf-description">
            {t("automation.workflowDescription")}{" "}
            <span className="text-grid-muted">({t("common.optional")})</span>
          </FieldLabel>
          <Textarea
            id="wf-description"
            rows={2}
            maxLength={400}
            readOnly={readOnly}
            value={description}
            onChange={(e) => onDescription(e.target.value)}
            data-workflow-description
          />
        </Field>
        <section className="space-y-2">
          <h3 className="text-sm font-medium">{t("canvas.startsHow")}</h3>
          <ul className="divide-y divide-line rounded-md border border-line">
            <li className="flex gap-3 px-3 py-2.5">
              <HandIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" />
              <div className="text-sm">
                <p className="font-medium">{t("canvas.start.manual")}</p>
                <p className="text-xs text-grid-muted">{t("canvas.start.manualHint")}</p>
              </div>
            </li>
            <li className="flex gap-3 px-3 py-2.5" data-trigger-schedules={mine.length}>
              <CalendarClockIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" />
              <div className="min-w-0 text-sm">
                <p className="font-medium">{t("canvas.start.schedule")}</p>
                {mine.length === 0 ? (
                  <p className="text-xs text-grid-muted">
                    {workflowId ? t("canvas.start.noSchedules") : t("canvas.start.saveFirst")}
                  </p>
                ) : (
                  <ul className="mt-1 space-y-1">
                    {mine.map((s) => (
                      <li key={s.id} className="flex flex-wrap items-center gap-2 text-xs">
                        <span dir="auto">{s.name}</span>
                        <span dir="ltr" className="ltr-isolate font-mono text-grid-muted">
                          {s.cron}
                        </span>
                        <Badge variant={s.enabled ? "default" : "outline"}>
                          {s.enabled ? t("automation.scheduleOn") : t("automation.scheduleOff")}
                        </Badge>
                      </li>
                    ))}
                  </ul>
                )}
                {workflowId ? (
                  <Link
                    className="mt-1 inline-block text-xs underline"
                    href={`/${locale}/w/${ws.slug}/automation/schedules`}
                  >
                    {t("canvas.start.manageSchedules")}
                  </Link>
                ) : null}
              </div>
            </li>
            <li className="flex gap-3 px-3 py-2.5">
              <WorkflowIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" />
              <div className="text-sm">
                <p className="font-medium">{t("canvas.start.mcp")}</p>
                <p className="text-xs text-grid-muted">{t("canvas.start.mcpHint")}</p>
              </div>
            </li>
          </ul>
        </section>

        {/* The sites it needs (MH-517): a run adds whichever its browser does
            not allow yet, under the rights of whoever runs it. */}
        <section className="space-y-2" data-trigger-required-sites={sites.join(",")}>
          <h3 className="text-sm font-medium">{t("canvas.sites.title")}</h3>
          {sites.length === 0 ? (
            <p className="text-xs text-grid-muted">{t("canvas.sites.none")}</p>
          ) : (
            <>
              <ul className="flex flex-wrap gap-1.5">
                {sites.map((h) => (
                  <li key={h}>
                    <Badge variant="outline" className="font-mono">
                      <GlobeIcon />
                      <span dir="ltr">{h}</span>
                    </Badge>
                  </li>
                ))}
              </ul>
              <p className="text-xs text-grid-muted" data-auto-add={autoAdd ? "on" : "off"}>
                {autoAdd
                  ? canManage
                    ? t("canvas.sites.auto")
                    : t("canvas.sites.autoAdmin")
                  : t("canvas.sites.off")}
              </p>
            </>
          )}
          <p className="text-xs text-grid-muted">{t("canvas.sites.values")}</p>
        </section>

        <section className="space-y-3">
          <div>
            <h3 className="text-sm font-medium">{t("automation.params.title")}</h3>
            <p className="text-xs text-grid-muted">{t("canvas.paramsHint")}</p>
          </div>
          {undeclared.length > 0 && !readOnly ? (
            <div
              className="flex flex-wrap items-center gap-2 rounded-md border border-line bg-grid-card px-3 py-2 text-xs"
              data-missing-params
            >
              <CircleHelpIcon className="size-3.5 text-grid-muted" />
              <span className="text-grid-muted">
                {t("automation.params.undeclared", { names: undeclared.join(", ") })}
              </span>
              <Button
                type="button"
                size="xs"
                variant="outline"
                onClick={() =>
                  onParams([...params, ...undeclared.map((name) => ({ name, required: true }))])
                }
              >
                {t("automation.params.declare")}
              </Button>
            </div>
          ) : null}
          {params.length === 0 ? (
            <p className="text-sm text-grid-muted">{t("automation.params.none")}</p>
          ) : null}
          {params.map((p, i) => (
            <div
              key={i}
              className="space-y-2 rounded-md border border-line p-3"
              data-param={p.name}
            >
              <div className="flex items-center gap-2">
                <Input
                  dir="ltr"
                  className="font-mono text-xs"
                  aria-label={t("automation.params.name")}
                  placeholder={t("automation.params.name")}
                  aria-invalid={!validParamName(p.name) || undefined}
                  readOnly={readOnly}
                  value={p.name}
                  onChange={(e) => set(i, { name: e.target.value.trim() })}
                />
                {!readOnly ? (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    className="text-grid-danger-text"
                    aria-label={t("automation.params.remove")}
                    onClick={() => onParams(params.filter((_, j) => j !== i))}
                  >
                    <Trash2Icon />
                  </Button>
                ) : null}
              </div>
              <Input
                aria-label={t("automation.params.label")}
                placeholder={t("automation.params.label")}
                readOnly={readOnly}
                value={p.label ?? ""}
                onChange={(e) => set(i, { label: e.target.value })}
              />
              {p.secret ? (
                <p className="text-xs text-grid-muted">{t("automation.params.secretNoDefault")}</p>
              ) : (
                <Input
                  aria-label={t("automation.params.default")}
                  placeholder={t("automation.params.default")}
                  readOnly={readOnly}
                  value={p.default ?? ""}
                  onChange={(e) => set(i, { default: e.target.value })}
                />
              )}
              <div className="flex flex-wrap gap-4 text-xs">
                <label className="flex items-center gap-2">
                  <Checkbox
                    checked={!!p.required}
                    disabled={readOnly}
                    onCheckedChange={(v) => set(i, { required: !!v })}
                  />
                  {t("automation.params.required")}
                </label>
                <label className="flex items-center gap-2">
                  <Checkbox
                    checked={!!p.secret}
                    disabled={readOnly}
                    onCheckedChange={(v) => set(i, { secret: !!v, ...(v ? { default: "" } : {}) })}
                  />
                  {t("automation.params.secret")}
                </label>
              </div>
              {!used.includes(p.name) && validParamName(p.name) ? (
                <p className="text-xs text-grid-muted">{t("automation.params.unused")}</p>
              ) : null}
            </div>
          ))}
          {!readOnly ? (
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onParams([...params, { name: "", required: true }])}
            >
              <PlusIcon />
              {t("automation.params.add")}
            </Button>
          ) : null}
        </section>

        {fields.length > 0 && !readOnly ? (
          <section className="space-y-3" data-test-values>
            <div>
              <h3 className="text-sm font-medium">{t("canvas.testValues")}</h3>
              <p className="text-xs text-grid-muted">{t("canvas.testValuesHint")}</p>
            </div>
            {fields.map((f) => (
              <Field key={f.name}>
                <FieldLabel htmlFor={`val-${f.name}`}>
                  {f.label || f.name}
                  {f.required ? "" : ` (${t("common.optional")})`}
                </FieldLabel>
                <Input
                  id={`val-${f.name}`}
                  type={f.secret ? "password" : "text"}
                  autoComplete={f.secret ? "new-password" : "off"}
                  value={values[f.name] ?? f.default ?? ""}
                  onChange={(e) => onValues({ ...values, [f.name]: e.target.value })}
                />
                {f.secret ? (
                  <FieldDescription>{t("automation.secretHint")}</FieldDescription>
                ) : null}
              </Field>
            ))}
          </section>
        ) : null}
      </div>
    </div>
  )
}
