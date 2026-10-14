// The app's small UI kit, in the web's visual language (Nasaq on the Moharrik brand): paper and
// ink grounds, the teal action, one gold accent. Everything lays out with start/end, never
// left/right, so the root `direction` (src/app/_layout.tsx) mirrors it for Arabic.
import { Search, X } from "lucide-react-native"
import { type ReactNode } from "react"
import {
  ActivityIndicator,
  Image,
  Pressable,
  Text as RNText,
  TextInput,
  View,
  type PressableProps,
  type StyleProp,
  type TextInputProps,
  type TextProps,
  type TextStyle,
  type ViewStyle,
} from "react-native"
import { SafeAreaView, type Edge } from "react-native-safe-area-context"
import Svg, { Rect } from "react-native-svg"

import { MARK_ACCENT_INDEX, MARK_CELLS, type Palette } from "@moharrik/client"

import { usePrefs } from "@/lib/prefs"

type Weight = "regular" | "medium" | "bold"

const FAMILY: Record<"ar" | "en", Record<Weight, string>> = {
  ar: { regular: "IBMPlexSansArabic-Regular", medium: "IBMPlexSansArabic-Medium", bold: "IBMPlexSansArabic-Bold" },
  en: { regular: "Inter_400Regular", medium: "Inter_500Medium", bold: "Inter_700Bold" },
}

export const SIZE = { xs: 12, sm: 13, md: 15, lg: 17, xl: 22, xxl: 28 } as const

export function useColors(): Palette {
  return usePrefs().colors
}

export function Text({
  weight = "regular",
  size = "md",
  tone = "fg",
  style,
  ...rest
}: TextProps & { weight?: Weight; size?: keyof typeof SIZE; tone?: keyof Palette }) {
  const { locale, rtl, colors } = usePrefs()
  const base: TextStyle = {
    fontFamily: FAMILY[locale][weight],
    fontSize: SIZE[size],
    lineHeight: Math.round(SIZE[size] * (locale === "ar" ? 1.55 : 1.4)),
    color: colors[tone],
    // "auto" is the paragraph's start. Not "right": under an RTL `direction` React Native swaps
    // left and right (iOS and Android), so "right" put full-width text — headers — on the left.
    textAlign: "auto",
    writingDirection: rtl ? "rtl" : "ltr",
  }
  return <RNText {...rest} style={[base, style]} />
}

export function Screen({ children, edges = ["top"], style }: { children: ReactNode; edges?: Edge[]; style?: StyleProp<ViewStyle> }) {
  const c = useColors()
  return (
    <SafeAreaView edges={edges} style={[{ flex: 1, backgroundColor: c.bg }, style]}>
      {children}
    </SafeAreaView>
  )
}

export function Header({ title, start, end, sub }: { title: string; start?: ReactNode; end?: ReactNode; sub?: string }) {
  const c = useColors()
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 8, paddingHorizontal: 16, paddingVertical: 10, borderBottomWidth: 1, borderColor: c.line }}>
      {start}
      <View style={{ flex: 1 }}>
        <Text size="xl" weight="bold" numberOfLines={1}>
          {title}
        </Text>
        {sub ? (
          <Text size="sm" tone="muted" numberOfLines={1}>
            {sub}
          </Text>
        ) : null}
      </View>
      {end}
    </View>
  )
}

export function Button({
  title,
  onPress,
  kind = "primary",
  busy,
  disabled,
  icon,
  style,
}: {
  title: string
  onPress?: () => void
  kind?: "primary" | "secondary" | "danger" | "ghost"
  busy?: boolean
  disabled?: boolean
  icon?: ReactNode
  style?: StyleProp<ViewStyle>
}) {
  const c = useColors()
  const bg = kind === "primary" ? c.action : kind === "danger" ? c.danger : kind === "secondary" ? c.raised : "transparent"
  const fg = kind === "primary" ? c.onAction : kind === "danger" ? "#fff" : kind === "ghost" ? c.action : c.fg
  return (
    <Pressable
      accessibilityRole="button"
      onPress={onPress}
      disabled={disabled || busy}
      style={({ pressed }) => [
        {
          flexDirection: "row",
          alignItems: "center",
          justifyContent: "center",
          gap: 8,
          minHeight: 44,
          paddingHorizontal: 16,
          borderRadius: 10,
          backgroundColor: bg,
          borderWidth: kind === "secondary" ? 1 : 0,
          borderColor: c.line,
          opacity: disabled ? 0.5 : pressed ? 0.8 : 1,
        },
        style,
      ]}
    >
      {busy ? <ActivityIndicator color={fg} /> : icon}
      <RNTextLike color={fg}>{title}</RNTextLike>
    </Pressable>
  )
}

