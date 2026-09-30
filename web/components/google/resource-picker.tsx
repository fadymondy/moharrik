"use client"

/*
  The resource picker (MH-440): which GA4 properties, Search Console sites and
  YouTube channels this workspace reads, from every connected Google account.
  Discovery switches a lone one on by itself; with several, this asks. Each
  row says when it last synced and, when it failed, why — in the words the
  server recorded — so a broken sync is never a blank page somewhere else.
*/

import { useState, type ReactNode } from "react"
import {
  ChartLineIcon,
  RefreshCwIcon,
  SearchIcon,
  TriangleAlertIcon,
  YoutubeIcon,
} from "lucide-react"
import { Badge, Button, Input, Switch, toast } from "@fadymondy/nasaq/web"

import { Ltr } from "@/components/copy-field"
import { useFormat } from "@/components/overview/overview-cards"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  GOOGLE_KINDS,
  type GoogleDiscovery,
  type GoogleKind,
  type GoogleResource,
  type GoogleResources,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useGoogleResources } from "@/lib/queries"

export const KIND_ICON: Record<GoogleKind, ReactNode> = {
  ga4_property: <ChartLineIcon />,
  gsc_site: <SearchIcon />,
  yt_channel: <YoutubeIcon />,
}

const KIND_PRODUCT: Record<GoogleKind, string> = {
  ga4_property: "analytics",
  gsc_site: "search_console",
  yt_channel: "youtube",
}

export function ResourcePicker({ emails }: { emails: Record<string, string> }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const res = useGoogleResources(ws.id)
  const [looking, setLooking] = useState(false)
  const [filter, setFilter] = useState("")
  const data = res.data
  const list = data?.resources ?? []
  const needs = data?.needs_choice ?? []
  const discoveries = data?.discoveries ?? []

  async function discover() {
    setLooking(true)
    try {
      const out = await api<GoogleResources>("/api/w/google/resources/discover", {
        method: "POST",
        workspaceId: ws.id,
      })
      await res.mutate(out, { revalidate: false })
      toast.success(t("resources.discovered"))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setLooking(false)
    }
  }

  if (!data) return null
  const failures = discoveries.filter((d) => d.error)
  if (list.length === 0 && failures.length === 0 && discoveries.length === 0) return null

  return (
    <section className="pt-6" data-resource-picker data-needs-choice={needs.join(",")}>
      <div className="flex flex-wrap items-start gap-3 px-6 pb-3">
        <div className="min-w-0 flex-1">
          <h3 className="text-sm font-medium">{t("resources.title")}</h3>
          <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{t("resources.hint")}</p>
        </div>
        <Button variant="secondary" size="sm" onClick={discover} disabled={looking} data-discover>
          <RefreshCwIcon className={looking ? "animate-spin" : undefined} />
          {looking ? t("resources.looking") : t("resources.lookAgain")}
        </Button>
      </div>

      {needs.length > 0 ? (
        <div
          role="status"
          className="mx-6 mb-4 flex items-start gap-3 border border-nq-action bg-card px-4 py-3 text-sm"
          data-choose
        >
          <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-nq-action" />
          <p className="text-foreground">
            {t("resources.chooseBody", {
              kinds: needs.map((k) => t(`resources.kind.${k}`)).join(" · "),
            })}
          </p>
        </div>
      ) : null}

      {list.length > 8 ? (
        <div className="px-6 pb-3">
          <Input
            type="search"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={t("resources.filter")}
            aria-label={t("resources.filter")}
            className="max-w-sm"
            data-resource-filter
          />
        </div>
      ) : null}

      <div className="divide-y divide-nq-line border-y border-nq-line">
        {GOOGLE_KINDS.map((k) => {
          const needle = filter.trim().toLowerCase()
          const all = list.filter((r) => r.kind === k)
          // Switched-on rows first, so what is being read is never lost in a long list.
          const rows = all
            .filter(
              (r) =>
                !needle ||
                [r.display_name, r.external_id, r.parent_name].some((v) =>
                  v.toLowerCase().includes(needle),
                ),
            )
            .sort((a, b) => Number(b.enabled) - Number(a.enabled))
          const errs = failures.filter((d) => d.product === KIND_PRODUCT[k])
          if (all.length === 0 && errs.length === 0) return null
          return (
            <div key={k} data-kind={k}>
              <p className="flex items-center gap-2 bg-nq-surface-soft px-6 py-2 text-xs font-medium text-muted-foreground [&_svg]:size-3.5">
                {KIND_ICON[k]}
                {t(`resources.kind.${k}`)}
                {needs.includes(k) ? (
                  <Badge variant="outline">{t("resources.chooseBadge")}</Badge>
                ) : null}
                <span className="ms-auto tabular-nums" data-kind-count>
                  {t("resources.count", { on: all.filter((r) => r.enabled).length, n: all.length })}
                </span>
              </p>
              {errs.map((d) => (
                <DiscoveryError
                  key={d.connection_id + d.product}
                  d={d}
                  email={emails[d.connection_id]}
                />
              ))}
              {needle && rows.length === 0 ? (
                <p className="px-6 py-3 text-sm text-muted-foreground">{t("resources.noMatch")}</p>
              ) : null}
              <ul className="divide-y divide-nq-line">
                {rows.map((r) => (
                  <ResourceRow
                    key={r.id}
                    r={r}
                    onChange={() => res.mutate()}
                    showAccount={new Set(list.map((x) => x.connection_id)).size > 1}
                  />
                ))}
              </ul>
            </div>
          )
        })}
      </div>
    </section>
  )
}

