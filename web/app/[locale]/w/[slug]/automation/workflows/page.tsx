"use client"

/*
The workflows list (MH-480), as n8n's overview has it: every workflow of the
workspace as a row you can open straight into its canvas — with the chain of
its steps drawn small beside the name, so a workflow is recognised by its shape
before it is opened — and an Executions tab listing every run of any of them.
*/

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Suspense, useMemo, useState } from "react"
import { ChevronRightIcon, PlusIcon, SearchIcon, SparklesIcon, ZapIcon } from "lucide-react"

import { ExecutionIcon, useDuration } from "@/components/automation/canvas/executions-view"
import { kindOf, specOf } from "@/components/automation/canvas/model"
import { ConfirmButton } from "@/components/confirm-button"
import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge, Button, Input, toast } from "@fadymondy/nasaq/web"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { Bdi, isolate } from "@/components/bidi"
import { api, ApiError, type AutomationWorkflow } from "@/lib/api"
import { useExecutions, type WorkflowExecution } from "@/lib/canvas-queries"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { useWorkflows } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { cn } from "@/lib/utils"

export default function WorkflowsPage() {
  return (
    <Suspense fallback={<LoadingRows rows={4} />}>
      <Workflows />
    </Suspense>
  )
}

/** "3 hours ago", in the page's language. */
function useRelative() {
  const { locale } = useTranslations()
  return useMemo(() => {
    const rtf = new Intl.RelativeTimeFormat(intlTag(locale), { numeric: "auto" })
    return (iso: string) => {
      const s = (new Date(iso).getTime() - Date.now()) / 1000
      const steps: [number, Intl.RelativeTimeFormatUnit][] = [
        [60, "second"],
        [3600, "minute"],
        [86400, "hour"],
        [604800, "day"],
        [2629800, "week"],
        [31557600, "month"],
        [Infinity, "year"],
      ]
      const div: Record<string, number> = {
        second: 1,
        minute: 60,
        hour: 3600,
        day: 86400,
        week: 604800,
        month: 2629800,
        year: 31557600,
      }
      for (const [limit, unit] of steps)
        if (Math.abs(s) < limit) return rtf.format(Math.round(s / div[unit]), unit)
      return ""
    }
  }, [locale])
}

function Workflows() {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const search = useSearchParams()
  useDocumentTitle(`${t("workflows.title")} · ${ws.summary.name}`)
  const canRun = ws.can("automation:run")
  const tab = search.get("tab") === "executions" ? "executions" : "workflows"
  const base = `/${locale}/w/${ws.slug}/automation`

  const workflows = useWorkflows(ws.id)
  const executions = useExecutions(ws.id)
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "run" || e.kind === "schedule") {
      void executions.mutate()
      void workflows.mutate()
    }
  })
  const runs = (executions.data ?? []).flatMap((p) => p.executions)
  // The newest run of each workflow, from the executions already loaded.
  const lastRun = useMemo(() => {
    const m = new Map<string, WorkflowExecution>()
    for (const r of runs) if (!m.has(r.workflow_id)) m.set(r.workflow_id, r)
    return m
  }, [runs])

  const [q, setQ] = useState("")
  const [sort, setSort] = useState("updated")
  const list = useMemo(() => {
    const needle = q.trim().toLowerCase()
    const all = (workflows.data?.workflows ?? []).filter(
      (w) => !needle || `${w.name} ${w.description}`.toLowerCase().includes(needle),
    )
    return [...all].sort((a, b) =>
      sort === "name"
        ? a.name.localeCompare(b.name, locale)
        : sort === "created"
          ? b.created_at.localeCompare(a.created_at)
          : b.updated_at.localeCompare(a.updated_at),
    )
  }, [workflows.data, q, sort, locale])

  const setTab = (next: string) =>
    router.replace(`${base}/workflows${next === "executions" ? "?tab=executions" : ""}`, {
      scroll: false,
    })
  const create = canRun ? (
    <div className="flex flex-wrap items-center gap-2">
      {/* New workflow → from a preset (MH-478): the preset gallery. */}
      <Button
        variant="secondary"
        nativeButton={false}
        render={<Link href={`${base}/presets`} />}
        data-create-from-preset
      >
        <SparklesIcon />
        {t("presets.fromPreset")}
      </Button>
      <Button
        nativeButton={false}
        render={<Link href={`${base}/workflows/new`} />}
        data-create-workflow
      >
        <PlusIcon />
        {t("workflows.new")}
      </Button>
    </div>
  ) : undefined

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("workflows.title")}
        description={t("workflows.hint")}
        action={create}
      />

      <div className="flex flex-wrap items-center gap-3 border-b border-nq-line px-6">
        {(["workflows", "executions"] as const).map((id) => (
          <button
            key={id}
            type="button"
            onClick={() => setTab(id)}
            className={cn(
              "-mb-px border-b-2 px-1 py-2.5 text-sm font-medium transition-colors",
              tab === id
                ? "border-nq-action text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
            aria-current={tab === id ? "page" : undefined}
            data-list-tab={id}
          >
            {t(`workflows.tab.${id}`)}
          </button>
        ))}
      </div>

      {tab === "workflows" ? (
        <section data-workflow-list>
          <div className="flex flex-wrap items-center gap-3 px-6 py-4">
            <div className="relative min-w-0 flex-1 sm:max-w-xs">
              <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
              <Input
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder={t("workflows.search")}
                aria-label={t("workflows.search")}
                className="ps-8"
                data-workflow-search
              />
            </div>
            <div className="w-44">
              <PickSelect
                id="workflow-sort"
                value={sort}
                onChange={setSort}
                options={[
                  { value: "updated", label: t("workflows.sort.updated") },
                  { value: "created", label: t("workflows.sort.created") },
                  { value: "name", label: t("workflows.sort.name") },
                ]}
              />
            </div>
          </div>
          {workflows.error ? (
            <ErrorState error={workflows.error} />
          ) : workflows.isLoading ? (
            <LoadingRows rows={3} />
          ) : (workflows.data?.workflows ?? []).length === 0 ? (
            <EmptyState
              title={t("workflows.none")}
              body={canRun ? t("workflows.noneBody") : undefined}
              action={create}
            />
          ) : list.length === 0 ? (
            <EmptyState title={t("workflows.noMatch", { q })} />
          ) : (
            <ul className="divide-y divide-nq-line border-y border-nq-line">
              {list.map((w) => (
                <WorkflowRow
                  key={w.id}
                  workflow={w}
                  last={lastRun.get(w.id)}
                  href={`${base}/workflows/${w.id}`}
                  canRun={canRun}
                  created={formatDate(w.created_at)}
                  onDeleted={() => void workflows.mutate()}
                />
              ))}
            </ul>
          )}
        </section>
      ) : (
        <AllExecutions
          runs={runs}
          loading={!executions.data && !executions.error}
          error={executions.error}
          more={!!executions.data?.[executions.data.length - 1]?.next_before}
          onMore={() => executions.setSize(executions.size + 1)}
          base={base}
        />
      )}
      <div className="pb-8" />
    </>
  )
}

