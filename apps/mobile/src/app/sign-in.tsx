// Sign-in: Sign in with Apple first (native on iOS, the browser flow on Android), then Google
// (the browser flow, straight to Google), then email and password in the app (sign in, create an
// account, forgot password). Apple's guideline 4.8: Sign in with Apple is offered, first and the
// same size as Google. Only what the server has configured shows (GET /api/auth/methods); the
// browser flow stays one tap away for anything else the web offers (CircleXO).
import { useQuery } from "@tanstack/react-query"
import * as AppleAuthentication from "expo-apple-authentication"
import { useEffect, useState } from "react"
import { KeyboardAvoidingView, Platform, Pressable, ScrollView, View } from "react-native"
import Svg, { Path } from "react-native-svg"

import { ApiError, errorCode, signInOptions, type Key, type SocialProvider } from "@moharrik/client"

import { Button, Input, Mark, Screen, Text, useColors } from "@/components/ui"
import { usePrefs } from "@/lib/prefs"
import { defaultServer, useSession } from "@/lib/session"

const BUTTON_HEIGHT = 48

// simple-icons glyphs (CC0), 24×24.
const GLYPH: Record<SocialProvider, string> = {
  apple:
    "M12.152 6.896c-.948 0-2.415-1.078-3.96-1.04-2.04.027-3.91 1.183-4.961 3.014-2.117 3.675-.546 9.103 1.519 12.09 1.013 1.454 2.208 3.09 3.792 3.039 1.52-.065 2.09-.987 3.935-.987 1.831 0 2.35.987 3.96.948 1.637-.026 2.676-1.48 3.676-2.948 1.156-1.688 1.636-3.325 1.662-3.415-.039-.013-3.182-1.221-3.22-4.857-.026-3.04 2.48-4.494 2.597-4.559-1.429-2.09-3.623-2.324-4.39-2.376-2-.156-3.675 1.09-4.61 1.09zM15.53 3.83c.843-1.012 1.4-2.427 1.245-3.83-1.207.052-2.662.805-3.532 1.818-.78.896-1.454 2.338-1.273 3.714 1.338.104 2.715-.688 3.559-1.701",
  google:
    "M12.48 10.92v3.28h7.84c-.24 1.84-.853 3.187-1.787 4.133-1.147 1.147-2.933 2.4-6.053 2.4-4.827 0-8.6-3.893-8.6-8.72s3.773-8.72 8.6-8.72c2.6 0 4.507 1.027 5.907 2.347l2.307-2.307C18.747 1.44 16.133 0 12.48 0 5.867 0 .307 5.387.307 12s5.56 12 12.173 12c3.573 0 6.267-1.173 8.373-3.36 2.16-2.16 2.84-5.213 2.84-7.667 0-.76-.053-1.467-.173-2.053H12.48z",
}

function Glyph({ provider, color }: { provider: SocialProvider; color: string }) {
  return (
    <Svg width={18} height={18} viewBox="0 0 24 24">
      <Path d={GLYPH[provider]} fill={color} />
    </Svg>
  )
}

const KNOWN_ERRORS = [
  "account_conflict",
  "identity_taken",
  "email_unverified",
  "no_email",
  "suspended",
  "not_configured",
  "invalid_credentials",
  "email_taken",
  "invalid_email",
  "password_too_short",
  "password_too_long",
  "rate_limited",
]

/** The refusal code: the {code} the social endpoints send, or the bare detail of the email ones. */
function refusal(err: unknown): string | undefined {
  if (err instanceof ApiError && err.status === 429) return "rate_limited"
  return errorCode(err) ?? (err instanceof ApiError ? err.message : undefined)
}

