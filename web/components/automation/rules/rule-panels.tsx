"use client"

/*
The rule builder's side panels (MH-427), opened by clicking a node the way the
workflow canvas's are:

  trigger     — which event, what it carries, the schedule for the clock
                trigger, and the rule's brakes (once per record, runs an hour);
  conditions  — all/any, and each test on one of the event's fields;
  an action   — its settings, each text setting with the event's fields a
                click away as {{placeholders}};
  add         — the actions a rule can take;
  test        — a sample event, editable, and what the rule would do with it.

Every edit goes straight back to the page's draft; nothing here keeps state
the page does not.
*/

import { useMemo, useState, type ReactNode } from "react"
import {
  BracesIcon,
  FlaskConicalIcon,
  LoaderCircleIcon,
  PlusIcon,
  ShieldCheckIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react"

import { PickSelect } from "@/components/crm/shared"
import { Bdi } from "@/components/bidi"
import {
  Button,
  Checkbox,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  Switch,
  Textarea,
} from "@fadymondy/nasaq/web"
import { Label } from "@/components/form-layout"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import {
  useBrowsers,
  useCannedReplies,
  useMailLists,
  useMembers,
  usePipelineStages,
  useSmtpServiceOptions,
  useWorkflows,
} from "@/lib/queries"
import type {
  ActionDef,
  RuleAction,
  RuleCatalog,
  RuleCondition,
  RuleRun,
  TriggerDef,
} from "@/lib/rules"
import { cn } from "@/lib/utils"

import { actionIcon, TRIGGER_GROUPS, triggerIcon, UNARY, type Draft } from "./rule-model"
import { RunResult } from "./run-view"

// ── the frame ────────────────────────────────────────────────────────────

export function PanelFrame({
  icon,
  title,
  sub,
  onClose,
  children,
  actions,
}: {
  icon: ReactNode
  title: ReactNode
  sub?: ReactNode
  onClose: () => void
  children: ReactNode
  actions?: ReactNode
}) {
  const { t } = useTranslations()
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-start gap-3 border-b border-nq-line px-4 py-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-md border border-nq-line bg-card [&>svg]:size-5">
          {icon}
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-base font-medium">{title}</h2>
          {sub ? <p className="line-clamp-2 text-xs text-muted-foreground">{sub}</p> : null}
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
      <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-4 py-4">{children}</div>
    </div>
  )
}

/** The fields a {{placeholder}} can name: the event's, and what earlier actions made. */
export function useFieldNames(def: TriggerDef | undefined, actions: RuleAction[], upto: number) {
  return useMemo(() => {
    const names = (def?.fields ?? []).map((f) => f.name)
    actions.slice(0, upto).forEach((a) => {
      if (a.type === "create_deal" && !names.includes("deal_id")) names.push("deal_id")
      if (
        (a.type === "create_contact" || a.type === "add_to_list") &&
        !names.includes("contact_id")
      )
        names.push("contact_id")
    })
    return names
  }, [def, actions, upto])
}

