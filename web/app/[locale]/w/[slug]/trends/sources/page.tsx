"use client"

import Link from "next/link"
import { useState, type FormEvent, type ReactNode } from "react"
import {
  ArrowLeftIcon,
  CheckIcon,
  EllipsisIcon,
  GaugeIcon,
  LinkIcon,
  PencilIcon,
  PlusIcon,
  RefreshCwIcon,
  RssIcon,
  TagsIcon,
  Trash2Icon,
  TriangleAlertIcon,
} from "lucide-react"
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger, Field, FieldDescription, FieldLabel, Input, Switch, toast, Toggle, ToggleGroup } from "@fadymondy/nasaq/web"
import { FieldError } from "@/components/form-layout"

import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { INTERVALS, SourceStatus, useWhen } from "@/components/trends/shared"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  type FeedCatalogEntry,
  type FeedSource,
  type Topic,
  type TrendSettings,
} from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useFeedCatalog, useFeedSources, useTopics, useTrendSettings } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { isolate } from "@/components/bidi"

const CATEGORIES = ["technology", "business", "world", "industry"] as const

function errText(e: unknown, fallback: string) {
  return e instanceof ApiError ? e.message : fallback
}

/**
 * Where trends come from (MH-387). Three parts, in the order a person uses
 * them: the sources this workspace reads (each with its own schedule and
 * status), the catalog to add more from in one click, and the keyword topics
 * that make a story relevant. The budget sits above all three, because it is
 * what every read spends. Adding by URL, renaming and editing a topic happen
 * in dialogs, so the page itself is lists, not stacks of empty inputs.
 */
