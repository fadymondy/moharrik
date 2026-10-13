import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import * as WebBrowser from "expo-web-browser"
import { CheckCheck } from "lucide-react-native"
import { useMemo, useState } from "react"
import { FlatList, RefreshControl, View } from "react-native"

import { ago, type NotificationItem } from "@moharrik/client"

import { Tag } from "@/components/conversation-row"
import { Chip, Dot, Empty, ErrorView, Header, IconButton, Loading, Row, Screen, Text, useColors } from "@/components/ui"
import { usePrefs } from "@/lib/prefs"
import { qk } from "@/lib/query"
import { useSession } from "@/lib/session"

const SEVERITY: Record<string, "danger" | "warning" | "success" | "action"> = {
  critical: "danger",
  error: "danger",
  high: "danger",
  warning: "warning",
  medium: "warning",
  success: "success",
}

export default function NotificationsScreen() {
  const { t, locale } = usePrefs()
  const c = useColors()
  const qc = useQueryClient()
  const { api, workspace } = useSession()
  const ws = workspace?.id ?? ""
  const [unreadOnly, setUnreadOnly] = useState(false)

  const list = useInfiniteQuery({
    queryKey: qk.notifications(ws, unreadOnly),
    queryFn: ({ pageParam }) => api.notifications({ unread: unreadOnly || undefined, cursor: pageParam || undefined }),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
    enabled: !!workspace,
  })
  const items = useMemo(() => list.data?.pages.flatMap((p) => p.items ?? []) ?? [], [list.data])
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["notifications", ws] })
    void qc.invalidateQueries({ queryKey: ["bell", ws] })
  }
  const markAll = useMutation({ mutationFn: () => api.markAllNotificationsRead(), onSuccess: refresh })
  const mark = useMutation({ mutationFn: (id: string) => api.markNotification(id, true), onSuccess: refresh })

  function open(n: NotificationItem) {
    if (!n.read_at) mark.mutate(n.id)
    if (/^https?:\/\//.test(n.url)) void WebBrowser.openBrowserAsync(n.url)
  }

  return (
    <Screen>
      <Header
        title={t("notifications.title")}
        sub={workspace?.name}
        end={
          <IconButton label={t("notifications.markAllRead")} onPress={() => markAll.mutate()}>
            <CheckCheck size={22} color={c.action} />
          </IconButton>
        }
      />
      <View style={{ flexDirection: "row", paddingHorizontal: 16, paddingVertical: 8 }}>
        <Chip label={t("notifications.unreadOnly")} active={unreadOnly} onPress={() => setUnreadOnly((v) => !v)} />
      </View>
      {list.isLoading ? (
        <Loading />
      ) : list.isError ? (
        <ErrorView onRetry={() => void list.refetch()} />
      ) : (
        <FlatList
          data={items}
          keyExtractor={(x) => x.id}
          renderItem={({ item: n }) => (
            <Row onPress={() => open(n)} style={{ alignItems: "flex-start" }}>
              <View style={{ paddingTop: 7 }}>
                <Dot color={n.read_at ? c.line : c[SEVERITY[n.severity] ?? "action"]} />
              </View>
              <View style={{ flex: 1, gap: 2 }}>
                <View style={{ flexDirection: "row", gap: 8 }}>
                  <Text weight={n.read_at ? "medium" : "bold"} numberOfLines={2} style={{ flex: 1 }}>
                    {n.title}
                  </Text>
                  <Text size="xs" tone="muted">
                    {ago(t, locale, n.occurred_at)}
                  </Text>
                </View>
                {n.body ? (
                  <Text size="sm" tone="body" numberOfLines={3}>
                    {n.body}
                  </Text>
                ) : null}
                <View style={{ flexDirection: "row", gap: 6, marginTop: 2 }}>
                  {n.source_name ? <Tag text={n.source_name} /> : null}
                  {n.kind ? <Tag text={n.kind} /> : null}
                </View>
              </View>
            </Row>
          )}
          onEndReached={() => list.hasNextPage && !list.isFetchingNextPage && void list.fetchNextPage()}
          onEndReachedThreshold={0.4}
          refreshControl={<RefreshControl refreshing={list.isRefetching && !list.isFetchingNextPage} onRefresh={() => void list.refetch()} tintColor={c.action} colors={[c.action]} />}
          ListEmptyComponent={<Empty title={t("notifications.empty")} />}
          contentContainerStyle={items.length ? undefined : { flexGrow: 1 }}
        />
      )}
    </Screen>
  )
}
