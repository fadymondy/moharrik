"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { useState } from "react"
import {
  ArrowLeftIcon,
  CopyIcon,
  ExternalLinkIcon,
  LanguagesIcon,
  LoaderCircleIcon,
  MailIcon,
} from "lucide-react"
import { Badge, Button, Skeleton, toast } from "@fadymondy/nasaq/web"

import { Bdi } from "@/components/bidi"
import { DecideButtons } from "@/components/prospects/decide"
import { ReasonList, ScoreBadge, StatusBadge } from "@/components/prospects/shared"
import { SectionHeader } from "@/components/public-frame"
import { EvidenceItem, SignalBadge } from "@/components/research/evidence"
import { useFormatDateTime } from "@/components/research/shared"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type ProspectDraft } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useProspect } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function ProspectPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const fmt = useFormatDateTime()
  const { id } = useParams<{ id: string }>()
  const detail = useProspect(ws.id, id)
  const p = detail.data?.prospect
  useDocumentTitle(`${p?.name || t("nav.prospects")} · ${ws.summary.name}`)
  const canDecide = ws.can("contacts:write")
  const base = `/${locale}/w/${ws.slug}`

  return (
    <>
      <SectionHeader
        micro={t("nav.prospects")}
        title={p ? <Bdi>{p.name}</Bdi> : <Skeleton className="h-7 w-60" />}
        description={p?.domain ? <span dir="ltr">{p.domain}</span> : undefined}
        action={
          <Button
            variant="ghost"
            size="sm"
            nativeButton={false}
            render={<Link href={`${base}/prospects`} />}
          >
            <ArrowLeftIcon className="rtl:rotate-180" />
            {t("prospects.back")}
          </Button>
        }
      />
      {detail.error ? (
        <ErrorState error={detail.error} />
      ) : !p ? (
        <LoadingRows rows={5} />
      ) : (
        <>
          <div
            className="flex flex-wrap items-center gap-3 px-6 pb-4"
            data-prospect-detail={p.status}
          >
            <ScoreBadge score={p.score} />
            <StatusBadge status={p.status} />
            {p.signal_types.map((s) => (
              <SignalBadge key={s} signal={s} />
            ))}
            {p.decided_at ? (
              <span className="text-xs text-muted-foreground">
                {t("prospects.decided", { date: fmt(p.decided_at) })}
              </span>
            ) : null}
            <div className="ms-auto">
              {p.status === "new" ? (
                canDecide ? (
                  <DecideButtons p={p} onDone={() => detail.mutate()} />
                ) : (
                  <p className="text-xs text-muted-foreground">{t("prospects.readOnly")}</p>
                )
              ) : p.status === "accepted" ? (
                <div className="flex flex-wrap gap-2 text-sm">
                  {p.crm_company_id ? (
                    <Button
                      variant="secondary"
                      size="sm"
                      nativeButton={false}
                      render={<Link href={`${base}/companies/${p.crm_company_id}`} />}
                    >
                      {t("prospects.openCompany")}
                    </Button>
                  ) : null}
                  {p.crm_contact_id ? (
                    <Button
                      variant="secondary"
                      size="sm"
                      nativeButton={false}
                      render={<Link href={`${base}/contacts/${p.crm_contact_id}`} />}
                    >
                      {t("prospects.openContact")}
                    </Button>
                  ) : null}
                  {p.crm_deal_id ? (
                    <Button
                      variant="secondary"
                      size="sm"
                      nativeButton={false}
                      render={<Link href={`${base}/deals/${p.crm_deal_id}`} />}
                    >
                      {t("prospects.openDeal")}
                    </Button>
                  ) : null}
                </div>
              ) : p.reject_reason ? (
                <p className="text-xs text-muted-foreground" dir="auto">
                  {p.reject_reason}
                </p>
              ) : null}
            </div>
          </div>

          <div className="grid gap-px border-y border-nq-line bg-nq-line lg:grid-cols-2">
            <section className="bg-background px-6 py-5">
              <h2 className="pb-3 text-base font-medium">{t("prospects.why")}</h2>
              <ReasonList reasons={p.reasons} />
            </section>
            <section className="bg-background px-6 py-5" data-contact={p.contact.name || "none"}>
              <h2 className="pb-3 text-base font-medium">{t("prospects.contact")}</h2>
              {p.contact.name || p.contact.email ? (
                <div className="space-y-1 text-sm">
                  {p.contact.name ? (
                    <p className="font-medium">
                      <Bdi>{p.contact.name}</Bdi>
                      {p.contact.title ? (
                        <span className="font-normal text-muted-foreground"> · {p.contact.title}</span>
                      ) : null}
                    </p>
                  ) : null}
                  {p.contact.email ? (
                    <p className="inline-flex items-center gap-1 text-muted-foreground" dir="ltr">
                      <MailIcon className="size-3.5" />
                      {p.contact.email}
                    </p>
                  ) : null}
                  {p.contact.source_url ? (
                    <p className="text-xs text-muted-foreground">
                      {t("prospects.contactFound")}{" "}
                      <a
                        href={p.contact.source_url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-nq-brand hover:underline"
                        dir="ltr"
                      >
                        {hostOf(p.contact.source_url)}
                        <ExternalLinkIcon className="ms-1 inline size-3" />
                      </a>
                    </p>
                  ) : null}
                </div>
              ) : (
                <p className="text-sm text-muted-foreground">{t("prospects.contactNone")}</p>
              )}
            </section>
          </div>

          <Drafts
            prospectId={p.id}
            drafts={detail.data?.drafts ?? []}
            canDraft={canDecide}
            onMade={() => detail.mutate()}
          />

          <div className="hatch h-8 border-y border-border mt-2" aria-hidden />
          <h2 className="px-6 pt-6 pb-1 text-base font-medium">{t("prospects.timeline")}</h2>
          <p className="px-6 pb-3 text-sm text-muted-foreground">{t("prospects.timelineHint")}</p>
          <ol
            className="divide-y divide-nq-line border-y border-nq-line"
            data-timeline={detail.data?.timeline.length ?? 0}
          >
            {(detail.data?.timeline ?? []).map((e) => (
              <EvidenceItem key={e.id} e={e} showRun />
            ))}
          </ol>
        </>
      )}
      <div className="pb-8" />
    </>
  )
}

