"use client"

import Link from "next/link"
import { useMemo, useState, type DragEvent, type FormEvent } from "react"
import { ArrowRightLeftIcon, GripVerticalIcon } from "lucide-react"
import { toast } from "sonner"

import { formatMoney, PickSelect, stageTone, toMajor, toMinor, useMemberNames } from "@/components/crm/shared"
import { Ltr } from "@/components/copy-field"
import { ScrollX } from "@/components/scroll-x"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Deal, type Stage } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCompanies } from "@/lib/queries"
import { ContactPicker } from "@/components/crm/contact-picker"

// ── board ────────────────────────────────────────────────────────────────

export type MoveFn = (dealId: string, stageId: string, position?: number) => void

/**
 * The pipeline as columns. Cards drag between (and within) stages with native
 * drag and drop; every card also has a "Move to" menu, which is the keyboard
 * and touch path (HTML drag and drop does not fire on touch screens). The row
 * follows the document direction, so in Arabic the first stage sits on the right.
 */
 * Columns share the width (at least 14rem each), so the default five stages fit a 1440 screen;
 * past that the board scrolls inside ScrollX, which shows that it does (MH-463).
export function PipelineBoard({ stages, deals, currency, onMove }: { stages: Stage[]; deals: Deal[]; currency: string; onMove: MoveFn }) {
  const [dragging, setDragging] = useState<string | null>(null)
  const [over, setOver] = useState<{ stage: string; before: string | null } | null>(null)
  const byStage = useMemo(() => {
    const m = new Map<string, Deal[]>()
    for (const s of stages) m.set(s.id, [])
    for (const d of deals) m.get(d.stage_id)?.push(d)
    for (const list of m.values()) list.sort((a, b) => a.position - b.position)
    return m
  }, [stages, deals])

  function drop(e: DragEvent, stageId: string) {
    e.preventDefault()
    const id = e.dataTransfer.getData("text/x-deal") || dragging
    const before = over?.stage === stageId ? over.before : null
    setDragging(null)
    setOver(null)
    if (!id) return
    const list = (byStage.get(stageId) ?? []).filter((d) => d.id !== id)
    let position: number
    const i = before ? list.findIndex((d) => d.id === before) : -1
    if (i < 0) position = list.length ? list[list.length - 1].position + 1 : 1
    else position = i === 0 ? list[0].position - 1 : (list[i - 1].position + list[i].position) / 2
    const deal = deals.find((d) => d.id === id)
    if (deal && deal.stage_id === stageId && Math.abs(deal.position - position) < 1e-9) return
    onMove(id, stageId, position)
  }

  return (
    <ScrollX className="border-y border-line" data-board>
      <div className="flex min-w-max divide-x divide-line rtl:divide-x-reverse">
        {stages.map((s) => {
          const list = byStage.get(s.id) ?? []
          const total = list.filter((d) => d.currency === currency).reduce((n, d) => n + d.value_minor, 0)
          const hot = over?.stage === s.id && dragging
          return (
            <section
              key={s.id}
              data-stage={s.name}
              data-stage-id={s.id}
              aria-label={s.name}
              className={`flex w-64 shrink-0 flex-col sm:w-auto sm:flex-[1_0_14rem] ${hot ? "bg-grid-card" : ""}`}
              onDragOver={(e) => {
                e.preventDefault()
                e.dataTransfer.dropEffect = "move"
                if (over?.stage !== s.id) setOver({ stage: s.id, before: null })
              }}
              onDrop={(e) => drop(e, s.id)}
            >
              <header className="border-b border-line px-4 py-3">
                <div className="flex items-center gap-2">
                  <span className={`size-2 shrink-0 rounded-full ${stageTone[s.kind]}`} aria-hidden />
                  <h3 className="truncate text-sm font-medium">{s.name}</h3>
                  <span className="ms-auto text-xs text-grid-muted tabular-nums" data-stage-count>
                    {list.length}
                  </span>
                </div>
                <p className="mt-1 text-xs text-grid-muted">
                  <Ltr>{formatMoney(total, currency, useLocale())}</Ltr>
                </p>
              </header>
              <ol className="flex min-h-40 flex-1 flex-col gap-2 p-3">
                {list.map((d) => (
                  <li
                    key={d.id}
                    onDragOver={(e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      if (over?.before !== d.id) setOver({ stage: s.id, before: d.id })
                    }}
                    onDrop={(e) => {
                      e.stopPropagation()
                      drop(e, s.id)
                    }}
                  >
                    {over?.before === d.id && dragging && dragging !== d.id ? <div className="mb-2 h-0.5 rounded bg-grid-brand" aria-hidden /> : null}
                    <DealCard
                      deal={d}
                      stages={stages}
                      dragging={dragging === d.id}
                      onDragStart={(e) => {
                        e.dataTransfer.setData("text/x-deal", d.id)
                        e.dataTransfer.effectAllowed = "move"
                        setDragging(d.id)
                      }}
                      onDragEnd={() => {
                        setDragging(null)
                        setOver(null)
                      }}
                      onMove={onMove}
                    />
                  </li>
                ))}
              </ol>
            </section>
          )
        })}
      </div>
    </ScrollX>
  )
}

