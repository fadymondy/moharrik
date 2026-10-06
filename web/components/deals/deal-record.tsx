"use client"

// MH-1161: the deal's record frame, the same as contacts and companies (MH-1157): cover, a deal
// tile, title, stage and actions, key facts, and tabs. The layout loads the deal and the pipeline
// once; every tab reads them from here.
import Link from "next/link"
import { useParams, usePathname } from "next/navigation"
import { createContext, useContext, useState, type ReactNode } from "react"
import {
  ActivityIcon,
  ArrowRightLeftIcon,
  Building2Icon,
  CalendarCheckIcon,
  CalendarClockIcon,
  CalendarIcon,
  CheckIcon,
  HandshakeIcon,
  LayoutGridIcon,
  PencilIcon,
  StickyNoteIcon,
  UserRoundIcon,
} from "lucide-react"
import {
  Badge,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  Popover,
  PopoverContent,
  PopoverTrigger,
  toast,
} from "@fadymondy/nasaq/web"

import { Bdi, isolate } from "@/components/bidi"
import { ConfirmButton } from "@/components/confirm-button"
import { DealValue } from "@/components/crm/deals"
import { stageTone } from "@/components/crm/shared"
import { NotFoundState } from "@/components/not-found-state"
import {
  RecordAvatar,
  RecordChip,
  RecordHeader,
  RecordSkeleton,
  type RecordTab,
} from "@/components/record/record"
import { ErrorState } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Deal, type Stage } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"
import { useDeal, useDeals } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

type DealData = NonNullable<ReturnType<typeof useDeal>["data"]>

export type DealRecordValue = {
  data: DealData
  deal: Deal
  stages: Stage[]
  currency: string
  canWrite: boolean
  base: string
  rec: string
  reload: () => void
  /** Moves the deal to another stage; resolves once the record has re-fetched. */
  moveTo: (stageId: string) => Promise<void>
}

const Ctx = createContext<DealRecordValue | null>(null)

export function useDealRecord(): DealRecordValue {
  const v = useContext(Ctx)
  if (!v) throw new Error("useDealRecord outside DealRecord")
  return v
}

