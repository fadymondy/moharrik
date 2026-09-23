"use client"

/*
  Ideal customer profile (MH-395): who this workspace sells to, as criteria with weights, and the
  signals that say a contact is warming up. Every contact and company is scored against it
  (internal/icp): score = Σ weight × how much of each criterion is present, over the sum of the
  positive weights, 0-100. A criterion left empty is not scored at all.

  Saving rescores the whole workspace before answering, so the ranking beside the form is the
  new one the moment the save returns — each contact with the reasons behind its score.
*/

import Link from "next/link"
import { useEffect, useMemo, useState, type FormEvent } from "react"
import { TargetIcon, UsersIcon } from "lucide-react"
import { toast } from "sonner"

import { Bdi, isolate } from "@/components/bidi"
import { Ltr } from "@/components/copy-field"
import { LifecycleBadge, ScoreButton } from "@/components/crm/icp"
import { Panel } from "@/components/overview/overview-cards"
import { SectionHeader } from "@/components/public-frame"
import { ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, ICP_SIGNALS, type Contact, type IcpCriterion, type IcpProfile, type IcpProfileOut } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useContactsPage, useIcpProfile } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

const SIZES = ["1-10", "11-50", "51-200", "201-1000", "1000+"] as const
type TextCriterion = "industries" | "regions" | "keywords"

/** "Hospitality, Cafés، Catering" → the values, split on Latin or Arabic commas. */
function splitValues(s: string): string[] {
  const seen = new Set<string>()
  return s
    .split(/[,،\n]/)
    .map((v) => v.trim().replace(/\s+/g, " "))
    .filter((v) => v && !seen.has(v.toLowerCase()) && (seen.add(v.toLowerCase()), true))
}

type Draft = {
  text: Record<TextCriterion, string>
  weights: Record<TextCriterion | "sizes", string>
  sizes: string[]
  signals: Record<string, string>
}

function toDraft(p: IcpProfile): Draft {
  const w = (c: IcpCriterion) => String(c.weight)
  return {
    text: { industries: (p.industries.values ?? []).join(", "), regions: (p.regions.values ?? []).join(", "), keywords: (p.keywords.values ?? []).join(", ") },
    weights: { industries: w(p.industries), sizes: w(p.sizes), regions: w(p.regions), keywords: w(p.keywords) },
    sizes: p.sizes.values ?? [],
    signals: Object.fromEntries(ICP_SIGNALS.map((s) => [s, String(p.signals?.[s] ?? 0)])),
  }
}

const weightOk = (s: string) => s.trim() !== "" && Number.isFinite(Number(s)) && Math.abs(Number(s)) <= 10

