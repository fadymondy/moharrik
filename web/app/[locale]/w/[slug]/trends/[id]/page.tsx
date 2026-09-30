"use client"

import Link from "next/link"
import { useParams } from "next/navigation"
import { ArrowLeftIcon } from "lucide-react"

import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import {
  ItemLine,
  ReasonChips,
  Score,
  TopicActions,
  useDayLabel,
  useWhen,
} from "@/components/trends/shared"
import { Badge, Button } from "@fadymondy/nasaq/web"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { ApiError, type TrendTopic, type TrendTopicDetail } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useTrendTopic } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

/** One trending topic (MH-385): its summary, why it ranks, every outlet and every item. */
export default function TrendTopicPage() {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const { id } = useParams<{ id: string }>()
  const res = useTrendTopic(ws.id, id)
  const when = useWhen()
  const dayLabel = useDayLabel()
  const topic = res.data?.topic
  useDocumentTitle(`${topic?.title ?? t("trends.topic")} · ${t("nav.trends")}`)
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "trends") void res.mutate()
  })

  const back = (
    <Button
      variant="ghost"
      size="sm"
      nativeButton={false}
      render={<Link href={`/${locale}/w/${ws.slug}/trends`} />}
      data-back
    >
      <ArrowLeftIcon className="rtl:rotate-180" />
      {t("trends.backToTopics")}
    </Button>
  )

  if (res.error) {
    return (
      <>
        <div className="px-6 pt-6">{back}</div>
        {res.error instanceof ApiError && res.error.status === 404 ? (
          <EmptyState title={t("trends.topicGone")} />
        ) : (
          <ErrorState error={res.error} />
        )}
      </>
    )
  }
  if (!topic) {
    return (
      <>
        <div className="px-6 pt-6">{back}</div>
        <LoadingRows rows={4} />
      </>
    )
  }

  const onChange = (updated: TrendTopic) =>
    void res.mutate(
      (d?: TrendTopicDetail) => (d ? { ...d, topic: { ...updated, items: d.topic.items } } : d),
      { revalidate: true },
    )
  const summaryNote =
    topic.summary_source === "extractive"
      ? t("trends.summary.extractive")
      : topic.summary_source === "extractive:headlines"
        ? t("trends.summary.headlines")
        : t("trends.summary.model", { model: topic.summary_source })

  return (
    <>
      <div className="px-6 pt-6">{back}</div>
      <SectionHeader
        micro={
          <span className="flex flex-wrap items-center gap-2">
            {dayLabel(topic.day)}
            <Badge variant="outline" data-topic-state={topic.state}>
              {t(`trends.state.${topic.state}`)}
            </Badge>
          </span>
        }
        title={
          <span className="flex items-start gap-3">
            <Score value={topic.score} />
            <bdi data-topic-title>{topic.title}</bdi>
          </span>
        }
        action={<TopicActions topic={topic} onChange={onChange} />}
      />

      <div className="grid gap-8 px-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <section className="flex flex-col gap-2" aria-labelledby="topic-summary">
            <h2 id="topic-summary" className="eyebrow">
              {t("trends.summaryTitle")}
            </h2>
            {topic.summary ? (
              <p
                className="max-w-3xl break-words text-sm leading-relaxed"
                data-topic-summary={topic.summary_source}
              >
                <bdi>{topic.summary}</bdi>
              </p>
            ) : (
              <p className="text-sm text-muted-foreground">{t("trends.summary.none")}</p>
            )}
            <p className="text-xs text-muted-foreground">{summaryNote}</p>
          </section>

          <section className="flex flex-col gap-2" aria-labelledby="topic-why">
            <h2 id="topic-why" className="eyebrow">
              {t("trends.whyRanked")}
            </h2>
            <ReasonChips reasons={topic.reasons} />
            <ul className="flex flex-col gap-1 text-xs text-muted-foreground">
              {topic.reasons.map((r) => (
                <li key={r.kind}>
                  <span className="font-mono tabular-nums" dir="ltr">
                    +{r.points}
                  </span>{" "}
                  {t(`trends.reasonKind.${r.kind}`)}
                  {r.detail.length ? ": " : ""}
                  <bdi>{r.detail.join(t("trends.listSeparator"))}</bdi>
                </li>
              ))}
            </ul>
          </section>

          <section className="flex flex-col gap-2" aria-labelledby="topic-items">
            <h2 id="topic-items" className="eyebrow">
              {t("trends.coverageCount", { n: formatNumber(topic.items.length) })}
            </h2>
            <ul className="divide-y divide-nq-line border-y border-nq-line" data-topic-items>
              {topic.items.map((item) => (
                <li key={item.id} className="py-3">
                  <ItemLine item={item} showSummary />
                </li>
              ))}
            </ul>
          </section>
        </div>

        <aside className="flex flex-col gap-6">
          <section className="flex flex-col gap-2" aria-labelledby="topic-outlets">
            <h2 id="topic-outlets" className="eyebrow">
              {t("trends.outlets")}
            </h2>
            <ul
              className="flex flex-col divide-y divide-nq-line border-y border-nq-line text-sm"
              data-topic-outlets
            >
              {(res.data?.outlets ?? []).map((o) => (
                <li key={o.outlet} className="flex items-center justify-between gap-2 py-2">
                  <bdi className="min-w-0 truncate">{o.outlet}</bdi>
                  <span className="shrink-0 font-mono text-xs tabular-nums text-muted-foreground">
                    {formatNumber(o.count)}
                  </span>
                </li>
              ))}
            </ul>
          </section>
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1.5 text-xs">
            <dt className="text-muted-foreground">{t("trends.firstSeen")}</dt>
            <dd>{when(topic.first_seen_at)}</dd>
            <dt className="text-muted-foreground">{t("trends.lastSeen")}</dt>
            <dd>{when(topic.last_seen_at)}</dd>
            <dt className="text-muted-foreground">{t("trends.grouping")}</dt>
            <dd>
              {topic.cluster_method.startsWith("deterministic+embed")
                ? t("trends.method.embed")
                : t("trends.method.deterministic")}
            </dd>
          </dl>
        </aside>
      </div>
      <div className="pb-8" />
    </>
  )
}
