import { useState } from "react"
import { KeyboardAvoidingView, Platform, Pressable, ScrollView, View } from "react-native"

import { Button, Input, Mark, Screen, Text } from "@/components/ui"
import { usePrefs } from "@/lib/prefs"
import { defaultServer, useSession } from "@/lib/session"

export default function SignIn() {
  const { t, locale, setLocale } = usePrefs()
  const { signIn, lost, server, setServer } = useSession()
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)
  const [editServer, setEditServer] = useState(false)
  const [url, setUrl] = useState(server)

  async function go() {
    setBusy(true)
    setFailed(false)
    try {
      if (editServer && url.trim() !== server) await setServer(url)
      const r = await signIn()
      if (r === "cancelled") setFailed(false)
    } catch {
      setFailed(true)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Screen edges={["top", "bottom"]}>
      <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} style={{ flex: 1 }}>
        <ScrollView contentContainerStyle={{ flexGrow: 1, padding: 24, justifyContent: "center", gap: 20 }}>
          <View style={{ alignItems: "center", gap: 16 }}>
            <Mark size={88} />
            <Text size="xxl" weight="bold" style={{ textAlign: "center" }}>
              {t("signin.title")}
            </Text>
            <Text tone="body" style={{ textAlign: "center" }}>
              {t("signin.body")}
            </Text>
          </View>
          {lost ? (
            <Text tone="warning" style={{ textAlign: "center" }}>
              {t("signin.lost")}
            </Text>
          ) : null}
          {failed ? (
            <Text tone="danger" style={{ textAlign: "center" }}>
              {t("signin.failed")}
            </Text>
          ) : null}
          <Button title={busy ? t("signin.waiting") : t("signin.button")} busy={busy} onPress={go} />
          {editServer ? (
            <View style={{ gap: 6 }}>
              <Text size="sm" tone="muted">
                {t("signin.server")}
              </Text>
              <Input value={url} onChangeText={setUrl} autoCapitalize="none" autoCorrect={false} keyboardType="url" placeholder={defaultServer()} />
            </View>
          ) : null}
          <View style={{ flexDirection: "row", justifyContent: "center", gap: 24 }}>
            <Pressable onPress={() => setLocale(locale === "ar" ? "en" : "ar")} hitSlop={8}>
              <Text tone="action" weight="medium">
                {locale === "ar" ? "English" : "العربية"}
              </Text>
            </Pressable>
            <Pressable onPress={() => setEditServer((v) => !v)} hitSlop={8}>
              <Text tone="muted" size="sm">
                {t("signin.server")}: {server.replace(/^https?:\/\//, "")}
              </Text>
            </Pressable>
          </View>
        </ScrollView>
      </KeyboardAvoidingView>
    </Screen>
  )
}