export default function TrendSourcesPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("trends.sources")} · ${ws.summary.name}`)
  const canManage = ws.can("trends:manage")
  const sources = useFeedSources(ws.id)
  const topics = useTopics(ws.id)
  const catalog = useFeedCatalog(ws.id)
  const settings = useTrendSettings(ws.id)
  const [addingUrl, setAddingUrl] = useState(false)
  const [addingTopic, setAddingTopic] = useState(false)
  const refreshSources = () => {
    void sources.mutate()
    void catalog.mutate()
    void settings.mutate()
  }
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "trends") refreshSources()
  })

  const list = sources.data?.sources ?? []
  const topicList = topics.data?.topics ?? []

  return (
    <>
      <div className="px-6 pt-6">
        <Button
          variant="ghost"
          size="sm"
          nativeButton={false}
          render={<Link href={`/${locale}/w/${ws.slug}/trends`} />}
          data-back
        >
          <ArrowLeftIcon className="rtl:rotate-180" />
          {t("trends.backToTopics")}
        </Button>
      </div>
      <SectionHeader
        title={t("trends.sources")}
        description={t("trends.sourcesHint")}
        action={
          canManage ? (
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setAddingUrl(true)}
              data-action="add-by-url"
            >
              <LinkIcon />
              {t("trends.addByUrl")}
            </Button>
          ) : undefined
        }
      />

      {settings.data ? (
        <Budget
          settings={settings.data.settings}
          canManage={canManage}
          onSaved={() => void settings.mutate()}
        />
      ) : null}

      {/* ---- your sources ---- */}
      <Section
        id="sources"
        icon={<RssIcon />}
        title={t("trends.yourSources")}
        count={sources.data ? list.length : undefined}
        hint={t("trends.feedsHint")}
      >
        {sources.error ? (
          <ErrorState error={sources.error} />
        ) : sources.isLoading ? (
          <LoadingRows rows={3} />
        ) : list.length === 0 ? (
          <EmptyState
            title={t("trends.noFeeds")}
            body={canManage ? t("trends.noFeedsBody") : undefined}
          />
        ) : (
          <ul className="divide-y divide-nq-line border-y border-nq-line">
            {list.map((s) => (
              <SourceRow key={s.id} source={s} canManage={canManage} onChange={refreshSources} />
            ))}
          </ul>
        )}
      </Section>

      {/* ---- the catalog ---- */}
      {canManage ? (
        <Section
          id="catalog"
          icon={<PlusIcon />}
          title={t("trends.catalog")}
          hint={t("trends.catalogHint")}
        >
          {catalog.error ? (
            <ErrorState error={catalog.error} />
          ) : !catalog.data ? (
            <LoadingRows rows={3} />
          ) : (
            <Catalog entries={catalog.data.entries} onAdded={refreshSources} />
          )}
        </Section>
      ) : null}

      {/* ---- keyword topics ---- */}
      <Section
        id="topics"
        icon={<TagsIcon />}
        title={t("trends.topics")}
        count={topics.data ? topicList.length : undefined}
        hint={t("trends.topicsHint")}
        action={
          canManage ? (
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setAddingTopic(true)}
              data-action="new-topic"
            >
              <PlusIcon />
              {t("trends.newTopic")}
            </Button>
          ) : undefined
        }
      >
        {topics.error ? (
          <ErrorState error={topics.error} />
        ) : topics.isLoading ? (
          <LoadingRows rows={2} />
        ) : topicList.length === 0 ? (
          <EmptyState
            title={t("trends.noTopics")}
            body={canManage ? t("trends.noTopicsBody") : undefined}
          />
        ) : (
          <ul className="divide-y divide-nq-line border-y border-nq-line">
            {topicList.map((tp) => (
              <TopicRow
                key={tp.id}
                topic={tp}
                canManage={canManage}
                onChange={() => void topics.mutate()}
              />
            ))}
          </ul>
        )}
      </Section>
      <div className="pb-10" />

      {canManage ? (
        <>
          <AddByUrlDialog open={addingUrl} onOpenChange={setAddingUrl} onAdded={refreshSources} />
          <TopicDialog
            open={addingTopic}
            onOpenChange={setAddingTopic}
            onSaved={() => void topics.mutate()}
          />
        </>
      ) : null}
    </>
  )
}

function Section({
  id,
  icon,
  title,
  count,
  hint,
  action,
  children,
}: {
  id: string
  icon: ReactNode
  title: string
  count?: number
  hint: string
  action?: ReactNode
  children: ReactNode
}) {
  const { formatNumber } = useTranslations()
  return (
    <section className="pt-10" data-section={id} aria-labelledby={`section-${id}`}>
      <div className="flex flex-wrap items-end justify-between gap-3 px-6 pb-4">
        <div className="min-w-0 space-y-1">
          <h2
            id={`section-${id}`}
            className="flex items-center gap-2 text-lg font-medium [&_svg]:size-4 [&_svg]:text-muted-foreground"
          >
            {icon}
            {title}
            {count !== undefined ? (
              <Badge variant="neutral" className="tabular-nums">
                {formatNumber(count)}
              </Badge>
            ) : null}
          </h2>
          <p className="max-w-2xl text-sm text-muted-foreground">{hint}</p>
        </div>
        {action}
      </div>
      {children}
    </section>
  )
}

function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  onConfirm,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  title: string
  description: string
  confirmLabel: string
  onConfirm: () => Promise<void>
}) {
  const { t } = useTranslations()
  const [busy, setBusy] = useState(false)
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction
            variant="danger"
            disabled={busy}
            onClick={async () => {
              setBusy(true)
              try {
                await onConfirm()
                onOpenChange(false)
              } catch (e) {
                toast.error(errText(e, t("common.networkError")))
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? t("common.working") : confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

// ---- the budget ---------------------------------------------------------------

function Budget({
  settings,
  canManage,
  onSaved,
}: {
  settings: TrendSettings
  canManage: boolean
  onSaved: () => void
}) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [editing, setEditing] = useState(false)
  const [value, setValue] = useState(String(settings.fetch_budget_per_day))
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const pct = Math.min(
    100,
    Math.round((settings.fetches_today / Math.max(1, settings.fetch_budget_per_day)) * 100),
  )
  const short = settings.scheduled_per_day > settings.fetch_budget_per_day

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/trend_settings", {
        method: "PATCH",
        json: { fetch_budget_per_day: Number(value) },
        workspaceId: ws.id,
      })
      toast.success(t("trends.budgetSaved"))
      onSaved()
      setEditing(false)
    } catch (e) {
      setErr(errText(e, t("common.networkError")))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section
      className="mx-6 flex flex-col gap-3 rounded-lg border border-nq-line p-4 sm:p-5"
      data-section="budget"
      aria-labelledby="budget-title"
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="budget-title" className="flex items-center gap-2 text-base font-medium">
          <GaugeIcon className="size-4 text-muted-foreground" aria-hidden />
          {t("trends.budget")}
        </h2>
        <div className="flex items-center gap-3">
          <p
            className="text-sm tabular-nums"
            data-budget-used={`${settings.fetches_today}/${settings.fetch_budget_per_day}`}
          >
            {t("trends.budgetUsed", {
              used: formatNumber(settings.fetches_today),
              budget: formatNumber(settings.fetch_budget_per_day),
            })}
          </p>
          {canManage ? (
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setValue(String(settings.fetch_budget_per_day))
                setErr(null)
                setEditing(true)
              }}
              data-action="change-budget"
            >
              <PencilIcon />
              {t("trends.budgetChange")}
            </Button>
          ) : null}
        </div>
      </div>
      <div
        className="h-1.5 w-full overflow-hidden rounded-full bg-muted"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct}
        aria-label={t("trends.budget")}
      >
        <div
          className="h-full rounded-full bg-primary transition-[width]"
          style={{ width: `${pct}%` }}
        />
      </div>
      <p className="text-sm text-muted-foreground">
        {t("trends.budgetHint")}{" "}
        {t("trends.budgetScheduled", { n: formatNumber(settings.scheduled_per_day) })}
      </p>
      {short ? (
        <p className="flex items-start gap-1.5 text-sm text-nq-danger-text" data-budget-short>
          <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" aria-hidden />
          {t("trends.budgetShort")}
        </p>
      ) : null}
      <Dialog open={editing} onOpenChange={setEditing}>
        <DialogContent>
          <form onSubmit={save} className="flex flex-col gap-4" data-form="budget">
            <DialogHeader>
              <DialogTitle>{t("trends.budgetChange")}</DialogTitle>
              <DialogDescription>
                {t("trends.budgetChangeHint", { max: formatNumber(settings.max_budget) })}
              </DialogDescription>
            </DialogHeader>
            <Field>
              <FieldLabel htmlFor="budget">{t("trends.budgetPerDay")}</FieldLabel>
              <Input
                id="budget"
                type="number"
                inputMode="numeric"
                min={1}
                max={settings.max_budget}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                dir="ltr"
                className="w-40"
              />
              <FieldDescription>
                {t("trends.budgetScheduled", { n: formatNumber(settings.scheduled_per_day) })}
              </FieldDescription>
            </Field>
            {err ? <FieldError>{err}</FieldError> : null}
            <DialogFooter>
              <Button type="button" variant="ghost" onClick={() => setEditing(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={busy || !value}>
                {busy ? t("common.working") : t("common.save")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </section>
  )
}

// ---- your sources -----------------------------------------------------------------

function ProviderBadge({ provider }: { provider: string }) {
  const { t } = useTranslations()
  return (
    <Badge variant="outline">
      {provider === "feed" ? t("trends.provider.feed") : t("trends.provider.api")}
    </Badge>
  )
}

/** A labelled fact in a source's meta line. */
function Fact({ label, children }: { label?: string; children: ReactNode }) {
  return (
    <span className="inline-flex min-w-0 items-baseline gap-1">
      {label ? <span className="text-muted-foreground">{label}</span> : null}
      <span className="min-w-0">{children}</span>
    </span>
  )
}

function SourceRow({
  source,
  canManage,
  onChange,
}: {
  source: FeedSource
  canManage: boolean
  onChange: () => void
}) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const when = useWhen()
  const [busy, setBusy] = useState(false)
  const [renaming, setRenaming] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const path = `/api/w/feed_sources/${encodeURIComponent(source.id)}`
  const nextDue = source.next_run_at && new Date(source.next_run_at).getTime() <= Date.now()

  async function patch(json: Record<string, unknown>) {
    try {
      await api(path, { method: "PATCH", json, workspaceId: ws.id })
      onChange()
    } catch (e) {
      toast.error(errText(e, t("common.networkError")))
    }
  }

  async function fetchNow() {
    setBusy(true)
    try {
      const res = await api<{ added: number; source: FeedSource }>(`${path}/fetch`, {
        method: "POST",
        workspaceId: ws.id,
      })
      if (res.source.last_status === "over_budget") toast.warning(t("trends.fetchedOverBudget"))
      else if (res.source.last_status === "failed") toast.error(res.source.last_error)
      else toast.success(t("trends.fetched", { n: formatNumber(res.added) }))
      onChange()
    } catch (e) {
      toast.error(errText(e, t("common.networkError")))
    } finally {
      setBusy(false)
    }
  }

  return (
    <li
      className="grid gap-3 px-6 py-4 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center"
      data-feed-row={source.name}
      data-provider={source.provider}
    >
      <div className="min-w-0 space-y-1">
        <p className="flex flex-wrap items-center gap-2">
          <bdi className="text-sm font-medium">{source.name}</bdi>
          <ProviderBadge provider={source.provider} />
          <SourceStatus source={source} />
        </p>
        <Ltr className="block truncate text-xs text-muted-foreground" mono>
          {source.url}
        </Ltr>
        <p className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          <Fact>
            <span data-items-count>
              {t("trends.itemsCollected", { n: formatNumber(source.item_count) })}
            </span>
          </Fact>
          <Fact>
            {source.last_fetched_at
              ? `${t("trends.lastFetchedWhen", { when: when(source.last_fetched_at) })}${source.last_status === "ok" ? ` (${t("trends.lastAdded", { n: formatNumber(source.last_added) })})` : ""}`
              : t("trends.neverRead")}
          </Fact>
          {source.enabled && source.next_run_at ? (
            <Fact>
              <span data-next-run>
                {nextDue
                  ? t("trends.nextRunSoon")
                  : t("trends.nextRun", { when: when(source.next_run_at) })}
              </span>
            </Fact>
          ) : null}
          {!canManage ? <Fact>{t(`trends.interval.${source.interval_minutes}`)}</Fact> : null}
        </p>
        {source.last_error && source.last_status !== "ok" ? (
          <p className="flex items-start gap-1 text-xs text-nq-danger-text" data-source-error>
            <TriangleAlertIcon className="mt-0.5 size-3 shrink-0" aria-hidden />
            <bdi>
              {source.last_status === "over_budget"
                ? t("trends.fetchedOverBudget")
                : source.last_error}
            </bdi>
          </p>
        ) : null}
      </div>
      {canManage ? (
        <div className="flex flex-wrap items-center gap-2 lg:justify-end">
          <div className="w-40">
            <PickSelect
              id={`interval-${source.id}`}
              value={String(source.interval_minutes)}
              onChange={(v) => patch({ interval_minutes: Number(v) })}
              options={INTERVALS.map((m) => ({
                value: String(m),
                label: t(`trends.interval.${m}`),
              }))}
            />
          </div>
          <Switch
            checked={source.enabled}
            onCheckedChange={() => patch({ enabled: !source.enabled })}
            aria-label={t("trends.collectThis", { name: source.name })}
          />
          <Button
            variant="secondary"
            size="sm"
            onClick={fetchNow}
            disabled={busy || !source.enabled}
            data-action="fetch"
          >
            <RefreshCwIcon className={busy ? "animate-spin" : undefined} />
            {t("trends.fetchNow")}
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger
              render={
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={t("trends.moreActions", { name: source.name })}
                  data-row-actions
                />
              }
            >
              <EllipsisIcon />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="min-w-40">
              <DropdownMenuItem onClick={() => setRenaming(true)} data-action="rename">
                <PencilIcon />
                {t("trends.rename")}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                variant="danger"
                onClick={() => setDeleting(true)}
                data-action="delete"
              >
                <Trash2Icon />
                {t("common.delete")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <RenameDialog
            open={renaming}
            onOpenChange={setRenaming}
            title={t("trends.renameSource")}
            value={source.name}
            onSave={async (name) => {
              await api(path, { method: "PATCH", json: { name }, workspaceId: ws.id })
              toast.success(t("trends.sourceSaved"))
              onChange()
            }}
          />
          <ConfirmDialog
            open={deleting}
            onOpenChange={setDeleting}
            title={t("trends.deleteFeedTitle")}
            description={t("trends.deleteFeedBody", { name: isolate(source.name) })}
            confirmLabel={t("common.delete")}
            onConfirm={async () => {
              await api(path, { method: "DELETE", workspaceId: ws.id })
              toast.success(t("trends.feedDeleted"))
              onChange()
            }}
          />
        </div>
      ) : null}
    </li>
  )
}

function RenameDialog({
  open,
  onOpenChange,
  title,
  value,
  onSave,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  title: string
  value: string
  onSave: (v: string) => Promise<void>
}) {
  const { t } = useTranslations()
  const [name, setName] = useState(value)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await onSave(name.trim())
      onOpenChange(false)
    } catch (e) {
      setErr(errText(e, t("common.networkError")))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (v) setName(value)
        onOpenChange(v)
      }}
    >
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4" data-form="rename">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="rename">{t("trends.feedName")}</FieldLabel>
            <Input
              id="rename"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={120}
              dir="auto"
            />
          </Field>
          {err ? <FieldError>{err}</FieldError> : null}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !name.trim() || name.trim() === value}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function AddByUrlDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onAdded: () => void
}) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [url, setUrl] = useState("")
  const [name, setName] = useState("")
  const [interval, setIntervalMinutes] = useState("720")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{ added: number; source: FeedSource }>("/api/w/feed_sources", {
        json: { url: url.trim(), name: name.trim(), interval_minutes: Number(interval) },
        workspaceId: ws.id,
      })
      if (res.source.last_error) toast.warning(res.source.last_error)
      else
        toast.success(
          t("trends.addedSource", { name: res.source.name, n: formatNumber(res.added) }),
        )
      setUrl("")
      setName("")
      onAdded()
      onOpenChange(false)
    } catch (e) {
      setErr(errText(e, t("common.networkError")))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} noValidate className="flex flex-col gap-4" data-form="add-feed">
          <DialogHeader>
            <DialogTitle>{t("trends.addByUrl")}</DialogTitle>
            <DialogDescription>{t("trends.addByUrlHint")}</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor="feed-url">{t("trends.feedUrl")}</FieldLabel>
            <Input
              id="feed-url"
              dir="ltr"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://example.com/feed.xml"
              autoFocus
            />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="feed-name">
                {t("trends.feedName")}{" "}
                <span className="text-muted-foreground">({t("common.optional")})</span>
              </FieldLabel>
              <Input
                id="feed-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={120}
                dir="auto"
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="feed-interval">{t("trends.schedule")}</FieldLabel>
              <PickSelect
                id="feed-interval"
                value={interval}
                onChange={setIntervalMinutes}
                options={INTERVALS.map((m) => ({
                  value: String(m),
                  label: t(`trends.interval.${m}`),
                }))}
              />
            </Field>
          </div>
          {err ? <FieldError>{err}</FieldError> : null}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !url.trim()}>
              <PlusIcon />
              {busy ? t("common.working") : t("trends.addSource")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// ---- the catalog --------------------------------------------------------------------

function Catalog({ entries, onAdded }: { entries: FeedCatalogEntry[]; onAdded: () => void }) {
  const { t, locale } = useTranslations()
  // The reader's own language first; "all" shows both.
  const [lang, setLang] = useState<string>("all")
  const shown = entries.filter((e) => lang === "all" || e.language === lang)
  const langs = locale === "ar" ? (["all", "ar", "en"] as const) : (["all", "en", "ar"] as const)
  return (
    <div className="flex flex-col gap-6 px-6" data-catalog>
      <ToggleGroup
        value={[lang]}
        onValueChange={(v) => {
          if (v[0]) setLang(v[0])
        }}
        aria-label={t("trends.catalogLanguage")}
        data-catalog-lang={lang}
      >
        {langs.map((l) => (
          <Toggle key={l} value={l} data-lang={l}>
            {l === "all" ? t("trends.lang.all") : t(`trends.lang.${l}`)}
          </Toggle>
        ))}
      </ToggleGroup>
      {CATEGORIES.map((cat) => {
        const list = shown.filter((e) => e.category === cat)
        if (list.length === 0) return null
        return (
          <section key={cat} aria-labelledby={`cat-${cat}`} data-category={cat}>
            <h3 id={`cat-${cat}`} className="eyebrow pb-2">
              {t(`trends.category.${cat}`)}
            </h3>
            <ul className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {list.map((e) => (
                <CatalogCard key={e.key} entry={e} onAdded={onAdded} />
              ))}
            </ul>
          </section>
        )
      })}
    </div>
  )
}

function CatalogCard({ entry, onAdded }: { entry: FeedCatalogEntry; onAdded: () => void }) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)
  const added = !!entry.source_id

  async function add() {
    setBusy(true)
    try {
      const res = await api<{ added: number; source: FeedSource }>("/api/w/feed_sources", {
        json: { catalog_key: entry.key },
        workspaceId: ws.id,
      })
      if (res.source.last_status === "failed") toast.warning(res.source.last_error)
      else if (res.source.last_status === "over_budget")
        toast.warning(t("trends.fetchedOverBudget"))
      else
        toast.success(
          t("trends.addedSource", { name: res.source.name, n: formatNumber(res.added) }),
        )
      onAdded()
    } catch (e) {
      toast.error(errText(e, t("common.networkError")))
    } finally {
      setBusy(false)
    }
  }

  return (
    <li
      className={`flex min-h-28 flex-col gap-2 rounded-lg border p-3.5 ${added ? "border-nq-line bg-muted/40" : "border-nq-line"}`}
      data-catalog-entry={entry.key}
      data-added={added ? "true" : "false"}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-sm font-medium">
            <bdi>{entry.name}</bdi>
          </p>
          <p className="mt-0.5 text-xs text-muted-foreground">
            <bdi>{entry.description}</bdi>
          </p>
        </div>
        {added ? (
          <span
            className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground"
            data-added-label
          >
            <CheckIcon className="size-3.5 text-primary" aria-hidden />
            {t("trends.added")}
          </span>
        ) : (
          <Button
            size="sm"
            variant="secondary"
            onClick={add}
            disabled={busy}
            data-action="add"
            aria-label={`${t("trends.add")}: ${entry.name}`}
          >
            <PlusIcon />
            {busy ? t("common.working") : t("trends.add")}
          </Button>
        )}
      </div>
      <p className="mt-auto flex flex-wrap gap-1">
        <Badge variant="neutral">{t(`trends.lang.${entry.language}`)}</Badge>
        <ProviderBadge provider={entry.provider} />
        <Badge variant="outline">{t(`trends.interval.${entry.interval_minutes}`)}</Badge>
      </p>
    </li>
  )
}

// ---- keyword topics -----------------------------------------------------------------

function TopicRow({
  topic,
  canManage,
  onChange,
}: {
  topic: Topic
  canManage: boolean
  onChange: () => void
}) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [editing, setEditing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const path = `/api/w/topics/${encodeURIComponent(topic.id)}`

  async function toggle() {
    try {
      await api(path, { method: "PATCH", json: { enabled: !topic.enabled }, workspaceId: ws.id })
      onChange()
    } catch (e) {
      toast.error(errText(e, t("common.networkError")))
    }
  }

  return (
    <li
      className="grid gap-3 px-6 py-4 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-center"
      data-topic-row={topic.name}
      data-enabled={topic.enabled ? "true" : "false"}
    >
      <div className="min-w-0 space-y-1.5">
        <p className="flex flex-wrap items-center gap-2">
          <bdi className="text-sm font-medium">{topic.name}</bdi>
          {!topic.enabled ? <Badge variant="outline">{t("trends.status.off")}</Badge> : null}
          {/* The count is its own element, outside the keyword list: mixed
              Arabic and English keywords would otherwise pull it into their run (MH-465). */}
          <span className="text-xs text-muted-foreground tabular-nums" data-topic-count>
            {t("trends.itemsMatched", { n: formatNumber(topic.item_count) })}
          </span>
        </p>
        {topic.keywords.length > 0 ? (
          <ul
            className="flex flex-wrap gap-1"
            aria-label={t("trends.keywords")}
            data-topic-keywords
          >
            {topic.keywords.map((k) => (
              <li key={k}>
                <Badge variant="outline" className="font-normal">
                  <bdi>{k}</bdi>
                </Badge>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-xs text-muted-foreground">{t("trends.matchesOwnName")}</p>
        )}
      </div>
      {canManage ? (
        <div className="flex flex-wrap items-center gap-2 lg:justify-end">
          <Switch
            checked={topic.enabled}
            onCheckedChange={toggle}
            aria-label={t("trends.useThisTopic", { name: topic.name })}
          />
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setEditing(true)}
            data-action="edit-topic"
          >
            <PencilIcon />
            {t("common.edit")}
          </Button>
          <Button
            variant="ghost"
            size="icon-sm"
            className="text-nq-danger-text"
            aria-label={`${t("common.delete")}: ${topic.name}`}
            onClick={() => setDeleting(true)}
            data-action="delete-topic"
          >
            <Trash2Icon />
          </Button>
          <TopicDialog open={editing} onOpenChange={setEditing} topic={topic} onSaved={onChange} />
          <ConfirmDialog
            open={deleting}
            onOpenChange={setDeleting}
            title={t("trends.deleteTopicTitle")}
            description={t("trends.deleteTopicBody", { name: isolate(topic.name) })}
            confirmLabel={t("common.delete")}
            onConfirm={async () => {
              await api(path, { method: "DELETE", workspaceId: ws.id })
              toast.success(t("trends.topicDeleted"))
              onChange()
            }}
          />
        </div>
      ) : null}
    </li>
  )
}

/** Create a keyword topic, or edit one (name, keywords). */
function TopicDialog({
  open,
  onOpenChange,
  topic,
  onSaved,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  topic?: Topic
  onSaved: () => void
}) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState(topic?.name ?? "")
  const [keywords, setKeywords] = useState(topic?.keywords.join(", ") ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    const json = {
      name: name.trim(),
      keywords: keywords
        .split(/[,،]/)
        .map((k) => k.trim())
        .filter(Boolean),
    }
    try {
      if (topic) {
        await api(`/api/w/topics/${encodeURIComponent(topic.id)}`, {
          method: "PATCH",
          json,
          workspaceId: ws.id,
        })
        toast.success(t("trends.topicSavedToast"))
      } else {
        const res = await api<{ topic: Topic }>("/api/w/topics", { json, workspaceId: ws.id })
        toast.success(t("trends.topicCreated", { n: formatNumber(res.topic.item_count) }))
      }
      onSaved()
      onOpenChange(false)
    } catch (e) {
      setErr(errText(e, t("common.networkError")))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (v) {
          setName(topic?.name ?? "")
          setKeywords(topic?.keywords.join(", ") ?? "")
          setErr(null)
        }
        onOpenChange(v)
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <form
          onSubmit={submit}
          noValidate
          className="flex flex-col gap-4"
          data-form={topic ? "edit-topic" : "add-topic"}
        >
          <DialogHeader>
            <DialogTitle>{topic ? t("trends.editTopic") : t("trends.newTopic")}</DialogTitle>
            <DialogDescription>
              {topic ? t("trends.editTopicHint") : t("trends.topicsHint")}
            </DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel htmlFor={topic ? `topic-name-${topic.id}` : "topic-name"}>
              {t("trends.topicName")}
            </FieldLabel>
            <Input
              id={topic ? `topic-name-${topic.id}` : "topic-name"}
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={80}
              dir="auto"
            />
          </Field>
          <Field>
            <FieldLabel htmlFor={topic ? `topic-keywords-${topic.id}` : "topic-keywords"}>
              {t("trends.keywords")}
            </FieldLabel>
            <Input
              id={topic ? `topic-keywords-${topic.id}` : "topic-keywords"}
              value={keywords}
              onChange={(e) => setKeywords(e.target.value)}
              placeholder="arabica, robusta, البن"
              dir="auto"
            />
            <FieldDescription>{t("trends.keywordsHint")}</FieldDescription>
          </Field>
          {err ? <FieldError>{err}</FieldError> : null}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : topic ? t("common.save") : t("trends.addTopic")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
