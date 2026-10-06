"use client"

/*
Settings → Audit log (MH-392): every write in this workspace — by a member in
the browser, a pasted MCP token, an app connected over OAuth, a browser agent
or the scheduler — with who, what, when, from where, and a before/after diff of
the fields that changed. Secrets are shown as redacted and message bodies as
hidden: the API never had them in the first place.

Owners and admins only (authz audit:read); everyone else is told so rather than
shown an empty list. Filters live in the address, so a token's "recent calls"
link on Settings → MCP opens this page already filtered to that token.
*/

import { Suspense, useMemo, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { ChevronDownIcon, FilterXIcon, HistoryIcon, ShieldIcon } from "lucide-react"
import { Badge, Button, Field, FieldLabel, toast } from "@fadymondy/nasaq/web"

import { PageNav, PageSummary, useCursorStack } from "@/components/cursor-pager"
import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import { Bdi, isolate } from "@/components/bidi"
import { DatePicker } from "@/components/date-picker"
import { RowsTable } from "@/components/nq-table"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AuditEntry } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"
import { useAuditFacets, useAuditPage } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { cn } from "@/lib/utils"

const PAGE_SIZE = 50
const RETENTION = [30, 90, 180, 365, 730, 1095, 1825, 3650]

export default function AuditPage() {
  return (
    <Suspense>
      <AuditLog />
    </Suspense>
  )
}

