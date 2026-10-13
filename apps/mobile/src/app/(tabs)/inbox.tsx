import { useInfiniteQuery, useQuery } from "@tanstack/react-query"
import { router } from "expo-router"
import { useMemo, useState } from "react"
import { FlatList, RefreshControl, ScrollView, View } from "react-native"

import { channelName, type ConversationFilter } from "@moharrik/client"

import { ConversationRow } from "@/components/conversation-row"
import { Chip, Empty, ErrorView, Header, Loading, Screen, SearchBox, Segmented, useColors } from "@/components/ui"
import { usePrefs } from "@/lib/prefs"
import { qk } from "@/lib/query"
import { useSession } from "@/lib/session"
import { useDebounced } from "@/lib/use-debounced"

type Status = "" | "open" | "snoozed" | "closed"

export default function InboxScreen() {
  const { t } = usePrefs()
  const c = useColors()
  const { api, workspace } = useSession()
  const ws = workspace?.id ?? ""
  const [status, setStatus] = useState<Status>("open")
  const [unread, setUnread] = useState(false)
  const [channel, setChannel] = useState("")
  const [search, setSearch] = useState("")
  const q = useDebounced(search.trim(), 300)

  const filter: ConversationFilter = { status, unread: unread || undefined, channel: channel || undefined, q: q || undefined }
  const list = useInfiniteQuery({
    queryKey: qk.conversations(ws, filter),
    queryFn: ({ pageParam }) => api.conversations({ ...filter, cursor: pageParam || undefined }),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
    enabled: !!workspace,
  })
  const members = useQuery({ queryKey: qk.members(ws), queryFn: () => api.members(), enabled: !!workspace, staleTime: 5 * 60_000 })
  const memberName = useMemo(() => {
    const m = new Map<string, string>()
    for (const x of members.data?.members ?? []) m.set(x.user_id, x.email.split("@")[0] ?? x.email)
    return m
  }, [members.data])

  const first = list.data?.pages[0]
  const rows = useMemo(() => {
    const pinned = first?.pinned ?? []
    const seen = new Set(pinned.map((p) => p.id))
    return [...pinned, ...(list.data?.pages.flatMap((p) => p.conversations ?? []) ?? []).filter((x) => !seen.has(x.id))]
  }, [list.data, first])
  const channels = (first?.channel_counts ?? []).filter((x) => x.total > 0)

  return (
    <Screen>
      <Header title={t("tabs.inbox")} sub={workspace?.name} />
      <SearchBox value={search} onChangeText={setSearch} placeholder={t("inbox.searchPlaceholder")} />
      <Segmented<Status>
        value={status}
        onChange={setStatus}
        options={[
          { value: "open", label: t("inbox.open") },
          { value: "snoozed", label: t("inbox.snoozed") },
          { value: "closed", label: t("inbox.closed") },
          { value: "", label: t("inbox.all") },
        ]}
      />
      <View>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 8, paddingHorizontal: 16, paddingBottom: 8 }}>
          <Chip label={t("inbox.unread")} active={unread} onPress={() => setUnread((v) => !v)} />
          {channels.map((ch) => (
            <Chip
              key={ch.channel}
              label={`${channelName(t, ch.channel)}${ch.unread ? ` · ${ch.unread}` : ""}`}
              active={channel === ch.channel}
              onPress={() => setChannel((v) => (v === ch.channel ? "" : ch.channel))}
            />
          ))}
        </ScrollView>
      </View>
      {list.isLoading ? (
        <Loading />
      ) : list.isError ? (
        <ErrorView onRetry={() => void list.refetch()} />
      ) : (
        <FlatList
          data={rows}
          keyExtractor={(x) => x.id}
          renderItem={({ item }) => (
            <ConversationRow
              c={item}
              assignee={item.assignee_user_id ? memberName.get(item.assignee_user_id) : undefined}
              onPress={() => router.push(`/conversation/${item.id}`)}
            />
          )}
          onEndReached={() => list.hasNextPage && !list.isFetchingNextPage && void list.fetchNextPage()}
          onEndReachedThreshold={0.4}
          refreshControl={<RefreshControl refreshing={list.isRefetching && !list.isFetchingNextPage} onRefresh={() => void list.refetch()} tintColor={c.action} colors={[c.action]} />}
          ListEmptyComponent={<Empty title={t("inbox.empty")} />}
          contentContainerStyle={rows.length ? undefined : { flexGrow: 1 }}
        />
      )}
    </Screen>
  )
}
