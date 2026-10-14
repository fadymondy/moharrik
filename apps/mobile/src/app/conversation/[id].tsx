// One conversation: the thread, the reply box, and the actions the web's conversation toolbar
// has (close/reopen, snooze, assign, labels, mark unread, block for managers and up).
//
// The reply box sends a REAL message to the customer on the conversation's channel (WhatsApp,
// email, chat…). Test only against a test conversation or a local stack.
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { router, useLocalSearchParams } from "expo-router"
import { ArrowLeft, ArrowRight, Ban, Check, Clock, MoreVertical, Paperclip, SendHorizontal, Tag as TagIcon, UserRound } from "lucide-react-native"
import { useMemo, useState } from "react"
import { ActivityIndicator, Alert, FlatList, KeyboardAvoidingView, Linking, Platform, Pressable, TextInput, View } from "react-native"
import { useSafeAreaInsets } from "react-native-safe-area-context"

import { ago, channelName, type ConversationPatch, type Message } from "@moharrik/client"

import { useBanner } from "@/components/banner"
import { customerName, Tag } from "@/components/conversation-row"
import { Sheet } from "@/components/sheet"
import { Empty, ErrorView, Header, IconButton, labelColor, Loading, Row, Screen, Text, useColors } from "@/components/ui"
import { useWorkspaceParam } from "@/lib/navigate"
import { usePrefs } from "@/lib/prefs"
import { qk } from "@/lib/query"
import { useSession } from "@/lib/session"

type SheetKind = null | "actions" | "snooze" | "assign" | "labels"

