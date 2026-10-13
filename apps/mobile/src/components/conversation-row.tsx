import { Users } from "lucide-react-native"
import { View } from "react-native"

import { ago, channelName, type Conversation } from "@moharrik/client"

import { usePrefs } from "@/lib/prefs"

import { Avatar, Dot, labelColor, Row, Text } from "./ui"

export function customerName(c: Pick<Conversation, "customer_name" | "customer_email" | "customer_phone" | "customer_handle" | "contact">): string {
  return c.contact?.name || c.customer_name || c.customer_email || c.customer_phone || c.customer_handle || "—"
}

export function ConversationRow({ c, onPress, assignee }: { c: Conversation; onPress: () => void; assignee?: string }) {
  const { t, locale, colors } = usePrefs()
  const name = customerName(c)
  return (
    <Row onPress={onPress} style={{ alignItems: "flex-start" }}>
      <Avatar name={name} url={c.avatar_url} />
      <View style={{ flex: 1, gap: 2 }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
          {c.is_group ? <Users size={14} color={colors.muted} /> : null}
          <Text weight={c.unread ? "bold" : "medium"} numberOfLines={1} style={{ flex: 1 }}>
            {name}
          </Text>
          <Text size="xs" tone={c.unread ? "action" : "muted"}>
            {ago(t, locale, c.last_message_at)}
          </Text>
        </View>
        {c.subject ? (
          <Text size="sm" weight="medium" numberOfLines={1}>
            {c.subject}
          </Text>
        ) : null}
        <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
          <Text size="sm" tone={c.unread ? "body" : "muted"} numberOfLines={1} style={{ flex: 1 }}>
            {c.preview || t("inbox.noMessages")}
          </Text>
          {c.unread ? <Dot /> : null}
        </View>
        <View style={{ flexDirection: "row", flexWrap: "wrap", alignItems: "center", gap: 6, marginTop: 2 }}>
          <Tag text={channelName(t, c.channel)} />
          {c.status !== "open" ? <Tag text={t(c.status === "snoozed" ? "inbox.snoozed" : "inbox.closed")} /> : null}
          {assignee ? <Tag text={assignee} /> : null}
          {(c.labels ?? []).slice(0, 3).map((l) => (
            <Tag key={l.id} text={l.name} color={labelColor(l.color)} />
          ))}
        </View>
      </View>
    </Row>
  )
}

export function Tag({ text, color }: { text: string; color?: string }) {
  const { colors } = usePrefs()
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 4, paddingHorizontal: 7, paddingVertical: 1, borderRadius: 6, backgroundColor: colors.raised, borderWidth: 1, borderColor: colors.line }}>
      {color ? <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: color }} /> : null}
      <Text size="xs" tone="body">
        {text}
      </Text>
    </View>
  )
}
