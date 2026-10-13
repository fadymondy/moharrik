import { type ReactNode } from "react"
import { Modal, Pressable, ScrollView, View } from "react-native"
import { useSafeAreaInsets } from "react-native-safe-area-context"

import { usePrefs } from "@/lib/prefs"

import { Text } from "./ui"

/** A bottom sheet over a dimmed backdrop. A Modal sits outside the root view, so it sets its own direction. */
export function Sheet({ open, onClose, title, sub, children }: { open: boolean; onClose: () => void; title?: string; sub?: string; children: ReactNode }) {
  const { colors: c, rtl } = usePrefs()
  const insets = useSafeAreaInsets()
  return (
    <Modal visible={open} transparent animationType="slide" onRequestClose={onClose} statusBarTranslucent navigationBarTranslucent>
      <Pressable style={{ flex: 1, backgroundColor: "rgba(11,20,41,0.45)" }} onPress={onClose} />
      <View style={{ direction: rtl ? "rtl" : "ltr", backgroundColor: c.overlay, borderTopLeftRadius: 18, borderTopRightRadius: 18, paddingTop: 10, paddingBottom: insets.bottom, maxHeight: "75%" }}>
        <View style={{ alignSelf: "center", width: 40, height: 4, borderRadius: 2, backgroundColor: c.line, marginBottom: 10 }} />
        {title ? (
          <Text weight="bold" size="lg" style={{ paddingHorizontal: 16, paddingBottom: sub ? 2 : 8 }}>
            {title}
          </Text>
        ) : null}
        {sub ? (
          <Text size="sm" tone="muted" numberOfLines={1} style={{ paddingHorizontal: 16, paddingBottom: 8 }}>
            {sub}
          </Text>
        ) : null}
        <ScrollView>{children}</ScrollView>
      </View>
    </Modal>
  )
}