function hostOf(u: string) {
  try {
    return new URL(u).host.replace(/^www\./, "")
  } catch {
    return u
  }
}

/** Outreach drafts: made on request, shown with how they were written, copied by a person. Never sent. */
function Drafts({
  prospectId,
  drafts,
  canDraft,
  onMade,
}: {
  prospectId: string
  drafts: ProspectDraft[]
  canDraft: boolean
  onMade: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState<"" | "en" | "ar">("")
  const latest = new Map<string, ProspectDraft>()
  for (const d of drafts) if (!latest.has(d.locale)) latest.set(d.locale, d)

  async function make(locale: "en" | "ar") {
    setBusy(locale)
    try {
      await api(`/api/w/prospects/${encodeURIComponent(prospectId)}/draft`, {
        json: { locale },
        workspaceId: ws.id,
      })
      onMade()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy("")
    }
  }

  return (
    <section className="px-6 py-6" data-drafts={drafts.length}>
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-base font-medium">{t("prospects.drafts")}</h2>
        {canDraft ? (
          <div className="ms-auto flex flex-wrap gap-2">
            {(["en", "ar"] as const).map((l) => (
              <Button
                key={l}
                variant="secondary"
                size="sm"
                onClick={() => make(l)}
                disabled={!!busy}
                data-action={`draft-${l}`}
              >
                {busy === l ? <LoaderCircleIcon className="animate-spin" /> : <LanguagesIcon />}
                {l === "en" ? t("prospects.draftEn") : t("prospects.draftAr")}
              </Button>
            ))}
          </div>
        ) : null}
      </div>
      <p className="mt-1 text-sm text-muted-foreground">{t("prospects.draftsHint")}</p>
      {latest.size === 0 ? (
        <p className="mt-3 text-sm text-muted-foreground">{t("prospects.noDrafts")}</p>
      ) : (
        <div className="mt-4 grid gap-4 lg:grid-cols-2">
          {[...latest.values()].map((d) => (
            <DraftCard key={d.id} d={d} />
          ))}
        </div>
      )}
    </section>
  )
}

function DraftCard({ d }: { d: ProspectDraft }) {
  const { t } = useTranslations()
  const model = d.generator.startsWith("model:") ? d.generator.slice(6) : ""
  async function copy() {
    try {
      await navigator.clipboard.writeText(`${d.subject}\n\n${d.body}`)
      toast.success(t("prospects.copied"))
    } catch {
      toast.error(t("common.copyFailed"))
    }
  }
  return (
    <article
      className="border border-nq-line bg-card"
      data-draft={d.locale}
      data-generator={d.generator}
    >
      <header className="flex flex-wrap items-center gap-2 border-b border-nq-line px-4 py-2 text-xs">
        <Badge variant="outline">{d.locale === "ar" ? "العربية" : "English"}</Badge>
        <Badge variant="neutral" data-not-sent>
          {t("prospects.draftNotSent")}
        </Badge>
        <Button variant="ghost" size="sm" className="ms-auto" onClick={copy}>
          <CopyIcon />
          {t("prospects.copyDraft")}
        </Button>
      </header>
      <div className="space-y-2 px-4 py-3 text-sm">
        {/* The chrome follows the page's language; the draft, its own. */}
        <p>
          <span className="text-muted-foreground">{t("prospects.subject")}: </span>
          <span className="font-medium" dir={d.locale === "ar" ? "rtl" : "ltr"} lang={d.locale}>
            {d.subject}
          </span>
        </p>
        <pre
          className="font-sans text-sm leading-relaxed whitespace-pre-wrap break-words"
          dir={d.locale === "ar" ? "rtl" : "ltr"}
          lang={d.locale}
        >
          {d.body}
        </pre>
      </div>
      <p className="border-t border-nq-line px-4 py-2 text-xs text-muted-foreground" dir="auto">
        {model ? t("prospects.draftModel", { model }) : t("prospects.draftTemplate")}
      </p>
    </article>
  )
}
