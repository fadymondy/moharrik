"use client"

/*
The workflow editor (MH-400): edit a saved workflow — or write a new one —
step by step, pick selectors on the live page, test-run the draft in a paired
browser with each step's progress shown on its row, and save it as a new
version. Every earlier version is listed, and any of them can be restored
(which is itself a new version, so the restore can be undone too).
*/

import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { useEffect, useMemo, useState, type FormEvent } from "react"
import { ArrowLeftIcon, FlaskConicalIcon, HistoryIcon, SaveIcon } from "lucide-react"
import { toast } from "sonner"

import { PickControl } from "@/components/automation/pick-control"
import { ParamsEditor, placeholdersIn, validParamName } from "@/components/automation/params-editor"
import {
  StepEditor,
  fromEditorSteps,
  pageFor,
  toEditorSteps,
  type EditorStep,
  type StepStatus,
} from "@/components/automation/step-editor"
import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
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
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationWorkflow, type BrowserAgent, type WorkflowParam, type WorkflowVersion } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { useAutomationSettings, useBrowsers, useRun, useWorkflow, useWorkflowVersions } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function WorkflowEditorPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const params = useParams<{ id: string }>()
  const isNew = params.id === "new"
  const wf = useWorkflow(ws.id, isNew ? undefined : params.id)
  const versions = useWorkflowVersions(ws.id, isNew ? undefined : params.id)
  const workflow = wf.data?.workflow
  useDocumentTitle(`${workflow?.name || t("automation.editor.newTitle")} · ${ws.summary.name}`)

  const back = (
    <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation`} />}>
      <ArrowLeftIcon className="rtl:rotate-180" />
      {t("automation.backToAutomation")}
    </Button>
  )

  if (!ws.can("automation:read")) {
    return <ErrorState error={new ApiError(403, t("automation.editor.noAccess"))} />
  }
  if (!isNew && wf.error) {
    return (
      <>
        <SectionHeader micro={ws.summary.name} title={t("automation.editor.title")} action={back} />
        <ErrorState error={wf.error} />
      </>
    )
  }
  if (!isNew && !workflow) {
    return (
      <>
        <SectionHeader micro={ws.summary.name} title={t("automation.editor.title")} action={back} />
        <LoadingRows rows={5} />
      </>
    )
  }
  return (
    <Editor
      // Remounted on every new version: a save or a restore resets the draft to
      // exactly what is now stored.
      key={workflow ? `${workflow.id}-${workflow.version}` : "new"}
      workflow={workflow}
      versions={versions.data?.versions ?? []}
      back={back}
      onSaved={() => {
        void wf.mutate()
        void versions.mutate()
      }}
    />
  )
}

type Draft = { name: string; description: string; steps: EditorStep[]; params: WorkflowParam[] }

const snapshot = (d: Draft) =>
  JSON.stringify({ name: d.name.trim(), description: d.description.trim(), steps: fromEditorSteps(d.steps), params: d.params })

function Editor({
  workflow,
  versions,
  back,
  onSaved,
}: {
  workflow?: AutomationWorkflow
  versions: WorkflowVersion[]
  back: React.ReactNode
  onSaved: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const canEdit = ws.can("automation:run")

  const initial = useMemo<Draft>(
    () => ({
      name: workflow?.name ?? "",
      description: workflow?.description ?? "",
      steps: toEditorSteps(workflow?.steps ?? [{ command: "navigate", args: { url: "https://" } }]),
      params: workflow?.params ?? [],
    }),
    [workflow],
  )
  const [draft, setDraft] = useState<Draft>(initial)
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const dirty = snapshot(draft) !== snapshot(initial) || !workflow

  // Leaving with unsaved edits asks first.
  useEffect(() => {
    if (!dirty || !workflow) return
    const warn = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener("beforeunload", warn)
    return () => window.removeEventListener("beforeunload", warn)
  }, [dirty, workflow])

  const browsers = useBrowsers(ws.id)
  const settings = useAutomationSettings(ws.id)
  const usable = (browsers.data?.browsers ?? []).filter((b) => !b.revoked_at)
  const [agentId, setAgentId] = useState("")
  const agent = usable.find((b) => b.id === agentId) ?? usable.find((b) => b.status === "online") ?? usable[0]
  const paused = settings.data?.paused ?? false
  const [values, setValues] = useState<Record<string, string>>({})

  const steps = fromEditorSteps(draft.steps)
  const used = placeholdersIn(steps)

  // Why a browser cannot be used right now — said, never a dead button.
  const browserBlocked = !agent
    ? t("automation.editor.noBrowser")
    : paused
      ? t("automation.pausedTitle")
      : agent.status !== "online"
        ? t("automation.editor.browserOffline", { name: agent.name })
        : ""

  // A page to pick on: the last navigate before the step, with any
  // placeholder filled from the test values or the parameter's default.
  const fillUrl = (raw: string) =>
    raw.replace(/\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}/g, (m, name: string) => {
      const p = draft.params.find((x) => x.name === name)
      if (p?.secret) return m
      return values[name] || p?.default || m
    })

  function setStepArg(index: number, field: string, value: string) {
    setDraft((d) => ({ ...d, steps: d.steps.map((s, i) => (i === index ? { ...s, args: { ...s.args, [field]: value } } : s)) }))
  }

  // ---- test run ----------------------------------------------------------
  const [test, setTest] = useState<{ runId: string; steps: string } | null>(null)
  const [testing, setTesting] = useState(false)
  const [testErr, setTestErr] = useState<string | null>(null)
  const run = useRun(ws.id, test?.runId)
  const runLive = run.data?.run.status === "queued" || run.data?.run.status === "running"
  const statuses = useMemo(() => {
    // Only while the draft is still the steps that were tested: a reordered
    // list would otherwise show each result against the wrong row.
    if (!run.data || !test || test.steps !== JSON.stringify(steps)) return undefined
    const out: Record<number, { status: StepStatus; summary?: string; error?: string }> = {}
    let runningShown = false
    for (const s of run.data.steps) {
      let status: StepStatus = s.status
      if (s.status === "pending" && run.data.run.status === "running" && !runningShown) {
        status = "running"
        runningShown = true
      }
      out[s.position] = { status, summary: s.summary, error: s.error }
    }
    return out
  }, [run.data, test, steps])

  async function testRun() {
    if (!agent) return
    setTesting(true)
    setTestErr(null)
    try {
      const { run_id } = await api<{ run_id: string }>("/api/w/workflows/test", {
        json: { agent_id: agent.id, workflow_id: workflow?.id, steps, params: draft.params.filter((p) => p.name), values },
        workspaceId: ws.id,
      })
      setTest({ runId: run_id, steps: JSON.stringify(steps) })
    } catch (e) {
      setTestErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setTesting(false)
    }
  }

  // ---- save --------------------------------------------------------------
  async function save(e: FormEvent) {
    e.preventDefault()
    setErr(null)
    if (!draft.name.trim()) return setErr(t("automation.editor.needName"))
    if (steps.length === 0) return setErr(t("automation.editor.needStep"))
    const badParam = draft.params.find((p) => !validParamName(p.name))
    if (badParam) return setErr(t("automation.params.badName", { name: badParam.name || "…" }))
    setSaving(true)
    try {
      const body = { name: draft.name.trim(), description: draft.description.trim(), steps, params: draft.params }
      if (!workflow) {
        const { workflow: created } = await api<{ workflow: AutomationWorkflow }>("/api/w/workflows", { json: body, workspaceId: ws.id })
        toast.success(t("automation.editor.created"))
        router.replace(`/${locale}/w/${ws.slug}/automation/workflows/${created.id}`)
        return
      }
      const { workflow: saved } = await api<{ workflow: AutomationWorkflow }>(`/api/w/workflows/${encodeURIComponent(workflow.id)}`, {
        method: "PUT",
        json: { ...body, base_version: workflow.version },
        workspaceId: ws.id,
      })
      toast.success(t("automation.editor.savedAs", { n: saved.version }))
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setSaving(false)
    }
  }

  const fields = [...draft.params.filter((p) => p.name), ...used.filter((n) => !draft.params.some((p) => p.name === n)).map((name) => ({ name }) as WorkflowParam)]

  return (
    <form onSubmit={save} noValidate data-form="workflow-editor" data-version={workflow?.version ?? 0}>
      <SectionHeader
        micro={
          <span className="flex items-center gap-2">
            {ws.summary.name}
            {workflow ? <Badge variant="outline">{t("automation.editor.versionBadge", { n: workflow.version })}</Badge> : null}
          </span>
        }
        title={workflow ? workflow.name : t("automation.editor.newTitle")}
        description={t("automation.editor.hint")}
        action={
          <div className="flex flex-wrap items-center gap-2">
            {back}
            {canEdit ? (
              <Button type="submit" disabled={saving || !dirty} data-save>
                <SaveIcon />
                {saving ? t("common.working") : workflow ? t("automation.editor.saveVersion", { n: workflow.version + 1 }) : t("automation.saveWorkflow")}
              </Button>
            ) : null}
          </div>
        }
      />
      {err ? (
        <FieldError className="mx-6 mb-4" data-save-error>
          {err}
        </FieldError>
      ) : null}
      {workflow && dirty ? <p className="px-6 pb-4 text-xs text-grid-muted">{t("automation.editor.unsaved")}</p> : null}

      <div className="grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="min-w-0 space-y-8">
          <section>
            <FieldGroup className="px-6">
              <div className="grid gap-4 sm:grid-cols-2">
                <Field>
                  <FieldLabel htmlFor="wf-name">{t("automation.workflowName")}</FieldLabel>
                  <Input id="wf-name" value={draft.name} maxLength={80} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} />
                </Field>
                <Field>
                  <FieldLabel htmlFor="wf-desc">
                    {t("automation.workflowDescription")} <span className="text-grid-muted">({t("common.optional")})</span>
                  </FieldLabel>
                  <Input id="wf-desc" value={draft.description} maxLength={400} onChange={(e) => setDraft((d) => ({ ...d, description: e.target.value }))} />
                </Field>
              </div>
            </FieldGroup>
          </section>

          <section>
            <h2 className="px-6 pb-1 text-base font-medium">{t("automation.steps")}</h2>
            <p className="px-6 pb-4 text-sm text-grid-muted">{t("automation.editor.stepsHint")}</p>
            <StepEditor
              steps={draft.steps}
              onChange={(next) => setDraft((d) => ({ ...d, steps: next }))}
              statuses={statuses}
              renderPick={(index, field) => (
                <PickControl
                  id={`${draft.steps[index]?.key}-${field}`}
                  agentId={agent?.id ?? ""}
                  unavailable={browserBlocked || undefined}
                  pageUrl={fillUrl(pageFor(draft.steps, index))}
                  onPicked={(selector) => setStepArg(index, field, selector)}
                />
              )}
            />
          </section>

          <section>
            <h2 className="px-6 pb-1 text-base font-medium">{t("automation.params.title")}</h2>
            <p className="px-6 pb-4 text-sm text-grid-muted">{t("automation.params.hint")}</p>
            <ParamsEditor params={draft.params} used={used} onChange={(next) => setDraft((d) => ({ ...d, params: next }))} />
          </section>
        </div>

        <aside className="min-w-0 space-y-8 lg:border-s lg:border-line">
          {/* ---- test run ---- */}
          <section data-test-panel>
            <h2 className="flex items-center gap-2 px-6 pb-1 text-base font-medium">
              <FlaskConicalIcon className="size-4" />
              {t("automation.editor.testTitle")}
            </h2>
            <p className="px-6 pb-4 text-sm text-grid-muted">{t("automation.editor.testHint")}</p>
            <FieldGroup className="px-6">
              <Field>
                <FieldLabel htmlFor="wf-agent">{t("automation.editor.browser")}</FieldLabel>
                {usable.length > 0 ? (
                  <PickSelect
                    id="wf-agent"
                    value={agent?.id ?? ""}
                    onChange={setAgentId}
                    options={usable.map((b: BrowserAgent) => ({
                      value: b.id,
                      label: `${b.name} (${b.status === "online" ? t("automation.online") : t("automation.offline")})`,
                    }))}
                  />
                ) : (
                  <FieldDescription>
                    {t("automation.editor.noBrowser")}{" "}
                    <Link className="underline" href={`/${locale}/w/${ws.slug}/automation/extension`}>
                      {t("automation.ext.link")}
                    </Link>
                  </FieldDescription>
                )}
              </Field>
              {fields.map((f) => (
                <Field key={f.name}>
                  <FieldLabel htmlFor={`wf-val-${f.name}`}>
                    {f.label || f.name}
                    {f.required ? "" : ` (${t("common.optional")})`}
                  </FieldLabel>
                  <Input
                    id={`wf-val-${f.name}`}
                    type={f.secret ? "password" : "text"}
                    autoComplete={f.secret ? "new-password" : "off"}
                    value={values[f.name] ?? f.default ?? ""}
                    onChange={(e) => setValues((v) => ({ ...v, [f.name]: e.target.value }))}
                  />
                  {f.secret ? <FieldDescription>{t("automation.secretHint")}</FieldDescription> : null}
                </Field>
              ))}
              <div className="flex flex-wrap items-center gap-2">
                <Button type="button" variant="outline" onClick={testRun} disabled={!canEdit || testing || runLive || !!browserBlocked} data-test-run>
                  <FlaskConicalIcon />
                  {testing || runLive ? t("automation.editor.testing") : t("automation.editor.testRun")}
                </Button>
                {browserBlocked && usable.length > 0 ? <span className="text-xs text-grid-muted">{browserBlocked}</span> : null}
              </div>
              {testErr ? <FieldError data-test-error>{testErr}</FieldError> : null}
              {run.data ? (
                <div className="flex flex-wrap items-center gap-2 text-sm" data-test-status={run.data.run.status}>
                  <Badge variant={run.data.run.status === "ok" ? "default" : runLive ? "secondary" : "outline"}>
                    {t(`automation.status.${run.data.run.status}`)}
                  </Badge>
                  <span className="text-grid-muted">{t("automation.stepsDone", { ok: run.data.run.ok_count, total: run.data.run.step_count })}</span>
                  <Link className="text-xs underline" href={`/${locale}/w/${ws.slug}/automation/runs/${run.data.run.id}`}>
                    {t("automation.openRun")}
                  </Link>
                  {run.data.run.error ? <p className="w-full text-xs text-grid-danger-text">{run.data.run.error}</p> : null}
                  {!statuses ? <p className="w-full text-xs text-grid-muted">{t("automation.editor.testStale")}</p> : null}
                </div>
              ) : null}
            </FieldGroup>
          </section>

          {workflow ? <Versions workflow={workflow} versions={versions} dirty={dirty} canEdit={canEdit} onRestored={onSaved} /> : null}
        </aside>
      </div>
      <div className="pb-8" />
    </form>
  )
}

function Versions({
  workflow,
  versions,
  dirty,
  canEdit,
  onRestored,
}: {
  workflow: AutomationWorkflow
  versions: WorkflowVersion[]
  dirty: boolean
  canEdit: boolean
  onRestored: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [confirm, setConfirm] = useState<WorkflowVersion | null>(null)
  const [open, setOpen] = useState<number | null>(null)
  const [busy, setBusy] = useState(false)
  const when = useMemo(() => new Intl.DateTimeFormat(intlTag(locale), { dateStyle: "medium", timeStyle: "short" }), [locale])

  async function restore(v: WorkflowVersion) {
    setBusy(true)
    try {
      const { workflow: now } = await api<{ workflow: AutomationWorkflow }>(
        `/api/w/workflows/${encodeURIComponent(workflow.id)}/versions/${v.version}/restore`,
        { json: { base_version: workflow.version }, workspaceId: ws.id },
      )
      toast.success(t("automation.versions.restored", { from: v.version, n: now.version }))
      setConfirm(null)
      onRestored()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section data-versions>
      <h2 className="flex items-center gap-2 px-6 pb-1 text-base font-medium">
        <HistoryIcon className="size-4" />
        {t("automation.versions.title")}
      </h2>
      <p className="px-6 pb-4 text-sm text-grid-muted">{t("automation.versions.hint")}</p>
      <ol className="divide-y divide-line border-y border-line">
        {versions.map((v) => {
          const current = v.version === workflow.version
          return (
            <li key={v.version} className="px-6 py-3" data-version-row={v.version}>
              <div className="flex flex-wrap items-center gap-2 text-sm">
                <span className="font-medium">{t("automation.editor.versionBadge", { n: v.version })}</span>
                {current ? <Badge>{t("automation.versions.current")}</Badge> : null}
                <span className="text-xs text-grid-muted">
                  {v.note === "restored" && v.restored_from ? t("automation.versions.note.restoredFrom", { n: v.restored_from }) : t(`automation.versions.note.${v.note}`)}
                  {" · "}
                  {when.format(new Date(v.created_at))}
                  {" · "}
                  {t("automation.stepCount", { n: v.steps.length })}
                </span>
              </div>
              <div className="mt-1 flex items-center gap-1">
                <Button type="button" variant="ghost" size="xs" onClick={() => setOpen(open === v.version ? null : v.version)}>
                  {open === v.version ? t("automation.versions.hide") : t("automation.versions.show")}
                </Button>
                {!current && canEdit ? (
                  <Button type="button" variant="ghost" size="xs" onClick={() => setConfirm(v)} data-restore={v.version}>
                    {t("automation.versions.restore")}
                  </Button>
                ) : null}
              </div>
              {open === v.version ? (
                <ol className="mt-2 space-y-1 text-xs text-grid-muted">
                  {v.steps.map((s, i) => (
                    <li key={i} className="flex gap-2">
                      <span className="tabular-nums">{i + 1}.</span>
                      <span dir="ltr" className="ltr-isolate font-mono break-all">
                        {s.command} {String(s.args?.url ?? s.args?.selector ?? s.args?.key ?? "")}
                      </span>
                    </li>
                  ))}
                </ol>
              ) : null}
            </li>
          )
        })}
      </ol>

      <AlertDialog open={!!confirm} onOpenChange={(o) => !o && setConfirm(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("automation.versions.confirmTitle", { n: confirm?.version ?? 0 })}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("automation.versions.confirmBody", { n: confirm?.version ?? 0, next: workflow.version + 1 })}
              {dirty ? ` ${t("automation.versions.confirmDirty")}` : ""}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction disabled={busy} onClick={() => confirm && restore(confirm)} data-restore-confirm>
              {busy ? t("common.working") : t("automation.versions.restore")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}
