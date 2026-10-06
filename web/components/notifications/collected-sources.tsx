"use client"

import { useState, type FormEvent } from "react"
import {
  ExternalLinkIcon,
  LockIcon,
  PlusIcon,
  RefreshCwIcon,
  TriangleAlertIcon,
} from "lucide-react"
import {
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  Switch,
  toast,
  Toggle,
  ToggleGroup,
} from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError } from "@/components/form-layout"

import { Bdi, isolate } from "@/components/bidi"
import { ConfirmButton } from "@/components/confirm-button"
import { PickSelect } from "@/components/crm/shared"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type CollectedKind, type CollectedSource } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"
import { useCollectedSources } from "@/lib/queries"

/*
Collected sources (MH-384): what the notification center polls, beside the
webhooks that post to it. Ported from fadymondy.com's center sources — each
source has on/off, an interval, push with kinds, and its last status, error and
next run — with the kinds this server cannot collect listed as "not
configured" and what they need, rather than as switches that do nothing.
*/

export function CollectedSources() {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const canManage = ws.can("notifications:manage")
  const list = useCollectedSources(ws.id)
  const [adding, setAdding] = useState(false)

  // A collection run (the ticker's or someone's "collect now") moves the rows.
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "notification" || e.kind === "notifications") void list.mutate()
  })

  const data = list.data
  const unavailable = (data?.catalog ?? []).filter((c) => !c.available)
  const over = data ? data.budget.runs_per_hour > data.budget.limit : false

  return (
    <section className="border-b border-nq-line pb-6" data-collected-sources>
      <div className="flex flex-wrap items-end justify-between gap-3 px-6 pb-4">
        <div className="max-w-2xl">
          <h2 className="text-lg font-semibold">{t("notifications.collected")}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{t("notifications.collectedHint")}</p>
        </div>
        <div className="flex items-center gap-3">
          {data ? (
            <span
              className={`text-xs tabular-nums ${over ? "text-nq-warning-text" : "text-muted-foreground"}`}
              data-budget={`${data.budget.runs_per_hour}/${data.budget.limit}`}
            >
              {t("notifications.budget", {
                used: formatNumber(Math.round(data.budget.runs_per_hour * 10) / 10),
                limit: formatNumber(data.budget.limit),
              })}
            </span>
          ) : null}
          {canManage ? (
            <Button onClick={() => setAdding(true)} disabled={!data} data-add-collected>
              <PlusIcon />
              {t("notifications.addCollected")}
            </Button>
          ) : null}
        </div>
      </div>

      {list.error ? (
        <ErrorState error={list.error} />
      ) : !data ? (
        <LoadingRows rows={2} />
      ) : data.sources.length === 0 ? (
        <EmptyState
          title={t("notifications.noCollected")}
          body={canManage ? t("notifications.noCollectedBody") : undefined}
        />
      ) : (
        <ul className="divide-y divide-nq-line border-y border-nq-line">
          {data.sources.map((s) => (
            <CollectedRow
              key={s.id}
              source={s}
              intervals={data.intervals}
              canManage={canManage}
              onChange={() => list.mutate()}
            />
          ))}
        </ul>
      )}

      {unavailable.length ? (
        <div className="px-6 pt-5" data-not-configured>
          <h3 className="eyebrow">{t("notifications.notConfigured")}</h3>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
            {t("notifications.notConfiguredHint")}
          </p>
          <ul className="mt-3 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {unavailable.map((c) => (
              <li
                key={c.kind}
                className="flex items-start gap-2 border border-nq-line p-3"
                data-kind-unavailable={c.kind}
              >
                <LockIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                <div className="min-w-0">
                  <p className="text-sm font-medium">{t(`notifications.sourceKind.${c.kind}`)}</p>
                  <p className="text-xs text-muted-foreground">
                    {t(`notifications.needs.${c.needs ?? "oauth_app"}`)}
                  </p>
                </div>
              </li>
            ))}
          </ul>
        </div>
      ) : null}

      <AddCollectedDialog
        open={adding}
        onOpenChange={setAdding}
        intervals={data?.intervals ?? []}
        kinds={(data?.catalog ?? []).filter((c) => c.available).map((c) => c.kind as CollectedKind)}
        onAdded={() => list.mutate()}
      />
    </section>
  )
}

function intervalLabel(t: ReturnType<typeof useTranslations>["t"], m: number) {
  if (m < 60) return t("notifications.everyMinutes", { n: m })
  if (m === 60) return t("notifications.everyHour")
  if (m < 1440) return t("notifications.everyHours", { n: m / 60 })
  return t("notifications.everyDay")
}

