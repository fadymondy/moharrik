import { useInfiniteQuery } from "@tanstack/react-query"
import { router } from "expo-router"
import { useMemo, useState } from "react"
import { FlatList, RefreshControl, View } from "react-native"

import { Tag } from "@/components/conversation-row"
import { Avatar, Empty, ErrorView, Header, Loading, Row, Screen, SearchBox, Segmented, Text, useColors } from "@/components/ui"
import { usePrefs } from "@/lib/prefs"
import { qk } from "@/lib/query"
import { useSession } from "@/lib/session"
import { useDebounced } from "@/lib/use-debounced"

type Tab = "contacts" | "companies"

export default function ContactsScreen() {
  const { t } = usePrefs()
  const [tab, setTab] = useState<Tab>("contacts")
  const [search, setSearch] = useState("")
  const q = useDebounced(search.trim(), 300)
  return (
    <Screen>
      <Header title={t(tab === "contacts" ? "contacts.contacts" : "contacts.companies")} />
      <Segmented<Tab>
        value={tab}
        onChange={setTab}
        options={[
          { value: "contacts", label: t("contacts.contacts") },
          { value: "companies", label: t("contacts.companies") },
        ]}
      />
      <SearchBox value={search} onChangeText={setSearch} placeholder={t(tab === "contacts" ? "contacts.searchContacts" : "contacts.searchCompanies")} />
      {tab === "contacts" ? <ContactList q={q} /> : <CompanyList q={q} />}
    </Screen>
  )
}

function ContactList({ q }: { q: string }) {
  const { t } = usePrefs()
  const c = useColors()
  const { api, workspace } = useSession()
  const list = useInfiniteQuery({
    queryKey: qk.contacts(workspace?.id ?? "", q),
    queryFn: ({ pageParam }) => api.contacts(q || undefined, pageParam || undefined),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
    enabled: !!workspace,
  })
  const rows = useMemo(() => list.data?.pages.flatMap((p) => p.contacts ?? []) ?? [], [list.data])
  if (list.isLoading) return <Loading />
  if (list.isError) return <ErrorView onRetry={() => void list.refetch()} />
  return (
    <FlatList
      data={rows}
      keyExtractor={(x) => x.id}
      renderItem={({ item: x }) => (
        <Row onPress={() => router.push(`/contact/${x.id}`)}>
          <Avatar name={x.name || x.email} url={x.avatar_url} />
          <View style={{ flex: 1, gap: 2 }}>
            <Text weight="medium" numberOfLines={1}>
              {x.name || x.email || x.phone}
            </Text>
            <Text size="sm" tone="muted" numberOfLines={1}>
              {[x.title, x.company_name].filter(Boolean).join(" · ") || x.email || x.phone}
            </Text>
          </View>
          {x.lifecycle ? <Tag text={t(`lifecycle.${x.lifecycle}`)} /> : null}
        </Row>
      )}
      onEndReached={() => list.hasNextPage && !list.isFetchingNextPage && void list.fetchNextPage()}
      onEndReachedThreshold={0.4}
      refreshControl={<RefreshControl refreshing={list.isRefetching && !list.isFetchingNextPage} onRefresh={() => void list.refetch()} tintColor={c.action} colors={[c.action]} />}
      ListEmptyComponent={<Empty title={t("contacts.empty")} />}
      contentContainerStyle={rows.length ? undefined : { flexGrow: 1 }}
    />
  )
}

function CompanyList({ q }: { q: string }) {
  const { t } = usePrefs()
  const c = useColors()
  const { api, workspace } = useSession()
  const list = useInfiniteQuery({
    queryKey: qk.companies(workspace?.id ?? "", q),
    queryFn: ({ pageParam }) => api.companies(q || undefined, pageParam || undefined),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
    enabled: !!workspace,
  })
  const rows = useMemo(() => list.data?.pages.flatMap((p) => p.companies ?? []) ?? [], [list.data])
  if (list.isLoading) return <Loading />
  if (list.isError) return <ErrorView onRetry={() => void list.refetch()} />
  return (
    <FlatList
      data={rows}
      keyExtractor={(x) => x.id}
      renderItem={({ item: x }) => (
        <Row onPress={() => router.push(`/company/${x.id}`)}>
          <Avatar name={x.name} />
          <View style={{ flex: 1, gap: 2 }}>
            <Text weight="medium" numberOfLines={1}>
              {x.name}
            </Text>
            <Text size="sm" tone="muted" numberOfLines={1}>
              {[x.domain, x.industry, x.country].filter(Boolean).join(" · ")}
            </Text>
          </View>
          <Text size="sm" tone="muted">
            {x.contact_count}
          </Text>
        </Row>
      )}
      onEndReached={() => list.hasNextPage && !list.isFetchingNextPage && void list.fetchNextPage()}
      onEndReachedThreshold={0.4}
      refreshControl={<RefreshControl refreshing={list.isRefetching && !list.isFetchingNextPage} onRefresh={() => void list.refetch()} tintColor={c.action} colors={[c.action]} />}
      ListEmptyComponent={<Empty title={t("contacts.empty")} />}
      contentContainerStyle={rows.length ? undefined : { flexGrow: 1 }}
    />
  )
}
