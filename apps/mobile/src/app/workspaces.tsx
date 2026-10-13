import { useQuery } from "@tanstack/react-query"
import { router } from "expo-router"
import { Check } from "lucide-react-native"
import { FlatList, View } from "react-native"

import { Avatar, Button, Empty, ErrorView, Header, Loading, Row, Screen, Text, useColors } from "@/components/ui"
import { pickWorkspace } from "@/lib/navigate"
import { usePrefs } from "@/lib/prefs"
import { qk } from "@/lib/query"
import { useSession } from "@/lib/session"

export default function Workspaces() {
  const { t } = usePrefs()
  const c = useColors()
  const { api, workspace, selectWorkspace, signOut } = useSession()
  const q = useQuery({ queryKey: qk.workspaces, queryFn: () => api.workspaces() })
  const list = q.data?.workspaces ?? []

  return (
    <Screen>
      <Header title={t("workspaces.title")} sub={t("workspaces.body")} />
      {q.isLoading ? (
        <Loading />
      ) : q.isError ? (
        <ErrorView onRetry={() => void q.refetch()} />
      ) : list.length === 0 ? (
        <Empty title={t("workspaces.none")} action={<Button title={t("settings.signOut")} kind="secondary" onPress={() => void signOut()} />} />
      ) : (
        <FlatList
          data={list}
          keyExtractor={(w) => w.id}
          renderItem={({ item: w }) => (
            <Row
              onPress={async () => {
                await selectWorkspace(pickWorkspace(w))
                router.replace("/inbox")
              }}
            >
              <Avatar name={w.name} url={w.avatar_url} />
              <View style={{ flex: 1 }}>
                <Text weight="medium">{w.name}</Text>
                <Text size="sm" tone="muted">
                  {t("workspaces.members", { n: w.member_count })} · {w.role}
                </Text>
              </View>
              {w.id === workspace?.id ? <Check size={20} color={c.action} /> : null}
            </Row>
          )}
        />
      )}
    </Screen>
  )
}
