"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useMemo, useState, type FormEvent, type SyntheticEvent } from "react"
import {
  ArrowRightLeftIcon,
  Building2Icon,
  CalendarClockIcon,
  ExternalLinkIcon,
  PencilIcon,
  UserRoundIcon,
} from "lucide-react"
import {
  Avatar,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
  Field,
  FieldLabel,
  Input,
  Popover,
  PopoverContent,
  PopoverTrigger,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  KanbanBoard,
  toast,
  type KanbanCardData,
  type KanbanColumnData,
} from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError, Label } from "@/components/form-layout"

import {
  formatMoney,
  PickSelect,
  stageTone,
  toMajor,
  toMinor,
  useMemberNames,
} from "@/components/crm/shared"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Deal, type Stage } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { CompanyPicker } from "@/components/crm/company-picker"
import { ContactPicker } from "@/components/crm/contact-picker"
import { Bdi } from "@/components/bidi"
import { cn } from "@/lib/utils"

// ── board ────────────────────────────────────────────────────────────────

export type MoveFn = (dealId: string, stageId: string, position?: number) => void

type BoardCard = KanbanCardData & { deal: Deal }

// Keys and pointer presses inside a card's own controls (the title link, the value editor, the move
// menu, and their portalled popups) must not reach the card's drag handle: Space in the value input
// would lift the card instead of typing a space.
const own = {
  onKeyDown: (e: SyntheticEvent) => e.stopPropagation(),
  onPointerDown: (e: SyntheticEvent) => e.stopPropagation(),
}

/**
 * MH-1170: the pipeline on Nasaq's KanbanBoard. Cards drag between and within stages by pointer, touch
 * or keyboard (Space to lift, arrows, Space to drop); every card also has a "Move to" menu, and the same
 * actions on context-click. Each column shows the stage's real count and value from the server, since a
 * column may hold only its first page of cards (MH-467), and loads the rest on demand.
 */
export function PipelineBoard({
  stages,
  deals,
  currency,
  onMove,
  onChanged,
  cursorOf,
  onLoadMore,
  loadingStage,
}: {
  stages: Stage[]
  deals: Deal[]
  currency: string
  onMove: MoveFn
  onChanged?: () => void
  /** The cursor for a stage's next cards, "" when every card is on the board (MH-467). */
  cursorOf?: (stageId: string) => string
  onLoadMore?: (stageId: string) => void
  loadingStage?: string | null
}) {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const canWrite = ws.can("contacts:write")
  const members = useMemberNames(ws.id)
  const base = `/${locale}/w/${ws.slug}`

  // Cards in board order: by stage, then by position inside it.
  const sorted = useMemo(() => {
    const order = new Map(stages.map((s, i) => [s.id, i]))
    return deals
      .filter((d) => order.has(d.stage_id))
      .sort((a, b) => order.get(a.stage_id)! - order.get(b.stage_id)! || a.position - b.position)
  }, [stages, deals])
  const cards: BoardCard[] = useMemo(
    () => sorted.map((d) => ({ id: d.id, columnId: d.stage_id, title: d.title, deal: d })),
    [sorted],
  )

  const columns: KanbanColumnData[] = stages.map((s) => {
    const shown = sorted.filter((d) => d.stage_id === s.id).length
    const more = cursorOf?.(s.id) ?? ""
    return {
      id: s.id,
      title: s.name,
      count: s.deal_count,
      accent: (
        <span
          className={cn("size-2 rounded-full", stageTone[s.kind])}
          data-stage-id={s.id}
          data-stage-name={s.name}
          data-stage-count={s.deal_count}
        />
      ),
      meta: (
        <span className="tabular-nums" data-stage-value={s.value_minor}>
          <Ltr>{formatMoney(s.value_minor, currency, locale)}</Ltr>
        </span>
      ),
      footer:
        shown < s.deal_count ? (
          <div
            className="flex flex-col items-start gap-2 border-t border-border pt-3"
            data-stage-more={`${shown}/${s.deal_count}`}
          >
            <span className="text-xs text-muted-foreground">
              {t("deals.showingInStage", {
                shown: formatNumber(shown),
                total: formatNumber(s.deal_count),
              })}
            </span>
            {more && onLoadMore ? (
              <Button
                variant="secondary"
                size="sm"
                onClick={() => onLoadMore(s.id)}
                disabled={loadingStage === s.id}
                data-stage-load-more
              >
                {loadingStage === s.id ? t("common.working") : t("common.loadMore")}
              </Button>
            ) : null}
          </div>
        ) : undefined,
    }
  })

  // The board reports the index the card takes in its new column; the server orders by a
  // fractional position, so the card goes halfway between its new neighbours.
  function move(cardId: string, toColumn: string, toIndex: number) {
    if (!canWrite) return
    const list = sorted.filter((d) => d.stage_id === toColumn && d.id !== cardId)
    const before = list[toIndex - 1]
    const after = list[toIndex]
    const position =
      before && after
        ? (before.position + after.position) / 2
        : before
          ? before.position + 1
          : after
            ? after.position - 1
            : 1
    onMove(cardId, toColumn, position)
  }

  return (
    <div className="px-4 sm:px-6" data-board>
      <KanbanBoard<BoardCard>
        columns={columns}
        cards={cards}
        onMove={move}
        label={t("deals.boardLabel")}
        emptyLabel={t("deals.stageEmpty")}
        columnClassName="w-72 sm:w-auto sm:flex-[1_0_15rem]"
        cardActions={(c) => [
          {
            id: "open",
            label: t("deals.openDeal"),
            icon: ExternalLinkIcon,
            onSelect: () => router.push(`${base}/deals/${c.deal.id}`),
          },
          ...(canWrite
            ? stages
                .filter((s) => s.id !== c.deal.stage_id)
                .map((s) => ({
                  id: `move-${s.id}`,
                  label: t("deals.moveToStage", { stage: s.name }),
                  icon: ArrowRightLeftIcon,
                  group: "move",
                  onSelect: () => onMove(c.deal.id, s.id),
                }))
            : []),
        ]}
        renderCard={(c, state) => (
          <DealCard
            deal={c.deal}
            stages={stages}
            owner={c.deal.owner_user_id ? members.name(c.deal.owner_user_id) : null}
            overlay={state.overlay}
            canWrite={canWrite}
            href={`${base}/deals/${c.deal.id}`}
            onMove={onMove}
            onChanged={onChanged}
          />
        )}
      />
    </div>
  )
}