export default function ConversationScreen() {
  const { id, ws: wsParam } = useLocalSearchParams<{ id: string; ws?: string }>()
  useWorkspaceParam(wsParam)
  const { t, locale, rtl, colors: c } = usePrefs()
  const qc = useQueryClient()
  const banner = useBanner()
  const insets = useSafeAreaInsets()
  const { api, workspace } = useSession()
  const ws = workspace?.id ?? ""
  const [sheet, setSheet] = useState<SheetKind>(null)
  const [draft, setDraft] = useState("")

  const detail = useQuery({ queryKey: qk.conversation(ws, id), queryFn: () => api.conversation(id), enabled: !!workspace && !!id })
  const conv = detail.data?.conversation
  // Older pages, fetched on scroll past the first page the detail carries.
  const older = useInfiniteQuery({
    queryKey: ["olderMessages", ws, id, detail.data?.messages_next_cursor ?? ""],
    queryFn: ({ pageParam }) => api.messages(id, pageParam),
    initialPageParam: detail.data?.messages_next_cursor ?? "",
    getNextPageParam: (p) => p.next_cursor || undefined,
    enabled: false,
  })
  const members = useQuery({ queryKey: qk.members(ws), queryFn: () => api.members(), enabled: !!workspace, staleTime: 5 * 60_000 })
  const labels = useQuery({ queryKey: qk.labels(ws), queryFn: () => api.labels(), enabled: !!workspace && sheet === "labels" })
  const me = useQuery({ queryKey: qk.me, queryFn: () => api.me(), staleTime: 10 * 60_000 })

  // Newest first, for the inverted list: the detail's page, then the older pages.
  const messages = useMemo(
    () => [
      ...[...(detail.data?.messages ?? [])].reverse(),
      ...(older.data?.pages ?? []).flatMap((p) => [...(p.messages ?? [])].reverse()),
    ],
    [detail.data, older.data],
  )

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: qk.conversation(ws, id) })
    void qc.invalidateQueries({ queryKey: ["conversations", ws] })
    void qc.invalidateQueries({ queryKey: ["bell", ws] })
  }
  const patch = useMutation({ mutationFn: (p: ConversationPatch) => api.updateConversation(id, p), onSuccess: invalidate })
  const organise = useMutation({ mutationFn: (labelIds: string[]) => api.organise(id, { label_ids: labelIds }), onSuccess: invalidate })
  const block = useMutation({
    mutationFn: (blocked: boolean) => api.setBlocked(id, blocked),
    onSuccess: invalidate,
    onError: () => banner({ title: t("inbox.blockFailed") }),
  })
  const reply = useMutation({
    mutationFn: (body: string) => api.reply(id, body),
    onSuccess: () => {
      setDraft("")
      invalidate()
    },
    onError: () => banner({ title: t("common.networkError") }),
  })

  const memberName = (uid: string | null | undefined) => {
    if (!uid) return ""
    if (me.data && String(me.data.id) === uid) return t("inbox.you")
    const m = members.data?.members?.find((x) => x.user_id === uid)
    return m ? (m.email.split("@")[0] ?? m.email) : ""
  }
  const Back = rtl ? ArrowRight : ArrowLeft
  const back = () => (router.canGoBack() ? router.back() : router.replace("/inbox"))

  if (detail.isLoading || !workspace) {
    return (
      <Screen>
        <Header title="" start={<IconButton label={t("common.back")} onPress={back}><Back size={22} color={c.fg} /></IconButton>} />
        <Loading />
      </Screen>
    )
  }
  if (detail.isError || !conv) {
    return (
      <Screen>
        <Header title="" start={<IconButton label={t("common.back")} onPress={back}><Back size={22} color={c.fg} /></IconButton>} />
        <ErrorView onRetry={() => void detail.refetch()} />
      </Screen>
    )
  }

  const name = customerName(conv)
  const closed = conv.status === "closed"
  const snoozeAt = (hours: number | "tomorrow" | "week") => {
    const d = new Date()
    if (hours === "tomorrow") {
      d.setDate(d.getDate() + 1)
      d.setHours(9, 0, 0, 0)
    } else if (hours === "week") {
      d.setDate(d.getDate() + ((8 - d.getDay()) % 7 || 7))
      d.setHours(9, 0, 0, 0)
    } else d.setTime(d.getTime() + hours * 3600_000)
    patch.mutate({ status: "snoozed", snoozed_until: d.toISOString() })
    setSheet(null)
  }
  const selected = new Set((conv.labels ?? []).map((l) => l.id))
  // Blocking needs inbox:manage on the server: managers, admins and owners.
  const canBlock = ["owner", "admin", "manager"].includes(workspace.role ?? "")
  const blocked = !!conv.blocked
  const toggleBlock = () => {
    setSheet(null)
    if (blocked) {
      block.mutate(false)
      return
    }
    Alert.alert(t("inbox.block"), t("inbox.blockBody"), [
      { text: t("common.cancel"), style: "cancel" },
      { text: t("inbox.block"), style: "destructive", onPress: () => block.mutate(true) },
    ])
  }

  return (
    <Screen edges={["top"]}>
      <Header
        title={name}
        sub={[channelName(t, conv.channel), conv.assignee_user_id ? t("inbox.assignedTo", { name: memberName(conv.assignee_user_id) }) : t("inbox.unassigned")].join(" · ")}
        start={
          <IconButton label={t("common.back")} onPress={back}>
            <Back size={22} color={c.fg} />
          </IconButton>
        }
        end={
          <View style={{ flexDirection: "row", gap: 2 }}>
            <IconButton label={closed ? t("inbox.reopen") : t("inbox.close")} onPress={() => patch.mutate({ status: closed ? "open" : "closed" })}>
              <Check size={22} color={closed ? c.muted : c.success} />
            </IconButton>
            <IconButton label={t("common.more")} onPress={() => setSheet("actions")}>
              <MoreVertical size={22} color={c.fg} />
            </IconButton>
          </View>
        }
      />
      {conv.status === "snoozed" && conv.snoozed_until ? (
        <Text size="sm" tone="warning" style={{ paddingHorizontal: 16, paddingVertical: 6 }}>
          {t("inbox.snoozedUntil", { at: new Date(conv.snoozed_until).toLocaleString(locale === "ar" ? "ar-EG" : "en-GB") })}
        </Text>
      ) : null}
      {blocked ? (
        <Text size="sm" tone="danger" style={{ paddingHorizontal: 16, paddingVertical: 6 }}>
          {t("inbox.blocked")}
        </Text>
      ) : null}
      {(conv.labels ?? []).length ? (
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6, paddingHorizontal: 16, paddingVertical: 6 }}>
          {(conv.labels ?? []).map((l) => (
            <Tag key={l.id} text={l.name} color={labelColor(l.color)} />
          ))}
        </View>
      ) : null}
      <KeyboardAvoidingView style={{ flex: 1 }} behavior={Platform.OS === "ios" ? "padding" : undefined}>
        <FlatList
          inverted
          data={messages}
          keyExtractor={(m) => m.id}
          renderItem={({ item }) => <Bubble m={item} author={memberName(item.author_user_id)} />}
          contentContainerStyle={{ padding: 12, gap: 8, flexGrow: 1 }}
          onEndReached={() => {
            if (!detail.data?.messages_next_cursor || older.isFetching) return
            if (!older.data) void older.refetch()
            else if (older.hasNextPage) void older.fetchNextPage()
          }}
          ListFooterComponent={older.isFetching ? <ActivityIndicator color={c.action} /> : null}
          ListEmptyComponent={<Empty title={t("inbox.noMessages")} />}
        />
        <View style={{ borderTopWidth: 1, borderColor: c.line, backgroundColor: c.surface, paddingHorizontal: 12, paddingTop: 8, paddingBottom: 8 + insets.bottom, gap: 4 }}>
          <View style={{ flexDirection: "row", alignItems: "flex-end", gap: 8 }}>
            <TextInput
              editable={!blocked}
              value={draft}
              onChangeText={setDraft}
              placeholder={t("inbox.replyPlaceholder")}
              placeholderTextColor={c.muted}
              multiline
              style={{
                flex: 1,
                maxHeight: 140,
                minHeight: 42,
                paddingHorizontal: 12,
                paddingVertical: 10,
                borderRadius: 12,
                borderWidth: 1,
                borderColor: c.line,
                backgroundColor: c.bg,
                color: c.fg,
                fontFamily: locale === "ar" ? "IBMPlexSansArabic-Regular" : "Inter_400Regular",
                fontSize: 15,
                textAlign: rtl ? "right" : "left",
              }}
            />
            <Pressable
              accessibilityRole="button"
              accessibilityLabel={t("inbox.send")}
              disabled={!draft.trim() || reply.isPending || blocked}
              onPress={() => reply.mutate(draft.trim())}
              style={{ width: 42, height: 42, borderRadius: 21, alignItems: "center", justifyContent: "center", backgroundColor: draft.trim() ? c.action : c.line }}
            >
              {reply.isPending ? <ActivityIndicator color={c.onAction} /> : <SendHorizontal size={20} color={c.onAction} style={{ transform: [{ scaleX: rtl ? -1 : 1 }] }} />}
            </Pressable>
          </View>
          <Text size="xs" tone="muted">
            {t("inbox.sendHint", { channel: channelName(t, conv.channel) })}
          </Text>
        </View>
      </KeyboardAvoidingView>

      <Sheet open={sheet === "actions"} onClose={() => setSheet(null)} title={name}>
        <Action icon={<Clock size={20} color={c.fg} />} label={t("inbox.snooze")} onPress={() => setSheet("snooze")} />
        <Action icon={<UserRound size={20} color={c.fg} />} label={t("inbox.assign")} onPress={() => setSheet("assign")} />
        <Action icon={<TagIcon size={20} color={c.fg} />} label={t("inbox.labels")} onPress={() => setSheet("labels")} />
        <Action
          icon={<View style={{ width: 20, alignItems: "center" }}><View style={{ width: 10, height: 10, borderRadius: 5, backgroundColor: c.action }} /></View>}
          label={t("inbox.markUnread")}
          onPress={() => {
            patch.mutate({ unread: true })
            setSheet(null)
            back()
          }}
        />
        {conv.contact?.id ? (
          <Action
            icon={<UserRound size={20} color={c.action} />}
            label={conv.contact.name || t("contacts.contacts")}
            onPress={() => {
              setSheet(null)
              router.push(`/contact/${conv.contact.id}`)
            }}
          />
        ) : null}
        {canBlock ? (
          <Action icon={<Ban size={20} color={blocked ? c.fg : c.danger} />} label={blocked ? t("inbox.unblock") : t("inbox.block")} onPress={toggleBlock} />
        ) : null}
      </Sheet>

      <Sheet open={sheet === "snooze"} onClose={() => setSheet(null)} title={t("inbox.snooze")}>
        <Action label={t("inbox.snooze1h")} onPress={() => snoozeAt(1)} />
        <Action label={t("inbox.snoozeTomorrow")} onPress={() => snoozeAt("tomorrow")} />
        <Action label={t("inbox.snoozeWeek")} onPress={() => snoozeAt("week")} />
      </Sheet>

      <Sheet open={sheet === "assign"} onClose={() => setSheet(null)} title={t("inbox.assign")}>
        {me.data ? (
          <Action
            label={t("inbox.assignMe")}
            onPress={() => {
              patch.mutate({ assignee_user_id: String(me.data.id) })
              setSheet(null)
            }}
          />
        ) : null}
        <Action
          label={t("inbox.unassigned")}
          checked={!conv.assignee_user_id}
          onPress={() => {
            patch.mutate({ assignee_user_id: "" })
            setSheet(null)
          }}
        />
        {(members.data?.members ?? [])
          .filter((m) => m.status !== "removed")
          .map((m) => (
            <Action
              key={m.user_id}
              label={m.email}
              checked={conv.assignee_user_id === m.user_id}
              onPress={() => {
                patch.mutate({ assignee_user_id: m.user_id })
                setSheet(null)
              }}
            />
          ))}
      </Sheet>

      <Sheet open={sheet === "labels"} onClose={() => setSheet(null)} title={t("inbox.labels")}>
        {labels.isLoading ? <Loading /> : null}
        {labels.data && !(labels.data.labels ?? []).length ? (
          <Text tone="muted" style={{ padding: 16 }}>
            {t("inbox.noLabels")}
          </Text>
        ) : null}
        {(labels.data?.labels ?? []).map((l) => (
          <Action
            key={l.id}
            icon={<View style={{ width: 20, alignItems: "center" }}><View style={{ width: 10, height: 10, borderRadius: 5, backgroundColor: labelColor(l.color) }} /></View>}
            label={l.name}
            checked={selected.has(l.id)}
            onPress={() => {
              const next = new Set(selected)
              if (next.has(l.id)) next.delete(l.id)
              else next.add(l.id)
              organise.mutate([...next])
            }}
          />
        ))}
      </Sheet>
    </Screen>
  )
}

