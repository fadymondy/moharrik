"use client"

/*
Automation rules (MH-427): when something happens, do something.

Every rule of the workspace as a row — its trigger, its chain drawn small, a
switch to turn it on or off, and how its last run went — with templates for the
three rules most workspaces start from, and a Run log tab listing every
evaluation of every rule, including the ones that did not match, so "why did
it not fire" has an answer.
*/

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Suspense, useMemo } from "react"
import { ChevronRightIcon, FilterIcon, PlusIcon } from "lucide-react"
import { Button, Switch, toast } from "@fadymondy/nasaq/web"

import { actionIcon, TEMPLATES, triggerIcon } from "@/components/automation/rules/rule-model"
import { RunLog, RunStatusIcon } from "@/components/automation/rules/run-view"
import { Bdi, isolate } from "@/components/bidi"
import { ConfirmButton } from "@/components/confirm-button"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { useRuleRuns, useRules, type Rule } from "@/lib/rules"
import { useDocumentTitle } from "@/lib/title"
import { cn } from "@/lib/utils"

export default function RulesPage() {
  return (
    <Suspense fallback={<LoadingRows rows={4} />}>
      <Rules />
    </Suspense>
  )
}

function Rules() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const search = useSearchParams()
  useDocumentTitle(`${t("rules.title")} · ${ws.summary.name}`)
  const canRun = ws.can("automation:run")
  const tab = search.get("tab") === "runs" ? "runs" : "rules"
  const base = `/${locale}/w/${ws.slug}/automation/rules`

  const rules = useRules(ws.id)
  const runs = useRuleRuns(ws.id)
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "rules") {
      void rules.mutate()
      void runs.mutate()
    }
  })
  const allRuns = (runs.data ?? []).flatMap((p) => p.runs)

  const setTab = (next: string) =>
    router.replace(`${base}${next === "runs" ? "?tab=runs" : ""}`, { scroll: false })
  const create = canRun ? (
    <Button nativeButton={false} render={<Link href={`${base}/new`} />} data-create-rule>
      <PlusIcon />
      {t("rules.new")}
    </Button>
  ) : undefined

  const list = rules.data?.rules ?? []
  return (
    <>
      <SectionHeader
        title={t("rules.title")}
        description={t("rules.hint")}
        action={create}
      />

      <div className="flex flex-wrap items-center gap-3 border-b border-nq-line px-6">
        {(["rules", "runs"] as const).map((id) => (
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
            data-rules-tab={id}
          >
            {t(`rules.tab.${id}`)}
          </button>
        ))}
      </div>

      {tab === "rules" ? (
        <section data-rule-list className="pt-4">
          {rules.error ? (
            <ErrorState error={rules.error} />
          ) : rules.isLoading ? (
            <LoadingRows rows={3} />
          ) : list.length === 0 ? (
            <EmptyState
              title={t("rules.none")}
              body={canRun ? t("rules.noneBody") : t("rules.noneReadOnly")}
              action={create}
            />
          ) : (
            <ul className="divide-y divide-nq-line border-y border-nq-line">
              {list.map((r) => (
                <RuleRow
                  key={r.id}
                  rule={r}
                  href={`${base}/${r.id}`}
                  canRun={canRun}
                  onChanged={() => void rules.mutate()}
                />
              ))}
            </ul>
          )}
          {canRun ? (
            <div className="px-6 pt-8">
              <h2 className="text-sm font-medium">{t("rules.templates")}</h2>
              <p className="mb-3 text-sm text-muted-foreground">{t("rules.templatesHint")}</p>
              <ul className="grid gap-3 md:grid-cols-3" data-templates>
                {TEMPLATES.map((tpl) => (
                  <li key={tpl.id}>
                    <Link
                      href={`${base}/new?template=${tpl.id}`}
                      className="block h-full rounded-lg border border-nq-line bg-card p-4 transition-colors hover:border-primary"
                      data-template={tpl.id}
                    >
                      <p className="text-sm font-medium">{t(`rules.tpl.${tpl.id}.name`)}</p>
                      <p className="mt-1 text-xs text-muted-foreground">
                        {t(`rules.tpl.${tpl.id}.hint`)}
                      </p>
                    </Link>
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
        </section>
      ) : (
        <div className="pt-4">
          <RunLog
            runs={allRuns}
            loading={!runs.data && !runs.error}
            error={runs.error}
            more={!!runs.data?.[runs.data.length - 1]?.next}
            onMore={() => runs.setSize(runs.size + 1)}
          />
        </div>
      )}
      <div className="pb-8" />
    </>
  )
}

/** The rule's chain drawn small: trigger, IF, then an icon per action. */
function Chain({ rule }: { rule: Rule }) {
  const Trigger = triggerIcon(rule.trigger)
  return (
    <div className="flex items-center" aria-hidden data-chain={rule.actions.length}>
      <span className="flex size-7 items-center justify-center rounded-s-full rounded-e-md border border-nq-line bg-card">
        <Trigger className="size-3.5 text-nq-accent" />
      </span>
      <span className="h-px w-2.5 bg-nq-fg-muted/60" />
      <span className="flex size-7 items-center justify-center rounded-full border border-nq-line bg-card">
        <FilterIcon className="size-3.5 text-nq-accent" />
      </span>
      {rule.actions.slice(0, 5).map((a, i) => {
        const Icon = actionIcon(a.type)
        return (
          <span key={i} className="flex items-center">
            <span className="h-px w-2.5 bg-nq-fg-muted/60" />
            <span className="flex size-7 items-center justify-center rounded-md border border-nq-line bg-card">
              <Icon className="size-3.5 text-primary" />
            </span>
          </span>
        )
      })}
    </div>
  )
}

function RuleRow({
  rule,
  href,
  canRun,
  onChanged,
}: {
  rule: Rule
  href: string
  canRun: boolean
  onChanged: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const when = useMemo(
    () => new Intl.DateTimeFormat(intlTag(locale), { dateStyle: "medium", timeStyle: "short" }),
    [locale],
  )

  async function toggle(on: boolean) {
    try {
      await api(`/api/w/rules/${encodeURIComponent(rule.id)}`, {
        method: "PATCH",
        json: { enabled: on },
        workspaceId: ws.id,
      })
      toast.success(on ? t("rules.turnedOn") : t("rules.turnedOff"))
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }
  async function remove() {
    try {
      await api(`/api/w/rules/${encodeURIComponent(rule.id)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      toast.success(t("rules.deleted"))
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const status = rule.last_status
  return (
    <li
      className="flex flex-wrap items-center gap-x-6 gap-y-3 px-6 py-4 transition-colors hover:bg-nq-surface-soft"
      data-rule-row={rule.name}
      data-enabled={rule.enabled ? "true" : "false"}
    >
      <Link href={href} className="min-w-0 flex-1 basis-64 space-y-1.5" data-open-rule={rule.id}>
        <p className="text-sm font-medium">
          <Bdi>{rule.name}</Bdi>
        </p>
        <p className="text-xs text-muted-foreground">
          {t("rules.whenShort", { event: t(`rules.trigger.${rule.trigger}`) })} ·{" "}
          {t("rules.counts", { c: rule.conditions.length, a: rule.actions.length })}
        </p>
      </Link>
      <Link href={href} className="hidden lg:block" tabIndex={-1}>
        <Chain rule={rule} />
      </Link>
      <div className="flex w-48 items-center gap-1.5 text-xs" data-last-status={status || "never"}>
        {status ? (
          <>
            <RunStatusIcon status={status} className="size-3.5" />
            <span className="min-w-0">
              {t(`rules.status.${status}`)}
              {rule.last_run_at ? (
                <span className="block text-muted-foreground">
                  {when.format(new Date(rule.last_run_at))}
                </span>
              ) : null}
            </span>
          </>
        ) : (
          <span className="text-muted-foreground">{t("rules.neverRan")}</span>
        )}
      </div>
      <label className="flex items-center gap-2 text-xs">
        <Switch
          checked={rule.enabled}
          disabled={!canRun}
          onCheckedChange={(v) => void toggle(!!v)}
          aria-label={t("rules.enabled")}
          data-rule-switch={rule.id}
        />
        {rule.enabled ? t("rules.on") : t("rules.off")}
      </label>
      <div className="flex items-center gap-1">
        {canRun ? (
          <ConfirmButton
            label={t("common.delete")}
            title={t("rules.deleteTitle")}
            description={t("rules.deleteBody", { name: isolate(rule.name) })}
            confirmLabel={t("common.delete")}
            onConfirm={remove}
          />
        ) : null}
        <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={href} />}>
          {t("rules.open")}
          <ChevronRightIcon className="rtl:rotate-180" />
        </Button>
      </div>
    </li>
  )
}
