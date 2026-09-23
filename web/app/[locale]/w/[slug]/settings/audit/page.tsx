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
import { toast } from "sonner"

import { PageNav, PageSummary, useCursorStack } from "@/components/cursor-pager"
import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import { Bdi, isolate } from "@/components/bidi"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
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
      to: filter.to ? new Date(new Date(`${filter.to}T00:00:00`).getTime() + 86_400_000).toISOString() : undefined,
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
      <SectionHeader micro={<Bdi>{ws.summary.name}</Bdi>} title={t("audit.title")} description={t("audit.hint")} />

      {!ws.current ? (
        <LoadingRows rows={3} />
      ) : !canRead ? (
        <p className="flex items-start gap-2 border-y border-line px-6 py-6 text-sm text-grid-muted" data-audit="admin-only">
          <ShieldIcon className="mt-0.5 size-4 shrink-0" />
          {t("audit.adminOnly")}
        </p>
      ) : (
        <>
          <div className="grid gap-3 border-y border-line px-6 py-4 sm:grid-cols-2 lg:grid-cols-6" data-audit-filters>
            <Field className="lg:col-span-2">
              <FieldLabel htmlFor="audit-actor">{t("audit.filterActor")}</FieldLabel>
              <PickSelect
                id="audit-actor"
                value={filter.actor}
                onChange={(v) => set("actor", v)}
                noneLabel={t("audit.anyone")}
                options={(facets.data?.actors ?? []).map((a) => ({ value: a.id, label: `${isolate(a.name || a.id)} · ${t(`audit.kind.${a.kind || "member"}`)}` }))}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="audit-entity">{t("audit.filterEntity")}</FieldLabel>
              <PickSelect
                id="audit-entity"
                value={filter.entityType}
                onChange={(v) => set("entity_type", v)}
                noneLabel={t("audit.anything")}
                options={(facets.data?.entity_types ?? []).map((e) => ({ value: e, label: entityLabel(t, e) }))}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="audit-action">{t("audit.filterAction")}</FieldLabel>
              <PickSelect
                id="audit-action"
                value={filter.action}
                onChange={(v) => set("action", v)}
                noneLabel={t("audit.anyAction")}
                options={(facets.data?.actions ?? []).map((a) => ({ value: a, label: actionLabel(t, a) }))}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="audit-from">{t("audit.from")}</FieldLabel>
              <Input id="audit-from" type="date" dir="ltr" value={filter.from} max={filter.to || undefined} onChange={(e) => set("from", e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="audit-to">{t("audit.to")}</FieldLabel>
              <Input id="audit-to" type="date" dir="ltr" value={filter.to} min={filter.from || undefined} onChange={(e) => set("to", e.target.value)} />
            </Field>
            {filtered ? (
              <div className="sm:col-span-2 lg:col-span-6">
                <Button variant="ghost" size="sm" onClick={() => router.replace(pathname, { scroll: false })} data-audit-clear>
                  <FilterXIcon />
                  {t("audit.clear")}
                </Button>
              </div>
            ) : null}
          </div>

          {page.error ? (
            <ErrorState error={page.error} />
          ) : !page.data ? (
            <LoadingRows rows={6} />
          ) : entries.length === 0 ? (
            <EmptyState title={filtered ? t("audit.noMatch") : t("audit.empty")} body={filtered ? t("audit.noMatchBody") : t("audit.emptyBody")} />
          ) : (
            <div className="pt-4">
              <PageSummary index={pager.index} pageSize={PAGE_SIZE} shown={entries.length} total={page.data.total} name="audit" />
              <ul className={cn("divide-y divide-line border-y border-line", !page.current && "opacity-60")} data-audit-list>
                {entries.map((e) => (
                  <EntryRow key={e.id} e={e} onActor={(id) => set("actor", id)} actorName={actorName} />
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

function EntryRow({ e, onActor, actorName }: { e: AuditEntry; onActor: (id: string) => void; actorName: Map<string, string> }) {
  const { t, locale } = useTranslations()
  const [open, setOpen] = useState(false)
  const when = useMemo(() => dateFormat(locale, { dateStyle: "medium", timeStyle: "medium" }).format(new Date(e.occurred_at)), [locale, e.occurred_at])
  const fields = Object.entries(e.changes ?? {})
  const refused = e.kind === "request" && e.status >= 400
  const name = e.actor_name || actorName.get(e.actor_id) || e.actor_id || t("audit.kind.system")

  return (
    <li className="px-6 py-3" data-audit-entry={e.id} data-kind={e.kind} data-action={e.action}>
      <div className="grid gap-x-4 gap-y-1.5 md:grid-cols-[11rem_minmax(0,1fr)_minmax(0,1.2fr)_20rem] md:items-start">
        <p className="text-xs text-grid-muted tabular-nums md:pt-0.5">
          <Ltr>{when}</Ltr>
        </p>

        <div className="min-w-0">
          <button type="button" className="max-w-full truncate text-start text-sm font-medium hover:underline" onClick={() => e.actor_id && onActor(e.actor_id)} title={t("audit.onlyThisActor")}>
            <Bdi>{name}</Bdi>
          </button>
          <p className="mt-0.5 flex flex-wrap items-center gap-1.5">
            <Badge variant="outline">{t(`audit.kind.${e.actor_kind || "system"}`)}</Badge>
            <span className="text-xs text-grid-muted">{t(`audit.via.${e.via || "web"}`)}</span>
          </p>
        </div>

        <div className="min-w-0">
          <p className="flex flex-wrap items-center gap-1.5 text-sm">
            <Badge variant={refused ? "destructive" : e.action === "delete" ? "secondary" : "default"} data-audit-action>
              {actionLabel(t, e.action)}
            </Badge>
            {e.entity_type ? <span className="text-grid-body">{entityLabel(t, e.entity_type)}</span> : null}
            {refused ? <span className="text-xs text-grid-warn-text">{t("audit.refused", { status: e.status })}</span> : null}
          </p>
          {e.operation || e.entity_id ? (
            <p className="mt-0.5 truncate text-xs text-grid-muted" title={e.entity_id || undefined}>
              {/* One LTR run, so the operation stays before the id in an RTL line. */}
              <Ltr mono>
                {e.operation}
                {e.operation && e.entity_id ? "  " : ""}
                {e.entity_id ? (e.entity_id.length > 12 ? `${e.entity_id.slice(0, 8)}…` : e.entity_id) : ""}
              </Ltr>
            </p>
          ) : null}
        </div>

        <div className="flex items-start justify-between gap-3 md:justify-end">
          <p className="min-w-0 text-xs text-grid-muted md:text-end">
            <Ltr className="block">{e.ip || "—"}</Ltr>
            {/* dir=ltr on the truncating box itself: the ellipsis goes at the END of the agent string. */}
            <span dir="ltr" className="block max-w-56 truncate md:ms-auto" title={e.user_agent}>
              {e.user_agent}
            </span>
          </p>
          {fields.length > 0 ? (
            <Button variant="ghost" size="sm" aria-expanded={open} onClick={() => setOpen((o) => !o)} data-audit-diff-toggle>
              {t("audit.changes", { n: fields.length })}
              <ChevronDownIcon className={cn("transition-transform", open && "rotate-180")} />
            </Button>
          ) : null}
        </div>
      </div>

      {open ? (
        <div className="mt-3 overflow-x-auto border border-line" data-audit-diff>
          <table className="w-full text-xs">
            <thead className="bg-grid-card text-grid-muted">
              <tr>
                <th className="px-3 py-1.5 text-start font-medium">{t("audit.field")}</th>
                {e.action !== "create" ? <th className="px-3 py-1.5 text-start font-medium">{t("audit.before")}</th> : null}
                {e.action !== "delete" ? <th className="px-3 py-1.5 text-start font-medium">{t("audit.after")}</th> : null}
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {fields.map(([field, v]) => (
                <tr key={field} className="align-top">
                  <td className="px-3 py-1.5">
                    <Ltr mono>{field}</Ltr>
                  </td>
                  {e.action !== "create" ? (
                    <td className="max-w-80 px-3 py-1.5">
                      <Value v={v.before} />
                    </td>
                  ) : null}
                  {e.action !== "delete" ? (
                    <td className="max-w-80 px-3 py-1.5">
                      <Value v={v.after} />
                    </td>
                  ) : null}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </li>
  )
}

function Value({ v }: { v: unknown }) {
  const { t } = useTranslations()
  if (v === "[redacted]") return <Badge variant="secondary">{t("audit.redacted")}</Badge>
  if (v === "[hidden]") return <Badge variant="outline">{t("audit.hidden")}</Badge>
  if (v === null || v === undefined || v === "") return <span className="text-grid-muted">—</span>
  const s = typeof v === "string" ? v : JSON.stringify(v)
  return (
    <bdi dir="auto" className="block break-words whitespace-pre-wrap text-grid-fg">
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
  const options = RETENTION.includes(days ?? 0) || !days ? RETENTION : [...RETENTION, days].sort((a, b) => a - b)

  async function save() {
    setBusy(true)
    try {
      await api("/api/w/audit/retention", { method: "PUT", json: { retention_days: Number(current) }, workspaceId: ws.id })
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
    <section className="mt-8 border-t border-line px-6 py-8" aria-labelledby="audit-retention" data-audit-retention>
      <div className="max-w-2xl space-y-1.5">
        <h2 id="audit-retention" className="grid-title flex items-center gap-2 text-lg">
          <HistoryIcon className="size-4 text-grid-muted" />
          {t("audit.retentionTitle")}
        </h2>
        <p className="grid-body text-sm">{days ? t("audit.retentionHint", { days: formatNumber(days) }) : "…"}</p>
      </div>
      {canManage && days ? (
        <div className="mt-4 flex flex-wrap items-end gap-3">
          <Field className="w-56">
            <FieldLabel htmlFor="audit-retention-days">{t("audit.keepFor")}</FieldLabel>
            <PickSelect
              id="audit-retention-days"
              value={current}
              onChange={setValue}
              options={options.map((d) => ({ value: String(d), label: t("audit.days", { n: formatNumber(d) }) }))}
            />
          </Field>
          <Button onClick={save} disabled={busy || current === String(days)} data-audit-retention-save>
            {busy ? t("common.working") : t("common.save")}
          </Button>
        </div>
      ) : null}
    </section>
  )
}
