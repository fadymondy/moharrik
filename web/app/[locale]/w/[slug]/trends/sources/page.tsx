"use client"

import { useState, type FormEvent } from "react"
import { PlusIcon, RefreshCwIcon, TriangleAlertIcon } from "lucide-react"
import { toast } from "sonner"

import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type FeedSource, type Topic } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useFeedSources, useTopics } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function TrendSourcesPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("trends.sources")} · ${ws.summary.name}`)
  const canManage = ws.can("trends:manage")
  const sources = useFeedSources(ws.id)
  const topics = useTopics(ws.id)

  return (
    <>
      <SectionHeader micro={ws.summary.name} title={t("trends.sources")} description={t("trends.sourcesHint")} />

      <section>
        <h2 className="px-6 pb-1 text-base font-medium">{t("trends.feeds")}</h2>
        <p className="px-6 pb-4 text-sm text-grid-muted">{t("trends.feedsHint")}</p>
        {sources.error ? (
          <ErrorState error={sources.error} />
        ) : sources.isLoading ? (
          <LoadingRows rows={2} />
        ) : (sources.data?.sources ?? []).length === 0 ? (
          <EmptyState title={t("trends.noFeeds")} body={canManage ? t("trends.noFeedsBody") : undefined} />
        ) : (
          <ul className="divide-y divide-line border-y border-line">
            {(sources.data?.sources ?? []).map((s) => (
              <FeedRow key={s.id} source={s} canManage={canManage} onChange={() => sources.mutate()} />
            ))}
          </ul>
        )}
        {canManage ? <AddFeed onAdded={() => sources.mutate()} /> : null}
      </section>

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />

      <section>
        <h2 className="px-6 pb-1 text-base font-medium">{t("trends.topics")}</h2>
        <p className="px-6 pb-4 text-sm text-grid-muted">{t("trends.topicsHint")}</p>
        {topics.error ? (
          <ErrorState error={topics.error} />
        ) : topics.isLoading ? (
          <LoadingRows rows={2} />
        ) : (topics.data?.topics ?? []).length === 0 ? (
          <EmptyState title={t("trends.noTopics")} body={canManage ? t("trends.noTopicsBody") : undefined} />
        ) : (
          <ul className="divide-y divide-line border-y border-line">
            {(topics.data?.topics ?? []).map((tp) => (
              <TopicRow key={tp.id} topic={tp} canManage={canManage} onChange={() => topics.mutate()} />
            ))}
          </ul>
        )}
        {canManage ? <AddTopic onAdded={() => topics.mutate()} /> : null}
      </section>
      <div className="pb-8" />
    </>
  )
}

function FeedRow({ source, canManage, onChange }: { source: FeedSource; canManage: boolean; onChange: () => void }) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)

  async function fetchNow() {
    setBusy(true)
    try {
      const res = await api<{ added: number }>(`/api/w/feed_sources/${encodeURIComponent(source.id)}/fetch`, { method: "POST", workspaceId: ws.id })
      toast.success(t("trends.fetched", { n: res.added }))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function toggle() {
    try {
      await api(`/api/w/feed_sources/${encodeURIComponent(source.id)}`, { method: "PATCH", json: { enabled: !source.enabled }, workspaceId: ws.id })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function remove() {
    await api(`/api/w/feed_sources/${encodeURIComponent(source.id)}`, { method: "DELETE", workspaceId: ws.id })
    toast.success(t("trends.feedDeleted"))
    onChange()
  }

  return (
    <li className="flex flex-wrap items-start gap-3 px-6 py-4" data-feed-row={source.name}>
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">{source.name}</p>
        <Ltr className="block truncate text-xs text-grid-muted" mono>
          {source.url}
        </Ltr>
        <p className="mt-1 flex flex-wrap items-center gap-x-2 text-xs text-grid-muted">
          <Badge variant={source.enabled ? "default" : "outline"}>{source.enabled ? t("trends.enabled") : t("trends.disabled")}</Badge>
          <span>{t("trends.itemsCollected", { n: source.item_count })}</span>
          {source.last_fetched_at ? (
            <>
              <span aria-hidden>·</span>
              <span>{t("trends.lastFetched", { date: formatDate(source.last_fetched_at) })}</span>
            </>
          ) : null}
        </p>
        {source.last_error ? (
          <p className="mt-1 flex items-start gap-1 text-xs text-grid-danger-text">
            <TriangleAlertIcon className="mt-0.5 size-3 shrink-0" />
            {source.last_error}
          </p>
        ) : null}
      </div>
      {canManage ? (
        <div className="flex items-center gap-2">
          <Switch checked={source.enabled} onCheckedChange={toggle} aria-label={source.enabled ? t("trends.enabled") : t("trends.disabled")} />
          <Button variant="ghost" size="sm" onClick={fetchNow} disabled={busy}>
            <RefreshCwIcon />
            {busy ? t("common.working") : t("trends.fetchNow")}
          </Button>
          <ConfirmButton
            label={t("common.delete")}
            title={t("trends.deleteFeedTitle")}
            description={t("trends.deleteFeedBody", { name: source.name })}
            confirmLabel={t("common.delete")}
            onConfirm={remove}
          />
        </div>
      ) : null}
    </li>
  )
}

function AddFeed({ onAdded }: { onAdded: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [url, setUrl] = useState("")
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{ added: number; source: FeedSource }>("/api/w/feed_sources", {
        json: { url: url.trim(), name: name.trim() },
        workspaceId: ws.id,
      })
      if (res.source.last_error) toast.warning(res.source.last_error)
      else toast.success(t("trends.fetched", { n: res.added }))
      setUrl("")
      setName("")
      onAdded()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="px-6 pt-6" data-form="add-feed">
      <FieldGroup className="sm:flex-row sm:items-end">
        <Field className="sm:flex-1">
          <FieldLabel htmlFor="feed-url">{t("trends.feedUrl")}</FieldLabel>
          <Input id="feed-url" dir="ltr" value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://example.com/feed.xml" />
          <FieldDescription>{t("trends.feedUrlHint")}</FieldDescription>
        </Field>
        <Field className="sm:w-56">
          <FieldLabel htmlFor="feed-name">
            {t("trends.feedName")} <span className="text-grid-muted">({t("common.optional")})</span>
          </FieldLabel>
          <Input id="feed-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={120} />
        </Field>
        <Button type="submit" disabled={busy || !url.trim()}>
          <PlusIcon />
          {busy ? t("common.working") : t("trends.addSource")}
        </Button>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}

function TopicRow({ topic, canManage, onChange }: { topic: Topic; canManage: boolean; onChange: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()

  async function remove() {
    await api(`/api/w/topics/${encodeURIComponent(topic.id)}`, { method: "DELETE", workspaceId: ws.id })
    toast.success(t("trends.topicDeleted"))
    onChange()
  }

  return (
    <li className="flex flex-wrap items-center gap-3 px-6 py-4" data-topic-row={topic.name}>
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">{topic.name}</p>
        <p className="text-xs text-grid-muted">
          {topic.keywords.length > 0 ? topic.keywords.join(", ") : t("trends.matchesOwnName")} · {t("trends.itemsMatched", { n: topic.item_count })}
        </p>
      </div>
      {canManage ? (
        <ConfirmButton
          label={t("common.delete")}
          title={t("trends.deleteTopicTitle")}
          description={t("trends.deleteTopicBody", { name: topic.name })}
          confirmLabel={t("common.delete")}
          onConfirm={remove}
        />
      ) : null}
    </li>
  )
}

function AddTopic({ onAdded }: { onAdded: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [keywords, setKeywords] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{ topic: Topic }>("/api/w/topics", {
        json: { name: name.trim(), keywords: keywords.split(",").map((k) => k.trim()).filter(Boolean) },
        workspaceId: ws.id,
      })
      toast.success(t("trends.topicCreated", { n: res.topic.item_count }))
      setName("")
      setKeywords("")
      onAdded()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="px-6 pt-6" data-form="add-topic">
      <FieldGroup className="sm:flex-row sm:items-end">
        <Field className="sm:w-56">
          <FieldLabel htmlFor="topic-name">{t("trends.topicName")}</FieldLabel>
          <Input id="topic-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
        </Field>
        <Field className="sm:flex-1">
          <FieldLabel htmlFor="topic-keywords">
            {t("trends.keywords")} <span className="text-grid-muted">({t("common.optional")})</span>
          </FieldLabel>
          <Input id="topic-keywords" value={keywords} onChange={(e) => setKeywords(e.target.value)} placeholder="postgres, pgsql" />
          <FieldDescription>{t("trends.keywordsHint")}</FieldDescription>
        </Field>
        <Button type="submit" disabled={busy || !name.trim()}>
          <PlusIcon />
          {busy ? t("common.working") : t("trends.addTopic")}
        </Button>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}
