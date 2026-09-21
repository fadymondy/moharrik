"use client"

import Link from "next/link"
import { useState } from "react"
import { BookmarkIcon, ExternalLinkIcon, SettingsIcon } from "lucide-react"
import { toast } from "sonner"

import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type NewsItem, type Topic } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useFeedSources, useNews, useTopics } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function TrendsPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.trends")} · ${ws.summary.name}`)
  const canManage = ws.can("trends:manage")

  const [topic, setTopic] = useState("")
  const [source, setSource] = useState("")
  const [saved, setSaved] = useState(false)
  const [q, setQ] = useState("")
  const [search, setSearch] = useState("")

  const topics = useTopics(ws.id)
  const sources = useFeedSources(ws.id)
  const news = useNews(ws.id, { topic: topic || undefined, source: source || undefined, saved, q: search || undefined })

  const topicOpts = (topics.data?.topics ?? []).map((x) => ({ value: x.id, label: x.name }))
  const sourceOpts = (sources.data?.sources ?? []).map((x) => ({ value: x.id, label: x.name }))
  const byTopic = new Map((topics.data?.topics ?? []).map((x) => [x.id, x] as const))

  return (
    <>
      <SectionHeader
        micro={ws.summary.name}
        title={t("nav.trends")}
        description={t("trends.hint")}
        action={
          canManage ? (
            <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/trends/sources`} />}>
              <SettingsIcon />
              {t("trends.sources")}
            </Button>
          ) : undefined
        }
      />

      <div className="flex flex-wrap items-center gap-2 px-6 pb-4" role="group" aria-label={t("trends.filters")}>
        <div className="w-44">
          <PickSelect id="trends-topic" value={topic} onChange={setTopic} options={topicOpts} noneLabel={t("trends.allTopics")} />
        </div>
        <div className="w-44">
          <PickSelect id="trends-source" value={source} onChange={setSource} options={sourceOpts} noneLabel={t("trends.allSources")} />
        </div>
        <Button type="button" size="sm" variant={saved ? "default" : "outline"} aria-pressed={saved} onClick={() => setSaved((v) => !v)} data-filter="saved">
          {t("trends.filterSaved")}
        </Button>
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            setSearch(q.trim())
          }}
        >
          <Input
            aria-label={t("trends.search")}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t("trends.search")}
            className="w-48"
          />
          <Button type="submit" variant="outline" size="sm">
            {t("trends.searchGo")}
          </Button>
        </form>
      </div>

      {news.error ? (
        <ErrorState error={news.error} />
      ) : !news.data ? (
        <LoadingRows rows={5} />
      ) : news.data.items.length === 0 ? (
        <EmptyState
          title={news.data.total === 0 ? t("trends.empty") : t("trends.noMatches")}
          body={news.data.total === 0 && canManage ? t("trends.emptyBody") : undefined}
          action={
            news.data.total === 0 && canManage ? (
              <Button variant="outline" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/trends/sources`} />}>
                {t("trends.addSource")}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <ul className="divide-y divide-line border-y border-line">
          {news.data.items.map((item) => (
            <NewsRow key={item.id} item={item} byTopic={byTopic} onChange={() => news.mutate()} />
          ))}
        </ul>
      )}
      <div className="pb-8" />
    </>
  )
}

function NewsRow({ item, byTopic, onChange }: { item: NewsItem; byTopic: Map<string, Topic>; onChange: () => void }) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const isSaved = !!item.saved_at

  async function toggleSaved() {
    try {
      await api(`/api/w/news/${encodeURIComponent(item.id)}`, { method: "PATCH", json: { saved: !isSaved }, workspaceId: ws.id })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <li className="flex items-start gap-3 px-6 py-4" data-news={item.id} data-news-saved={isSaved ? "true" : "false"}>
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium break-words">
          {item.url ? (
            <a className="hover:underline" href={item.url} target="_blank" rel="noreferrer">
              {item.title}
              <ExternalLinkIcon className="ms-1 inline size-3.5 align-[-2px] text-grid-muted" aria-hidden />
            </a>
          ) : (
            item.title
          )}
        </p>
        {item.summary ? <p className="mt-1 line-clamp-2 text-sm text-grid-muted break-words">{item.summary}</p> : null}
        <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-grid-muted">
          <span>{item.source_name || t("trends.unknownSource")}</span>
          {item.author ? (
            <>
              <span aria-hidden>·</span>
              <span>{item.author}</span>
            </>
          ) : null}
          <span aria-hidden>·</span>
          <Ltr>{formatDate(item.published_at)}</Ltr>
          {item.topic_ids.map((id) => {
            const topic = byTopic.get(id)
            return topic ? (
              <Badge key={id} variant="secondary">
                {topic.name}
              </Badge>
            ) : null
          })}
        </p>
      </div>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-pressed={isSaved}
        aria-label={isSaved ? t("trends.unsave") : t("trends.save")}
        onClick={toggleSaved}
        className={isSaved ? "text-grid-brand" : undefined}
      >
        <BookmarkIcon fill={isSaved ? "currentColor" : "none"} />
      </Button>
    </li>
  )
}
