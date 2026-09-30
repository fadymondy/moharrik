"use client"

import { useEffect, useState, type FormEvent } from "react"
import { LoaderCircleIcon } from "lucide-react"
import Link from "next/link"
import { Badge, Button, Field, FieldDescription, FieldError, FieldLabel, Input, Switch, Textarea, toast } from "@fadymondy/nasaq/web"

import { PickSelect } from "@/components/crm/shared"
import { useFormatDateTime } from "@/components/research/shared"
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
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const fmt = useFormatDateTime()
  const canEdit = ws.can("research:run")
  const p = data.profile
  const [offer, setOffer] = useState(p.offer)
  const [exclude, setExclude] = useState(join(p.exclude_keywords))
  const [queries, setQueries] = useState(p.queries.join("\n"))
  const [weights, setWeights] = useState<Record<string, string>>(() =>
    Object.fromEntries(SIGNAL_TYPES.map((s) => [s, String(p.signal_weights[s] ?? 1)])),
  )
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
          exclude_keywords: split(exclude),
          queries: queries
            .split("\n")
            .map((q) => q.trim())
            .filter(Boolean),
          signal_weights: Object.fromEntries(
            Object.entries(weights).map(([k, v]) => [k, Math.max(0, Math.min(3, Number(v) || 0))]),
          ),
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
      <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{t("prospects.profileHint")}</p>
      <form onSubmit={save} noValidate className="mt-5 max-w-3xl space-y-5">
        <fieldset disabled={!canEdit} className="space-y-5">
          <Field>
            <FieldLabel htmlFor="pp-offer">{t("prospects.offer")}</FieldLabel>
            <Textarea
              id="pp-offer"
              dir="auto"
              rows={2}
              maxLength={600}
              value={offer}
              onChange={(e) => setOffer(e.target.value)}
            />
            <FieldDescription>{t("prospects.offerHint")}</FieldDescription>
          </Field>
          <div data-icp-who="" className="rounded-md border border-nq-line p-4">
            <div className="flex items-center justify-between gap-2">
              <p className="text-sm font-medium">{t("prospects.icpWho")}</p>
              <Link
                href={`/${locale}/w/${ws.slug}/settings/icp`}
                className="text-xs text-nq-brand hover:underline"
                data-action="edit-icp"
              >
                {t("prospects.icpEdit")}
              </Link>
            </div>
            <p className="mt-1 text-xs text-muted-foreground">{t("prospects.icpWhoHint")}</p>
            {p.icp_configured ? (
              <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
                {(
                  [
                    ["industries", p.industries],
                    ["sizes", p.sizes],
                    ["regions", p.regions],
                    ["keywords", p.keywords],
                  ] as const
                ).map(([key, values]) => (
                  <div key={key}>
                    <p className="text-xs font-medium text-muted-foreground">{t(`prospects.${key}`)}</p>
                    <div className="mt-1 flex flex-wrap gap-1" dir="auto">
                      {values.length ? (
                        values.map((v) => (
                          <Badge key={v} variant="neutral">
                            {v}
                          </Badge>
                        ))
                      ) : (
                        <span className="text-xs text-muted-foreground">{t("prospects.icpEmpty")}</span>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <p className="mt-2 text-xs text-nq-warning-text">{t("prospects.icpNotConfigured")}</p>
            )}
          </div>
          <Field>
            <FieldLabel htmlFor="pp-ex">{t("prospects.exclude")}</FieldLabel>
            <Input
              id="pp-ex"
              dir="auto"
              value={exclude}
              onChange={(e) => setExclude(e.target.value)}
              placeholder="crypto"
            />
            <FieldDescription>{t("prospects.listHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="pp-q">{t("prospects.queries")}</FieldLabel>
            <Textarea
              id="pp-q"
              dir="auto"
              rows={3}
              value={queries}
              onChange={(e) => setQueries(e.target.value)}
            />
            <FieldDescription>{t("prospects.queriesHint")}</FieldDescription>
          </Field>
          {p.effective_queries.length ? (
            <div
              className="text-xs text-muted-foreground"
              data-effective-queries={p.effective_queries.length}
            >
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
            <p className="text-xs text-nq-warning-text">{t("prospects.profileEmpty")}</p>
          )}
          <div>
            <p className="text-sm font-medium">{t("prospects.weights")}</p>
            <p className="text-xs text-muted-foreground">{t("prospects.weightsHint")}</p>
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
          <div className="flex flex-wrap items-end gap-4 border-t border-nq-line pt-4">
            <div className="flex items-center gap-2">
              <Switch
                id="pp-on"
                checked={enabled}
                onCheckedChange={(v) => setEnabled(!!v)}
                disabled={!data.can_search && !p.enabled}
              />
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
          <p className="text-xs text-muted-foreground">
            {t("prospects.scheduleHint", {
              used: formatNumber(data.budget.runs_today),
              budget: formatNumber(data.budget.daily_run_budget),
            })}
            {p.last_status
              ? ` ${t("prospects.lastPass", { date: fmt(p.last_run_at), status: t(`prospects.last.${p.last_status}`) })}`
              : ""}
          </p>
        </fieldset>
        {err ? <FieldError>{err}</FieldError> : null}
        {canEdit ? (
          <Button type="submit" disabled={busy} data-action="save-profile">
            {busy ? <LoaderCircleIcon className="animate-spin" /> : null}
            {t("prospects.saveProfile")}
          </Button>
        ) : (
          <p className="text-sm text-muted-foreground">{t("prospects.profileReadOnly")}</p>
        )}
      </form>
    </section>
  )
}
