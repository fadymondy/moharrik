import { router } from "expo-router"
import { ArrowLeft, ArrowRight } from "lucide-react-native"

import { usePrefs } from "@/lib/prefs"

import { IconButton } from "./ui"

/** The header's back arrow, pointing the reading direction's way. */
export function BackButton({ fallback = "/inbox" }: { fallback?: string }) {
  const { t, rtl, colors } = usePrefs()
  const Icon = rtl ? ArrowRight : ArrowLeft
  return (
    <IconButton label={t("common.back")} onPress={() => (router.canGoBack() ? router.back() : router.replace(fallback))}>
      <Icon size={22} color={colors.fg} />
    </IconButton>
  )
}
