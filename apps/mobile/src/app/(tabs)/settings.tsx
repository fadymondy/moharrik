import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import * as Application from "expo-application"
import { router } from "expo-router"
import { ChevronRight, ChevronLeft } from "lucide-react-native"
import { useEffect, useState } from "react"
import { Alert, Linking, ScrollView, Switch, View } from "react-native"

import type { PushPrefs } from "@moharrik/client"

import { Avatar, Button, Header, Row, Screen, Section, Segmented, Text, useColors } from "@/components/ui"
import { usePrefs, type ThemePref } from "@/lib/prefs"
import { disablePush, enablePush, pushState, type PushState } from "@/lib/push"
import { qk } from "@/lib/query"
import { useSession } from "@/lib/session"

export default function SettingsScreen() {
  const { t, locale, setLocale, theme, setTheme, rtl } = usePrefs()
  const c = useColors()
  const qc = useQueryClient()
  const { api, workspace, signOut, server } = useSession()
  const ws = workspace?.id ?? ""
  const me = useQuery({ queryKey: qk.me, queryFn: () => api.me(), staleTime: 10 * 60_000 })

  const [push, setPush] = useState<PushState>("off")
  const [pushBusy, setPushBusy] = useState(false)
  useEffect(() => {
    void pushState().then(setPush)
  }, [])

  const prefs = useQuery({ queryKey: qk.pushPrefs(ws), queryFn: () => api.pushPrefs(), enabled: !!workspace && push === "on" })
  const savePrefs = useMutation({
    mutationFn: (p: PushPrefs) => api.setPushPrefs(p),
    onMutate: (p) => qc.setQueryData(qk.pushPrefs(ws), p),
    onSettled: () => void qc.invalidateQueries({ queryKey: qk.pushPrefs(ws) }),
  })

  async function togglePush(on: boolean) {
    setPushBusy(true)
    try {
      if (on) setPush(await enablePush(api, locale))
      else {
        await disablePush(api)
        setPush("off")
      }
    } finally {
      setPushBusy(false)
    }
  }

  function confirmSignOut() {
    Alert.alert(t("settings.signOut"), t("settings.signOutBody"), [
      { text: t("common.cancel"), style: "cancel" },
      { text: t("settings.signOut"), style: "destructive", onPress: () => void signOut() },
    ])
  }

  const Chevron = rtl ? ChevronLeft : ChevronRight
  const p = prefs.data
  const setP = (k: keyof Omit<PushPrefs, "$schema">, v: boolean) => p && savePrefs.mutate({ ...p, [k]: v })

  return (
    <Screen>
      <Header title={t("settings.title")} />
      <ScrollView contentContainerStyle={{ paddingBottom: 32 }}>
        <Section title={t("settings.workspace")}>
          <Row onPress={() => router.push("/workspaces")}>
            <Avatar name={workspace?.name ?? ""} url={workspace?.avatar_url} />
            <View style={{ flex: 1 }}>
              <Text weight="medium">{workspace?.name}</Text>
              <Text size="sm" tone="muted">
                {t("settings.switch")}
              </Text>
            </View>
            <Chevron size={20} color={c.muted} />
          </Row>
        </Section>

        <Section title={t("settings.push")}>
          <Toggle
            label={t("settings.push")}
            sub={push === "unavailable" ? t("settings.pushOff") : push === "denied" ? t("settings.pushOff") : t("settings.pushBody")}
            value={push === "on"}
            disabled={pushBusy || push === "unavailable"}
            onChange={(v) => {
              if (push === "denied") void Linking.openSettings()
              else void togglePush(v)
            }}
          />
          {push === "on" && p ? (
            <>
              <Toggle label={t("settings.pushMessages")} value={p.messages} onChange={(v) => setP("messages", v)} />
              <Toggle label={t("settings.pushAssignedOnly")} value={p.assigned_only} disabled={!p.messages} onChange={(v) => setP("assigned_only", v)} />
              <Toggle label={t("settings.pushAssignments")} value={p.assignments} onChange={(v) => setP("assignments", v)} />
              <Toggle label={t("settings.pushNotifications")} value={p.notifications} onChange={(v) => setP("notifications", v)} />
            </>
          ) : null}
        </Section>

        <Section title={t("settings.language")}>
          <Segmented
            value={locale}
            onChange={setLocale}
            options={[
              { value: "ar", label: "العربية" },
              { value: "en", label: "English" },
            ]}
          />
        </Section>

        <Section title={t("settings.appearance")}>
          <Segmented<ThemePref>
            value={theme}
            onChange={setTheme}
            options={[
              { value: "system", label: t("settings.system") },
              { value: "light", label: t("settings.light") },
              { value: "dark", label: t("settings.dark") },
            ]}
          />
        </Section>

        <Section title={t("settings.account")}>
          <Row>
            <View style={{ flex: 1 }}>
              <Text>{me.data?.email ? t("settings.signedInAs", { email: me.data.email }) : "…"}</Text>
              <Text size="sm" tone="muted">
                {server.replace(/^https?:\/\//, "")}
              </Text>
            </View>
          </Row>
          <View style={{ padding: 16 }}>
            <Button title={t("settings.signOut")} kind="secondary" onPress={confirmSignOut} />
          </View>
        </Section>

        <Text size="xs" tone="muted" style={{ textAlign: "center", marginTop: 8 }}>
          {t("settings.version", { v: `${Application.nativeApplicationVersion ?? "dev"} (${Application.nativeBuildVersion ?? "0"})` })}
        </Text>
      </ScrollView>
    </Screen>
  )
}

function Toggle({ label, sub, value, onChange, disabled }: { label: string; sub?: string; value: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  const c = useColors()
  return (
    <Row>
      <View style={{ flex: 1, opacity: disabled ? 0.55 : 1 }}>
        <Text>{label}</Text>
        {sub ? (
          <Text size="sm" tone="muted">
            {sub}
          </Text>
        ) : null}
      </View>
      <Switch value={value} onValueChange={onChange} disabled={disabled} trackColor={{ true: c.action, false: c.line }} thumbColor={c.raised} />
    </Row>
  )
}
