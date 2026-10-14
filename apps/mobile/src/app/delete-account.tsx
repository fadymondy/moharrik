// Settings → Delete account (App Store 5.1.1(v); Google Play's account-deletion policy).
//
// The server decides (POST /api/me/account/delete): the e-mail typed to confirm, plus the password
// or a sign-in within the last ten minutes. "Sign in again" runs the normal sign-in (native Apple
// on iOS when the account has Apple, else the browser), which gives this install a fresh token.
// Workspaces only this person is in go with the account; being the only owner of a shared one
// blocks it until ownership moves (on the web).
import { useQuery, useQueryClient } from "@tanstack/react-query"
import * as AppleAuthentication from "expo-apple-authentication"
import { router } from "expo-router"
import { useEffect, useState } from "react"
import { Alert, KeyboardAvoidingView, Platform, ScrollView, View } from "react-native"

import { errorCode, type Key } from "@moharrik/client"

import { BackButton } from "@/components/back"
import { Button, ErrorView, Header, Input, Loading, Screen, Section, Text } from "@/components/ui"
import { usePrefs } from "@/lib/prefs"
import { useSession } from "@/lib/session"

const KNOWN = ["wrong_password", "reauth_required", "sole_owner", "confirm_mismatch"]

export default function DeleteAccount() {
  const { t } = usePrefs()
  const qc = useQueryClient()
  const { api, signIn, signInWithApple, forget } = useSession()
  const preview = useQuery({ queryKey: ["account-deletion"], queryFn: () => api.accountDeletion() })
  const methods = useQuery({ queryKey: ["sign-in-methods"], queryFn: () => api.signInMethods() })
  const [typed, setTyped] = useState("")
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [appleHere, setAppleHere] = useState(false)
  useEffect(() => {
    if (Platform.OS === "ios") void AppleAuthentication.isAvailableAsync().then(setAppleHere).catch(() => {})
  }, [])

  const d = preview.data
  const blocking = d?.blocking_workspaces ?? []
  const deleting = d?.delete_workspaces ?? []
  const needsPassword = !!d && !d.fresh && d.has_password
  const needsReauth = !!d && !d.fresh && !d.has_password
  const ready = !!d && blocking.length === 0 && typed.trim().toLowerCase() === d.email.toLowerCase() && (!needsPassword || password.length > 0) && !needsReauth

  async function reauth() {
    setError(null)
    setBusy(true)
    try {
      const ids = methods.data?.identities ?? []
      const hasApple = ids.some((i) => i.provider === "apple")
      if (hasApple && appleHere) await signInWithApple()
      else await signIn(hasApple ? "apple" : ids.some((i) => i.provider === "google") ? "google" : undefined)
      await qc.invalidateQueries({ queryKey: ["account-deletion"] })
    } catch {
      setError(t("signin.failed"))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    if (!ready) return
    setBusy(true)
    setError(null)
    try {
      await api.deleteAccount(typed.trim(), needsPassword ? password : undefined)
      await forget()
      Alert.alert(t("delete.done"))
      router.replace("/sign-in")
    } catch (err) {
      const code = errorCode(err)
      setError(code && KNOWN.includes(code) ? t(`delete.error.${code}` as Key) : err instanceof Error ? err.message : t("common.networkError"))
      if (code === "reauth_required" || code === "sole_owner") void preview.refetch()
    } finally {
      setBusy(false)
    }
  }

  return (
    <Screen>
      <Header title={t("delete.title")} start={<BackButton fallback="/settings" />} />
      <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} style={{ flex: 1 }}>
        {preview.isLoading ? (
          <Loading />
        ) : preview.isError || !d ? (
          <ErrorView onRetry={() => void preview.refetch()} />
        ) : (
          <ScrollView contentContainerStyle={{ padding: 16, gap: 16, paddingBottom: 40 }} keyboardShouldPersistTaps="handled">
            <Text tone="body">{t("delete.body")}</Text>
            {blocking.length > 0 ? (
              <Section>
                <Text tone="danger" style={{ paddingHorizontal: 16 }}>
                  {t("delete.blocking")}
                </Text>
                {blocking.map((w) => (
                  <Text key={w.id} weight="medium" style={{ paddingHorizontal: 16, paddingTop: 6 }}>
                    • {w.name}
                  </Text>
                ))}
              </Section>
            ) : null}
            {deleting.length > 0 ? (
              <Section>
                <Text style={{ paddingHorizontal: 16 }}>{t("delete.workspaces")}</Text>
                {deleting.map((w) => (
                  <Text key={w.id} weight="medium" style={{ paddingHorizontal: 16, paddingTop: 6 }}>
                    • {w.name}
                  </Text>
                ))}
              </Section>
            ) : null}
            {d.fresh ? (
              <Text tone="muted" size="sm">
                {t("delete.fresh")}
              </Text>
            ) : null}
            {needsReauth ? (
              <View style={{ gap: 8 }}>
                <Text>{t("delete.reauth")}</Text>
                <Button title={t("delete.reauthButton")} kind="secondary" busy={busy} onPress={() => void reauth()} />
              </View>
            ) : null}
            {needsPassword ? (
              <View style={{ gap: 6 }}>
                <Text size="sm" tone="muted">
                  {t("delete.password")}
                </Text>
                <Input value={password} onChangeText={setPassword} secureTextEntry autoCapitalize="none" autoComplete="current-password" textContentType="password" />
              </View>
            ) : null}
            <View style={{ gap: 6 }}>
              <Text size="sm" tone="muted">
                {t("delete.confirm", { email: d.email })}
              </Text>
              <Input value={typed} onChangeText={setTyped} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" placeholder={d.email} />
            </View>
            {error ? (
              <Text tone="danger" accessibilityLiveRegion="polite">
                {error}
              </Text>
            ) : null}
            <Button title={t("delete.button")} kind="danger" busy={busy} disabled={!ready} onPress={() => void remove()} />
          </ScrollView>
        )}
      </KeyboardAvoidingView>
    </Screen>
  )
}
