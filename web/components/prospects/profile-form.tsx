"use client"

import { useEffect, useState, type FormEvent } from "react"
import { LoaderCircleIcon } from "lucide-react"
import { toast } from "sonner"

import { PickSelect } from "@/components/crm/shared"
import { useFormatDateTime } from "@/components/research/shared"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, SIGNAL_TYPES, type ProspectProfileOut } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

const split = (s: string) =>
  s
    .split(/[,،\n]/)
    .map((x) => x.trim())
    .filter(Boolean)
const join = (l: string[]) => l.join(", ")

/**
 * The ideal customer (MH-397): what discovery searches for and scores
 * against. Saving rescores the prospects waiting for review.
 */
export function ProfileForm({ data, onSaved }: { data: ProspectProfileOut; onSaved: () => void }) {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const fmt = useFormatDateTime()
  const canEdit = ws.can("research:run")
  const p = data.profile
  const [offer, setOffer] = useState(p.offer)
  const [industries, setIndustries] = useState(join(p.industries))
  const [keywords, setKeywords] = useState(join(p.keywords))
  const [regions, setRegions] = useState(join(p.regions))
  const [exclude, setExclude] = useState(join(p.exclude_keywords))
  const [queries, setQueries] = useState(p.queries.join("\n"))
  const [weights, setWeights] = useState<Record<string, string>>(() => Object.fromEntries(SIGNAL_TYPES.map((s) => [s, String(p.signal_weights[s] ?? 1)])))
  const [enabled, setEnabled] = useState(p.enabled)
  const [cadence, setCadence] = useState<string>(p.cadence)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  useEffect(() => setEnabled(p.enabled), [p.enabled])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/prospect-profile", {
        method: "PUT",
        json: {
          offer: offer.trim(),
          industries: split(industries),
          keywords: split(keywords),
          regions: split(regions),
          exclude_keywords: split(exclude),
          queries: queries
            .split("\n")
            .map((q) => q.trim())
            .filter(Boolean),
          signal_weights: Object.fromEntries(Object.entries(weights).map(([k, v]) => [k, Math.max(0, Math.min(3, Number(v) || 0))])),
          enabled,
          cadence,
        },
        workspaceId: ws.id,
      })
      toast.success(t("prospects.profileSaved"))
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="px-6 py-6" data-form="prospect-profile">
      <h2 className="text-base font-medium">{t("prospects.profile")}</h2>
      <p className="mt-1 max-w-3xl text-sm text-grid-muted">{t("prospects.profileHint")}</p>
      <form onSubmit={save} noValidate className="mt-5 max-w-3xl space-y-5">
        <fieldset disabled={!canEdit} className="space-y-5">
          <Field>
            <FieldLabel htmlFor="pp-offer">{t("prospects.offer")}</FieldLabel>
            <Textarea id="pp-offer" dir="auto" rows={2} maxLength={600} value={offer} onChange={(e) => setOffer(e.target.value)} />
            <FieldDescription>{t("prospects.offerHint")}</FieldDescription>
          </Field>
          <FieldGroup className="sm:grid sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="pp-ind">{t("prospects.industries")}</FieldLabel>
              <Input id="pp-ind" dir="auto" value={industries} onChange={(e) => setIndustries(e.target.value)} placeholder="fintech, logistics" />
            </Field>
            <Field>
              <FieldLabel htmlFor="pp-kw">{t("prospects.keywords")}</FieldLabel>
              <Input id="pp-kw" dir="auto" value={keywords} onChange={(e) => setKeywords(e.target.value)} placeholder="payments, e-commerce" />
            </Field>
            <Field>
              <FieldLabel htmlFor="pp-reg">{t("prospects.regions")}</FieldLabel>
              <Input id="pp-reg" dir="auto" value={regions} onChange={(e) => setRegions(e.target.value)} placeholder="Egypt, Saudi Arabia" />
            </Field>
            <Field>
              <FieldLabel htmlFor="pp-ex">{t("prospects.exclude")}</FieldLabel>
              <Input id="pp-ex" dir="auto" value={exclude} onChange={(e) => setExclude(e.target.value)} placeholder="crypto" />
            </Field>
          </FieldGroup>
          <p className="-mt-2 text-xs text-grid-muted">{t("prospects.listHint")}</p>
          <Field>
            <FieldLabel htmlFor="pp-q">{t("prospects.queries")}</FieldLabel>
            <Textarea id="pp-q" dir="auto" rows={3} value={queries} onChange={(e) => setQueries(e.target.value)} />
            <FieldDescription>{t("prospects.queriesHint")}</FieldDescription>
          </Field>
          {p.effective_queries.length ? (
            <div className="text-xs text-grid-muted" data-effective-queries={p.effective_queries.length}>
              <p>{t("prospects.willSearch")}</p>
              <ol className="mt-1 list-decimal space-y-0.5 ps-5">
                {p.effective_queries.slice(0, 6).map((q) => (
                  <li key={q} dir="auto">
                    {q}
                  </li>
                ))}
              </ol>
            </div>
          ) : (
            <p className="text-xs text-grid-warn-text">{t("prospects.profileEmpty")}</p>
          )}
          <div>
            <p className="text-sm font-medium">{t("prospects.weights")}</p>
            <p className="text-xs text-grid-muted">{t("prospects.weightsHint")}</p>
            <div className="mt-2 grid grid-cols-2 gap-3 sm:grid-cols-4">
              {SIGNAL_TYPES.map((s) => (
                <Field key={s}>
                  <FieldLabel htmlFor={`pp-w-${s}`} className="text-xs">
                    {t(`research.signal.${s}`)}
                  </FieldLabel>
                  <Input
                    id={`pp-w-${s}`}
                    type="number"
                    min={0}
                    max={3}
                    step={0.1}
                    dir="ltr"
                    value={weights[s]}
                    onChange={(e) => setWeights((w) => ({ ...w, [s]: e.target.value }))}
                  />
                </Field>
              ))}
            </div>
          </div>
          <div className="flex flex-wrap items-end gap-4 border-t border-line pt-4">
            <div className="flex items-center gap-2">
              <Switch id="pp-on" checked={enabled} onCheckedChange={(v) => setEnabled(!!v)} disabled={!data.can_search && !p.enabled} />
              <label htmlFor="pp-on" className="text-sm font-medium">
                {t("prospects.schedule")}
              </label>
            </div>
            <Field className="w-40">
              <FieldLabel htmlFor="pp-cad" className="sr-only">
                {t("research.cadence")}
              </FieldLabel>
              <PickSelect
                id="pp-cad"
                value={cadence}
                onChange={setCadence}
                options={[
                  { value: "daily", label: t("research.cadence.daily") },
                  { value: "weekly", label: t("research.cadence.weekly") },
                ]}
              />
            </Field>
          </div>
          <p className="text-xs text-grid-muted">
            {t("prospects.scheduleHint", { used: formatNumber(data.budget.runs_today), budget: formatNumber(data.budget.daily_run_budget) })}
            {p.last_status ? ` ${t("prospects.lastPass", { date: fmt(p.last_run_at), status: t(`prospects.last.${p.last_status}`) })}` : ""}
          </p>
        </fieldset>
        {err ? <FieldError>{err}</FieldError> : null}
        {canEdit ? (
          <Button type="submit" disabled={busy} data-action="save-profile">
            {busy ? <LoaderCircleIcon className="animate-spin" /> : null}
            {t("prospects.saveProfile")}
          </Button>
        ) : (
          <p className="text-sm text-grid-muted">{t("prospects.profileReadOnly")}</p>
        )}
      </form>
    </section>
  )
}