function DealCard({
  deal,
  stages,
  owner,
  overlay,
  canWrite,
  href,
  onMove,
  onChanged,
}: {
  deal: Deal
  stages: Stage[]
  owner: string | null
  overlay: boolean
  canWrite: boolean
  href: string
  onMove: MoveFn
  onChanged?: () => void
}) {
  const { t, formatDate } = useTranslations()
  const today = new Date().toISOString().slice(0, 10)
  const overdue = deal.stage_kind === "open" && !!deal.expected_close && deal.expected_close < today
  const who = [deal.company?.name, deal.contact ? deal.contact.name || deal.contact.email : null]
  // The copy that follows the pointer is a picture of the card: no links, menus or editors in it.
  const live = canWrite && !overlay
  return (
    <article
      data-deal-card={overlay ? undefined : deal.title}
      data-deal-stage={deal.stage_id}
      className={cn(
        "flex flex-col gap-2.5 rounded-card border border-border bg-card p-3 text-sm text-card-foreground shadow-xs transition-shadow hover:shadow-sm",
        overlay && "shadow-lg",
      )}
    >
      <div className="flex items-start gap-1.5">
        {overlay ? (
          <span className="min-w-0 flex-1 font-medium break-words">
            <Bdi>{deal.title}</Bdi>
          </span>
        ) : (
          <Link
            href={href}
            draggable={false}
            className="min-w-0 flex-1 font-medium break-words hover:underline"
            onKeyDown={own.onKeyDown}
          >
            <Bdi>{deal.title}</Bdi>
          </Link>
        )}
        {live ? (
          <span {...own} className="-me-1 -mt-1 shrink-0">
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("deals.moveTo")}
                    data-move-menu
                  />
                }
              >
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
                        <Bdi>{s.name}</Bdi>
                      </DropdownMenuItem>
                    ))}
                </DropdownMenuGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          </span>
        ) : null}
      </div>

      <div className="flex items-center justify-between gap-2">
        {live ? (
          <span {...own} className="min-w-0">
            <DealValueEditor deal={deal} onSaved={onChanged} />
          </span>
        ) : (
          <DealValue deal={deal} className="font-medium" />
        )}
        {owner ? <Avatar name={owner} size="xs" title={owner} data-deal-owner /> : null}
      </div>

      {deal.company || deal.contact || deal.expected_close ? (
        <div className="flex flex-col gap-1 text-xs text-muted-foreground [&_svg]:size-3.5 [&_svg]:shrink-0">
          {deal.company || deal.contact ? (
            <p className="flex min-w-0 items-center gap-1.5">
              {deal.company ? <Building2Icon /> : <UserRoundIcon />}
              <span className="truncate">
                {who.filter(Boolean).map((part, i) => (
                  <span key={i}>
                    {i ? " · " : null}
                    <Bdi>{part}</Bdi>
                  </span>
                ))}
              </span>
            </p>
          ) : null}
          {deal.expected_close ? (
            <p
              className={cn("flex items-center gap-1.5", overdue && "text-nq-danger-text")}
              data-deal-overdue={overdue || undefined}
            >
              <CalendarClockIcon />
              {t(overdue ? "deals.overdueOn" : "deals.closesOn", {
                date: formatDate(deal.expected_close),
              })}
            </p>
          ) : null}
        </div>
      ) : null}
    </article>
  )
}

