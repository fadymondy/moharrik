import { useQuery } from "@tanstack/react-query"
import { Tabs, useLocalSearchParams } from "expo-router"
import { Bell, Briefcase, Inbox, Settings, Users } from "lucide-react-native"
import { useSafeAreaInsets } from "react-native-safe-area-context"

import { useWorkspaceParam } from "@/lib/navigate"
import { usePrefs } from "@/lib/prefs"
import { qk } from "@/lib/query"
import { useSession } from "@/lib/session"

export default function TabsLayout() {
  const { t, colors: c, locale } = usePrefs()
  const { api, workspace } = useSession()
  const insets = useSafeAreaInsets()
  const { ws } = useLocalSearchParams<{ ws?: string }>()
  useWorkspaceParam(ws)
  const bell = useQuery({
    queryKey: qk.bell(workspace?.id ?? ""),
    queryFn: () => api.bell(),
    enabled: !!workspace,
    refetchInterval: 30_000,
  })
  const font = locale === "ar" ? "Lusail-Medium" : "Inter_500Medium"
  const badge = (n: number | undefined) => (n ? (n > 99 ? "99+" : n) : undefined)

  return (
    <Tabs
      screenOptions={{
        headerShown: false,
        tabBarActiveTintColor: c.action,
        tabBarInactiveTintColor: c.muted,
        tabBarStyle: { backgroundColor: c.surface, borderTopColor: c.line, height: 58 + insets.bottom, paddingTop: 6 },
        tabBarLabelStyle: { fontFamily: font, fontSize: 11 },
        tabBarBadgeStyle: { backgroundColor: c.action, color: c.onAction, fontSize: 10 },
        sceneStyle: { backgroundColor: c.bg },
      }}
    >
      <Tabs.Screen
        name="inbox"
        options={{ title: t("tabs.inbox"), tabBarIcon: ({ color, size }) => <Inbox color={color} size={size} />, tabBarBadge: badge(bell.data?.unread_conversations) }}
      />
      <Tabs.Screen
        name="notifications"
        options={{ title: t("tabs.notifications"), tabBarIcon: ({ color, size }) => <Bell color={color} size={size} />, tabBarBadge: badge(bell.data?.unread_notifications) }}
      />
      <Tabs.Screen name="contacts" options={{ title: t("tabs.contacts"), tabBarIcon: ({ color, size }) => <Users color={color} size={size} /> }} />
      <Tabs.Screen name="deals" options={{ title: t("tabs.deals"), tabBarIcon: ({ color, size }) => <Briefcase color={color} size={size} /> }} />
      <Tabs.Screen name="settings" options={{ title: t("tabs.settings"), tabBarIcon: ({ color, size }) => <Settings color={color} size={size} /> }} />
    </Tabs>
  )
}