function AuditLog() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("audit.title")} · ${ws.summary.name}`)
  const canRead = ws.can("audit:read")

  const router = useRouter()
  const pathname = usePathname()
  const sp = useSearchParams()
  const filter = {
    actor: sp.get("actor") ?? "",
    entityType: sp.get("entity_type") ?? "",
    action: sp.get("action") ?? "",
    from: sp.get("from") ?? "",
    to: sp.get("to") ?? "",
  }
  const key = JSON.stringify(filter)
  const pager = useCursorStack(key)
  const facets = useAuditFacets(ws.id, canRead)
  const page = useAuditPage(
    ws.id,
    {
      ...filter,
      // Dates are days in the member's own timezone; the API takes instants.
      from: filter.from ? new Date(`${filter.from}T00:00:00`).toISOString() : undefined,
      to: filter.to
        ? new Date(new Date(`${filter.to}T00:00:00`).getTime() + 86_400_000).toISOString()
        : undefined,
      cursor: pager.cursor,
      limit: PAGE_SIZE,
    },
    canRead,
  )

  function set(name: string, value: string) {
    const next = new URLSearchParams(sp.toString())
    if (value) next.set(name, value)
    else next.delete(name)
    router.replace(`${pathname}${next.size ? `?${next}` : ""}`, { scroll: false })
  }
  const filtered = Object.values(filter).some(Boolean)

  const actorName = useMemo(() => {
    const m = new Map<string, string>()
    for (const a of facets.data?.actors ?? []) m.set(a.id, a.name || a.id)
    return m
  }, [facets.data])

  const entries = page.data?.entries ?? []

  return (
    <>
      <SectionHeader title={t("audit.title")} description={t("audit.hint")} />

      {!ws.current ? (
        <LoadingRows rows={3} />
      ) : !canRead ? (
        <p
          className="flex items-start gap-2 border-y border-nq-line px-6 py-6 text-sm text-muted-foreground"
          data-audit="admin-only"
        >
          <ShieldIcon className="mt-0.5 size-4 shrink-0" />
          {t("audit.adminOnly")}
        </p>
      ) : (
        <>
          <div
            className="grid gap-3 border-y border-nq-line px-6 py-4 sm:grid-cols-2 lg:grid-cols-6"
            data-audit-filters
          >
            <Field className="lg:col-span-2">
              <FieldLabel htmlFor="audit-actor">{t("audit.filterActor")}</FieldLabel>
              <PickSelect
                id="audit-actor"
                value={filter.actor}
                onChange={(v) => set("actor", v)}
                noneLabel={t("audit.anyone")}
                options={(facets.data?.actors ?? []).map((a) => ({
                  value: a.id,
                  label: `${isolate(a.name || a.id)} · ${t(`audit.kind.${a.kind || "member"}`)}`,
                }))}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="audit-entity">{t("audit.filterEntity")}</FieldLabel>
              <PickSelect
                id="audit-entity"
                value={filter.entityType}
                onChange={(v) => set("entity_type", v)}
                noneLabel={t("audit.anything")}
                options={(facets.data?.entity_types ?? []).map((e) => ({
                  value: e,
                  label: entityLabel(t, e),
                }))}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="audit-action">{t("audit.filterAction")}</FieldLabel>
              <PickSelect
                id="audit-action"
                value={filter.action}
                onChange={(v) => set("action", v)}
                noneLabel={t("audit.anyAction")}
                options={(facets.data?.actions ?? []).map((a) => ({
                  value: a,
                  label: actionLabel(t, a),
                }))}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="audit-from">{t("audit.from")}</FieldLabel>
              <DatePicker
                id="audit-from"
                value={filter.from}
                max={filter.to || undefined}
                onValueChange={(v) => set("from", v)}
                placeholder={t("audit.anyDate")}
                clearable
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="audit-to">{t("audit.to")}</FieldLabel>
              <DatePicker
                id="audit-to"
                value={filter.to}
                min={filter.from || undefined}
                onValueChange={(v) => set("to", v)}
                placeholder={t("audit.anyDate")}
                clearable
              />
            </Field>
            {filtered ? (
              <div className="sm:col-span-2 lg:col-span-6">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => router.replace(pathname, { scroll: false })}
                  data-audit-clear
                >
                  <FilterXIcon />
                  {t("audit.clear")}
                </Button>
              </div>
            ) : null}
          </div>

          {page.error ? (
            <ErrorState error={page.error} />
          ) : !page.data ? (
            <LoadingRows rows={6} layout="table" />
          ) : entries.length === 0 ? (
            <EmptyState
              title={filtered ? t("audit.noMatch") : t("audit.empty")}
              body={filtered ? t("audit.noMatchBody") : t("audit.emptyBody")}
            />
          ) : (
            <div className="pt-4">
              <PageSummary
                index={pager.index}
                pageSize={PAGE_SIZE}
                shown={entries.length}
                total={page.data.total}
                name="audit"
              />
              <ul
                className={cn(
                  "divide-y divide-nq-line border-y border-nq-line",
                  !page.current && "opacity-60",
                )}
                data-audit-list
              >
                {entries.map((e) => (
                  <EntryRow
                    key={e.id}
                    e={e}
                    onActor={(id) => set("actor", id)}
                    actorName={actorName}
                  />
                ))}
              </ul>
              <PageNav
                index={pager.index}
                pageSize={PAGE_SIZE}
                total={page.data.total}
                nextCursor={page.data.next_cursor}
                busy={!page.current}
                onNext={pager.next}
                onPrev={pager.prev}
                name="audit"
              />
            </div>
          )}

          <Retention days={facets.data?.retention_days} onSaved={() => facets.mutate()} />
        </>
      )}
    </>
  )
}

function entityLabel(t: ReturnType<typeof useTranslations>["t"], e: string) {
  const k = `audit.entity.${e}`
  const v = t(k)
  return v === k ? e.replaceAll("_", " ") : v
}

function actionLabel(t: ReturnType<typeof useTranslations>["t"], a: string) {
  const k = `audit.action.${a}`
  const v = t(k)
  return v === k ? a : v
}

function EntryRow({
  e,
  onActor,
  actorName,
}: {
  e: AuditEntry
  onActor: (id: string) => void
  actorName: Map<string, string>
}) {
  const { t, locale } = useTranslations()
  const [open, setOpen] = useState(false)
  const when = useMemo(
    () =>
      dateFormat(locale, { dateStyle: "medium", timeStyle: "medium" }).format(
        new Date(e.occurred_at),
      ),
    [locale, e.occurred_at],
  )
  const fields = Object.entries(e.changes ?? {})
  const refused = e.kind === "request" && e.status >= 400
  const name = e.actor_name || actorName.get(e.actor_id) || e.actor_id || t("audit.kind.system")

  return (
    <li className="px-6 py-3" data-audit-entry={e.id} data-kind={e.kind} data-action={e.action}>
      <div className="grid gap-x-4 gap-y-1.5 md:grid-cols-[11rem_minmax(0,1fr)_minmax(0,1.2fr)_20rem] md:items-start">
        <p className="text-xs text-muted-foreground tabular-nums md:pt-0.5">
          <Ltr>{when}</Ltr>
        </p>

        <div className="min-w-0">
          <button
            type="button"
            className="max-w-full truncate text-start text-sm font-medium hover:underline"
            onClick={() => e.actor_id && onActor(e.actor_id)}
            title={t("audit.onlyThisActor")}
          >
            <Bdi>{name}</Bdi>
          </button>
          <p className="mt-0.5 flex flex-wrap items-center gap-1.5">
            <Badge variant="outline">{t(`audit.kind.${e.actor_kind || "system"}`)}</Badge>
            <span className="text-xs text-muted-foreground">
              {t(`audit.via.${e.via || "web"}`)}
            </span>
          </p>
        </div>

        <div className="min-w-0">
          <p className="flex flex-wrap items-center gap-1.5 text-sm">
            <Badge
              variant={refused ? "danger" : e.action === "delete" ? "neutral" : "brand"}
              data-audit-action
            >
              {actionLabel(t, e.action)}
            </Badge>
            {e.entity_type ? (
              <span className="text-nq-fg-body">{entityLabel(t, e.entity_type)}</span>
            ) : null}
            {refused ? (
              <span className="text-xs text-nq-warning-text">
                {t("audit.refused", { status: e.status })}
              </span>
            ) : null}
          </p>
          {e.operation || e.entity_id ? (
            <p
              className="mt-0.5 truncate text-xs text-muted-foreground"
              title={e.entity_id || undefined}
            >
              {/* One LTR run, so the operation stays before the id in an RTL line. */}
              <Ltr mono>
                {e.operation}
                {e.operation && e.entity_id ? "  " : ""}
                {e.entity_id
                  ? e.entity_id.length > 12
                    ? `${e.entity_id.slice(0, 8)}…`
                    : e.entity_id
                  : ""}
              </Ltr>
            </p>
          ) : null}
        </div>

        <div className="flex items-start justify-between gap-3 md:justify-end">
          <p className="min-w-0 text-xs text-muted-foreground md:text-end">
            <Ltr className="block">{e.ip || "—"}</Ltr>
            {/* dir=ltr on the truncating box itself: the ellipsis goes at the END of the agent string. */}
            <span dir="ltr" className="block max-w-56 truncate md:ms-auto" title={e.user_agent}>
              {e.user_agent}
            </span>
          </p>
          {fields.length > 0 ? (
            <Button
              variant="ghost"
              size="sm"
              aria-expanded={open}
              onClick={() => setOpen((o) => !o)}
              data-audit-diff-toggle
            >
              {t("audit.changes", { n: fields.length })}
              <ChevronDownIcon className={cn("transition-transform", open && "rotate-180")} />
            </Button>
          ) : null}
        </div>
      </div>

      {open ? (
        <div className="mt-3" data-audit-diff>
          <RowsTable<(typeof fields)[number]>
            data={fields}
            getRowId={([field]) => field}
            rowLabel={([field]) => field}
            label={t("audit.changes", { n: fields.length })}
            columns={[
              {
                id: "field",
                header: t("audit.field"),
                hideable: false,
                sortValue: ([field]) => field,
                cell: ([field]) => <Ltr mono>{field}</Ltr>,
              },
              ...(e.action !== "create"
                ? [
                    {
                      id: "before",
                      header: t("audit.before"),
                      className: "max-w-80 align-top",
                      cell: ([, v]: (typeof fields)[number]) => <Value v={v.before} />,
                    },
                  ]
                : []),
              ...(e.action !== "delete"
                ? [
                    {
                      id: "after",
                      header: t("audit.after"),
                      className: "max-w-80 align-top",
                      cell: ([, v]: (typeof fields)[number]) => <Value v={v.after} />,
                    },
                  ]
                : []),
            ]}
          />
        </div>
      ) : null}
    </li>
  )
}

function Value({ v }: { v: unknown }) {
  const { t } = useTranslations()
  if (v === "[redacted]") return <Badge variant="neutral">{t("audit.redacted")}</Badge>
  if (v === "[hidden]") return <Badge variant="outline">{t("audit.hidden")}</Badge>
  if (v === null || v === undefined || v === "")
    return <span className="text-muted-foreground">—</span>
  const s = typeof v === "string" ? v : JSON.stringify(v)
  return (
    <bdi dir="auto" className="block break-words whitespace-pre-wrap text-foreground">
      {s}
    </bdi>
  )
}

function Retention({ days, onSaved }: { days?: number; onSaved: () => void }) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const canManage = ws.can("audit:manage")
  const [value, setValue] = useState<string>("")
  const [busy, setBusy] = useState(false)
  const current = value || (days ? String(days) : "")
  const options =
    RETENTION.includes(days ?? 0) || !days ? RETENTION : [...RETENTION, days].sort((a, b) => a - b)

  async function save() {
    setBusy(true)
    try {
      await api("/api/w/audit/retention", {
        method: "PUT",
        json: { retention_days: Number(current) },
        workspaceId: ws.id,
      })
      toast.success(t("audit.retentionSaved"))
      setValue("")
      onSaved()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section
      className="mt-8 border-t border-nq-line px-6 py-8"
      aria-labelledby="audit-retention"
      data-audit-retention
    >
      <div className="max-w-2xl space-y-1.5">
        <h2
          id="audit-retention"
          className="font-medium tracking-tight text-foreground flex items-center gap-2 text-lg"
        >
          <HistoryIcon className="size-4 text-muted-foreground" />
          {t("audit.retentionTitle")}
        </h2>
        <p className="leading-relaxed text-nq-fg-body text-sm">
          {days ? t("audit.retentionHint", { days: formatNumber(days) }) : "…"}
        </p>
      </div>
      {canManage && days ? (
        <div className="mt-4 flex flex-wrap items-end gap-3">
          <Field className="w-56">
            <FieldLabel htmlFor="audit-retention-days">{t("audit.keepFor")}</FieldLabel>
            <PickSelect
              id="audit-retention-days"
              value={current}
              onChange={setValue}
              options={options.map((d) => ({
                value: String(d),
                label: t("audit.days", { n: formatNumber(d) }),
              }))}
            />
          </Field>
          <Button
            onClick={save}
            disabled={busy || current === String(days)}
            data-audit-retention-save
          >
            {busy ? t("common.working") : t("common.save")}
          </Button>
        </div>
      ) : null}
    </section>
  )
}