export default function IcpSettingsPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("icp.title")} · ${ws.summary.name}`)
  const prof = useIcpProfile(ws.id)

  return (
    <>
      <SectionHeader micro={t("nav.settings")} title={t("icp.title")} description={t("icp.hint")} />
      {prof.error ? <ErrorState error={prof.error} /> : !prof.data ? <LoadingRows rows={6} /> : <Editor out={prof.data} onSaved={(o) => prof.mutate(o, { revalidate: false })} />}
    </>
  )
}

function Editor({ out, onSaved }: { out: IcpProfileOut; onSaved: (o: IcpProfileOut) => void }) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const [draft, setDraft] = useState<Draft>(() => toDraft(out.profile))
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const ranking = useContactsPage(ws.id, { sort: "score", limit: 8 })
  const readOnly = !out.can_edit

  useEffect(() => setDraft(toDraft(out.profile)), [out.profile])

  const allWeights = [...Object.values(draft.weights), ...Object.values(draft.signals)]
  const valid = allWeights.every(weightOk)

  async function save(e: FormEvent) {
    e.preventDefault()
    if (!valid) return
    setBusy(true)
    setErr(null)
    const crit = (k: TextCriterion) => ({ values: splitValues(draft.text[k]), weight: Number(draft.weights[k]) })
    const body: IcpProfile = {
      industries: crit("industries"),
      regions: crit("regions"),
      keywords: crit("keywords"),
      sizes: { values: draft.sizes, weight: Number(draft.weights.sizes) },
      signals: Object.fromEntries(ICP_SIGNALS.map((s) => [s, Number(draft.signals[s])])),
    }
    try {
      const saved = await api<IcpProfileOut>("/api/w/icp", { method: "PUT", json: body, workspaceId: ws.id })
      onSaved(saved)
      await ranking.mutate()
      toast.success(t("icp.saved"))
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const weightInput = (id: string, value: string, set: (v: string) => void) => (
    <Input
      id={id}
      type="number"
      inputMode="decimal"
      step={0.5}
      min={-10}
      max={10}
      dir="ltr"
      className="w-24"
      value={value}
      onChange={(e) => set(e.target.value)}
      disabled={readOnly}
      aria-invalid={!weightOk(value)}
      data-weight={id}
    />
  )

  const textCriterion = (k: TextCriterion) => {
    const values = splitValues(draft.text[k])
    return (
      <div key={k} className="grid gap-3 border-b border-line px-5 py-4 last:border-b-0 sm:grid-cols-[1fr_auto]" data-criterion={k}>
        <Field>
          <FieldLabel htmlFor={`icp-${k}`}>{t(`icp.crit.${k}`)}</FieldLabel>
          <Input
            id={`icp-${k}`}
            value={draft.text[k]}
            onChange={(e) => setDraft((d) => ({ ...d, text: { ...d.text, [k]: e.target.value } }))}
            placeholder={t(`icp.crit.${k}Placeholder`)}
            dir="auto"
            disabled={readOnly}
            maxLength={2000}
          />
          <FieldDescription>{t(`icp.crit.${k}Hint`)}</FieldDescription>
          {values.length ? (
            <div className="flex flex-wrap gap-1.5" data-values={k}>
              {values.map((v) => (
                <Badge key={v} variant="outline">
                  <Bdi>{v}</Bdi>
                </Badge>
              ))}
            </div>
          ) : null}
        </Field>
        <Field>
          <FieldLabel htmlFor={`icp-${k}-weight`}>{t("icp.weight")}</FieldLabel>
          {weightInput(`icp-${k}-weight`, draft.weights[k], (v) => setDraft((d) => ({ ...d, weights: { ...d.weights, [k]: v } })))}
        </Field>
      </div>
    )
  }

  return (
    <div className="grid gap-6 px-4 pb-10 sm:px-6 xl:grid-cols-[minmax(0,1fr)_24rem]">
      <form onSubmit={save} noValidate className="flex min-w-0 flex-col gap-6" data-form="icp">
        {readOnly ? <p className="text-sm text-grid-muted">{t("icp.readOnly")}</p> : null}
        {!out.configured ? <p className="text-sm text-grid-muted" data-icp-defaults>{t("icp.defaults")}</p> : null}

        <Panel id="icp-fit" title={t("icp.fitSection")} icon={<TargetIcon />}>
          <p className="px-5 pt-3 text-xs text-grid-muted">{t("icp.fitHint")}</p>
          {textCriterion("industries")}
          <div className="grid gap-3 border-b border-line px-5 py-4 sm:grid-cols-[1fr_auto]" data-criterion="sizes">
            <fieldset>
              <legend className="mb-2 text-sm font-medium">{t("icp.crit.sizes")}</legend>
              <div className="flex flex-wrap gap-x-5 gap-y-2">
                {SIZES.map((sz) => (
                  <label key={sz} className="inline-flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={draft.sizes.includes(sz)}
                      disabled={readOnly}
                      onCheckedChange={(on) => setDraft((d) => ({ ...d, sizes: on ? [...d.sizes, sz] : d.sizes.filter((x) => x !== sz) }))}
                      data-size={sz}
                    />
                    <Ltr>{sz}</Ltr>
                  </label>
                ))}
              </div>
              <p className="mt-2 text-sm text-grid-muted">{t("icp.crit.sizesHint")}</p>
            </fieldset>
            <Field>
              <FieldLabel htmlFor="icp-sizes-weight">{t("icp.weight")}</FieldLabel>
              {weightInput("icp-sizes-weight", draft.weights.sizes, (v) => setDraft((d) => ({ ...d, weights: { ...d.weights, sizes: v } })))}
            </Field>
          </div>
          {textCriterion("regions")}
          {textCriterion("keywords")}
        </Panel>

        <Panel id="icp-signals" title={t("icp.signalsSection")} icon={<UsersIcon />}>
          <p className="px-5 pt-3 text-xs text-grid-muted">{t("icp.signalsHint")}</p>
          <ul className="divide-y divide-line">
            {ICP_SIGNALS.map((s) => (
              <li key={s} className="flex items-center gap-4 px-5 py-3" data-signal={s}>
                <label htmlFor={`icp-signal-${s}`} className="min-w-0 flex-1">
                  <span className="block text-sm font-medium">{t(`icp.signal.${s}`)}</span>
                  <span className="block text-xs text-grid-muted">{t(`icp.signal.${s}.hint`)}</span>
                </label>
                {weightInput(`icp-signal-${s}`, draft.signals[s], (v) => setDraft((d) => ({ ...d, signals: { ...d.signals, [s]: v } })))}
              </li>
            ))}
          </ul>
        </Panel>

        <p className="text-xs text-grid-muted">{t("icp.formula")}</p>
        {!readOnly ? (
          <div className="flex flex-wrap items-center gap-3">
            <Button type="submit" disabled={busy || !valid} data-save-icp>
              {busy ? t("common.working") : t("icp.save")}
            </Button>
            {!valid ? <span className="text-sm text-grid-danger-text">{t("icp.weightRange")}</span> : null}
            {err ? <span className="text-sm text-grid-danger-text">{err}</span> : null}
          </div>
        ) : null}
      </form>

      <aside className="min-w-0" data-icp-ranking>
        <Panel
          id="icp-ranking"
          title={t("icp.ranking")}
          icon={<TargetIcon />}
          meta={<span className="ms-auto text-xs text-grid-muted">{t("icp.scoredOf", { n: formatNumber(out.scored), total: formatNumber(out.contacts) })}</span>}
        >
          {ranking.error ? (
            <ErrorState error={ranking.error} />
          ) : !ranking.data ? (
            <LoadingRows rows={4} />
          ) : (ranking.data.contacts ?? []).length === 0 ? (
            <p className="px-5 py-6 text-sm text-grid-muted">{t("icp.noContacts")}</p>
          ) : (
            <ol className="divide-y divide-line" data-ranking>
              {(ranking.data.contacts ?? []).map((c, i) => (
                <RankRow key={c.id} i={i} c={c} />
              ))}
            </ol>
          )}
          <p className="border-t border-line px-5 py-2.5 text-xs text-grid-muted">{t("icp.rankingHint")}</p>
        </Panel>
      </aside>
    </div>
  )
}

function RankRow({ i, c }: { i: number; c: Contact }) {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const top = useMemo(() => [...(c.score_reasons ?? [])].filter((r) => r.contribution > 0).sort((a, b) => b.contribution - a.contribution)[0], [c.score_reasons])
  return (
    <li className="flex items-center gap-3 px-5 py-2.5" data-rank-row={c.email}>
      <span className="w-5 shrink-0 text-end text-xs text-grid-muted tabular-nums">{formatNumber(i + 1)}</span>
      <span className="min-w-0 flex-1">
        <Link href={`/${locale}/w/${ws.slug}/contacts/${c.id}`} className="block truncate text-sm font-medium hover:underline">
          {c.name ? <Bdi>{c.name}</Bdi> : <Ltr>{c.email}</Ltr>}
        </Link>
        <span className="flex items-center gap-1.5 truncate text-xs text-grid-muted">
          <LifecycleBadge stage={c.lifecycle ?? ""} />
          {top ? <span className="truncate">{t(["industry", "size", "region", "keywords"].includes(top.dimension) ? `icp.dim.${top.dimension}` : `icp.signal.${top.dimension}`)}</span> : null}
        </span>
      </span>
      <ScoreButton score={c.score} reasons={c.score_reasons} who={isolate(c.name || c.email)} />
    </li>
  )
}
