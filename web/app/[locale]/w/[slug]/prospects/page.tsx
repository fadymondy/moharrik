"use client"

import Link from "next/link"
import { useEffect, useRef, useState } from "react"
import { ArrowRightIcon, InfoIcon, LoaderCircleIcon, RadarIcon, UserIcon } from "lucide-react"
import { Button, Tabs, TabsList, TabsTab, toast } from "@fadymondy/nasaq/web"

import { Bdi } from "@/components/bidi"
import { ProfileForm } from "@/components/prospects/profile-form"
import { ReasonList, ScoreBadge, StatusBadge } from "@/components/prospects/shared"
import { SectionHeader } from "@/components/public-frame"
import { SignalBadge } from "@/components/research/evidence"
import { isLive } from "@/components/research/shared"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Prospect } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useProspectProfile, useProspects, useResearchRuns } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

const STATUSES = ["new", "accepted", "rejected", "all"] as const

/**
 * Prospect discovery (MH-397): companies research found that look like the
 * ideal customer, scored with the reasons, for a person to accept or reject.
 */
export default function ProspectsPage() {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.prospects")} · ${ws.summary.name}`)
  const canRun = ws.can("research:run")
  const [status, setStatus] = useState<string>("new")
  const profile = useProspectProfile(ws.id)
  const list = useProspects(ws.id, status)
  // Discovery is a research run; the research list polls while one is live.
  const runs = useResearchRuns(ws.id)
  const liveDiscovery = runs.data?.runs.find((r) => r.origin === "discovery" && isLive(r.status))
  const wasLive = useRef(false)
  useEffect(() => {
    // When a discovery run settles, its prospects appear: re-read the list.
    if (wasLive.current && !liveDiscovery) {
      void list.mutate()
      void profile.mutate()
    }
    wasLive.current = !!liveDiscovery
  }, [liveDiscovery, list, profile])
  const [starting, setStarting] = useState(false)

  async function discover() {
    setStarting(true)
    try {
      const res = await api<{ run_id: string; query: string }>("/api/w/prospect-profile/discover", {
        method: "POST",
        workspaceId: ws.id,
      })
      toast.success(t("prospects.discoverStarted", { query: res.query }))
      void runs.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setStarting(false)
    }
  }

  const counts = list.data?.counts
  const prospects = list.data?.prospects ?? []
  const p = profile.data

  return (
    <>
      <SectionHeader
        title={t("nav.prospects")}
        description={t("prospects.hint")}
        action={
          canRun && p?.can_search && p.profile.configured ? (
            <Button
              size="sm"
              onClick={discover}
              disabled={starting || !!liveDiscovery}
              data-action="discover-now"
            >
              {starting || liveDiscovery ? (
                <LoaderCircleIcon className="animate-spin" />
              ) : (
                <RadarIcon />
              )}
              {liveDiscovery ? t("prospects.discoverLive") : t("prospects.discoverNow")}
            </Button>
          ) : undefined
        }
      />

      {p && !p.can_search ? (
        <p
          className="flex items-start gap-2 px-6 pb-4 text-sm text-muted-foreground"
          role="status"
          data-capability="search"
        >
          <InfoIcon className="mt-0.5 size-4 shrink-0 text-nq-warning" />
          <span>{t("prospects.notConfigured")}</span>
        </p>
      ) : null}
      {liveDiscovery ? (
        <p className="flex items-center gap-2 px-6 pb-4 text-sm" data-discovery-live>
          <LoaderCircleIcon className="size-4 animate-spin text-nq-brand" />
          <span>
            {t("prospects.discoverLive")}: <bdi>{liveDiscovery.topic}</bdi>
          </span>
          <Link
            className="text-nq-brand hover:underline"
            href={`/${locale}/w/${ws.slug}/research/${encodeURIComponent(liveDiscovery.id)}`}
          >
            {t("research.watch")}
          </Link>
        </p>
      ) : null}

      <Tabs value={status} onValueChange={(v) => setStatus(String(v))} className="gap-0">
        <div className="px-6 pb-3">
          <TabsList variant="underline">
            {STATUSES.map((s) => (
              <TabsTab key={s} value={s} data-tab={s}>
                {t(`prospects.status.${s}`)}
                {counts && s !== "all" ? (
                  <span className="ms-1 text-muted-foreground tabular-nums">
                    {formatNumber(counts[s] ?? 0)}
                  </span>
                ) : null}
              </TabsTab>
            ))}
          </TabsList>
        </div>
      </Tabs>

      {list.error ? (
        <ErrorState error={list.error} />
      ) : !list.data ? (
        <LoadingRows rows={4} />
      ) : prospects.length === 0 ? (
        status === "new" && counts && counts.accepted + counts.rejected === 0 ? (
          <EmptyState title={t("prospects.empty")} body={t("prospects.emptyBody")} />
        ) : (
          <p className="border-y border-nq-line px-6 py-6 text-sm text-muted-foreground">
            {t("prospects.emptyStatus")}
          </p>
        )
      ) : (
        <ul className="divide-y divide-nq-line border-y border-nq-line" data-prospects>
          {prospects.map((x) => (
            <ProspectRow key={x.id} p={x} />
          ))}
        </ul>
      )}

      <div className="hatch h-8 border-y border-border mt-8" aria-hidden />
      {profile.error ? (
        <ErrorState error={profile.error} />
      ) : p ? (
        <ProfileForm
          data={p}
          onSaved={() => {
            void profile.mutate()
            void list.mutate()
          }}
        />
      ) : (
        <LoadingRows rows={3} />
      )}
      <div className="pb-8" />
    </>
  )
}

function ProspectRow({ p }: { p: Prospect }) {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const href = `/${locale}/w/${ws.slug}/prospects/${encodeURIComponent(p.id)}`
  return (
    <li
      className="flex flex-wrap items-start gap-3 px-6 py-4"
      data-prospect={p.name}
      data-prospect-status={p.status}
    >
      <ScoreBadge score={p.score} />
      <div className="min-w-0 flex-1 space-y-1.5">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          <Link href={href} className="hover:underline">
            <Bdi>{p.name}</Bdi>
          </Link>
          {p.domain ? (
            <span className="text-xs font-normal text-muted-foreground" dir="ltr">
              {p.domain}
            </span>
          ) : null}
          {p.signal_types.map((s) => (
            <SignalBadge key={s} signal={s} />
          ))}
          {p.status !== "new" ? <StatusBadge status={p.status} /> : null}
        </p>
        <ReasonList reasons={p.reasons} compact />
        <p className="flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
          {p.contact.name ? (
            <span className="inline-flex items-center gap-1">
              <UserIcon className="size-3.5" aria-hidden />
              <Bdi>{p.contact.name}</Bdi>
              {p.contact.title ? <span>· {p.contact.title}</span> : null}
            </span>
          ) : null}
          <span>{t("prospects.evidence", { n: formatNumber(p.evidence_count) })}</span>
        </p>
      </div>
      <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={href} />}>
        {t("prospects.open")}
        <ArrowRightIcon className="rtl:rotate-180" />
      </Button>
    </li>
  )
}