function InsertField({ fields, onInsert }: { fields: string[]; onInsert: (f: string) => void }) {
  const { t } = useTranslations()
  if (fields.length === 0) return null
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            className="h-6 gap-1 px-1.5 text-xs text-muted-foreground"
            aria-label={t("rules.insertField")}
            data-insert-field
          />
        }
      >
        <BracesIcon />
        {t("rules.insertField")}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-72 min-w-52 overflow-y-auto">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{t("rules.eventFields")}</DropdownMenuLabel>
          {fields.map((f) => (
            <DropdownMenuItem key={f} onClick={() => onInsert(f)}>
              <span className="flex-1">{t(`rules.field.${f}`)}</span>
              <code
                dir="ltr"
                className="font-mono text-[11px] text-muted-foreground"
              >{`{{${f}}}`}</code>
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// ── trigger ──────────────────────────────────────────────────────────────

export function TriggerPanel({
  draft,
  catalog,
  readOnly,
  onChange,
  onClose,
}: {
  draft: Draft
  catalog: RuleCatalog
  readOnly: boolean
  onChange: (d: Partial<Draft>) => void
  onClose: () => void
}) {
  const { t } = useTranslations()
  const def = catalog.triggers.find((x) => x.type === draft.trigger)
  const Icon = triggerIcon(draft.trigger)
  const options = TRIGGER_GROUPS.flatMap((g) =>
    g.types
      .filter((ty) => catalog.triggers.some((x) => x.type === ty))
      .map((ty) => ({
        value: ty,
        label: `${t(`rules.group.${g.id}`)} · ${t(`rules.trigger.${ty}`)}`,
      })),
  )
  return (
    <PanelFrame
      icon={<Icon className="text-nq-accent" />}
      title={t("rules.when")}
      sub={t(`rules.triggerHint.${draft.trigger}`)}
      onClose={onClose}
    >
      <Field>
        <FieldLabel htmlFor="rule-trigger">{t("rules.triggerLabel")}</FieldLabel>
        <PickSelect
          id="rule-trigger"
          value={draft.trigger}
          disabled={readOnly}
          onChange={(v) => {
            // Conditions name the old event's fields; keep only those the new one carries.
            const next = catalog.triggers.find((x) => x.type === v)
            const known = new Set(next?.fields.map((f) => f.name))
            onChange({ trigger: v, conditions: draft.conditions.filter((c) => known.has(c.field)) })
          }}
          options={options}
        />
      </Field>

      {draft.trigger === "schedule" ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="rule-cron">{t("rules.cron")}</FieldLabel>
            <Input
              id="rule-cron"
              dir="ltr"
              className="font-mono"
              value={draft.cron}
              readOnly={readOnly}
              onChange={(e) => onChange({ cron: e.target.value })}
              data-rule-cron
            />
            <FieldDescription>{t("rules.cronHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="rule-tz">{t("rules.timezone")}</FieldLabel>
            <Input
              id="rule-tz"
              dir="ltr"
              value={draft.timezone}
              readOnly={readOnly}
              onChange={(e) => onChange({ timezone: e.target.value })}
            />
          </Field>
        </div>
      ) : null}

      <div className="space-y-2">
        <p className="text-sm font-medium">{t("rules.carries")}</p>
        <ul className="flex flex-wrap gap-1.5" data-trigger-fields>
          {(def?.fields ?? []).map((f) => (
            <li key={f.name} className="rounded-md border border-nq-line bg-card px-2 py-1 text-xs">
              {t(`rules.field.${f.name}`)}{" "}
              <code
                dir="ltr"
                className="ms-1 font-mono text-[11px] text-muted-foreground"
              >{`{{${f.name}}}`}</code>
            </li>
          ))}
        </ul>
      </div>

      <div className="space-y-3 border-t border-nq-line pt-4">
        <p className="flex items-center gap-2 text-sm font-medium">
          <ShieldCheckIcon className="size-4 text-muted-foreground" />
          {t("rules.brakes")}
        </p>
        <label className="flex items-start gap-3 text-sm">
          <Switch
            checked={draft.once_per_subject}
            disabled={readOnly}
            onCheckedChange={(v) => onChange({ once_per_subject: !!v })}
            data-once
          />
          <span>
            {t("rules.once")}
            <span className="block text-xs text-muted-foreground">{t("rules.onceHint")}</span>
          </span>
        </label>
        <Field>
          <FieldLabel htmlFor="rule-max">{t("rules.maxPerHour")}</FieldLabel>
          <Input
            id="rule-max"
            type="number"
            min={1}
            max={1000}
            className="w-28"
            value={draft.max_runs_per_hour}
            readOnly={readOnly}
            onChange={(e) =>
              onChange({
                max_runs_per_hour: Math.max(1, Math.min(1000, Number(e.target.value) || 1)),
              })
            }
          />
          <FieldDescription>
            {t("rules.maxPerHourHint", {
              cap: catalog.workspace_hourly_cap,
              depth: catalog.max_depth,
            })}
          </FieldDescription>
        </Field>
      </div>

      <Field>
        <FieldLabel htmlFor="rule-desc">{t("rules.description")}</FieldLabel>
        <Textarea
          id="rule-desc"
          rows={3}
          dir="auto"
          value={draft.description}
          readOnly={readOnly}
          maxLength={500}
          onChange={(e) => onChange({ description: e.target.value })}
        />
      </Field>
    </PanelFrame>
  )
}

// ── conditions ───────────────────────────────────────────────────────────

export function ConditionsPanel({
  draft,
  catalog,
  readOnly,
  onChange,
  onClose,
}: {
  draft: Draft
  catalog: RuleCatalog
  readOnly: boolean
  onChange: (d: Partial<Draft>) => void
  onClose: () => void
}) {
  const { t } = useTranslations()
  const def = catalog.triggers.find((x) => x.type === draft.trigger)
  const fields = def?.fields ?? []
  const set = (i: number, c: Partial<RuleCondition>) =>
    onChange({ conditions: draft.conditions.map((x, j) => (j === i ? { ...x, ...c } : x)) })
  return (
    <PanelFrame
      icon={<BracesIcon className="text-nq-accent" />}
      title={t("rules.if")}
      sub={t("rules.ifHint")}
      onClose={onClose}
    >
      <div
        className="inline-flex rounded-lg bg-muted p-[3px]"
        role="radiogroup"
        aria-label={t("rules.match")}
      >
        {(["all", "any"] as const).map((m) => (
          <button
            key={m}
            type="button"
            role="radio"
            aria-checked={draft.match === m}
            disabled={readOnly}
            onClick={() => onChange({ match: m })}
            className={cn(
              "rounded-md px-3 py-1 text-sm font-medium transition-colors",
              draft.match === m
                ? "bg-background text-foreground shadow-sm"
                : "text-foreground/60 hover:text-foreground",
            )}
            data-match={m}
          >
            {t(`rules.match.${m}`)}
          </button>
        ))}
      </div>

      {draft.conditions.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t("rules.noConditions")}</p>
      ) : null}
      <ol className="space-y-3" data-conditions>
        {draft.conditions.map((c, i) => (
          <li
            key={i}
            className="space-y-2 rounded-lg border border-nq-line bg-card p-3"
            data-condition={i}
          >
            <div className="flex items-center justify-between gap-2">
              <span className="text-xs text-muted-foreground">
                {i === 0 ? t("rules.condFirst") : t(`rules.condJoin.${draft.match}`)}
              </span>
              {!readOnly ? (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  className="text-nq-danger-text"
                  onClick={() =>
                    onChange({ conditions: draft.conditions.filter((_, j) => j !== i) })
                  }
                  aria-label={t("rules.removeCondition")}
                  title={t("rules.removeCondition")}
                >
                  <Trash2Icon />
                </Button>
              ) : null}
            </div>
            <div className="grid gap-2 sm:grid-cols-2">
              <PickSelect
                id={`cond-field-${i}`}
                value={c.field}
                disabled={readOnly}
                onChange={(v) => set(i, { field: v })}
                options={fields.map((f) => ({ value: f.name, label: t(`rules.field.${f.name}`) }))}
              />
              <PickSelect
                id={`cond-op-${i}`}
                value={c.op}
                disabled={readOnly}
                onChange={(v) => set(i, { op: v, value: UNARY.has(v) ? "" : c.value })}
                options={catalog.operators.map((o) => ({ value: o, label: t(`rules.op.${o}`) }))}
              />
            </div>
            {!UNARY.has(c.op) ? (
              <Input
                value={c.value ?? ""}
                dir="auto"
                readOnly={readOnly}
                placeholder={
                  c.op === "in" || c.op === "not_in" ? t("rules.valueList") : t("rules.value")
                }
                aria-label={t("rules.value")}
                onChange={(e) => set(i, { value: e.target.value })}
                data-cond-value={i}
              />
            ) : null}
          </li>
        ))}
      </ol>
      {!readOnly ? (
        <Button
          variant="secondary"
          size="sm"
          onClick={() =>
            onChange({
              conditions: [
                ...draft.conditions,
                { field: fields[0]?.name ?? "", op: "equals", value: "" },
              ],
            })
          }
          disabled={fields.length === 0 || draft.conditions.length >= 20}
          data-add-condition
        >
          <PlusIcon />
          {t("rules.addCondition")}
        </Button>
      ) : null}
    </PanelFrame>
  )
}

// ── an action ────────────────────────────────────────────────────────────

export function ActionPanel({
  index,
  action,
  def,
  fields,
  readOnly,
  count,
  onChange,
  onRemove,
  onMove,
  onClose,
}: {
  index: number
  action: RuleAction
  def: ActionDef | undefined
  fields: string[]
  readOnly: boolean
  count: number
  onChange: (a: RuleAction) => void
  onRemove: () => void
  onMove: (to: number) => void
  onClose: () => void
}) {
  const { t } = useTranslations()
  const Icon = actionIcon(action.type)
  const params = action.params ?? {}
  const set = (name: string, v: string) => onChange({ ...action, params: { ...params, [name]: v } })
  return (
    <PanelFrame
      icon={<Icon className="text-primary" />}
      title={t("rules.stepN", { n: index + 1, what: t(`rules.action.${action.type}`) })}
      sub={t(`rules.actionHint.${action.type}`)}
      onClose={onClose}
      actions={
        !readOnly ? (
          <>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => onMove(index - 1)}
              disabled={index === 0}
              data-move-up
            >
              {t("rules.earlier")}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => onMove(index + 1)}
              disabled={index === count - 1}
              data-move-down
            >
              {t("rules.later")}
            </Button>
            <Button
              variant="ghost"
              size="icon-sm"
              className="text-nq-danger-text"
              onClick={onRemove}
              aria-label={t("rules.removeAction")}
              title={t("rules.removeAction")}
              data-remove-action
            >
              <Trash2Icon />
            </Button>
          </>
        ) : null
      }
    >
      {def?.to_customer ? (
        <p
          className="rounded-md border border-nq-line bg-card px-3 py-2 text-xs text-muted-foreground"
          data-consent-note
        >
          {t(`rules.consent.${action.type}`)}
        </p>
      ) : null}
      {(def?.params ?? []).map((p) => (
        <ParamField
          key={p.name}
          kind={p.kind}
          name={p.name}
          required={!!p.required}
          value={params[p.name] ?? ""}
          fields={fields}
          readOnly={readOnly}
          onChange={(v) => set(p.name, v)}
        />
      ))}
    </PanelFrame>
  )
}

