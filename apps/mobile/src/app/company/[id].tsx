import { useQuery } from "@tanstack/react-query"
import { router, useLocalSearchParams } from "expo-router"
import { Globe, MessageSquare } from "lucide-react-native"
import { Linking, ScrollView, View } from "react-native"

import { ago, channelName, money, stageName } from "@moharrik/client"

import { BackButton } from "@/components/back"
import { Tag } from "@/components/conversation-row"
import { Avatar, Card, ErrorView, Header, Loading, Row, Screen, Section, Text, useColors } from "@/components/ui"
import { usePrefs } from "@/lib/prefs"
import { qk } from "@/lib/query"
import { useSession } from "@/lib/session"

export default function CompanyScreen() {
  const { id } = useLocalSearchParams<{ id: string }>()
  const { t, locale } = usePrefs()
  const c = useColors()
  const { api, workspace } = useSession()
  const page = useQuery({ queryKey: qk.company(workspace?.id ?? "", id), queryFn: () => api.company(id), enabled: !!workspace && !!id })
  const x = page.data?.company

  return (
    <Screen>
      <Header title={x?.name ?? ""} start={<BackButton fallback="/contacts" />} />
      {page.isLoading ? (
        <Loading />
      ) : page.isError || !x ? (
        <ErrorView onRetry={() => void page.refetch()} />
      ) : (
        <ScrollView contentContainerStyle={{ paddingBottom: 32 }}>
          <View style={{ alignItems: "center", gap: 8, padding: 20 }}>
            <Avatar name={x.name} size={72} />
            <Text size="xl" weight="bold" style={{ textAlign: "center" }}>
              {x.name}
            </Text>
            <View style={{ flexDirection: "row", flexWrap: "wrap", justifyContent: "center", gap: 6 }}>
              {[x.industry, x.country, x.size].filter(Boolean).map((v) => (
                <Tag key={v} text={v} />
              ))}
            </View>
          </View>
          {x.domain ? (
            <Card style={{ marginHorizontal: 16 }}>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
                <Globe size={16} color={c.action} />
                <Text tone="action" onPress={() => void Linking.openURL(`https://${x.domain}`)}>
                  {x.domain}
                </Text>
              </View>
            </Card>
          ) : null}
          {(page.data?.contacts ?? []).length ? (
            <Section title={t("contacts.contacts")}>
              {(page.data?.contacts ?? []).map((p) => (
                <Row key={p.id} onPress={() => router.push(`/contact/${p.id}`)}>
                  <Avatar name={p.name || p.email} size={32} />
                  <View style={{ flex: 1 }}>
                    <Text numberOfLines={1}>{p.name || p.email}</Text>
                    {p.title ? (
                      <Text size="sm" tone="muted">
                        {p.title}
                      </Text>
                    ) : null}
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
          {(page.data?.conversations ?? []).length ? (
            <Section title={t("contacts.conversations")}>
              {(page.data?.conversations ?? []).map((cv) => (
                <Row key={cv.id} onPress={() => router.push(`/conversation/${cv.id}`)}>
                  <MessageSquare size={18} color={c.muted} />
                  <View style={{ flex: 1 }}>
                    <Text numberOfLines={1}>{cv.customer_name || cv.subject || cv.preview}</Text>
                    <Text size="sm" tone="muted">
                      {channelName(t, cv.channel)} · {ago(t, locale, cv.last_message_at)}
                    </Text>
                  </View>
                </Row>
              ))}
            </Section>
          ) : null}
        </ScrollView>
      )}
    </Screen>
  )
}