function DiscoveryError({ d, email }: { d: GoogleDiscovery; email?: string }) {
  const { t } = useTranslations()
  return (
    <div
      role="alert"
      className="flex items-start gap-3 px-6 py-3 text-sm"
      data-discovery-error={d.product}
    >
      <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-nq-danger" />
      <div className="min-w-0 space-y-0.5">
        <p className="text-nq-danger-text">
          {t("resources.discoveryFailed", { product: t(`integrations.product.${d.product}`) })}
        </p>
        <p className="break-words text-muted-foreground">
          {email ? (
            <>
              <Ltr>{email}</Ltr>:{" "}
            </>
          ) : null}
          <Ltr>{d.error}</Ltr>
        </p>
      </div>
    </div>
  )
}

export function SyncStatus({ r }: { r: GoogleResource }) {
  const { t } = useTranslations()
  const f = useFormat()
  if (r.reauth) return <Badge variant="danger">{t("integrations.statusReauth")}</Badge>
  if (!r.available) return <Badge variant="outline">{t("resources.unavailable")}</Badge>
  if (r.syncing)
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground" data-syncing>
        <RefreshCwIcon className="size-3 animate-spin" />
        {t("resources.syncing")}
      </span>
    )
  if (!r.enabled) return null
  if (r.last_error) return <Badge variant="danger">{t("resources.failed")}</Badge>
  if (!r.last_synced_at)
    return <span className="text-xs text-muted-foreground">{t("resources.notSyncedYet")}</span>
  return (
    <span className="text-xs text-muted-foreground" title={f.exact(r.last_synced_at)}>
      {t("resources.syncedAgo", { when: f.ago(r.last_synced_at) })}
    </span>
  )
}

function ResourceRow({
  r,
  onChange,
  showAccount,
}: {
  r: GoogleResource
  onChange: () => void
  showAccount: boolean
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)

  async function toggle(enabled: boolean) {
    setBusy(true)
    try {
      await api(`/api/w/google/resources/${encodeURIComponent(r.id)}`, {
        method: "PATCH",
        json: { enabled },
        workspaceId: ws.id,
      })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function syncNow() {
    setBusy(true)
    try {
      await api(`/api/w/google/resources/${encodeURIComponent(r.id)}/sync`, {
        method: "POST",
        workspaceId: ws.id,
      })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const avatar = r.kind === "yt_channel" ? r.details?.avatar_url : undefined
  const sub =
    r.kind === "gsc_site"
      ? t(`resources.permission.${r.parent_name}`, {})
      : r.kind === "yt_channel"
        ? r.details?.handle
        : r.parent_name
  return (
    <li
      className="flex flex-col gap-2 px-6 py-3 sm:flex-row sm:items-center"
      data-resource={r.external_id}
      data-enabled={r.enabled}
    >
      <div className="flex min-w-0 flex-1 items-center gap-3">
        {avatar ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={avatar}
            alt=""
            className="size-8 shrink-0 rounded-full border border-nq-line"
            referrerPolicy="no-referrer"
          />
        ) : null}
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">
            <bdi>{r.display_name}</bdi>
            {r.auto_enabled && r.enabled ? (
              <span className="ms-2 text-xs font-normal text-muted-foreground">
                {t("resources.autoEnabled")}
              </span>
            ) : null}
          </p>
          <p className="truncate text-xs text-muted-foreground">
            <Ltr mono>{r.external_id}</Ltr>
            {sub && !sub.startsWith("resources.permission.") ? (
              <>
                {" "}
                · <bdi>{sub}</bdi>
              </>
            ) : null}
            {showAccount && r.connection_email ? (
              <>
                {" · "}
                <Ltr>{r.connection_email}</Ltr>
              </>
            ) : null}
          </p>
          {r.enabled && r.last_error ? (
            <p className="mt-1 text-xs break-words text-nq-danger-text" data-last-error>
              <Ltr>{r.last_error}</Ltr>
            </p>
          ) : null}
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-3 sm:justify-end">
        <SyncStatus r={r} />
        {r.enabled && !r.reauth && !r.syncing ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={syncNow}
            disabled={busy || !!r.backoff_until}
            data-sync-now
          >
            <RefreshCwIcon />
            {t("resources.syncNow")}
          </Button>
        ) : null}
        <Switch
          checked={r.enabled}
          onCheckedChange={(v) => toggle(v)}
          disabled={busy || (!r.available && !r.enabled)}
          aria-label={t(r.enabled ? "resources.turnOff" : "resources.turnOn", {
            name: r.display_name,
          })}
          data-toggle
        />
      </div>
    </li>
  )
}
