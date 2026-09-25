"use client"

import Link from "next/link"
import { useEffect, useMemo, useState } from "react"
import { BookmarkIcon, RadioIcon, SettingsIcon } from "lucide-react"
import { toast } from "sonner"

import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { ItemLine, TopicCard, useDayLabel, useWhen } from "@/components/trends/shared"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type NewsItem, type TrendTopic, type TrendTopicPage } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useFeedSources, useNewsPages, useTrendTopics } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

const STATES = ["open", "saved", "reviewed", "dismissed", "all"] as const

/** Typed text, settled: the list is asked once the reader stops typing. */
function useSettled(value: string, ms = 300) {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setSettled(value.trim()), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return settled
}

/**
 * Trends (MH-385): what the workspace's sources are saying, as STORIES — items
 * from different outlets about the same thing, grouped, scored with the
 * reasons, summarised, and reviewed. The raw items are still here, paged, on
 * the second tab.
 */
export default function TrendsPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.trends")} · ${ws.summary.name}`)
  const canManage = ws.can("trends:manage")
  const [tab, setTab] = useState("topics")
  const [updatedAt, setUpdatedAt] = useState<string | null>(null)
  const when = useWhen()

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.trends")}
        description={t("trends.hint")}
        action={
          canManage ? (
            <Button
              variant="outline"
              size="sm"
              nativeButton={false}
              render={<Link href={`/${locale}/w/${ws.slug}/trends/sources`} />}
            >
              <SettingsIcon />
              {t("trends.sources")}
            </Button>
          ) : undefined
        }
      />
      <Tabs value={tab} onValueChange={(v) => setTab(String(v))} className="gap-0">
        <div className="flex flex-wrap items-center justify-between gap-2 px-6 pb-3">
          <TabsList variant="line">
            <TabsTrigger value="topics" data-tab="topics">
              {t("trends.tabTopics")}
            </TabsTrigger>
            <TabsTrigger value="items" data-tab="items">
              {t("trends.tabItems")}
            </TabsTrigger>
          </TabsList>
          <p
            className="flex items-center gap-1.5 text-xs text-muted-foreground"
            aria-live="polite"
            data-live
          >
            <RadioIcon className="size-3.5 text-primary" aria-hidden />
            {updatedAt ? t("trends.liveUpdated", { when: when(updatedAt) }) : t("trends.live")}
          </p>
        </div>
        <TabsContent value="topics">
          <TopicsTab onLive={() => setUpdatedAt(new Date().toISOString())} />
        </TabsContent>
        <TabsContent value="items">
          <ItemsTab />
        </TabsContent>
      </Tabs>
      <div className="pb-8" />
    </>
  )
}

