import { Inter_400Regular, Inter_500Medium, Inter_700Bold } from "@expo-google-fonts/inter"
import { QueryClientProvider, useQueryClient } from "@tanstack/react-query"
import { useFonts } from "expo-font"
import { router, Stack, useSegments } from "expo-router"
import * as SplashScreen from "expo-splash-screen"
import { StatusBar } from "expo-status-bar"
import * as SystemUI from "expo-system-ui"
import { useEffect } from "react"
import { View } from "react-native"
import { GestureHandlerRootView } from "react-native-gesture-handler"
import { SafeAreaProvider } from "react-native-safe-area-context"

import { BannerProvider, useBanner } from "@/components/banner"
import { routeOf } from "@/lib/navigate"
import { PrefsProvider, usePrefs } from "@/lib/prefs"
import { watchPush } from "@/lib/push"
import { queryClient } from "@/lib/query"
import { SessionProvider, useSession } from "@/lib/session"

void SplashScreen.preventAutoHideAsync().catch(() => {})

export default function Root() {
  const [fonts] = useFonts({
    Inter_400Regular,
    Inter_500Medium,
    Inter_700Bold,
    "Lusail-Regular": require("../../assets/fonts/Lusail-Regular.ttf"),
    "Lusail-Medium": require("../../assets/fonts/Lusail-Medium.ttf"),
    "Lusail-Bold": require("../../assets/fonts/Lusail-Bold.ttf"),
  })
  return (
    <GestureHandlerRootView style={{ flex: 1 }}>
      <SafeAreaProvider>
        <QueryClientProvider client={queryClient}>
          <PrefsProvider>
            <SessionProvider>
              <Shell fonts={fonts} />
            </SessionProvider>
          </PrefsProvider>
        </QueryClientProvider>
      </SafeAreaProvider>
    </GestureHandlerRootView>
  )
}

function Shell({ fonts }: { fonts: boolean }) {
  const { ready, rtl, colors, scheme } = usePrefs()
  const { status } = useSession()
  const loaded = ready && fonts && status !== "loading"

  useEffect(() => {
    void SystemUI.setBackgroundColorAsync(colors.bg).catch(() => {})
  }, [colors.bg])
  useEffect(() => {
    if (loaded) void SplashScreen.hideAsync().catch(() => {})
  }, [loaded])

  if (!loaded) return null
  return (
    // `direction` mirrors every row below it for Arabic, at once, without a restart.
    <View style={{ flex: 1, direction: rtl ? "rtl" : "ltr", backgroundColor: colors.bg }}>
      <StatusBar style={scheme === "dark" ? "light" : "dark"} />
      <BannerProvider>
        <Guard />
        <Push />
        <Stack screenOptions={{ headerShown: false, contentStyle: { backgroundColor: colors.bg }, animation: rtl ? "slide_from_left" : "slide_from_right" }} />
      </BannerProvider>
    </View>
  )
}

/** Signed out → sign-in; signed in without a workspace → the picker. */
function Guard() {
  const { status, workspace } = useSession()
  const segments = useSegments()
  const first = segments[0] as string | undefined
  useEffect(() => {
    const open = first === "sign-in" || first === "auth"
    if (status === "signedOut" && !open) router.replace("/sign-in")
    else if (status === "signedIn" && open) router.replace(workspace ? "/inbox" : "/workspaces")
    else if (status === "signedIn" && !workspace && first !== "workspaces" && first !== undefined) router.replace("/workspaces")
  }, [status, workspace, first])
  return null
}

/** Push registration, taps and foreground banners, while signed in. */
function Push() {
  const { status, api, workspace } = useSession()
  const { locale, t } = usePrefs()
  const banner = useBanner()
  const qc = useQueryClient()
  const signedIn = status === "signedIn"
  useEffect(() => {
    if (!signedIn) return
    return watchPush({
      api,
      locale,
      onOpen: (target) => router.push(routeOf(target)),
      onForeground: (m) => {
        void qc.invalidateQueries({ queryKey: ["bell"] })
        void qc.invalidateQueries({ queryKey: ["conversations"] })
        void qc.invalidateQueries({ queryKey: ["notifications"] })
        if (m.target?.screen === "conversation") void qc.invalidateQueries({ queryKey: ["conversation", workspace?.id ?? "", m.target.id] })
        const target = m.target
        banner({ title: m.title || t("app.name"), body: m.body, onPress: target ? () => router.push(routeOf(target)) : undefined })
      },
    })
    // Re-subscribe on sign-in and on a language change (the device registers its locale).
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signedIn, api, locale])
  return null
}