function ParamField({
  kind,
  name,
  required,
  value,
  fields,
  readOnly,
  onChange,
}: {
  kind: string
  name: string
  required: boolean
  value: string
  fields: string[]
  readOnly: boolean
  onChange: (v: string) => void
}) {
  const { t } = useTranslations()
  const id = `param-${name}`
  const label = (
    <span className="flex w-full items-center justify-between gap-2">
      <Label htmlFor={id}>
        {t(`rules.param.${name}`)}
        {required ? <span className="text-nq-danger-text"> *</span> : null}
      </Label>
      {["text", "textarea", "number", "contact_ref", "url"].includes(kind) && !readOnly ? (
        <InsertField fields={fields} onInsert={(f) => onChange(`${value}{{${f}}}`)} />
      ) : null}
    </span>
  )
  const hint = t(`rules.paramHint.${name}`)
  const hasHint = hint !== `rules.paramHint.${name}`
  let control: ReactNode
  switch (kind) {
    case "textarea":
      control = (
        <Textarea
          id={id}
          rows={4}
          dir="auto"
          value={value}
          readOnly={readOnly}
          onChange={(e) => onChange(e.target.value)}
          data-param={name}
        />
      )
      break
    case "json":
      control = (
        <Textarea
          id={id}
          rows={4}
          dir="ltr"
          className="font-mono text-xs"
          placeholder={'{"name": "{{name}}"}'}
          value={value}
          readOnly={readOnly}
          onChange={(e) => onChange(e.target.value)}
          data-param={name}
        />
      )
      break
    case "bool":
      control = (
        <Switch
          id={id}
          checked={value === "true"}
          disabled={readOnly}
          onCheckedChange={(v) => onChange(v ? "true" : "false")}
          data-param={name}
        />
      )
      break
    case "member":
      control = <MemberSelect id={id} value={value} readOnly={readOnly} onChange={onChange} />
      break
    case "members":
      control = <MembersPick value={value} readOnly={readOnly} onChange={onChange} />
      break
    case "list":
      control = <ListSelect id={id} value={value} readOnly={readOnly} onChange={onChange} />
      break
    case "stage":
      control = <StageSelect id={id} value={value} readOnly={readOnly} onChange={onChange} />
      break
    case "canned":
      control = <CannedSelect id={id} value={value} readOnly={readOnly} onChange={onChange} />
      break
    case "smtp":
      control = <SmtpSelect id={id} value={value} readOnly={readOnly} onChange={onChange} />
      break
    case "workflow":
      control = <WorkflowSelect id={id} value={value} readOnly={readOnly} onChange={onChange} />
      break
    case "browser":
      control = <BrowserSelect id={id} value={value} readOnly={readOnly} onChange={onChange} />
      break
    default:
      control = (
        <Input
          id={id}
          dir={kind === "url" || kind === "contact_ref" ? "ltr" : "auto"}
          inputMode={kind === "number" ? "decimal" : undefined}
          className={kind === "contact_ref" ? "font-mono text-xs" : undefined}
          value={value}
          readOnly={readOnly}
          onChange={(e) => onChange(e.target.value)}
          data-param={name}
        />
      )
  }
  return (
    <Field data-param-field={name}>
      {label}
      {control}
      {hasHint ? <FieldDescription>{hint}</FieldDescription> : null}
    </Field>
  )
}

