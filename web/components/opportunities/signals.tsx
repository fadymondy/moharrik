"use client"

/*
The Signals view of Opportunities (MH-1065). Each row is one real thing from GET
/api/w/opportunities/signals: an open deal gone quiet, a lead that has not been converted, a prospect
scoring high. The API sends the reason as a code plus its data; this file turns it into words, and
shows only the actions the API says exist for that row. Tones are semantic: warning = needs a look,
info = new, violet = Moharrik's own scoring.
*/

import Link from "next/link"
import { useState, type ReactNode } from "react"
import {
  ArrowRightIcon,
  ArrowRightLeftIcon,
  CheckIcon,
  HandshakeIcon,
  SparklesIcon,
  UserPlusIcon,
  XIcon,
} from "lucide-react"
import { Badge, Button, toast } from "@fadymondy/nasaq/web"

import { isolate } from "@/components/bidi"
import { Empty, Panel, useFormat } from "@/components/overview/overview-cards"
import { AcceptDialog, RejectDialog } from "@/components/prospects/decide"
import { ReasonList, ScoreBadge } from "@/components/prospects/shared"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useOpportunitySignals } from "@/lib/queries"
import { api, ApiError, type LeadConversion, type ProspectDetail } from "@/lib/api"
import type { HomeReason } from "@/lib/home-types"
import { useTranslations } from "@/lib/i18n"
import type { OpportunitySignals, SignalItem, SignalKind } from "@/lib/opportunity-types"
import { cn } from "@/lib/utils"

type Tone = "info" | "violet" | "warning"

const TONE_TEXT: Record<Tone, string> = {
  info: "text-nq-info-text",
  violet: "text-nq-tag-violet",
  warning: "text-nq-warning-text",
}
const TONE_SOFT: Record<Tone, string> = {
  info: "bg-nq-info-soft",
  violet: "bg-nq-tag-violet-soft",
  warning: "bg-nq-warning-soft",
}

const KINDS: { kind: SignalKind; count: keyof OpportunitySignals["counts"]; icon: ReactNode; tone: Tone }[] = [
  { kind: "at_risk_deal", count: "at_risk_deals", icon: <HandshakeIcon />, tone: "warning" },
  { kind: "new_lead", count: "new_leads", icon: <UserPlusIcon />, tone: "info" },
  { kind: "high_fit_prospect", count: "high_fit_prospects", icon: <SparklesIcon />, tone: "violet" },
]

type T = (key: string, vars?: Record<string, string | number>) => string

/** One reason code, in words. Unknown codes fall back to the code itself, never to invented prose. */
function whyText(t: T, r: HomeReason, ago: (iso: string) => string, n: (v: number) => string): string {
  const vars = { n: n(r.n ?? 0), text: r.text ?? "", when: r.at ? ago(r.at) : "" }
  switch (r.code) {
    case "lead_status":
      return t("opps.why.lead_status", { text: t(`leads.status.${r.text}`) })
    case "utm": {
      // "utm_source=google": the part after "=" is the value the visitor arrived with.
      const [k, v] = (r.text ?? "").split("=")
      return t("opps.why.utm", { text: v ? `${k.replace("utm_", "")}: ${v}` : (r.text ?? "") })
    }
    default: {
      const key = `opps.why.${r.code}`
      const s = t(key, vars)
      return s === key ? r.code : s
    }
  }
}