function useLocale() {
  return useTranslations().locale
}

function DealCard({
  deal,
  stages,
  dragging,
  onDragStart,
  onDragEnd,
  onMove,
}: {
  deal: Deal
  stages: Stage[]
  dragging: boolean
  onDragStart: (e: DragEvent) => void
  onDragEnd: () => void
  onMove: MoveFn
}) {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  const base = `/${locale}/w/${ws.slug}`
  return (
    <article
      draggable={canWrite}
      onDragStart={onDragStart}
      onDragEnd={onDragEnd}
      data-deal-card={deal.title}
      className={`rounded-lg border border-line bg-background p-3 text-sm shadow-xs ${canWrite ? "cursor-grab active:cursor-grabbing" : ""} ${dragging ? "opacity-40" : ""}`}
    >
      <div className="flex items-start gap-1.5">
        {canWrite ? <GripVerticalIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" aria-hidden /> : null}
        <Link href={`${base}/deals/${deal.id}`} className="min-w-0 flex-1 font-medium break-words hover:underline" draggable={false}>
          {deal.title}
        </Link>
        {canWrite ? (
          <DropdownMenu>
            <DropdownMenuTrigger render={<Button variant="ghost" size="icon-xs" aria-label={t("deals.moveTo")} data-move-menu />}>
              <ArrowRightLeftIcon />
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="min-w-44">
              <DropdownMenuGroup>
                <DropdownMenuLabel>{t("deals.moveTo")}</DropdownMenuLabel>
                {stages
                  .filter((s) => s.id !== deal.stage_id)
                  .map((s) => (
                    <DropdownMenuItem key={s.id} onClick={() => onMove(deal.id, s.id)}>
                      <span className={`size-2 rounded-full ${stageTone[s.kind]}`} aria-hidden />
                      {s.name}
                    </DropdownMenuItem>
                  ))}
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        ) : null}
      </div>
      <p className="mt-2 font-medium tabular-nums">
        <Ltr>{formatMoney(deal.value_minor, deal.currency, locale)}</Ltr>
      </p>
      {deal.company || deal.contact ? (
        <p className="mt-1 truncate text-xs text-grid-muted">
          {[deal.company?.name, deal.contact ? deal.contact.name || deal.contact.email : null].filter(Boolean).join(" · ")}
        </p>
      ) : null}
      {deal.expected_close ? (
        <p className="mt-1 text-xs text-grid-muted">{t("deals.closesOn", { date: formatDate(deal.expected_close) })}</p>
      ) : null}
    </article>
  )
}

// ── list ─────────────────────────────────────────────────────────────────

export function DealsTable({ deals, empty }: { deals: Deal[]; empty?: string }) {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const members = useMemberNames(ws.id)
  const base = `/${locale}/w/${ws.slug}`
  if (deals.length === 0) return <p className="border-y border-line px-6 py-6 text-sm text-grid-muted">{empty ?? t("deals.empty")}</p>
  return (
    <div className="border-y border-line">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="ps-6">{t("deals.dealTitle")}</TableHead>
            <TableHead>{t("deals.stage")}</TableHead>
            <TableHead>{t("deals.value")}</TableHead>
            <TableHead>{t("deals.company")}</TableHead>
            <TableHead>{t("deals.contact")}</TableHead>
            <TableHead>{t("crm.owner")}</TableHead>
            <TableHead className="pe-6">{t("deals.expectedClose")}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {deals.map((d) => (
            <TableRow key={d.id} data-deal-row={d.title}>
              <TableCell className="min-w-56 ps-6 font-medium whitespace-normal">
                <Link className="hover:underline" href={`${base}/deals/${d.id}`}>
                  {d.title}
                </Link>
              </TableCell>
              <TableCell>
                <span className="inline-flex items-center gap-2">
                  <span className={`size-2 rounded-full ${stageTone[d.stage_kind]}`} aria-hidden />
                  {d.stage_name}
                </span>
              </TableCell>
              <TableCell className="tabular-nums">
                <Ltr>{formatMoney(d.value_minor, d.currency, locale)}</Ltr>
              </TableCell>
              <TableCell>
                {d.company ? (
                  <Link className="hover:underline" href={`${base}/companies/${d.company.id}`}>
                    {d.company.name}
                  </Link>
                ) : (
                  <span className="text-grid-muted">—</span>
                )}
              </TableCell>
              <TableCell>
                {d.contact ? (
                  <Link className="hover:underline" href={`${base}/contacts/${d.contact.id}`}>
                    {d.contact.name || <Ltr>{d.contact.email}</Ltr>}
                  </Link>
                ) : (
                  <span className="text-grid-muted">—</span>
                )}
              </TableCell>
              <TableCell className="text-grid-muted">
                <Ltr>{members.name(d.owner_user_id)}</Ltr>
              </TableCell>
              <TableCell className="pe-6 text-grid-muted">{d.expected_close ? formatDate(d.expected_close) : "—"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

// ── create / edit ────────────────────────────────────────────────────────

/** The deal form, for a new deal (optionally pre-linked) and for editing one. */
export function DealForm({
  stages,
  currency,
  deal,
  preset,
  onSaved,
  formId = "deal",
}: {
  stages: Stage[]
  currency: string
  deal?: Deal
  preset?: { contact_id?: string; company_id?: string }
  onSaved: (d: Deal) => void
  formId?: string
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const members = useMemberNames(ws.id)
  const companies = useCompanies(ws.id)
  const [title, setTitle] = useState(deal?.title ?? "")
  const [cur, setCur] = useState(deal?.currency ?? currency)
  const [value, setValue] = useState(deal ? toMajor(deal.value_minor, deal.currency) : "")
  const [stageId, setStageId] = useState(deal?.stage_id ?? "")
  const [contactId, setContactId] = useState(deal?.contact?.id ?? preset?.contact_id ?? "")
  const [companyId, setCompanyId] = useState(deal?.company?.id ?? preset?.company_id ?? "")
  const [ownerId, setOwnerId] = useState<string | null>(deal ? (deal.owner_user_id ?? "") : null)
  const [close, setClose] = useState(deal?.expected_close ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const stageOpts = stages.map((s) => ({ value: s.id, label: s.name }))
  const companyOpts = (companies.data?.companies ?? []).map((c) => ({ value: c.id, label: c.name }))
  const id = (f: string) => `${formId}-${f}`

  async function submit(e: FormEvent) {
    e.preventDefault()
    const minor = toMinor(value, cur)
    if (minor === null) {
      setErr(t("deals.valueInvalid"))
      return
    }
    setBusy(true)
    setErr(null)
    const json: Record<string, unknown> = {
      title: title.trim(),
      value_minor: minor,
      currency: cur.trim().toUpperCase(),
      contact_id: contactId,
      company_id: companyId,
      expected_close: close,
    }
    if (stageId) json.stage_id = stageId
    if (ownerId !== null) json.owner_user_id = ownerId
    try {
      const res = deal
        ? await api<{ deal: Deal }>(`/api/w/deals/${encodeURIComponent(deal.id)}`, { method: "PATCH", json, workspaceId: ws.id })
        : await api<{ deal: Deal }>("/api/w/deals", { json, workspaceId: ws.id })
      toast.success(deal ? t("deals.saved") : t("deals.created"))
      if (!deal) {
        setTitle("")
        setValue("")
        setClose("")
      }
      onSaved(res.deal)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate data-form={formId}>
      <FieldGroup className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Field className="sm:col-span-2">
          <FieldLabel htmlFor={id("title")}>{t("deals.dealTitle")}</FieldLabel>
          <Input id={id("title")} value={title} onChange={(e) => setTitle(e.target.value)} maxLength={160} placeholder={t("deals.titlePlaceholder")} />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("value")}>{t("deals.value")}</FieldLabel>
          <Input id={id("value")} inputMode="decimal" dir="ltr" value={value} onChange={(e) => setValue(e.target.value)} placeholder="0" />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("currency")}>{t("deals.currency")}</FieldLabel>
          <Input
            id={id("currency")}
            dir="ltr"
            value={cur}
            maxLength={3}
            onChange={(e) => setCur(e.target.value.toUpperCase().replace(/[^A-Z]/g, ""))}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("stage")}>{t("deals.stage")}</FieldLabel>
          <PickSelect id={id("stage")} value={stageId || stageOpts[0]?.value || ""} onChange={setStageId} options={stageOpts} />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("company")}>{t("deals.company")}</FieldLabel>
          <PickSelect id={id("company")} value={companyId} onChange={setCompanyId} options={companyOpts} noneLabel={t("crm.none")} />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("contact")}>{t("deals.contact")}</FieldLabel>
          <ContactPicker id={id("contact")} value={contactId} selected={deal?.contact ?? null} onChange={setContactId} />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("close")}>{t("deals.expectedClose")}</FieldLabel>
          <Input id={id("close")} type="date" dir="ltr" value={close} onChange={(e) => setClose(e.target.value)} />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("owner")}>{t("crm.owner")}</FieldLabel>
          <PickSelect
            id={id("owner")}
            value={ownerId ?? "__me"}
            onChange={(v) => setOwnerId(v === "__me" ? null : v)}
            options={deal ? members.options : [{ value: "__me", label: t("crm.ownerMe") }, ...members.options]}
            noneLabel={t("crm.unassigned")}
          />
        </Field>
      </FieldGroup>
      <div className="mt-4 flex items-center gap-3">
        <Button type="submit" disabled={busy || !title.trim()}>
          {busy ? t("common.working") : deal ? t("common.save") : t("deals.create")}
        </Button>
        {err ? <FieldError>{err}</FieldError> : null}
      </div>
    </form>
  )
}
