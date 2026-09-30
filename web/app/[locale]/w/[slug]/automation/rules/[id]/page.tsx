"use client"

/*
One automation rule (MH-427), built on the workflow canvas's surface: the
trigger, an IF node holding the conditions, and the actions in order. A node
opens its panel; Test runs the rule against a sample event, paints the nodes
with what would happen and sends nothing; the switch turns it on. /rules/new
starts empty or from a template (?template=lead|sentry|optin).
*/

import Link from "next/link"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Suspense, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react"
import {
  ArrowLeftIcon,
  CircleAlertIcon,
  FlaskConicalIcon,
  LoaderCircleIcon,
  SaveIcon,
} from "lucide-react"
import { Button, Input, Switch, toast } from "@fadymondy/nasaq/web"

import { RuleCanvas, type RuleSelection } from "@/components/automation/rules/rule-canvas"
import {
  emptyDraft,
  paint,
  TEMPLATES,
  toInput,
  type Draft,
} from "@/components/automation/rules/rule-model"
import {
  ActionPanel,
  ActionPicker,
  ConditionsPanel,
  TestPanel,
  TriggerPanel,
  useFieldNames,
} from "@/components/automation/rules/rule-panels"
import { RunLog, statusLabel } from "@/components/automation/rules/run-view"
import { isolate } from "@/components/bidi"
import { ConfirmButton } from "@/components/confirm-button"
import { NotFoundState } from "@/components/not-found-state"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useMailLists, useMe } from "@/lib/queries"
import { render, useRule, useRuleCatalog, useRuleRuns, type Rule, type RuleRun } from "@/lib/rules"
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

export default function RulePage() {
  return (
    <Suspense fallback={<LoadingRows rows={5} />}>
      <RuleBuilder />
    </Suspense>
  )
}

const fromRule = (r: Rule): Draft => ({
  name: r.name,
  description: r.description,
  trigger: r.trigger,
  match: r.match,
  conditions: r.conditions.map((c) => ({ ...c, value: c.value ?? "" })),
  actions: r.actions.map((a) => ({ type: a.type, params: { ...(a.params ?? {}) } })),
  cron: r.cron || "0 9 * * 1-5",
  timezone: r.timezone || "UTC",
  once_per_subject: r.once_per_subject,
  max_runs_per_hour: r.max_runs_per_hour,
})