function TopicsTab({ onLive }: { onLive: () => void }) {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const canManage = ws.can("trends:manage")
  const dayLabel = useDayLabel()
  const [state, setState] = useState<string>("open")
  const [source, setSource] = useState("")
  const [day, setDay] = useState("")
  const [q, setQ] = useState("")
  const search = useSettled(q)

  const sources = useFeedSources(ws.id)
  const pages = useTrendTopics(ws.id, { state, source, day, q: search })
  const first = pages.data?.[0]
  const topics = useMemo(() => (pages.data ?? []).flatMap((p) => p.topics), [pages.data])
  const hasMore = !!pages.data?.[pages.data.length - 1]?.has_more
  const loadingMore = pages.isValidating && pages.size > (pages.data?.length ?? 0)

  // Live: a collection pass (or another tab's review) re-reads what is shown.
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "trends") {
      void pages.mutate()
      void sources.mutate()
      onLive()
    }
  })

  // A handled topic leaves a list it no longer belongs to; otherwise it updates in place.
  function onChange(updated: TrendTopic) {
    void pages.mutate(
      (data?: TrendTopicPage[]) =>
        data?.map((p) => ({
          ...p,
          topics:
            state === "all" || updated.state === state
              ? p.topics.map((x) => (x.id === updated.id ? updated : x))
              : p.topics.filter((x) => x.id !== updated.id),
        })),
      { revalidate: true },
    )
  }

  const count = (s: string) => (first ? first.counts[s as keyof typeof first.counts] : undefined)
  const stateOpts = STATES.map((s) => ({
    value: s,
    label:
      count(s) === undefined
        ? t(`trends.state.${s}`)
        : `${t(`trends.state.${s}`)} (${formatNumber(count(s)!)})`,
  }))
  const sourceOpts = (sources.data?.sources ?? []).map((s) => ({ value: s.id, label: s.name }))
  const dayOpts = (first?.days ?? []).map((d) => ({
    value: d.day,
    label: `${dayLabel(d.day)} (${formatNumber(d.count)})`,
  }))
  if (day && !dayOpts.some((o) => o.value === day))
    dayOpts.unshift({ value: day, label: dayLabel(day) })

  // Topics grouped under their day, in list order.
  const groups: { day: string; topics: TrendTopic[] }[] = []
  for (const topic of topics) {
    const last = groups[groups.length - 1]
    if (last && last.day === topic.day) last.topics.push(topic)
    else groups.push({ day: topic.day, topics: [topic] })
  }
  const total = first ? (state === "all" ? first.counts.all : (count(state) ?? 0)) : 0
  const filtered = !!(source || day || search)
  const noSources = (sources.data?.sources ?? []).length === 0

  return (
    <section>
      <div
        className="grid grid-cols-1 gap-2 px-6 pb-4 sm:grid-cols-2 lg:flex lg:flex-wrap lg:items-center"
        role="group"
        aria-label={t("trends.filters")}
      >
        <div className="lg:w-48" data-filter="state">
          <PickSelect
            id="trends-state"
            value={state}
            onChange={(v) => setState(v || "open")}
            options={stateOpts}
          />
        </div>
        <div className="lg:w-52" data-filter="day">
          <PickSelect
            id="trends-day"
            value={day}
            onChange={setDay}
            options={dayOpts}
            noneLabel={t("trends.anyDay")}
          />
        </div>
        <div className="lg:w-52" data-filter="source">
          <PickSelect
            id="trends-source"
            value={source}
            onChange={setSource}
            options={sourceOpts}
            noneLabel={t("trends.allSources")}
          />
        </div>
        <Input
          aria-label={t("trends.searchTopics")}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("trends.searchTopics")}
          className="lg:w-56"
        />
      </div>

      {pages.error ? (
        <ErrorState error={pages.error} />
      ) : !pages.data ? (
        <LoadingRows rows={5} />
      ) : topics.length === 0 ? (
        <EmptyState
          title={
            noSources
              ? t("trends.empty")
              : filtered
                ? t("trends.noMatches")
                : t(`trends.emptyState.${state}`)
          }
          body={noSources && canManage ? t("trends.emptyBody") : undefined}
          action={
            noSources && canManage ? (
              <Button
                variant="outline"
                size="sm"
                nativeButton={false}
                render={<Link href={`/${locale}/w/${ws.slug}/trends/sources`} />}
              >
                {t("trends.addSource")}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <>
          <p
            className="px-6 pb-2 text-xs text-muted-foreground"
            data-topics-summary={`${topics.length}/${total}`}
          >
            {t("trends.showingTopics", {
              shown: formatNumber(topics.length),
              total: formatNumber(total),
            })}
          </p>
          <div className="flex flex-col gap-6" data-trend-topics>
            {groups.map((g) => (
              <section key={g.day} aria-labelledby={`day-${g.day}`}>
                <h2 id={`day-${g.day}`} className="grid-micro px-6 pb-1">
                  {dayLabel(g.day)}
                </h2>
                <ul className="divide-y divide-line border-y border-line">
                  {g.topics.map((topic) => (
                    <TopicCard
                      key={topic.id}
                      topic={topic}
                      href={`/${locale}/w/${ws.slug}/trends/${topic.id}`}
                      onChange={onChange}
                    />
                  ))}
                </ul>
              </section>
            ))}
          </div>
          {hasMore ? (
            <div className="flex justify-center px-6 pt-5">
              <Button
                variant="outline"
                onClick={() => void pages.setSize(pages.size + 1)}
                disabled={loadingMore}
                data-load-more="topics"
              >
                {loadingMore ? t("trends.loadingMore") : t("trends.loadMore")}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </section>
  )
}

function ItemsTab() {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const dayLabel = useDayLabel()
  const [source, setSource] = useState("")
  const [day, setDay] = useState("")
  const [saved, setSaved] = useState(false)
  const [q, setQ] = useState("")
  const search = useSettled(q)

  const sources = useFeedSources(ws.id)
  const pages = useNewsPages(ws.id, { source, day, saved, q: search })
  const items = useMemo(() => (pages.data ?? []).flatMap((p) => p.items), [pages.data])
  const hasMore = !!pages.data?.[pages.data.length - 1]?.has_more
  const loadingMore = pages.isValidating && pages.size > (pages.data?.length ?? 0)
  const total = pages.data?.[0]?.total ?? 0

  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "trends") void pages.mutate()
  })

  // The last fortnight, today first (UTC, as the API counts days).
  const dayOpts = useMemo(() => {
    const out: { value: string; label: string }[] = []
    const now = Date.now()
    for (let i = 0; i < 14; i++) {
      const d = new Date(now - i * 86400000).toISOString().slice(0, 10)
      out.push({ value: d, label: dayLabel(d) })
    }
    return out
  }, [dayLabel])

  return (
    <section>
      <div
        className="grid grid-cols-1 gap-2 px-6 pb-4 sm:grid-cols-2 lg:flex lg:flex-wrap lg:items-center"
        role="group"
        aria-label={t("trends.filters")}
      >
        <div className="lg:w-52">
          <PickSelect
            id="items-day"
            value={day}
            onChange={setDay}
            options={dayOpts}
            noneLabel={t("trends.anyDay")}
          />
        </div>
        <div className="lg:w-52">
          <PickSelect
            id="items-source"
            value={source}
            onChange={setSource}
            options={(sources.data?.sources ?? []).map((s) => ({ value: s.id, label: s.name }))}
            noneLabel={t("trends.allSources")}
          />
        </div>
        <Input
          aria-label={t("trends.search")}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("trends.search")}
          className="lg:w-56"
        />
        <Button
          type="button"
          variant={saved ? "default" : "outline"}
          aria-pressed={saved}
          onClick={() => setSaved((v) => !v)}
          data-filter="saved"
          className="lg:w-auto"
        >
          <BookmarkIcon />
          {t("trends.filterSaved")}
        </Button>
      </div>
      {pages.error ? (
        <ErrorState error={pages.error} />
      ) : !pages.data ? (
        <LoadingRows rows={5} />
      ) : items.length === 0 ? (
        <EmptyState title={total === 0 ? t("trends.empty") : t("trends.noMatches")} />
      ) : (
        <>
          <p className="px-6 pb-2 text-xs text-muted-foreground" data-items-summary={items.length}>
            {t("trends.showingItems", {
              shown: formatNumber(items.length),
              total: formatNumber(total),
            })}
          </p>
          <ul className="divide-y divide-line border-y border-line" data-news-list>
            {items.map((item) => (
              <NewsRow key={item.id} item={item} onChange={() => void pages.mutate()} />
            ))}
          </ul>
          {hasMore ? (
            <div className="flex justify-center px-6 pt-5">
              <Button
                variant="outline"
                onClick={() => void pages.setSize(pages.size + 1)}
                disabled={loadingMore}
                data-load-more="items"
              >
                {loadingMore ? t("trends.loadingMore") : t("trends.loadMore")}
              </Button>
            </div>
          ) : null}
        </>
      )}
    </section>
  )
}

function NewsRow({ item, onChange }: { item: NewsItem; onChange: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const isSaved = !!item.saved_at

  async function toggleSaved() {
    try {
      await api(`/api/w/news/${encodeURIComponent(item.id)}`, {
        method: "PATCH",
        json: { saved: !isSaved },
        workspaceId: ws.id,
      })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <li
      className="flex items-start gap-3 px-6 py-3"
      data-news={item.id}
      data-news-saved={isSaved ? "true" : "false"}
    >
      <div className="min-w-0 flex-1">
        <ItemLine item={item} showSummary />
      </div>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-pressed={isSaved}
        aria-label={isSaved ? t("trends.unsave") : t("trends.save")}
        onClick={toggleSaved}
        className={isSaved ? "text-primary" : undefined}
      >
        <BookmarkIcon fill={isSaved ? "currentColor" : "none"} />
      </Button>
    </li>
  )
}