export function DealRecord({ children }: { children: ReactNode }) {
  const { id } = useParams<{ id: string }>()
  const pathname = usePathname()
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  const detail = useDeal(ws.id, id)
  const pipeline = useDeals(ws.id)
  const base = `/${locale}/w/${ws.slug}`
  const rec = `${base}/deals/${id}`
  const deal = detail.data?.deal
  useDocumentTitle(deal ? `${deal.title} · ${ws.summary.name}` : t("deals.dealTitle"))

  if (detail.error) {
    const notFound = detail.error instanceof ApiError && detail.error.status === 404
    if (notFound)
      return (
        <NotFoundState
          title={t("deals.notFound")}
          body={t("deals.notFoundBody")}
          backHref={`${base}/deals`}
          backLabel={t("deals.backToList")}
        />
      )
    return <ErrorState error={detail.error} />
  }
  if (pipeline.error) return <ErrorState error={pipeline.error} />
  if (!detail.data || !deal || !pipeline.data) return <RecordSkeleton />

  const data: DealData = detail.data
  const stages = pipeline.data.stages
  const reload = () => {
    void detail.mutate()
    void pipeline.mutate()
  }
  const moveTo = async (stageId: string) => {
    if (stageId === deal.stage_id) return
    try {
      await api(`/api/w/deals/${encodeURIComponent(deal.id)}`, {
        method: "PATCH",
        workspaceId: ws.id,
        json: { stage_id: stageId },
      })
      await Promise.all([detail.mutate(), pipeline.mutate()])
      toast.success(t("deals.saved"))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const active = pathname.split(`/deals/${id}`)[1]?.split("/")[1] || "overview"
  const tabs: RecordTab[] = [
    { value: "overview", label: t("record.tab.overview"), icon: LayoutGridIcon, href: rec },
    {
      value: "activity",
      label: t("record.tab.activity"),
      icon: ActivityIcon,
      href: `${rec}/activity`,
      count: data.activities_total || undefined,
    },
    ...(canWrite
      ? [
          {
            value: "details",
            label: t("record.tab.details"),
            icon: PencilIcon,
            href: `${rec}/details`,
          },
        ]
      : []),
  ]
  const day = (s: string) => dateFormat(locale).format(new Date(s))

  return (
    <Ctx.Provider
      value={{
        data,
        deal,
        stages,
        currency: pipeline.data.currency,
        canWrite,
        base,
        rec,
        reload,
        moveTo,
      }}
    >
      <div data-deal-record={deal.id}>
        <RecordHeader
          back={{ href: `${base}/deals`, label: t("nav.deals") }}
          avatar={<RecordAvatar name={deal.title} icon={<HandshakeIcon />} />}
          status={<StageBadge deal={deal} />}
          title={<Bdi>{deal.title}</Bdi>}
          subtitle={<DealValue deal={deal} className="font-medium text-foreground" />}
          actions={
            canWrite ? (
              <>
                <MoveStage deal={deal} stages={stages} onMove={moveTo} />
                <Button
                  variant="secondary"
                  size="sm"
                  nativeButton={false}
                  render={<Link href={`${rec}/activity?focus=activity-body`} />}
                  data-action="record-log-activity"
                >
                  <StickyNoteIcon />
                  {t("c360.actions.logActivity")}
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  nativeButton={false}
                  render={<Link href={`${rec}/details`} />}
                  data-action="record-edit"
                >
                  <PencilIcon />
                  {t("common.edit")}
                </Button>
                <ConfirmButton
                  label={t("common.delete")}
                  title={t("deals.deleteTitle")}
                  description={t("deals.deleteBody", { title: isolate(deal.title) })}
                  confirmLabel={t("common.delete")}
                  onConfirm={async () => {
                    await api(`/api/w/deals/${encodeURIComponent(deal.id)}`, {
                      method: "DELETE",
                      workspaceId: ws.id,
                    })
                    window.location.href = `${base}/deals`
                  }}
                />
              </>
            ) : null
          }
          chips={
            <>
              {deal.contact ? (
                <Link href={`${base}/contacts/${deal.contact.id}`} data-record-contact>
                  <RecordChip icon={<UserRoundIcon />}>
                    <Bdi>{deal.contact.name || deal.contact.email}</Bdi>
                  </RecordChip>
                </Link>
              ) : null}
              {deal.company ? (
                <Link href={`${base}/companies/${deal.company.id}`} data-record-company>
                  <RecordChip icon={<Building2Icon />}>
                    <Bdi>{deal.company.name}</Bdi>
                  </RecordChip>
                </Link>
              ) : null}
              {deal.closed_at ? (
                <RecordChip icon={<CalendarCheckIcon />}>
                  {t("deals.closedOn", { date: day(deal.closed_at) })}
                </RecordChip>
              ) : deal.expected_close ? (
                <RecordChip icon={<CalendarClockIcon />}>
                  {t("deals.closesOn", { date: day(deal.expected_close) })}
                </RecordChip>
              ) : null}
            </>
          }
          meta={
            <span className="inline-flex items-center gap-1.5">
              <CalendarIcon className="size-3.5" />
              {t("record.since", { when: formatDate(deal.created_at) })}
            </span>
          }
          tabs={tabs}
          active={active}
        />
        {children}
      </div>
    </Ctx.Provider>
  )
}

/** The stage the deal sits in, with won/lost said in words as well as colour. */
export function StageBadge({ deal }: { deal: Deal }) {
  const { t } = useTranslations()
  return (
    <Badge
      variant={deal.stage_kind === "won" ? "brand" : "outline"}
      className="gap-1.5 bg-card"
      data-deal-stage={deal.stage_kind}
    >
      <span className={cn("size-2 rounded-full", stageTone[deal.stage_kind])} aria-hidden />
      <Bdi>{deal.stage_name}</Bdi>
      {deal.stage_kind !== "open" ? (
        <span className="text-muted-foreground">
          · {t(`deals.closed.${deal.stage_kind === "lost" ? "lost" : "won"}`)}
        </span>
      ) : null}
    </Badge>
  )
}

/** The header's "Move to" menu: every other stage, in pipeline order. */
function MoveStage({
  deal,
  stages,
  onMove,
}: {
  deal: Deal
  stages: Stage[]
  onMove: (stageId: string) => Promise<void>
}) {
  const { t } = useTranslations()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button size="sm" data-action="record-move-stage" />}>
        <ArrowRightLeftIcon />
        {t("deals.moveTo")}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{t("deals.moveTo")}</DropdownMenuLabel>
          {stages
            .filter((s) => s.id !== deal.stage_id)
            .map((s) => (
              <DropdownMenuItem key={s.id} onClick={() => void onMove(s.id)} data-move-to={s.name}>
                <span className={cn("size-2 rounded-full", stageTone[s.kind])} aria-hidden />
                <Bdi>{s.name}</Bdi>
              </DropdownMenuItem>
            ))}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * The pipeline as a path: every stage in order, the ones behind the deal filled, its own stage
 * marked. With write access each step moves the deal there.
 */
export function StagePath() {
  const { t } = useTranslations()
  const { deal, stages, canWrite, moveTo } = useDealRecord()
  const [busy, setBusy] = useState("")
  const at = stages.findIndex((s) => s.id === deal.stage_id)
  return (
    <ol className="flex gap-1.5 overflow-x-auto px-4 pb-4" data-stage-path>
      {stages.map((s, i) => {
        const current = i === at
        const passed = at >= 0 && i < at
        const tone =
          current && s.kind === "won"
            ? "border-nq-success bg-nq-success/12 text-foreground"
            : current && s.kind === "lost"
              ? "border-nq-danger bg-nq-danger/12 text-foreground"
              : current
                ? "border-primary bg-primary/12 text-foreground"
                : passed
                  ? "border-primary/30 bg-primary/5 text-foreground"
                  : "border-border text-muted-foreground"
        const body = (
          <>
            <span
              className={cn(
                "flex size-5 shrink-0 items-center justify-center rounded-full text-[11px] font-medium tabular-nums [&_svg]:size-3",
                current || passed
                  ? cn(stageTone[current ? s.kind : "open"], "text-white")
                  : "bg-nq-surface-soft",
              )}
              aria-hidden
            >
              {passed ? <CheckIcon /> : i + 1}
            </span>
            <span className="truncate">
              <Bdi>{s.name}</Bdi>
            </span>
          </>
        )
        const cls = cn(
          "flex h-10 min-w-36 flex-1 items-center gap-2 rounded-lg border px-3 text-start text-sm transition-colors",
          tone,
        )
        return (
          <li key={s.id} className="flex flex-1" data-stage-step={s.name} data-current={current}>
            {canWrite && !current ? (
              <button
                type="button"
                className={cn(cls, "hover:border-primary/60 disabled:opacity-60")}
                disabled={!!busy}
                aria-label={`${t("deals.moveTo")} ${isolate(s.name)}`}
                onClick={async () => {
                  setBusy(s.id)
                  await moveTo(s.id)
                  setBusy("")
                }}
              >
                {body}
              </button>
            ) : (
              <div className={cls} aria-current={current ? "step" : undefined}>
                {body}
              </div>
            )}
          </li>
        )
      })}
    </ol>
  )
}

/**
 * The day a won or lost deal really closed (MH-394): a result logged after the fact gets its own
 * day, which is the day the Reports page counts it on. Never a future day (the API refuses one).
 */
export function ClosedOnEdit({
  dealId,
  closedAt,
  onSaved,
}: {
  dealId: string
  closedAt: string
  onSaved: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [open, setOpen] = useState(false)
  const [day, setDay] = useState(closedAt.slice(0, 10))
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const today = new Date().toISOString().slice(0, 10)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger render={<Button variant="ghost" size="sm" data-closed-on-edit />}>
        <CalendarIcon />
        {t("deals.changeClosedOn")}
      </PopoverTrigger>
      <PopoverContent align="start" className="w-72">
        <form
          className="flex flex-col gap-3"
          onSubmit={async (e) => {
            e.preventDefault()
            setBusy(true)
            setError("")
            try {
              await api(`/api/w/deals/${encodeURIComponent(dealId)}`, {
                method: "PATCH",
                workspaceId: ws.id,
                json: { closed_on: day },
              })
              setOpen(false)
              onSaved()
            } catch (err) {
              setError(err instanceof ApiError ? err.message : t("common.networkError"))
            } finally {
              setBusy(false)
            }
          }}
        >
          <Field>
            <FieldLabel htmlFor="deal-closed-on">{t("deals.closedOnLabel")}</FieldLabel>
            <Input
              id="deal-closed-on"
              type="date"
              value={day}
              max={today}
              onChange={(e) => setDay(e.target.value)}
              dir="ltr"
              required
            />
            <FieldDescription>{t("deals.closedOnHint")}</FieldDescription>
          </Field>
          {error ? <p className="text-sm text-nq-danger-text">{error}</p> : null}
          <Button type="submit" size="sm" disabled={busy || !day}>
            {t("common.save")}
          </Button>
        </form>
      </PopoverContent>
    </Popover>
  )
}