function Action({ label, onPress, icon, checked }: { label: string; onPress: () => void; icon?: React.ReactNode; checked?: boolean }) {
  const c = useColors()
  return (
    <Row onPress={onPress}>
      {icon}
      <Text style={{ flex: 1 }}>{label}</Text>
      {checked ? <Check size={18} color={c.action} /> : null}
    </Row>
  )
}

function Bubble({ m, author }: { m: Message; author: string }) {
  const { t, locale, colors: c } = usePrefs()
  const out = m.direction === "out"
  const who = out ? author || m.author_email : m.sender_name
  return (
    <View style={{ alignItems: out ? "flex-end" : "flex-start" }}>
      <View
        style={{
          maxWidth: "85%",
          paddingHorizontal: 12,
          paddingVertical: 8,
          borderRadius: 14,
          borderBottomEndRadius: out ? 4 : 14,
          borderBottomStartRadius: out ? 14 : 4,
          backgroundColor: out ? c.action : c.surface,
          borderWidth: out ? 0 : 1,
          borderColor: c.line,
          gap: 4,
        }}
      >
        {who ? (
          <Text size="xs" weight="medium" style={{ color: out ? c.onAction : c.muted }}>
            {who}
          </Text>
        ) : null}
        {m.subject ? (
          <Text size="sm" weight="bold" style={{ color: out ? c.onAction : c.fg }}>
            {m.subject}
          </Text>
        ) : null}
        {m.body ? <Text style={{ color: out ? c.onAction : c.fg }}>{m.body.trim()}</Text> : null}
        {(m.attachments ?? []).map((a) => (
          <Pressable key={a.id} onPress={() => a.url && void Linking.openURL(a.url)} style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
            <Paperclip size={14} color={out ? c.onAction : c.action} />
            <Text size="sm" numberOfLines={1} style={{ color: out ? c.onAction : c.action, flexShrink: 1 }}>
              {a.filename || t("inbox.attachment")}
            </Text>
          </Pressable>
        ))}
        <Text size="xs" style={{ color: out ? c.onAction : c.muted, opacity: 0.8, textAlign: "right" }}>
          {ago(t, locale, m.created_at)}
          {out && m.status ? ` · ${m.status}` : ""}
        </Text>
      </View>
    </View>
  )
}