// ── value ────────────────────────────────────────────────────────────────

/**
 * A deal's value, or "No value yet" when it has none (MH-464). A deal opened from a website
 * inquiry starts with no value, and "$0.00" read as if somebody had priced it at nothing.
 */
export function DealValue({
  deal,
  className,
}: {
  deal: Pick<Deal, "value_minor" | "currency">
  className?: string
}) {
  const { t, locale } = useTranslations()
  if (!deal.value_minor) {
    return (
      <span className={`text-muted-foreground ${className ?? ""}`} data-deal-value="none">
        {t("deals.noValue")}
      </span>
    )
  }
  return (
    <span className={`tabular-nums ${className ?? ""}`} data-deal-value={deal.value_minor}>
      <Ltr>{formatMoney(deal.value_minor, deal.currency, locale)}</Ltr>
    </span>
  )
}

/** The value on a board card, editable in place: the one number a new deal is most often missing. */
function DealValueEditor({ deal, onSaved }: { deal: Deal; onSaved?: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [open, setOpen] = useState(false)
  const [value, setValue] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const inputId = `deal-value-${deal.id}`

  async function save(e: FormEvent) {
    e.preventDefault()
    const minor = toMinor(value, deal.currency)
    if (minor === null) {
      setErr(t("deals.valueInvalid"))
      return
    }
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/deals/${encodeURIComponent(deal.id)}`, {
        method: "PATCH",
        json: { value_minor: minor },
        workspaceId: ws.id,
      })
      toast.success(t("deals.saved"))
      setOpen(false)
      onSaved?.()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next)
        if (next) {
          setValue(deal.value_minor ? toMajor(deal.value_minor, deal.currency) : "")
          setErr(null)
        }
      }}
    >
      <PopoverTrigger
        render={
          <button
            type="button"
            draggable={false}
            className="group inline-flex max-w-full items-center gap-1.5 rounded-sm font-medium outline-none hover:text-nq-brand focus-visible:ring-2 focus-visible:ring-ring"
            aria-label={deal.value_minor ? t("deals.editValue") : t("deals.setValue")}
            data-deal-value-edit
          />
        }
      >
        <DealValue deal={deal} />
        <PencilIcon
          className="size-3 shrink-0 text-muted-foreground opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100"
          aria-hidden
        />
      </PopoverTrigger>
      <PopoverContent align="start" className="w-64" data-deal-value-popover>
        <form onSubmit={save} noValidate className="flex flex-col gap-2">
          <Label htmlFor={inputId}>{t("deals.valueIn", { currency: deal.currency })}</Label>
          <div className="flex gap-2">
            <Input
              id={inputId}
              autoFocus
              inputMode="decimal"
              dir="ltr"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder="0"
            />
            <Button type="submit" disabled={busy}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </div>
          {err ? <FieldError>{err}</FieldError> : null}
        </form>
      </PopoverContent>
    </Popover>
  )
}

// ── list ─────────────────────────────────────────────────────────────────

export function DealsTable({
  deals,
  empty,
  total,
}: {
  deals: Deal[]
  empty?: string
  total?: number
}) {
  const { t, locale, formatDate, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const members = useMemberNames(ws.id)
  const base = `/${locale}/w/${ws.slug}`
  if (deals.length === 0)
    return (
      <p className="border-y border-nq-line px-6 py-6 text-sm text-muted-foreground">
        {empty ?? t("deals.empty")}
      </p>
    )
  return (
    <div className="border-y border-nq-line">
      {total !== undefined && total > deals.length ? (
        // A contact or company page lists its first deals only; it says so (MH-467).
        <p
          className="border-b border-nq-line px-6 py-3 text-sm text-muted-foreground"
          data-deals-more={`${deals.length}/${total}`}
        >
          {t("deals.relatedMore", {
            shown: formatNumber(deals.length),
            total: formatNumber(total),
          })}
        </p>
      ) : null}
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
                  <Bdi>{d.title}</Bdi>
                </Link>
              </TableCell>
              <TableCell>
                <span className="inline-flex items-center gap-2">
                  <span className={`size-2 rounded-full ${stageTone[d.stage_kind]}`} aria-hidden />
                  <Bdi>{d.stage_name}</Bdi>
                </span>
              </TableCell>
              <TableCell className="tabular-nums">
                <DealValue deal={d} />
              </TableCell>
              <TableCell>
                {d.company ? (
                  <Link className="hover:underline" href={`${base}/companies/${d.company.id}`}>
                    <Bdi>{d.company.name}</Bdi>
                  </Link>
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
              </TableCell>
              <TableCell>
                {d.contact ? (
                  <Link className="hover:underline" href={`${base}/contacts/${d.contact.id}`}>
                    {d.contact.name ? <Bdi>{d.contact.name}</Bdi> : <Ltr>{d.contact.email}</Ltr>}
                  </Link>
                ) : (
                  <span className="text-muted-foreground">—</span>
                )}
              </TableCell>
              <TableCell className="text-muted-foreground">
                <Ltr>{members.name(d.owner_user_id)}</Ltr>
              </TableCell>
              <TableCell className="pe-6 text-muted-foreground">
                {d.expected_close ? formatDate(d.expected_close) : "—"}
              </TableCell>
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
  const [title, setTitle] = useState(deal?.title ?? "")
  const [cur, setCur] = useState(deal?.currency ?? currency)
  const [value, setValue] = useState(
    deal?.value_minor ? toMajor(deal.value_minor, deal.currency) : "",
  )
  const [stageId, setStageId] = useState(deal?.stage_id ?? "")
  const [contactId, setContactId] = useState(deal?.contact?.id ?? preset?.contact_id ?? "")
  const [companyId, setCompanyId] = useState(deal?.company?.id ?? preset?.company_id ?? "")
  const [ownerId, setOwnerId] = useState<string | null>(deal ? (deal.owner_user_id ?? "") : null)
  const [close, setClose] = useState(deal?.expected_close ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const stageOpts = stages.map((s) => ({ value: s.id, label: s.name }))
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
        ? await api<{ deal: Deal }>(`/api/w/deals/${encodeURIComponent(deal.id)}`, {
            method: "PATCH",
            json,
            workspaceId: ws.id,
          })
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
          <Input
            id={id("title")}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            maxLength={160}
            placeholder={t("deals.titlePlaceholder")}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("value")}>{t("deals.value")}</FieldLabel>
          <Input
            id={id("value")}
            inputMode="decimal"
            dir="ltr"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="0"
          />
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
          <PickSelect
            id={id("stage")}
            value={stageId || stageOpts[0]?.value || ""}
            onChange={setStageId}
            options={stageOpts}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("company")}>{t("deals.company")}</FieldLabel>
          <CompanyPicker
            id={id("company")}
            value={companyId}
            selected={deal?.company ?? null}
            onChange={setCompanyId}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("contact")}>{t("deals.contact")}</FieldLabel>
          <ContactPicker
            id={id("contact")}
            value={contactId}
            selected={deal?.contact ?? null}
            onChange={setContactId}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("close")}>{t("deals.expectedClose")}</FieldLabel>
          <Input
            id={id("close")}
            type="date"
            dir="ltr"
            value={close}
            onChange={(e) => setClose(e.target.value)}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={id("owner")}>{t("crm.owner")}</FieldLabel>
          <PickSelect
            id={id("owner")}
            value={ownerId ?? "__me"}
            onChange={(v) => setOwnerId(v === "__me" ? null : v)}
            options={
              deal
                ? members.options
                : [{ value: "__me", label: t("crm.ownerMe") }, ...members.options]
            }
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