function CollectedRow({
  source,
  intervals,
  canManage,
  onChange,
}: {
  source: CollectedSource
  intervals: number[]
  canManage: boolean
  onChange: () => void
}) {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)
  const [kinds, setKinds] = useState(source.push_kinds)
  // Runs are minutes apart: the time matters as much as the day.
  const when = (v: string) =>
    dateFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(new Date(v))

  async function patch(json: Record<string, unknown>) {
    setBusy(true)
    try {
      await api(`/api/w/notification_sources/${encodeURIComponent(source.id)}`, {
        method: "PATCH",
        json,
        workspaceId: ws.id,
      })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function collectNow() {
    setBusy(true)
    try {
      const res = await api<{ source: CollectedSource; new: number }>(
        `/api/w/notification_sources/${encodeURIComponent(source.id)}/collect`,
        {
          method: "POST",
          workspaceId: ws.id,
        },
      )
      if (res.source.last_status === "failed")
        toast.error(res.source.last_error || t("notifications.collectFailed"))
      else toast.success(t("notifications.collected_n", { n: res.new }))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    await api(`/api/w/notification_sources/${encodeURIComponent(source.id)}`, {
      method: "DELETE",
      workspaceId: ws.id,
    })
    toast.success(t("notifications.collectedDeleted"))
    onChange()
  }

  const status =
    source.last_status === "failed" ? (
      <Badge variant="danger">{t("notifications.status.failed")}</Badge>
    ) : source.last_status === "ok" ? (
      <Badge variant="neutral">{t("notifications.status.ok")}</Badge>
    ) : (
      <Badge variant="outline">{t("notifications.status.never")}</Badge>
    )

  return (
    <li
      className="flex flex-col gap-3 px-6 py-4"
      data-collected-row={source.name}
      data-collected-kind={source.kind}
    >
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
        <div className="min-w-0 flex-1">
          <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
            <Bdi>{source.name}</Bdi>
            <Badge variant="outline">{t(`notifications.sourceKind.${source.kind}`)}</Badge>
            {status}
          </p>
          <a
            className="mt-0.5 inline-flex max-w-full items-center gap-1 text-xs break-all text-muted-foreground underline-offset-4 hover:underline"
            href={source.target_url}
            target="_blank"
            rel="noreferrer"
            dir="ltr"
          >
            {source.target}
            <ExternalLinkIcon className="size-3 shrink-0" />
          </a>
          <p className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
            <span>
              {t("notifications.itemCount", {
                n: formatNumber(source.item_count),
                unread: formatNumber(source.unread_count),
              })}
            </span>
            {source.last_run_at ? (
              <span>
                {t("notifications.lastRun", {
                  date: when(source.last_run_at),
                  n: formatNumber(source.last_item_count),
                })}
              </span>
            ) : null}
            {source.enabled && source.next_run_at ? (
              <span>{t("notifications.nextRun", { date: when(source.next_run_at) })}</span>
            ) : null}
            {!source.enabled ? <span>{t("notifications.paused")}</span> : null}
          </p>
          {source.last_status === "failed" && source.last_error ? (
            <p
              className="mt-1 flex items-start gap-1 text-xs text-nq-danger-text"
              data-collected-error
            >
              <TriangleAlertIcon className="mt-0.5 size-3 shrink-0" />
              <Bdi>{source.last_error}</Bdi>
            </p>
          ) : null}
        </div>
        {canManage ? (
          <div className="flex shrink-0 items-center gap-2">
            <Switch
              checked={source.enabled}
              disabled={busy}
              onCheckedChange={(v) => patch({ enabled: v })}
              aria-label={source.enabled ? t("notifications.enabled") : t("notifications.disabled")}
            />
            <Button
              variant="secondary"
              size="sm"
              onClick={collectNow}
              disabled={busy}
              data-collect-now
            >
              <RefreshCwIcon />
              {t("notifications.collectNow")}
            </Button>
            <ConfirmButton
              label={t("common.delete")}
              title={t("notifications.deleteCollectedTitle")}
              description={t("notifications.deleteCollectedBody", { name: isolate(source.name) })}
              confirmLabel={t("common.delete")}
              onConfirm={remove}
            />
          </div>
        ) : null}
      </div>
      {canManage ? (
        <div className="flex flex-wrap items-center gap-x-5 gap-y-3">
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground" id={`int-${source.id}`}>
              {t("notifications.interval")}
            </span>
            <div className="w-40">
              <PickSelect
                id={`interval-${source.id}`}
                value={String(source.interval_minutes)}
                onChange={(v) => v && patch({ interval_minutes: Number(v) })}
                options={intervals.map((m) => ({ value: String(m), label: intervalLabel(t, m) }))}
                disabled={busy}
              />
            </div>
          </div>
          <label className="flex items-center gap-2 text-xs">
            <Switch
              checked={source.push_enabled}
              disabled={busy}
              onCheckedChange={(v) => patch({ push_enabled: v })}
            />
            {t("notifications.pushEnabled")}
          </label>
          {source.push_enabled ? (
            <form
              className="flex items-center gap-2"
              onSubmit={(e) => {
                e.preventDefault()
                void patch({ push_kinds: kinds })
              }}
            >
              <Input
                className="h-8 w-44"
                dir="ltr"
                value={kinds}
                onChange={(e) => setKinds(e.target.value)}
                placeholder={t("notifications.pushKindsPlaceholder")}
                aria-label={t("notifications.pushKinds")}
              />
              <Button
                type="submit"
                variant="secondary"
                size="sm"
                disabled={busy || kinds === source.push_kinds}
              >
                {t("common.save")}
              </Button>
            </form>
          ) : null}
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">
          {intervalLabel(t, source.interval_minutes)} ·{" "}
          {source.push_enabled ? t("notifications.pushEnabled") : t("notifications.pushOff")}
        </p>
      )}
    </li>
  )
}

function AddCollectedDialog({
  open,
  onOpenChange,
  intervals,
  kinds,
  onAdded,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  intervals: number[]
  kinds: CollectedKind[]
  onAdded: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [kind, setKind] = useState<CollectedKind>("bluesky")
  const [target, setTarget] = useState("")
  const [name, setName] = useState("")
  const [interval, setIntervalMin] = useState("60")
  const [push, setPush] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  function handleOpenChange(next: boolean) {
    if (next) {
      setKind("bluesky")
      setTarget("")
      setName("")
      setIntervalMin("60")
      setPush(false)
      setErr(null)
    }
    onOpenChange(next)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{ source: CollectedSource }>("/api/w/notification_sources", {
        workspaceId: ws.id,
        json: {
          kind,
          target: target.trim(),
          name: name.trim() || undefined,
          interval_minutes: Number(interval),
          push_enabled: push,
        },
      })
      toast.success(t("notifications.collectedAdded", { name: res.source.name }))
      onAdded()
      handleOpenChange(false)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("notifications.addCollected")}</DialogTitle>
            <DialogDescription>{t("notifications.addCollectedHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel>{t("notifications.sourceKindLabel")}</FieldLabel>
              <ToggleGroup
                value={[kind]}
                onValueChange={(v) => v[0] && setKind(v[0] as CollectedKind)}
                aria-label={t("notifications.sourceKindLabel")}
              >
                {kinds.map((k) => (
                  <Toggle key={k} value={k} data-kind-option={k}>
                    {t(`notifications.sourceKind.${k}`)}
                  </Toggle>
                ))}
              </ToggleGroup>
              <FieldDescription>{t(`notifications.kindAbout.${kind}`)}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="collected-target">
                {t(`notifications.target.${kind}`)}
              </FieldLabel>
              <Input
                id="collected-target"
                dir="ltr"
                value={target}
                onChange={(e) => setTarget(e.target.value)}
                placeholder={t(`notifications.targetPlaceholder.${kind}`)}
                maxLength={2000}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="collected-name">
                {t("notifications.sourceName")}{" "}
                <span className="text-muted-foreground">({t("common.optional")})</span>
              </FieldLabel>
              <Input
                id="collected-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={80}
                placeholder={t("notifications.collectedNamePlaceholder")}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="collected-interval">{t("notifications.interval")}</FieldLabel>
              <PickSelect
                id="collected-interval"
                value={interval}
                onChange={setIntervalMin}
                options={intervals.map((m) => ({ value: String(m), label: intervalLabel(t, m) }))}
              />
            </Field>
            <Field className="flex-row items-center gap-3">
              <Switch id="collected-push" checked={push} onCheckedChange={setPush} />
              <div className="flex flex-col gap-1">
                <FieldLabel htmlFor="collected-push">{t("notifications.pushEnabled")}</FieldLabel>
                <FieldDescription>{t("notifications.pushHint")}</FieldDescription>
              </div>
            </Field>
          </FieldGroup>
          {err ? <FieldError>{err}</FieldError> : null}
          <DialogFooter>
            <Button type="submit" disabled={busy || !target.trim()}>
              {busy ? t("notifications.checking") : t("notifications.addCollected")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