function MemberSelect({
  id,
  value,
  readOnly,
  onChange,
}: {
  id: string
  value: string
  readOnly: boolean
  onChange: (v: string) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const members = useMembers(ws.id)
  const opts = (members.data?.members ?? [])
    .filter((m) => m.status === "active")
    .map((m) => ({ value: m.user_id, label: m.email }))
  return (
    <PickSelect
      id={id}
      value={value}
      disabled={readOnly}
      onChange={onChange}
      options={opts}
      noneLabel={t("rules.nobody")}
    />
  )
}

function MembersPick({
  value,
  readOnly,
  onChange,
}: {
  value: string
  readOnly: boolean
  onChange: (v: string) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const members = useMembers(ws.id)
  const all = value === "" || value === "all"
  const picked = new Set(
    all
      ? []
      : value
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
  )
  const toggle = (uid: string, on: boolean) => {
    const next = new Set(picked)
    if (on) next.add(uid)
    else next.delete(uid)
    onChange(next.size === 0 ? "all" : [...next].join(","))
  }
  return (
    <div className="space-y-2 rounded-md border border-nq-line p-3" data-members-pick>
      <label className="flex items-center gap-2 text-sm">
        <Checkbox checked={all} disabled={readOnly} onCheckedChange={(v) => v && onChange("all")} />
        {t("rules.everyone")}
      </label>
      {(members.data?.members ?? [])
        .filter((m) => m.status === "active")
        .map((m) => (
          <label key={m.user_id} className="flex items-center gap-2 text-sm">
            <Checkbox
              checked={picked.has(m.user_id)}
              disabled={readOnly}
              onCheckedChange={(v) => toggle(m.user_id, !!v)}
              data-member={m.email}
            />
            <Bdi>{m.email}</Bdi>
          </label>
        ))}
    </div>
  )
}

function ListSelect({
  id,
  value,
  readOnly,
  onChange,
}: {
  id: string
  value: string
  readOnly: boolean
  onChange: (v: string) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const lists = useMailLists(ws.id)
  const opts = (lists.data?.lists ?? []).map((l) => ({
    value: l.id,
    label: l.double_opt_in ? `${l.name} (${t("rules.doi")})` : l.name,
  }))
  return (
    <PickSelect
      id={id}
      value={value}
      disabled={readOnly}
      onChange={onChange}
      options={opts}
      noneLabel={t("rules.choose")}
    />
  )
}

function StageSelect({
  id,
  value,
  readOnly,
  onChange,
}: {
  id: string
  value: string
  readOnly: boolean
  onChange: (v: string) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const stages = usePipelineStages(ws.id)
  const opts = (stages.data?.stages ?? []).map((s) => ({ value: s.id, label: s.name }))
  return (
    <PickSelect
      id={id}
      value={value}
      disabled={readOnly}
      onChange={onChange}
      options={opts}
      noneLabel={t("rules.stageDefault")}
    />
  )
}

function CannedSelect({
  id,
  value,
  readOnly,
  onChange,
}: {
  id: string
  value: string
  readOnly: boolean
  onChange: (v: string) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const canned = useCannedReplies(ws.id)
  const opts = (canned.data?.replies ?? []).map((c) => ({ value: c.id, label: c.title }))
  return (
    <PickSelect
      id={id}
      value={value}
      disabled={readOnly}
      onChange={onChange}
      options={opts}
      noneLabel={t("rules.choose")}
    />
  )
}

function SmtpSelect({
  id,
  value,
  readOnly,
  onChange,
}: {
  id: string
  value: string
  readOnly: boolean
  onChange: (v: string) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const svc = useSmtpServiceOptions(ws.id)
  const opts = (svc.data?.services ?? []).map((s) => ({ value: s.id, label: s.name }))
  return (
    <PickSelect
      id={id}
      value={value}
      disabled={readOnly}
      onChange={onChange}
      options={opts}
      noneLabel={t("rules.smtpDefault")}
    />
  )
}

function WorkflowSelect({
  id,
  value,
  readOnly,
  onChange,
}: {
  id: string
  value: string
  readOnly: boolean
  onChange: (v: string) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const wf = useWorkflows(ws.id)
  const opts = (wf.data?.workflows ?? []).map((w) => ({ value: w.id, label: w.name }))
  return (
    <PickSelect
      id={id}
      value={value}
      disabled={readOnly}
      onChange={onChange}
      options={opts}
      noneLabel={t("rules.choose")}
    />
  )
}

function BrowserSelect({
  id,
  value,
  readOnly,
  onChange,
}: {
  id: string
  value: string
  readOnly: boolean
  onChange: (v: string) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const b = useBrowsers(ws.id)
  const opts = (b.data?.browsers ?? [])
    .filter((x) => !x.revoked_at)
    .map((x) => ({ value: x.id, label: x.name }))
  return (
    <PickSelect
      id={id}
      value={value}
      disabled={readOnly}
      onChange={onChange}
      options={opts}
      noneLabel={t("rules.choose")}
    />
  )
}

// ── add an action ────────────────────────────────────────────────────────

export function ActionPicker({
  catalog,
  onPick,
  onClose,
}: {
  catalog: RuleCatalog
  onPick: (type: string) => void
  onClose: () => void
}) {
  const { t } = useTranslations()
  return (
    <PanelFrame
      icon={<PlusIcon />}
      title={t("rules.addAction")}
      sub={t("rules.addActionHint")}
      onClose={onClose}
    >
      <ul className="space-y-1" data-action-picker>
        {catalog.actions.map((a) => {
          const Icon = actionIcon(a.type)
          return (
            <li key={a.type}>
              <button
                type="button"
                onClick={() => onPick(a.type)}
                className="flex w-full items-start gap-3 rounded-md px-2 py-2 text-start transition-colors hover:bg-nq-surface-soft focus-visible:outline-2 focus-visible:outline-ring"
                data-pick-action={a.type}
              >
                <span className="flex size-8 shrink-0 items-center justify-center rounded-md border border-nq-line bg-card">
                  <Icon className="size-4 text-primary" />
                </span>
                <span className="min-w-0">
                  <span className="block text-sm font-medium">{t(`rules.action.${a.type}`)}</span>
                  <span className="block text-xs text-muted-foreground">
                    {t(`rules.actionHint.${a.type}`)}
                  </span>
                </span>
              </button>
            </li>
          )
        })}
      </ul>
    </PanelFrame>
  )
}

// ── test ─────────────────────────────────────────────────────────────────

export function TestPanel({
  def,
  sample,
  onSample,
  onRun,
  running,
  result,
  error,
  onClose,
}: {
  def: TriggerDef | undefined
  sample: Record<string, string>
  onSample: (s: Record<string, string>) => void
  onRun: () => void
  running: boolean
  result: RuleRun | null
  error: string
  onClose: () => void
}) {
  const { t } = useTranslations()
  const [showAll, setShowAll] = useState(false)
  const fields = def?.fields ?? []
  const shown = showAll ? fields : fields.slice(0, 6)
  return (
    <PanelFrame
      icon={<FlaskConicalIcon />}
      title={t("rules.test")}
      sub={t("rules.testHint")}
      onClose={onClose}
    >
      <div className="space-y-3" data-sample>
        <p className="text-sm font-medium">{t("rules.sample")}</p>
        {shown.map((f) => (
          <Field key={f.name} className="flex-row items-center gap-3 items-center">
            <FieldLabel
              htmlFor={`sample-${f.name}`}
              className="w-32 shrink-0 text-xs font-normal text-muted-foreground"
            >
              {t(`rules.field.${f.name}`)}
            </FieldLabel>
            <Input
              id={`sample-${f.name}`}
              dir="auto"
              className="h-8 text-sm"
              value={sample[f.name] ?? ""}
              onChange={(e) => onSample({ ...sample, [f.name]: e.target.value })}
              data-sample-field={f.name}
            />
          </Field>
        ))}
        {fields.length > 6 ? (
          <Button variant="link" size="sm" className="px-0" onClick={() => setShowAll(!showAll)}>
            {showAll ? t("rules.fewerFields") : t("rules.allFields", { n: fields.length })}
          </Button>
        ) : null}
      </div>
      <Button onClick={onRun} disabled={running} data-run-test>
        {running ? <LoaderCircleIcon className="animate-spin" /> : <FlaskConicalIcon />}
        {t("rules.runTest")}
      </Button>
      <p className="text-xs text-muted-foreground">{t("rules.testSendsNothing")}</p>
      {error ? (
        <p className="text-sm text-nq-danger-text" role="alert" data-test-error>
          {error}
        </p>
      ) : null}
      {result ? <RunResult run={result} /> : null}
    </PanelFrame>
  )
}