/** A workflow's steps drawn small: its trigger, then an icon per step. */
function Chain({ workflow }: { workflow: AutomationWorkflow }) {
  const { t } = useTranslations()
  const max = 6
  const shown = workflow.steps.slice(0, max)
  return (
    <div className="flex items-center" aria-hidden data-chain={workflow.steps.length}>
      <span className="flex size-7 items-center justify-center rounded-s-full rounded-e-md border border-nq-line bg-card">
        <ZapIcon className="size-3.5 text-nq-accent" />
      </span>
      {shown.map((s, i) => {
        const Icon = specOf(kindOf(s)).icon
        return (
          <span key={i} className="flex items-center">
            <span className="h-px w-2.5 bg-nq-fg-muted/60" />
            <span className="flex size-7 items-center justify-center rounded-md border border-nq-line bg-card">
              <Icon className="size-3.5 text-foreground" />
            </span>
          </span>
        )
      })}
      {workflow.steps.length > max ? (
        <span className="ms-1.5 text-xs text-muted-foreground">
          {t("workflows.moreSteps", { n: workflow.steps.length - max })}
        </span>
      ) : null}
    </div>
  )
}

function WorkflowRow({
  workflow,
  last,
  href,
  canRun,
  created,
  onDeleted,
}: {
  workflow: AutomationWorkflow
  last?: WorkflowExecution
  href: string
  canRun: boolean
  created: string
  onDeleted: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const relative = useRelative()

  async function remove() {
    try {
      await api(`/api/w/workflows/${encodeURIComponent(workflow.id)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      toast.success(t("automation.workflowDeleted"))
      onDeleted()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <li
      className="group flex flex-wrap items-center gap-x-6 gap-y-3 px-6 py-4 transition-colors hover:bg-nq-surface-soft"
      data-workflow-row={workflow.name}
    >
      <Link
        href={href}
        className="min-w-0 flex-1 basis-64 space-y-1.5"
        data-open-workflow={workflow.id}
      >
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          <Bdi>{workflow.name}</Bdi>
          <Badge variant="outline">
            {t("automation.editor.versionBadge", { n: workflow.version })}
          </Badge>
          {workflow.writes ? (
            <Badge variant="neutral" data-workflow-writes>
              {t("presets.publishes")}
            </Badge>
          ) : null}
        </p>
        {workflow.description ? (
          <p className="line-clamp-1 text-sm text-muted-foreground">
            <Bdi>{workflow.description}</Bdi>
          </p>
        ) : null}
        <p className="text-xs text-muted-foreground">
          {t("workflows.updated", { when: relative(workflow.updated_at) })} ·{" "}
          {t("workflows.created", { date: created })} ·{" "}
          {t("automation.stepCount", { n: workflow.steps.length })}
        </p>
      </Link>
      <Link href={href} className="hidden lg:block" tabIndex={-1}>
        <Chain workflow={workflow} />
      </Link>
      <div className="flex w-40 items-center gap-2 text-xs" data-last-run={last?.status ?? "never"}>
        {last ? (
          <Link
            href={`${href}?view=executions&run=${last.id}`}
            className="flex items-center gap-1.5 hover:underline"
            title={t("workflows.lastRun")}
          >
            <ExecutionIcon status={last.status} className="size-3.5" />
            <span>
              {t(`automation.status.${last.status}`)} · {relative(last.created_at)}
            </span>
          </Link>
        ) : (
          <span className="text-muted-foreground">{t("workflows.never")}</span>
        )}
      </div>
      <div className="flex items-center gap-1">
        {canRun ? (
          <ConfirmButton
            label={t("common.delete")}
            title={t("automation.deleteWorkflowTitle")}
            description={t("automation.deleteWorkflowBody", { name: isolate(workflow.name) })}
            confirmLabel={t("common.delete")}
            onConfirm={remove}
          />
        ) : null}
        <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={href} />}>
          {t("workflows.open")}
          <ChevronRightIcon className="rtl:rotate-180" />
        </Button>
      </div>
    </li>
  )
}

function AllExecutions({
  runs,
  loading,
  error,
  more,
  onMore,
  base,
}: {
  runs: WorkflowExecution[]
  loading: boolean
  error: unknown
  more: boolean
  onMore: () => void
  base: string
}) {
  const { t, locale } = useTranslations()
  const duration = useDuration()
  const when = useMemo(
    () => new Intl.DateTimeFormat(intlTag(locale), { dateStyle: "medium", timeStyle: "short" }),
    [locale],
  )
  if (error) return <ErrorState error={error} />
  if (loading) return <LoadingRows rows={4} />
  if (runs.length === 0)
    return (
      <div className="pt-4">
        <EmptyState title={t("workflows.exec.none")} body={t("workflows.exec.noneBody")} />
      </div>
    )
  return (
    <section className="pt-4" data-all-executions>
      <div className="overflow-x-auto">
        <table className="w-full min-w-[640px] text-sm">
          <thead className="text-xs text-muted-foreground">
            <tr className="border-y border-nq-line">
              <th className="px-6 py-2 text-start font-normal">{t("workflows.exec.workflow")}</th>
              <th className="px-2 py-2 text-start font-normal">{t("workflows.exec.status")}</th>
              <th className="px-2 py-2 text-start font-normal">{t("workflows.exec.started")}</th>
              <th className="px-2 py-2 text-start font-normal">{t("workflows.exec.took")}</th>
              <th className="px-6 py-2 text-start font-normal">{t("workflows.exec.how")}</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-nq-line border-b border-nq-line">
            {runs.map((r) => (
              <tr
                key={r.id}
                className="hover:bg-nq-surface-soft"
                data-execution-row={r.id}
                data-execution-status={r.status}
              >
                <td className="px-6 py-3">
                  <Link
                    href={`${base}/workflows/${r.workflow_id}?view=executions&run=${r.id}`}
                    className="font-medium hover:underline"
                  >
                    <Bdi>{r.workflow_name || r.workflow_id}</Bdi>
                  </Link>
                </td>
                <td className="px-2 py-3">
                  <span className="flex items-center gap-1.5">
                    <ExecutionIcon status={r.status} className="size-3.5" />
                    {t(`automation.status.${r.status}`)}
                    <span className="text-xs text-muted-foreground">
                      ({t("automation.stepsDone", { ok: r.ok_count, total: r.step_count })})
                    </span>
                  </span>
                </td>
                <td className="px-2 py-3 whitespace-nowrap text-muted-foreground">
                  {when.format(new Date(r.created_at))}
                </td>
                <td className="px-2 py-3 whitespace-nowrap text-muted-foreground">
                  {duration(r) || "—"}
                </td>
                <td className="px-6 py-3">
                  <Badge variant="outline">{t(`canvas.exec.trigger.${r.trigger}`)}</Badge>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {more ? (
        <div className="px-6 pt-4">
          <Button variant="secondary" size="sm" onClick={onMore}>
            {t("common.loadMore")}
          </Button>
        </div>
      ) : null}
    </section>
  )
}
