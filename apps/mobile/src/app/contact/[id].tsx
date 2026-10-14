import { useInfiniteQuery, useQuery } from "@tanstack/react-query"
import { router, useLocalSearchParams } from "expo-router"
import { Building2, Mail, MessageSquare, Phone } from "lucide-react-native"
import { useMemo } from "react"
import { ActivityIndicator, FlatList, Linking, View } from "react-native"

import { ago, channelName, money, stageName, type ContactEvent } from "@moharrik/client"

import { BackButton } from "@/components/back"
import { Tag } from "@/components/conversation-row"
import { Avatar, Card, ErrorView, Header, Loading, Row, Screen, Section, Text, useColors } from "@/components/ui"
import { eventText } from "@/lib/events"
import { usePrefs } from "@/lib/prefs"
import { qk } from "@/lib/query"
import { useSession } from "@/lib/session"

export default function ContactScreen() {
  const { id } = useLocalSearchParams<{ id: string }>()
  const { t, locale } = usePrefs()
  const c = useColors()
  const { api, workspace } = useSession()
  const ws = workspace?.id ?? ""
  const page = useQuery({ queryKey: qk.contact(ws, id), queryFn: () => api.contact(id), enabled: !!workspace && !!id })
  const timeline = useInfiniteQuery({
    queryKey: qk.timeline(ws, id),
    queryFn: ({ pageParam }) => api.timeline(id, pageParam || undefined),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
    enabled: !!workspace && !!id,
  })
  const events = useMemo(() => timeline.data?.pages.flatMap((p) => p.events ?? []) ?? [], [timeline.data])

  if (page.isLoading)
    return (
      <Screen>
        <Header title="" start={<BackButton />} />
        <Loading />
      </Screen>
    )
  const x = page.data?.contact
  if (page.isError || !x)
    return (
      <Screen>
        <Header title="" start={<BackButton />} />
        <ErrorView onRetry={() => void page.refetch()} />
      </Screen>
    )

  const head = (
    <View>
      <View style={{ alignItems: "center", gap: 8, padding: 20 }}>
        <Avatar name={x.name || x.email} url={x.avatar_url || x.photo_url} size={72} />
        <Text size="xl" weight="bold" style={{ textAlign: "center" }}>
          {x.name || x.email || x.phone}
        </Text>
        {x.title || x.company?.name ? (
          <Text tone="muted" style={{ textAlign: "center" }}>
            {[x.title, x.company?.name].filter(Boolean).join(" · ")}
          </Text>
        ) : null}
        <View style={{ flexDirection: "row", gap: 6 }}>
          {x.lifecycle ? <Tag text={t(`lifecycle.${x.lifecycle}`)} /> : null}
          {x.score != null ? <Tag text={`${t("contacts.score")} ${x.score}`} /> : null}
        </View>
      </View>
      <Card style={{ marginHorizontal: 16 }}>
        {x.email ? <Field icon={<Mail size={16} color={c.action} />} label={t("contacts.email")} value={x.email} onPress={() => void Linking.openURL(`mailto:${x.email}`)} /> : null}
        {x.phone ? <Field icon={<Phone size={16} color={c.action} />} label={t("contacts.phone")} value={x.phone} onPress={() => void Linking.openURL(`tel:${x.phone}`)} /> : null}
        {x.company?.id ? (
          <Field icon={<Building2 size={16} color={c.action} />} label={t("contacts.company")} value={x.company.name} onPress={() => router.push(`/company/${x.company.id}`)} />
        ) : null}
      </Card>
      {(page.data?.conversations ?? []).length ? (
        <Section title={t("contacts.conversations")}>
          {(page.data?.conversations ?? []).map((cv) => (
            <Row key={cv.id} onPress={() => router.push(`/conversation/${cv.id}`)}>
              <MessageSquare size={18} color={c.muted} />
              <View style={{ flex: 1 }}>
                <Text numberOfLines={1}>{cv.subject || cv.preview || channelName(t, cv.channel)}</Text>
                <Text size="sm" tone="muted">
                  {channelName(t, cv.channel)} · {ago(t, locale, cv.last_message_at)}
                </Text>
              </View>
            </Row>
          ))}
        </Section>
      ) : null}
      {(page.data?.deals ?? []).length ? (
        <Section title={t("deals.title")}>
          {(page.data?.deals ?? []).map((d) => (
            <Row key={d.id}>
              <View style={{ flex: 1 }}>
                <Text numberOfLines={1}>{d.title}</Text>
                <Text size="sm" tone="muted">
                  {stageName(t, d.stage_name)}
                </Text>
              </View>
              <Text weight="bold" tone="action">
                {money(locale, d.value_minor, d.currency)}
              </Text>
            </Row>
          ))}
        </Section>
      ) : null}
      <Text size="sm" weight="medium" tone="muted" style={{ paddingHorizontal: 16, marginTop: 20, marginBottom: 6 }}>
        {t("contacts.timeline")}
      </Text>
    </View>
  )

  return (
    <Screen>
      <Header title={x.name || x.email} start={<BackButton fallback="/contacts" />} />
      <FlatList
        data={events}
        keyExtractor={(e) => e.id}
        ListHeaderComponent={head}
        renderItem={({ item }) => <EventRow e={item} />}
        onEndReached={() => timeline.hasNextPage && !timeline.isFetchingNextPage && void timeline.fetchNextPage()}
        ListFooterComponent={timeline.isFetching ? <ActivityIndicator color={c.action} style={{ margin: 16 }} /> : <View style={{ height: 24 }} />}
        ListEmptyComponent={
          timeline.isLoading ? null : (
            <Text tone="muted" style={{ paddingHorizontal: 16 }}>
              {t("contacts.timelineEmpty")}
            </Text>
          )
        }
      />
    </Screen>
  )
}

function Field({ icon, label, value, onPress }: { icon: React.ReactNode; label: string; value: string; onPress?: () => void }) {
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
      {icon}
      <Text size="sm" tone="muted" style={{ width: 70 }}>
        {label}
      </Text>
      <Text tone="action" onPress={onPress} numberOfLines={1} style={{ flex: 1 }}>
        {value}
      </Text>
    </View>
  )
}

function EventRow({ e }: { e: ContactEvent }) {
  const { t, locale } = usePrefs()
  const c = useColors()
  const { title, body } = eventText(t, e)
  return (
    <View style={{ flexDirection: "row", gap: 12, paddingHorizontal: 16, paddingVertical: 8 }}>
      <View style={{ alignItems: "center", width: 12 }}>
        <View style={{ width: 9, height: 9, borderRadius: 5, backgroundColor: e.kind.startsWith("deal.won") ? c.success : c.action, marginTop: 6 }} />
        <View style={{ flex: 1, width: 1, backgroundColor: c.line, marginTop: 4 }} />
      </View>
      <View style={{ flex: 1, gap: 2, paddingBottom: 6 }}>
        <View style={{ flexDirection: "row", gap: 8 }}>
          <Text weight="medium" style={{ flex: 1 }}>
            {title}
          </Text>
          <Text size="xs" tone="muted">
            {ago(t, locale, e.at)}
          </Text>
        </View>
        {body ? (
          <Text size="sm" tone="body" numberOfLines={3}>
            {body}
          </Text>
        ) : null}
      </View>
    </View>
  )
}