function RuleBuilder() {
  const { t, locale, isRtl } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const search = useSearchParams()
  const params = useParams<{ id: string }>()
  const isNew = params.id === "new"
  const ruleId = isNew ? null : params.id
  const base = `/${locale}/w/${ws.slug}/automation/rules`
  const canEdit = ws.can("automation:run")
  const narrow = useNarrow()

  const catalog = useRuleCatalog(ws.id)
  const rule = useRule(ws.id, ruleId)
  const me = useMe()
  const lists = useMailLists(ws.id)
  const runs = useRuleRuns(ruleId ? ws.id : null, ruleId ?? undefined)
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "rules" && ruleId) {
      void rule.mutate()
      void runs.mutate()
    }
  })

  const [draft, setDraft] = useState<Draft | null>(null)
  const [saved, setSaved] = useState<string>("")
  const [selected, setSelected] = useState<RuleSelection | "test">(null)
  const [view, setView] = useState<"editor" | "runs">(
    search.get("view") === "runs" ? "runs" : "editor",
  )
  const [saving, setSaving] = useState(false)
  const [saveErr, setSaveErr] = useState("")
  const [sample, setSample] = useState<Record<string, string>>({})
  const [test, setTest] = useState<{ run: RuleRun | null; running: boolean; error: string }>({
    run: null,
    running: false,
    error: "",
  })

  // The draft starts from the saved rule, a template, or empty — once.
  const started = useRef(false)
  useEffect(() => {
    if (started.current || !catalog.data) return
    if (ruleId) {
      if (!rule.data) return
      const d = fromRule(rule.data.rule)
      setDraft(d)
      setSaved(JSON.stringify(toInput(d)))
      started.current = true
      return
    }
    const tplId = search.get("template")
    const tpl = TEMPLATES.find((x) => x.id === tplId)
    if (tpl && (me.data === undefined || lists.data === undefined) && !lists.error) return
    const d = tpl
      ? tpl.draft({
          t,
          userId: me.data ? String(me.data.id) : "",
          listId: lists.data?.lists?.[0]?.id ?? "",
        })
      : { ...emptyDraft(), name: t("rules.untitled") }
    setDraft(d)
    setSaved("")
    setSelected(tpl ? null : "trigger")
    started.current = true
  }, [catalog.data, rule.data, ruleId, me.data, lists.data, lists.error, search, t])

  const def = catalog.data?.triggers.find((x) => x.type === draft?.trigger)
  // The sample follows the trigger: its own sample event, as text to edit.
  useEffect(() => {
    if (!def) return
    const s: Record<string, string> = {}
    for (const f of def.fields)
      s[f.name] = def.sample[f.name] == null ? "" : String(def.sample[f.name])
    setSample(s)
    setTest({ run: null, running: false, error: "" })
  }, [def])

  const current = rule.data?.rule
  useDocumentTitle(`${draft?.name || t("rules.title")} · ${ws.summary.name}`)
  const dirty = !!draft && JSON.stringify(toInput(draft)) !== saved
  const fieldNames = useFieldNames(
    def,
    draft?.actions ?? [],
    typeof selected === "number" ? selected : (draft?.actions.length ?? 0),
  )

  const sampleEvent = useMemo(() => {
    const out: Record<string, unknown> = {}
    for (const f of def?.fields ?? []) {
      const v = sample[f.name] ?? ""
      out[f.name] =
        f.type === "number" && v.trim() !== "" && !Number.isNaN(Number(v)) ? Number(v) : v
    }
    return out
  }, [sample, def])

  if (catalog.error) return <ErrorState error={catalog.error} />
  if (rule.error) {
    if (rule.error instanceof ApiError && rule.error.status === 404) return <NotFoundState />
    return <ErrorState error={rule.error} />
  }
  if (!draft || !catalog.data) return <LoadingRows rows={5} />
  const cat = catalog.data

  const change = (d: Partial<Draft>) => setDraft((x) => (x ? { ...x, ...d } : x))

  async function save() {
    if (!draft) return
    setSaving(true)
    setSaveErr("")
    try {
      if (ruleId) {
        const res = await api<{ rule: Rule }>(`/api/w/rules/${encodeURIComponent(ruleId)}`, {
          method: "PUT",
          json: toInput(draft),
          workspaceId: ws.id,
        })
        setSaved(JSON.stringify(toInput(draft)))
        void rule.mutate(res, { revalidate: false })
        toast.success(t("rules.saved"))
      } else {
        const res = await api<{ rule: Rule }>("/api/w/rules", {
          method: "POST",
          json: { ...toInput(draft), enabled: false },
          workspaceId: ws.id,
        })
        toast.success(t("rules.created"))
        router.replace(`${base}/${res.rule.id}`)
      }
    } catch (e) {
      setSaveErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setSaving(false)
    }
  }

  async function toggle(on: boolean) {
    if (!ruleId) return
    try {
      const res = await api<{ rule: Rule }>(`/api/w/rules/${encodeURIComponent(ruleId)}`, {
        method: "PATCH",
        json: { enabled: on },
        workspaceId: ws.id,
      })
      void rule.mutate(res, { revalidate: false })
      toast.success(on ? t("rules.turnedOn") : t("rules.turnedOff"))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function runTest() {
    if (!draft) return
    setTest((x) => ({ ...x, running: true, error: "" }))
    try {
      const body = { rule: toInput(draft), event: sampleEvent }
      const res = ruleId
        ? await api<{ run: RuleRun }>(`/api/w/rules/${encodeURIComponent(ruleId)}/test`, {
            method: "POST",
            json: body,
            workspaceId: ws.id,
          })
        : await api<{ run: RuleRun }>("/api/w/rules/test", {
            method: "POST",
            json: body,
            workspaceId: ws.id,
          })
      setTest({ run: res.run, running: false, error: "" })
      if (ruleId) void runs.mutate()
    } catch (e) {
      setTest({
        run: null,
        running: false,
        error: e instanceof ApiError ? e.message : t("common.networkError"),
      })
    }
  }

  async function remove() {
    if (!ruleId) return
    try {
      await api(`/api/w/rules/${encodeURIComponent(ruleId)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      toast.success(t("rules.deleted"))
      router.replace(base)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const summaryOf = (i: number) => {
    const p = draft.actions[i]?.params ?? {}
    const raw = p.title || p.subject || p.text || p.body || p.url || ""
    const s = render(raw, sampleEvent)
    return s.length > 60 ? `${s.slice(0, 60)}…` : s
  }
  const condSub = draft.conditions.length
    ? draft.conditions
        .slice(0, 2)
        .map((c) =>
          `${t(`rules.field.${c.field}`)} ${t(`rules.op.${c.op}`)} ${c.value ?? ""}`.trim(),
        )
        .join(draft.match === "all" ? ` ${t("rules.and")} ` : ` ${t("rules.or")} `)
    : t("rules.alwaysSub")
  const canvasRun = paint(test.run, draft.actions.length)
  const panelOpen = view === "editor" && selected !== null
  const readOnly = !canEdit

  const panel = (() => {
    if (selected === "trigger")
      return (
        <TriggerPanel
          draft={draft}
          catalog={cat}
          readOnly={readOnly}
          onChange={change}
          onClose={() => setSelected(null)}
        />
      )
    if (selected === "conditions")
      return (
        <ConditionsPanel
          draft={draft}
          catalog={cat}
          readOnly={readOnly}
          onChange={change}
          onClose={() => setSelected(null)}
        />
      )
    if (selected === "add")
      return (
        <ActionPicker
          catalog={cat}
          onClose={() => setSelected(null)}
          onPick={(type) => {
            const ad = cat.actions.find((a) => a.type === type)
            const params: Record<string, string> = {}
            for (const p of ad?.params ?? []) if (p.default) params[p.name] = p.default
            const next = [...draft.actions, { type, params }]
            change({ actions: next })
            setSelected(next.length - 1)
          }}
        />
      )
    if (selected === "test")
      return (
        <TestPanel
          def={def}
          sample={sample}
          onSample={setSample}
          onRun={() => void runTest()}
          running={test.running}
          result={test.run}
          error={test.error}
          onClose={() => setSelected(null)}
        />
      )
    if (typeof selected === "number" && draft.actions[selected])
      return (
        <ActionPanel
          key={selected}
          index={selected}
          action={draft.actions[selected]}
          def={cat.actions.find((a) => a.type === draft.actions[selected].type)}
          fields={fieldNames}
          readOnly={readOnly}
          count={draft.actions.length}
          onChange={(a) =>
            change({ actions: draft.actions.map((x, i) => (i === selected ? a : x)) })
          }
          onRemove={() => {
            change({ actions: draft.actions.filter((_, i) => i !== selected) })
            setSelected(null)
          }}
          onMove={(to) => {
            if (to < 0 || to >= draft.actions.length) return
            const next = [...draft.actions]
            const [x] = next.splice(selected, 1)
            next.splice(to, 0, x)
            change({ actions: next })
            setSelected(to)
          }}
          onClose={() => setSelected(null)}
        />
      )
    return null
  })()

  return (
    <div
      className="flex h-[calc(100dvh-3.5rem)] min-h-[480px] flex-col"
      data-rule-page
      data-dirty={dirty ? "true" : "false"}
      data-rule-id={ruleId ?? "new"}
    >
      <header className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-nq-line px-3 py-2 sm:px-4">
        <Button
          variant="ghost"
          size="icon-sm"
          nativeButton={false}
          render={<Link href={base} />}
          aria-label={t("rules.backToList")}
          title={t("rules.backToList")}
        >
          <ArrowLeftIcon className="rtl:rotate-180" />
        </Button>
        <div className="flex min-w-0 flex-1 items-center gap-2 md:flex-none">
          <Input
            value={draft.name}
            maxLength={80}
            readOnly={readOnly}
            dir="auto"
            placeholder={t("rules.namePlaceholder")}
            aria-label={t("rules.name")}
            onChange={(e) => change({ name: e.target.value })}
            className="h-8 min-w-0 border-transparent bg-transparent px-2 text-base font-medium shadow-none hover:border-nq-line focus-visible:border-ring md:w-72"
            data-rule-name
          />
          {dirty ? (
            <span className="text-xs whitespace-nowrap text-muted-foreground" data-unsaved>
              {t("canvas.unsaved")}
            </span>
          ) : null}
        </div>

        <nav
          className="order-last flex w-full items-center justify-center gap-1 md:order-none md:w-auto md:flex-1"
          aria-label={t("canvas.views")}
        >
          {ruleId ? (
            <div className="inline-flex rounded-lg bg-muted p-[3px]">
              {(["editor", "runs"] as const).map((v) => (
                <button
                  key={v}
                  type="button"
                  onClick={() => setView(v)}
                  className={cn(
                    "rounded-md px-3 py-1 text-sm font-medium transition-colors",
                    view === v
                      ? "bg-background text-foreground shadow-sm"
                      : "text-foreground/60 hover:text-foreground",
                  )}
                  aria-current={view === v ? "page" : undefined}
                  data-view-tab={v}
                >
                  {t(`rules.view.${v}`)}
                </button>
              ))}
            </div>
          ) : null}
        </nav>

        <div className="flex items-center gap-2">
          {current ? (
            <label
              className="flex items-center gap-2 text-sm"
              title={dirty ? t("rules.saveFirst") : undefined}
            >
              <Switch
                checked={current.enabled}
                disabled={!canEdit || dirty}
                onCheckedChange={(v) => void toggle(!!v)}
                data-rule-enabled
              />
              <span className="hidden sm:inline">
                {current.enabled ? t("rules.on") : t("rules.off")}
              </span>
            </label>
          ) : null}
          {canEdit ? (
            <Button
              variant={selected === "test" ? "secondary" : "secondary"}
              size="sm"
              onClick={() => {
                setView("editor")
                setSelected(selected === "test" ? null : "test")
              }}
              data-open-test
            >
              <FlaskConicalIcon />
              <span className="hidden sm:inline">{t("rules.test")}</span>
            </Button>
          ) : null}
          {canEdit ? (
            <Button
              size="sm"
              onClick={() => void save()}
              disabled={saving || (!dirty && !!ruleId)}
              data-save-rule
            >
              {saving ? <LoaderCircleIcon className="animate-spin" /> : <SaveIcon />}
              {saving ? t("common.working") : t("common.save")}
            </Button>
          ) : null}
          {canEdit && current ? (
            <ConfirmButton
              label={t("common.delete")}
              title={t("rules.deleteTitle")}
              description={t("rules.deleteBody", { name: isolate(current.name) })}
              confirmLabel={t("common.delete")}
              onConfirm={remove}
            />
          ) : null}
        </div>
      </header>
      {saveErr ? (
        <p
          className="flex items-center gap-2 border-b border-nq-line bg-card px-4 py-2 text-sm text-nq-danger-text"
          role="alert"
          data-save-error
        >
          <CircleAlertIcon className="size-4 shrink-0" />
          {saveErr}
        </p>
      ) : current && !current.enabled && !dirty ? (
        <p
          className="border-b border-nq-line bg-card px-4 py-2 text-xs text-muted-foreground"
          data-off-note
        >
          {t("rules.offNote")}
        </p>
      ) : null}

      {view === "runs" && ruleId ? (
        <div className="min-h-0 flex-1 overflow-y-auto pt-4">
          <RunLog
            runs={(runs.data ?? []).flatMap((p) => p.runs)}
            loading={!runs.data && !runs.error}
            error={runs.error}
            more={!!runs.data?.[runs.data.length - 1]?.next}
            onMore={() => runs.setSize(runs.size + 1)}
            showRule={false}
          />
        </div>
      ) : (
        <div className="relative flex min-h-0 flex-1">
          <div className="relative min-w-0 flex-1" data-canvas-area>
            <RuleCanvas
              trigger={{
                type: draft.trigger,
                title: t(`rules.trigger.${draft.trigger}`),
                subtitle: draft.trigger === "schedule" ? draft.cron : t("rules.whenLabel"),
              }}
              conditions={{
                title:
                  draft.conditions.length === 1
                    ? t("rules.ifTitle.one")
                    : draft.conditions.length
                      ? t(`rules.ifTitle.${draft.match}`, { n: draft.conditions.length })
                      : t("rules.always"),
                subtitle: condSub,
              }}
              actions={draft.actions.map((a, i) => ({
                type: a.type,
                title: t(`rules.action.${a.type}`),
                subtitle: summaryOf(i),
              }))}
              run={canvasRun}
              selected={selected === "test" ? null : selected}
              readOnly={readOnly}
              vertical={narrow}
              onSelect={(s) => setSelected(s)}
            />
            {canvasRun && test.run ? (
              <div className="pointer-events-none absolute inset-x-0 bottom-16 z-10 flex justify-center px-4 md:bottom-6">
                <p
                  className="pointer-events-auto rounded-full border border-nq-line bg-card px-3 py-1 text-xs shadow-sm"
                  data-canvas-test={test.run.status}
                >
                  {t("rules.testedAs", { status: statusLabel(t, test.run) })}{" "}
                  <button type="button" className="underline" onClick={() => setSelected("test")}>
                    {t("rules.seeTest")}
                  </button>
                </p>
              </div>
            ) : null}
          </div>
          {panelOpen && panel ? (
            <div
              className={cn(
                "z-20 min-h-0 border-nq-line bg-background",
                narrow ? "absolute inset-0" : "w-[26rem] shrink-0 border-s",
              )}
              dir={isRtl ? "rtl" : "ltr"}
              data-side-panel={typeof selected === "number" ? "action" : String(selected)}
            >
              {panel}
            </div>
          ) : null}
        </div>
      )}
    </div>
  )
}