export function SignalsPanel({ onChanged }: { onChanged?: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const f = useFormat()
  const signals = useOpportunitySignals(ws.id)
  const data = signals.data

  if (signals.error) return <ErrorState error={signals.error} />
  if (!data) return <LoadingRows rows={4} />
  const kinds = KINDS.filter((k) => data.counts[k.count] !== null)
  if (kinds.length === 0) {
    return (
      <div className="px-4 sm:px-6">
        <Empty title={t("opps.noAccess")} body={t("opps.noAccessBody")} />
      </div>
    )
  }
  const th = data.thresholds
  const refresh = () => {
    void signals.mutate()
    onChanged?.()
  }
  return (
    <div className="space-y-4 px-4 pb-8 sm:px-6" data-opps-signals>
      <p className="max-w-3xl text-sm text-muted-foreground" data-opps-thresholds>
        {t("opps.rules", {
          quiet: f.n(th.quiet_days),
          lead: f.n(th.new_lead_days),
          score: f.n(th.high_fit_score),
        })}
      </p>
      {kinds.map((k) => {
        const total = data.counts[k.count] ?? 0
        const items = data.items.filter((i) => i.kind === k.kind)
        return (
          <Panel
            key={k.kind}
            id={`opps-${k.kind}`}
            title={t(`opps.kind.${k.kind}`)}
            icon={k.icon}
            meta={total > 0 ? <Badge variant={k.tone === "violet" ? "brand" : k.tone}>{f.n(total)}</Badge> : undefined}
          >
            {items.length === 0 ? (
              <Empty title={t(`opps.empty.${k.kind}`)} body={t(`opps.emptyBody.${k.kind}`, { quiet: f.n(th.quiet_days), lead: f.n(th.new_lead_days), score: f.n(th.high_fit_score) })} />
            ) : (
              <ul className="divide-y divide-nq-line" data-signal-list={k.kind}>
                {items.map((it) => (
                  <SignalRow key={`${it.kind}-${it.id}`} item={it} onDone={refresh} />
                ))}
              </ul>
            )}
            {items.length > 0 && total > items.length ? (
              <p className="border-t border-nq-line px-5 py-3 text-xs text-muted-foreground">
                {t("opps.showing", { shown: f.n(items.length), total: f.n(total) })}
              </p>
            ) : null}
          </Panel>
        )
      })}
    </div>
  )
}

function SignalRow({ item, onDone }: { item: SignalItem; onDone: () => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const f = useFormat()
  const base = `/${locale}/w/${ws.slug}`
  const tone = item.tone as Tone
  const [busy, setBusy] = useState(false)
  const [prospect, setProspect] = useState<{ mode: "accept" | "reject"; p: ProspectDetail["prospect"] } | null>(null)

  const openHref =
    item.kind === "at_risk_deal"
      ? `${base}/deals/${item.id}`
      : item.kind === "new_lead"
        ? `${base}/leads/${item.id}`
        : `${base}/prospects/${item.id}`

  async function convert() {
    setBusy(true)
    try {
      const c = await api<LeadConversion>(`/api/w/leads/${encodeURIComponent(item.id)}/convert`, {
        method: "POST",
        workspaceId: ws.id,
      })
      toast.success(t("leads.converted"), {
        description: c.free_mail
          ? t("leads.convertedFreeMail")
          : c.company_id
            ? t("leads.convertedWithCompany", { company: isolate(c.lead.converted_company?.name ?? "") })
            : undefined,
      })
      onDone()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  // The accept and reject dialogs prefill from the whole prospect, so fetch it first.
  async function decide(mode: "accept" | "reject") {
    setBusy(true)
    try {
      const d = await api<ProspectDetail>(`/api/w/prospects/${encodeURIComponent(item.id)}`, { workspaceId: ws.id })
      setProspect({ mode, p: d.prospect })
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const has = (a: string) => item.actions.includes(a as never)
  const icon = KINDS.find((k) => k.kind === item.kind)?.icon
  return (
    <li className="flex gap-3 px-5 py-3" data-signal={item.kind} data-signal-id={item.id}>
      <span
        className={cn("flex size-8 shrink-0 items-center justify-center rounded-md [&>svg]:size-4", TONE_SOFT[tone], TONE_TEXT[tone])}
        aria-hidden
      >
        {icon}
      </span>
      <div className="min-w-0 flex-1 space-y-1.5">
        <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5">
          <p className="min-w-0 text-sm font-medium text-foreground">
            <bdi>{item.title}</bdi>
            {item.subtitle ? (
              <span className="ms-2 text-xs font-normal text-muted-foreground">
                <bdi>{item.subtitle}</bdi>
              </span>
            ) : null}
          </p>
          {item.value ? (
            <span className="text-sm font-medium text-foreground tabular-nums" data-signal-value>
              {f.money(item.value.minor, item.value.currency)}
            </span>
          ) : item.score !== undefined ? (
            <ScoreBadge score={item.score} />
          ) : null}
        </div>
        <ul className="space-y-0.5 text-xs text-muted-foreground" data-signal-why>
          {item.why.map((r, i) => (
            <li key={i}>
              <bdi>{whyText(t, r, f.ago, f.n)}</bdi>
            </li>
          ))}
        </ul>
        {item.reasons && item.reasons.length > 0 ? <ReasonList reasons={item.reasons} compact /> : null}
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 pt-0.5">
          {has("open") ? (
            <Link
              href={openHref}
              className="inline-flex items-center gap-1 text-xs font-medium text-nq-action hover:underline hover:underline-offset-4"
              data-action="open"
            >
              {t(`opps.open.${item.kind}`)}
              <ArrowRightIcon className="size-3 rtl:-scale-x-100" aria-hidden />
            </Link>
          ) : null}
          {has("convert_lead") ? (
            <Button size="sm" variant="secondary" disabled={busy} onClick={convert} data-action="convert-lead">
              <ArrowRightLeftIcon />
              {busy ? t("common.working") : t("leads.convert")}
            </Button>
          ) : null}
          {has("accept_prospect") ? (
            <Button size="sm" disabled={busy} onClick={() => decide("accept")} data-action="accept-prospect">
              <CheckIcon />
              {t("prospects.accept")}
            </Button>
          ) : null}
          {has("dismiss_prospect") ? (
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => decide("reject")} data-action="dismiss-prospect">
              <XIcon />
              {t("opps.dismiss")}
            </Button>
          ) : null}
        </div>
      </div>
      {prospect?.mode === "accept" ? (
        <AcceptDialog p={prospect.p} onClose={() => setProspect(null)} onDone={onDone} />
      ) : null}
      {prospect?.mode === "reject" ? (
        <RejectDialog p={prospect.p} onClose={() => setProspect(null)} onDone={onDone} />
      ) : null}
    </li>
  )
}

/** The number on the Signals tab: how many signals there are across the kinds the viewer can read. */
export function signalTotal(s: OpportunitySignals | undefined): number | null {
  if (!s) return null
  return KINDS.reduce((sum, k) => sum + (s.counts[k.count] ?? 0), 0)
}
