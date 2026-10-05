"use client"

import Link from "next/link"
import { useState } from "react"
import { toast } from "@fadymondy/nasaq/web"

import { Ltr } from "@/components/copy-field"
import { LifecycleBadge, ScoreMeter, ScoreReasons } from "@/components/crm/icp"
import { PickSelect, useMemberNames } from "@/components/crm/shared"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, LIFECYCLE_STAGES, type ContactDetail, type Lifecycle } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"

const NO_STAGE = "__none"

/**
 * Where the contact is in the lifecycle (MH-395), how it got there, and how well it fits the
 * workspace's ideal customer — the score with every reason behind it.
 */
export function ContactFit({
  contact,
  canWrite,
  onSaved,
}: {
  contact: ContactDetail
  canWrite: boolean
  onSaved: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const members = useMemberNames(ws.id)
  const [busy, setBusy] = useState(false)
  const history = contact.lifecycle_history ?? []
  const when = dateFormat(locale, { dateStyle: "medium", timeStyle: "short" })
  const stageOptions = [
    ...LIFECYCLE_STAGES.map((st) => ({ value: st, label: t(`lifecycle.${st}`) })),
  ]

  async function setStage(next: Lifecycle) {
    setBusy(true)
    try {
      await api(`/api/w/contacts/${encodeURIComponent(contact.id)}`, {
        method: "PATCH",
        json: { lifecycle: next },
        workspaceId: ws.id,
      })
      toast.success(t("lifecycle.saved"))
      onSaved()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="grid gap-4 lg:grid-cols-2" data-contact-fit>
      <div
        className="overflow-hidden rounded-xl border border-nq-line bg-card"
        data-lifecycle-panel
      >
        <div className="flex flex-wrap items-center gap-3 border-b border-nq-line px-5 py-4">
          <h2 className="text-base font-medium">{t("lifecycle.label")}</h2>
          <LifecycleBadge stage={contact.lifecycle} />
          {canWrite ? (
            <div className="ms-auto w-44">
              <PickSelect
                id="contact-lifecycle"
                value={contact.lifecycle || NO_STAGE}
                onChange={(v) => void setStage((v === NO_STAGE ? "" : v) as Lifecycle)}
                options={stageOptions}
                noneLabel={t("lifecycle.none")}
                disabled={busy}
              />
            </div>
          ) : null}
        </div>
        <p className="px-5 pt-3 text-xs text-muted-foreground">{t("lifecycle.hint")}</p>
        {history.length === 0 ? (
          <p className="px-5 py-4 text-sm text-muted-foreground">{t("lifecycle.noHistory")}</p>
        ) : (
          <ol className="divide-y divide-nq-line px-5 pb-2" data-lifecycle-history>
            {history.map((e) => (
              <li
                key={e.id}
                className="flex flex-wrap items-center gap-x-2 gap-y-1 py-2.5 text-sm"
                data-lifecycle-event={e.source}
              >
                <span className="inline-flex items-center gap-1.5">
                  {e.from ? (
                    <LifecycleBadge stage={e.from} />
                  ) : (
                    <span className="text-muted-foreground">{t("lifecycle.none")}</span>
                  )}
                  <span className="text-muted-foreground rtl:-scale-x-100" aria-hidden>
                    →
                  </span>
                  <LifecycleBadge stage={e.to} />
                </span>
                <span className="text-muted-foreground">
                  {e.source === "manual"
                    ? t("lifecycle.source.manualBy", { who: members.name(e.actor_user_id) })
                    : t(`lifecycle.source.${e.source}`)}
                </span>
                <span className="ms-auto text-xs text-muted-foreground">
                  <Ltr>{when.format(new Date(e.at))}</Ltr>
                </span>
              </li>
            ))}
            {contact.lifecycle_history_total > history.length ? (
              <li className="py-2 text-xs text-muted-foreground">
                {t("lifecycle.more", { n: contact.lifecycle_history_total - history.length })}
              </li>
            ) : null}
          </ol>
        )}
      </div>

      <div className="overflow-hidden rounded-xl border border-nq-line bg-card" data-score-panel>
        <div className="flex flex-wrap items-center gap-3 border-b border-nq-line px-5 py-4">
          <h2 className="text-base font-medium">{t("icp.fit")}</h2>
          <ScoreMeter score={contact.score} />
          <Link
            className="ms-auto text-xs text-muted-foreground hover:text-foreground hover:underline"
            href={`/${locale}/w/${ws.slug}/settings/icp`}
          >
            {t("icp.editProfile")}
          </Link>
        </div>
        <p className="px-5 pt-3 text-xs text-muted-foreground">{t("icp.scoreHow")}</p>
        <div className="px-5 pb-2">
          <ScoreReasons reasons={contact.score_reasons ?? []} />
        </div>
      </div>
    </section>
  )
}
