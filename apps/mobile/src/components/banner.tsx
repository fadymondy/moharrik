// The in-app banner for a push that arrives while the app is open (the OS shows nothing then).
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import { Animated, Pressable, View } from "react-native"
import { useSafeAreaInsets } from "react-native-safe-area-context"

import { Mark, Text, useColors } from "./ui"

interface Banner {
  title: string
  body?: string
  onPress?: () => void
}

const Ctx = createContext<(b: Banner) => void>(() => {})

export const useBanner = () => useContext(Ctx)

export function BannerProvider({ children }: { children: ReactNode }) {
  const c = useColors()
  const insets = useSafeAreaInsets()
  const [banner, setBanner] = useState<Banner | null>(null)
  const y = useRef(new Animated.Value(-140)).current
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)

  const hide = useCallback(() => {
    Animated.timing(y, { toValue: -140, duration: 180, useNativeDriver: true }).start(() => setBanner(null))
  }, [y])

  const show = useCallback(
    (b: Banner) => {
      setBanner(b)
      Animated.spring(y, { toValue: 0, useNativeDriver: true }).start()
      if (timer.current) clearTimeout(timer.current)
      timer.current = setTimeout(hide, 4500)
    },
    [y, hide],
  )
  useEffect(() => () => void (timer.current && clearTimeout(timer.current)), [])

  const value = useMemo(() => show, [show])
  return (
    <Ctx.Provider value={value}>
      {children}
      {banner ? (
        <Animated.View style={{ position: "absolute", top: insets.top + 6, left: 10, right: 10, transform: [{ translateY: y }] }}>
          <Pressable
            onPress={() => {
              hide()
              banner.onPress?.()
            }}
            style={{ flexDirection: "row", gap: 12, alignItems: "center", padding: 14, borderRadius: 14, backgroundColor: c.overlay, borderWidth: 1, borderColor: c.line, elevation: 6, shadowColor: "#000", shadowOpacity: 0.15, shadowRadius: 12, shadowOffset: { width: 0, height: 4 } }}
          >
            <Mark size={28} />
            <View style={{ flex: 1 }}>
              <Text weight="bold" numberOfLines={1}>
                {banner.title}
              </Text>
              {banner.body ? (
                <Text size="sm" tone="body" numberOfLines={2}>
                  {banner.body}
                </Text>
              ) : null}
            </View>
          </Pressable>
        </Animated.View>
      ) : null}
    </Ctx.Provider>
  )
}