export default function SignIn() {
  const { t, locale, setLocale, scheme } = usePrefs()
  const c = useColors()
  const { signIn, signInWithApple, signInWithEmail, forgotPassword, lost, server, setServer, client } = useSession()
  const [busy, setBusy] = useState<null | "apple" | "google" | "email" | "browser" | "forgot">(null)
  const [failed, setFailed] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [mode, setMode] = useState<"in" | "up">("in")
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [editServer, setEditServer] = useState(false)
  const [url, setUrl] = useState(server)

  const options = useQuery({ queryKey: ["signin-options", server], queryFn: () => signInOptions(client), retry: 1, staleTime: 5 * 60_000 })
  const [appleNativeHere, setAppleNativeHere] = useState(false)
  useEffect(() => {
    if (Platform.OS === "ios") void AppleAuthentication.isAvailableAsync().then(setAppleNativeHere).catch(() => setAppleNativeHere(false))
  }, [])

  const o = options.data
  const appleNative = Platform.OS === "ios" && appleNativeHere && !!o?.appleNative
  // Android (and an iOS build talking to a server without native Apple): Apple in the browser.
  const appleBrowser = !appleNative && !!o?.appleWeb
  const google = !!o?.googleWeb

  async function go(kind: "apple" | "google" | "email" | "browser" | "forgot") {
    if (kind === "forgot" && !email.trim()) {
      setNotice(null)
      setFailed(t("signin.forgotNeedsEmail"))
      return
    }
    setBusy(kind)
    setFailed(null)
    setNotice(null)
    try {
      if (editServer && url.trim() !== server) await setServer(url)
      if (kind === "apple" && appleNative) await signInWithApple()
      else if (kind === "email") await signInWithEmail(mode, email, password)
      else if (kind === "forgot") {
        await forgotPassword(email)
        setNotice(t("signin.forgotSent"))
      } else await signIn(kind === "browser" ? undefined : kind)
    } catch (err) {
      const code = refusal(err)
      setFailed(code && KNOWN_ERRORS.includes(code) ? t(`signin.error.${code}` as Key) : t("signin.failed"))
    } finally {
      setBusy(null)
    }
  }

  const fullWidth = { minHeight: BUTTON_HEIGHT, alignSelf: "stretch" as const }

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
            <Text tone="danger" style={{ textAlign: "center" }} accessibilityLiveRegion="polite">
              {failed}
            </Text>
          ) : null}
          {notice ? (
            <Text tone="body" style={{ textAlign: "center" }} accessibilityLiveRegion="polite">
              {notice}
            </Text>
          ) : null}

          <View style={{ gap: 12 }}>
            {appleNative ? (
              <AppleAuthentication.AppleAuthenticationButton
                buttonType={AppleAuthentication.AppleAuthenticationButtonType.SIGN_IN}
                buttonStyle={scheme === "dark" ? AppleAuthentication.AppleAuthenticationButtonStyle.WHITE : AppleAuthentication.AppleAuthenticationButtonStyle.BLACK}
                cornerRadius={10}
                style={{ height: BUTTON_HEIGHT, width: "100%", opacity: busy ? 0.5 : 1 }}
                onPress={() => {
                  if (!busy) void go("apple")
                }}
              />
            ) : null}
            {appleBrowser ? (
              <Button
                title={t("signin.apple")}
                kind="secondary"
                busy={busy === "apple"}
                disabled={!!busy}
                icon={<Glyph provider="apple" color={c.fg} />}
                style={fullWidth}
                onPress={() => void go("apple")}
              />
            ) : null}
            {google ? (
              <Button
                title={t("signin.google")}
                kind="secondary"
                busy={busy === "google"}
                disabled={!!busy}
                icon={<Glyph provider="google" color={c.fg} />}
                style={fullWidth}
                onPress={() => void go("google")}
              />
            ) : null}
          </View>

          {appleNative || appleBrowser || google ? (
            <Text tone="muted" size="sm" style={{ textAlign: "center" }}>
              {t("signin.or")}
            </Text>
          ) : null}

          <View style={{ gap: 10 }}>
            <Input
              value={email}
              onChangeText={setEmail}
              placeholder={t("signin.emailField")}
              accessibilityLabel={t("signin.emailField")}
              autoCapitalize="none"
              autoCorrect={false}
              autoComplete="email"
              textContentType="emailAddress"
              keyboardType="email-address"
              returnKeyType="next"
            />
            <Input
              value={password}
              onChangeText={setPassword}
              placeholder={t("signin.password")}
              accessibilityLabel={t("signin.password")}
              secureTextEntry
              autoCapitalize="none"
              autoCorrect={false}
              autoComplete={mode === "up" ? "new-password" : "current-password"}
              textContentType={mode === "up" ? "newPassword" : "password"}
              returnKeyType="go"
              onSubmitEditing={() => {
                if (!busy && email.trim() && password) void go("email")
              }}
            />
            <Button
              title={mode === "up" ? t("signin.create") : t("signin.submit")}
              busy={busy === "email"}
              disabled={!!busy || !email.trim() || !password}
              style={fullWidth}
              onPress={() => void go("email")}
            />
            <View style={{ flexDirection: "row", flexWrap: "wrap", justifyContent: "space-between", gap: 12 }}>
              <Pressable onPress={() => setMode((m) => (m === "in" ? "up" : "in"))} hitSlop={8} disabled={!!busy}>
                <Text tone="action" size="sm">
                  {mode === "in" ? t("signin.toSignUp") : t("signin.toSignIn")}
                </Text>
              </Pressable>
              {mode === "in" && o?.passwordReset ? (
                <Pressable onPress={() => void go("forgot")} hitSlop={8} disabled={!!busy}>
                  <Text tone="action" size="sm">
                    {t("signin.forgot")}
                  </Text>
                </Pressable>
              ) : null}
            </View>
          </View>

          <Button
            title={busy === "browser" ? t("signin.waiting") : t("signin.other")}
            kind="ghost"
            busy={busy === "browser"}
            disabled={!!busy}
            style={fullWidth}
            onPress={() => void go("browser")}
          />

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