function RNTextLike({ color, children }: { color: string; children: string }) {
  return (
    <Text weight="medium" style={{ color, textAlign: "center" }}>
      {children}
    </Text>
  )
}

export function IconButton({ onPress, children, label, ...rest }: PressableProps & { children: ReactNode; label: string }) {
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      hitSlop={8}
      onPress={onPress}
      style={({ pressed }) => ({ padding: 6, borderRadius: 8, opacity: pressed ? 0.6 : 1 })}
      {...rest}
    >
      {children}
    </Pressable>
  )
}

export function Chip({ label, active, onPress, color }: { label: string; active?: boolean; onPress?: () => void; color?: string }) {
  const c = useColors()
  return (
    <Pressable
      onPress={onPress}
      style={{
        flexDirection: "row",
        alignItems: "center",
        gap: 6,
        paddingHorizontal: 12,
        height: 32,
        borderRadius: 16,
        borderWidth: 1,
        borderColor: active ? c.action : c.line,
        backgroundColor: active ? c.action : c.surface,
      }}
    >
      {color ? <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: color }} /> : null}
      <Text size="sm" weight="medium" style={{ color: active ? c.onAction : c.fg }}>
        {label}
      </Text>
    </Pressable>
  )
}

export function Row({ children, onPress, style }: { children: ReactNode; onPress?: () => void; style?: StyleProp<ViewStyle> }) {
  const c = useColors()
  return (
    <Pressable
      onPress={onPress}
      disabled={!onPress}
      style={({ pressed }) => [
        { flexDirection: "row", alignItems: "center", gap: 12, paddingHorizontal: 16, paddingVertical: 12, backgroundColor: pressed ? c.surface : "transparent", borderBottomWidth: 1, borderColor: c.line },
        style,
      ]}
    >
      {children}
    </Pressable>
  )
}

export function Card({ children, style }: { children: ReactNode; style?: StyleProp<ViewStyle> }) {
  const c = useColors()
  return <View style={[{ backgroundColor: c.surface, borderRadius: 14, borderWidth: 1, borderColor: c.line, padding: 16, gap: 8 }, style]}>{children}</View>
}

const LABEL_COLORS: Record<string, string> = {
  gray: "#8A8F98",
  red: "#D9455F",
  orange: "#E8742A",
  amber: "#E0931A",
  green: "#4E9A3E",
  teal: "#00A0A8",
  blue: "#3B7DD8",
  violet: "#7E5BD6",
  pink: "#D65BA6",
}
export const labelColor = (name: string | undefined) => LABEL_COLORS[name ?? ""] ?? LABEL_COLORS.gray!

export function Avatar({ name, url, size = 40 }: { name: string; url?: string | null; size?: number }) {
  const c = useColors()
  const initials = (name || "?")
    .trim()
    .split(/\s+/)
    .slice(0, 2)
    .map((w) => w[0] ?? "")
    .join("")
    .toUpperCase()
  if (url) return <Image source={{ uri: url }} style={{ width: size, height: size, borderRadius: size / 2, backgroundColor: c.raised }} />
  return (
    <View style={{ width: size, height: size, borderRadius: size / 2, backgroundColor: c.raised, borderWidth: 1, borderColor: c.line, alignItems: "center", justifyContent: "center" }}>
      <Text weight="bold" size={size > 48 ? "lg" : "sm"} tone="body" style={{ textAlign: "center" }}>
        {initials}
      </Text>
    </View>
  )
}

export function Badge({ n, tone = "action" }: { n: number; tone?: "action" | "accent" | "danger" }) {
  const c = useColors()
  if (!n) return null
  return (
    <View style={{ minWidth: 20, height: 20, paddingHorizontal: 6, borderRadius: 10, backgroundColor: c[tone], alignItems: "center", justifyContent: "center" }}>
      <Text size="xs" weight="bold" style={{ color: c.onAction, textAlign: "center", lineHeight: 16 }}>
        {n > 99 ? "99+" : String(n)}
      </Text>
    </View>
  )
}

export function Dot({ color }: { color?: string }) {
  const c = useColors()
  return <View style={{ width: 9, height: 9, borderRadius: 5, backgroundColor: color ?? c.action }} />
}

