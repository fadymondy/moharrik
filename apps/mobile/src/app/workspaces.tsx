import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { router } from "expo-router"
import { Check } from "lucide-react-native"
import { useState } from "react"
import { FlatList, KeyboardAvoidingView, Platform, ScrollView, View } from "react-native"

import { Avatar, Button, ErrorView, Header, Input, Loading, Mark, Row, Screen, Text, useColors } from "@/components/ui"
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
        <CreateWorkspace onSignOut={() => void signOut()} />
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

/** A new account has no workspace yet: create one here (you become its owner) rather than on the web. */
function CreateWorkspace({ onSignOut }: { onSignOut: () => void }) {
  const { t } = usePrefs()
  const qc = useQueryClient()
  const { api, selectWorkspace } = useSession()
  const [name, setName] = useState("")
  const create = useMutation({
    mutationFn: () => api.createWorkspace(name.trim()),
    onSuccess: async (r) => {
      void qc.invalidateQueries({ queryKey: qk.workspaces })
      await selectWorkspace(pickWorkspace(r.workspace))
      router.replace("/inbox")
    },
  })
  const submit = () => {
    if (name.trim() && !create.isPending) create.mutate()
  }

  return (
    <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} style={{ flex: 1 }}>
      <ScrollView contentContainerStyle={{ flexGrow: 1, justifyContent: "center", padding: 24, gap: 16 }} keyboardShouldPersistTaps="handled">
        <View style={{ alignItems: "center", gap: 12 }}>
          <Mark size={48} muted />
          <Text weight="medium" size="lg" style={{ textAlign: "center" }}>
            {t("workspaces.none")}
          </Text>
          <Text tone="muted" style={{ textAlign: "center" }}>
            {t("workspaces.noneBody")}
          </Text>
        </View>
        <View style={{ gap: 8 }}>
          <Input
            value={name}
            onChangeText={setName}
            placeholder={t("workspaces.nameField")}
            accessibilityLabel={t("workspaces.nameField")}
            autoCapitalize="words"
            maxLength={80}
            returnKeyType="done"
            onSubmitEditing={submit}
          />
          <Text size="sm" tone="muted">
            {t("workspaces.nameHint")}
          </Text>
        </View>
        {create.isError ? (
          <Text tone="danger" style={{ textAlign: "center" }} accessibilityLiveRegion="polite">
            {t("workspaces.createFailed")}
          </Text>
        ) : null}
        <Button title={t("workspaces.create")} busy={create.isPending} disabled={!name.trim() || create.isPending} onPress={submit} />
        <Button title={t("settings.signOut")} kind="secondary" disabled={create.isPending} onPress={onSignOut} />
      </ScrollView>
    </KeyboardAvoidingView>
  )
}
