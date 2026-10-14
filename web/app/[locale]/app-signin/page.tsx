"use client"

/*
App sign-in (docs/apps-plan.md §2): where the Moharrik desktop and phone apps
send the member, in the SYSTEM browser, to sign in the same way they sign in
here — then hand that sign-in back to the app.

The app opens this page with its redirect (a 127.0.0.1 port, or
moharrik://auth/callback), a PKCE challenge, a state and its device name. The
member confirms; the API (POST /api/me/app-signin) checks the redirect is one of
the apps' and returns a one-time code bound to the challenge, and the browser
goes back to the app with it. The app alone holds the verifier that turns the
code into its device token.

Not behind the proxy's sign-in redirect: signed out, the page offers Apple /
Google and the e-mail sign-in, each coming back to this whole address.

?provider=apple|google (the phone app's "Sign in with Google" button, and
Apple on Android) goes straight to that provider when signed out; the person
still confirms the hand-off to the app here afterwards.
*/

import Link from "next/link"
import { useEffect, useMemo, useRef, useState } from "react"
import { CheckCircle2Icon, MonitorSmartphoneIcon, ShieldAlertIcon } from "lucide-react"

import { Button, Skeleton } from "@fadymondy/nasaq/web"
import { FieldError } from "@/components/form-layout"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import { Bdi } from "@/components/bidi"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMe } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { SocialButtons, socialHref, useSocialProviders } from "@/components/auth/social-buttons"

/** The same two shapes the API accepts (internal/appsignin validAppRedirect). */
function appRedirect(raw: string): boolean {
  try {
    const u = new URL(raw)
    if (u.search || u.hash || u.username || u.password) return false
    if (u.protocol === "http:")
      return (
        ["127.0.0.1", "localhost", "[::1]"].includes(u.hostname) &&
        u.port !== "" &&
        u.pathname === "/callback"
      )
    // A custom scheme parses with the host in u.host on modern URL parsers.
    return u.protocol === "moharrik:" && raw.startsWith("moharrik://auth/callback")
  } catch {
    return false
  }
}

export default function AppSigninPage() {
  const { t, locale } = useTranslations()
  useDocumentTitle(t("appSignin.title"))
  const me = useMe()

  const [search, setSearch] = useState<string | null>(null)
  const [here, setHere] = useState(`/${locale}/app-signin`)
  useEffect(() => {
    setSearch(window.location.search.replace(/^\?/, ""))
    setHere(window.location.pathname + window.location.search)
  }, [])
  const params = useMemo(() => new URLSearchParams(search ?? ""), [search])
  const redirectUri = params.get("redirect_uri") ?? ""
  const device = params.get("device") ?? ""
  const valid =
    appRedirect(redirectUri) &&
    (params.get("code_challenge") ?? "").length >= 43 &&
    (params.get("state") ?? "").length >= 16

  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [leaving, setLeaving] = useState(false)

  // Straight to the provider the app asked for, once, when nobody is signed in here.
  const { providers } = useSocialProviders()
  const wanted = params.get("provider")
  const jumped = useRef(false)
  const jumping =
    valid &&
    !me.isLoading &&
    me.data === null &&
    (wanted === "apple" || wanted === "google") &&
    providers.includes(wanted)
  useEffect(() => {
    if (jumping && !jumped.current && (wanted === "apple" || wanted === "google")) {
      jumped.current = true
      window.location.assign(socialHref(wanted, here))
    }
  }, [jumping, wanted, here])

  async function proceed() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ redirect_to: string }>("/api/me/app-signin", {
        json: {
          redirect_uri: redirectUri,
          code_challenge: params.get("code_challenge"),
          code_challenge_method: "S256",
          state: params.get("state"),
          device,
        },
      })
      setLeaving(true)
      window.location.assign(res.redirect_to)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.networkError"))
      setBusy(false)
    }
  }

  return (
    <PublicFrame
      eyebrow={t("appSignin.micro")}
      title={t("appSignin.title")}
      description={leaving ? undefined : t("appSignin.hint")}
    >
      <PublicPanel className="flex justify-center py-10">
        <div
          className="w-full max-w-lg"
          data-app-signin
          data-app-signin-state={leaving ? "leaving" : valid ? "confirm" : "invalid"}
        >
          {me.isLoading || search === null || jumping ? (
            <div className="space-y-3">
              <Skeleton className="h-6 w-2/3" />
              <Skeleton className="h-24 w-full" />
            </div>
          ) : !valid ? (
            <div className="space-y-3">
              <p className="flex items-center gap-2 text-sm font-medium">
                <ShieldAlertIcon className="size-4 text-muted-foreground" />
                {t("appSignin.invalidTitle")}
              </p>
              <p className="text-sm text-muted-foreground">{t("appSignin.invalidBody")}</p>
            </div>
          ) : !me.data ? (
            <div className="space-y-4">
              <p className="text-sm text-nq-fg-body">{t("appSignin.signInFirst")}</p>
              <SocialButtons providers={providers} next={here} />
              <Button
                variant={providers.length > 0 ? "ghost" : "primary"}
                size="lg"
                className="w-full"
                nativeButton={false}
                render={<Link href={`/${locale}/login?next=${encodeURIComponent(here)}`} />}
              >
                {t("appSignin.withEmail")}
              </Button>
            </div>
          ) : leaving ? (
            <p className="flex items-start gap-2 text-sm">
              <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-primary" />
              <span>{t("appSignin.returning")}</span>
            </p>
          ) : (
            <div className="space-y-6">
              <p className="flex items-center gap-2 text-sm font-medium">
                <MonitorSmartphoneIcon className="size-4 text-muted-foreground" />
                {t("appSignin.wants")}
              </p>
              <dl className="divide-y divide-nq-line border border-nq-line text-sm">
                <div className="flex items-center justify-between gap-4 px-4 py-2.5">
                  <dt className="shrink-0 text-muted-foreground">{t("appSignin.device")}</dt>
                  <dd className="min-w-0 truncate text-end">
                    <Bdi>{device || t("appSignin.unnamed")}</Bdi>
                  </dd>
                </div>
                <div className="flex items-center justify-between gap-4 px-4 py-2.5">
                  <dt className="shrink-0 text-muted-foreground">{t("appSignin.account")}</dt>
                  <dd className="min-w-0 truncate text-end">
                    <Ltr>{me.data.email}</Ltr>
                  </dd>
                </div>
              </dl>
              <p className="text-xs text-muted-foreground">{t("appSignin.warning")}</p>
              {error ? <FieldError>{error}</FieldError> : null}
              <div className="flex flex-wrap gap-2">
                <Button onClick={proceed} disabled={busy} data-app-signin-continue>
                  {busy ? t("common.working") : t("appSignin.continue")}
                </Button>
                <Button
                  variant="secondary"
                  nativeButton={false}
                  render={<Link href={`/${locale}/workspaces`} />}
                >
                  {t("appSignin.cancel")}
                </Button>
              </div>
            </div>
          )}
        </div>
      </PublicPanel>
    </PublicFrame>
  )
}