export function Empty({ title, body, action }: { title: string; body?: string; action?: ReactNode }) {
  return (
    <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 32, gap: 12 }}>
      <Mark size={48} muted />
      <Text weight="medium" size="lg" style={{ textAlign: "center" }}>
        {title}
      </Text>
      {body ? (
        <Text tone="muted" style={{ textAlign: "center" }}>
          {body}
        </Text>
      ) : null}
      {action}
    </View>
  )
}

export function Loading() {
  const c = useColors()
  return (
    <View style={{ flex: 1, alignItems: "center", justifyContent: "center", padding: 32 }}>
      <ActivityIndicator color={c.action} />
    </View>
  )
}

export function ErrorView({ onRetry }: { onRetry: () => void }) {
  const { t } = usePrefs()
  return <Empty title={t("common.networkError")} action={<Button title={t("common.retry")} kind="secondary" onPress={onRetry} />} />
}

export function SearchBox({ value, onChangeText, placeholder, ...rest }: TextInputProps & { value: string; onChangeText: (s: string) => void }) {
  const { colors: c, rtl, locale } = usePrefs()
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginHorizontal: 16, marginVertical: 8, paddingHorizontal: 12, height: 40, borderRadius: 10, backgroundColor: c.surface, borderWidth: 1, borderColor: c.line }}>
      <Search size={16} color={c.muted} />
      <TextInput
        value={value}
        onChangeText={onChangeText}
        placeholder={placeholder}
        placeholderTextColor={c.muted}
        returnKeyType="search"
        autoCorrect={false}
        style={{ flex: 1, color: c.fg, fontFamily: FAMILY[locale].regular, fontSize: SIZE.md, textAlign: rtl ? "right" : "left", paddingVertical: 0 }}
        {...rest}
      />
      {value ? (
        <IconButton label="clear" onPress={() => onChangeText("")}>
          <X size={16} color={c.muted} />
        </IconButton>
      ) : null}
    </View>
  )
}

export function Input(props: TextInputProps) {
  const { colors: c, rtl, locale } = usePrefs()
  return (
    <TextInput
      placeholderTextColor={c.muted}
      {...props}
      style={[
        { minHeight: 44, paddingHorizontal: 12, borderRadius: 10, borderWidth: 1, borderColor: c.line, backgroundColor: c.surface, color: c.fg, fontFamily: FAMILY[locale].regular, fontSize: SIZE.md, textAlign: rtl ? "right" : "left" },
        props.style,
      ]}
    />
  )
}

export function Segmented<V extends string>({ value, options, onChange }: { value: V; options: { value: V; label: string }[]; onChange: (v: V) => void }) {
  const c = useColors()
  return (
    <View style={{ flexDirection: "row", marginHorizontal: 16, marginVertical: 8, padding: 3, borderRadius: 10, backgroundColor: c.surface, borderWidth: 1, borderColor: c.line }}>
      {options.map((o) => {
        const on = o.value === value
        return (
          <Pressable key={o.value} onPress={() => onChange(o.value)} style={{ flex: 1, alignItems: "center", paddingVertical: 7, borderRadius: 8, backgroundColor: on ? c.raised : "transparent" }}>
            <Text size="sm" weight={on ? "bold" : "regular"} tone={on ? "fg" : "muted"} style={{ textAlign: "center" }}>
              {o.label}
            </Text>
          </Pressable>
        )
      })}
    </View>
  )
}

export function Section({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <View style={{ marginTop: 20 }}>
      {title ? (
        <Text size="sm" weight="medium" tone="muted" style={{ paddingHorizontal: 16, marginBottom: 6 }}>
          {title}
        </Text>
      ) : null}
      {children}
    </View>
  )
}

/** The Moharrik mark: seven cells of a 5×4 lattice, the second one gold (web/app/icon.svg). */
export function Mark({ size = 40, muted }: { size?: number; muted?: boolean }) {
  const c = useColors()
  const cell = 10
  return (
    <Svg width={size} height={(size * 4) / 5} viewBox={`0 0 ${cell * 5} ${cell * 4}`}>
      {MARK_CELLS.map(([x, y], i) => (
        <Rect
          key={i}
          x={x * cell}
          y={y * cell}
          width={cell}
          height={cell}
          fill={muted ? c.line : i === MARK_ACCENT_INDEX ? c.accent : c.action}
        />
      ))}
    </Svg>
  )
}
