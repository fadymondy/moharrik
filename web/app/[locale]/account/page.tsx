"use client"

/*
Account settings: how the person signs in (password, CircleXO, connected Apple / Google — connect
and disconnect, never the last way in) and deleting the account (internal/social/account.go).

Deleting needs a fresh proof: the password, or — without one — signing in again with a connected
provider (intent=reauth), which comes back here with ?reauth=<provider> and a session that is
fresh for ten minutes. Workspaces only this person is in go with the account; being the only
owner of a shared one blocks the deletion until ownership moves.
*/

import Link from "next/link"
import { Suspense, useEffect, useState, type FormEvent } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import useSWR, { useSWRConfig } from "swr"
import { CheckCircle2Icon, KeyRoundIcon, Trash2Icon } from "lucide-react"

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Badge,
  Button,
  Field,
  FieldLabel,
  Input,
  Skeleton,
  toast,
} from "@fadymondy/nasaq/web"
import { FieldError } from "@/components/form-layout"
import { PublicFrame } from "@/components/public-frame"
import { ErrorState } from "@/components/states"
import { Ltr } from "@/components/copy-field"
import { BdiBlock } from "@/components/bidi"
import { UserMenu } from "@/components/workspace/user-menu"
import {
  ProviderGlyph,
  SOCIAL_LABEL,
  socialHref,
  useSocialError,
  type SocialProvider,
} from "@/components/auth/social-buttons"
import { api, ApiError, resetCsrf } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useRequireAuth } from "@/lib/use-require-auth"
import { useDocumentTitle } from "@/lib/title"

type Identity = {
  provider: SocialProvider
  email: string
  display_name: string
  linked_at: string
  last_login_at: string
}

type Methods = {
  email: string
  has_password: boolean
  circlexo: boolean
  identities: Identity[] | null
  google_available: boolean
  apple_available: boolean
}

type DeletionWorkspace = { id: string; name: string; slug: string; members: number }

type Deletion = {
  email: string
  has_password: boolean
  fresh: boolean
  delete_workspaces: DeletionWorkspace[] | null
  blocking_workspaces: DeletionWorkspace[] | null
}

const METHODS_KEY = "/api/me/identities"

export default function AccountRoute() {
  return (
    <Suspense>
      <AccountPage />
    </Suspense>
  )
}

function AccountPage() {
  const { t, locale, formatDate } = useTranslations()
  useDocumentTitle(t("account.title"))
  const me = useRequireAuth()
  const params = useSearchParams()
  const router = useRouter()
  const methods = useSWR(me.data ? METHODS_KEY : null, (p: string) => api<Methods>(p), {
    revalidateOnFocus: false,
  })
  const socialError = useSocialError(params.get("social_error"))
  const linked = params.get("linked")
  const reauth = params.get("reauth")
  const here = `/${locale}/account`

  // A provider round trip comes back with ?linked= / ?reauth= / ?social_error=: say so once and
  // tidy the address.
  useEffect(() => {
    if (linked === "apple" || linked === "google") {
      toast.success(t("account.linked", { provider: SOCIAL_LABEL[linked] }))
      router.replace(here)
    }
  }, [linked, here, router, t])

  const [busyProvider, setBusyProvider] = useState<string | null>(null)
  async function disconnect(p: SocialProvider) {
    setBusyProvider(p)
    try {
      await api(`/api/me/identities/${p}`, { method: "DELETE" })
      toast.success(t("account.disconnected"))
      await methods.mutate()
    } catch (err) {
      toast.error(
        err instanceof ApiError && err.code === "last_method"
          ? t("account.lastMethod")
          : err instanceof Error
            ? err.message
            : t("common.networkError"),
      )
    } finally {
      setBusyProvider(null)
    }
  }

  if (!me.data || methods.isLoading) {
    return (
      <PublicFrame title={t("account.title")}>
        {me.error ? (
          <ErrorState error={me.error} />
        ) : (
          <div className="space-y-3">
            <Skeleton className="h-6 w-1/2" />
            <Skeleton className="h-40 w-full" />
          </div>
        )}
      </PublicFrame>
    )
  }

  const m = methods.data
  const identities = m?.identities ?? []
  const offered: SocialProvider[] = (["apple", "google"] as const).filter(
    (p) =>
      identities.some((i) => i.provider === p) ||
      (p === "apple" ? m?.apple_available : m?.google_available),
  )
  const ways = (m?.has_password ? 1 : 0) + (m?.circlexo ? 1 : 0) + identities.length

  return (
    <PublicFrame
      eyebrow={<Ltr>{me.data.email}</Ltr>}
      title={t("account.title")}
      description={t("account.hint")}
    >
      <section className="border border-nq-line" data-account-methods>
        <div className="flex items-center justify-between gap-4 border-b border-nq-line px-4 py-2">
          <span className="eyebrow">{t("account.methods")}</span>
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              nativeButton={false}
              render={<Link href={`/${locale}/workspaces`} />}
            >
              {t("workspaces.title")}
            </Button>
            <UserMenu email={me.data.email} />
          </div>
        </div>
        <p className="px-4 pt-3 text-sm text-muted-foreground">{t("account.methodsHint")}</p>
        {methods.error ? <ErrorState error={methods.error} /> : null}
        {socialError ? (
          <p role="alert" className="px-4 pt-3 text-sm text-nq-danger-text" data-social-error>
            {socialError}
          </p>
        ) : null}
        <ul className="divide-y divide-nq-line">
          <li className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
            <KeyRoundIcon className="size-4 text-muted-foreground" />
            <span className="min-w-0 flex-1 text-sm font-medium">{t("account.password")}</span>
            <span className="text-sm text-muted-foreground">
              {m?.has_password ? t("account.passwordSet") : t("account.passwordNotSet")}
            </span>
          </li>
          {m?.circlexo ? (
            <li className="flex items-center gap-4 px-4 py-3">
              <span className="min-w-0 flex-1 text-sm font-medium">{t("account.circlexo")}</span>
              <Badge variant="outline">{t("account.connected")}</Badge>
            </li>
          ) : null}
          {offered.map((p) => {
            const id = identities.find((i) => i.provider === p)
            return (
              <li
                key={p}
                className="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3"
                data-account-provider={p}
              >
                <ProviderGlyph provider={p} />
                <span className="min-w-0 flex-1">
                  <span className="block text-sm font-medium">{SOCIAL_LABEL[p]}</span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {id ? (
                      <>
                        {t("account.connectedAs")} <Ltr>{id.email || id.display_name}</Ltr> ·{" "}
                        {formatDate(id.linked_at)}
                      </>
                    ) : (
                      t("account.notConnected")
                    )}
                  </span>
                </span>
                {id ? (
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={busyProvider === p || ways <= 1}
                    title={ways <= 1 ? t("account.lastMethod") : undefined}
                    onClick={() => disconnect(p)}
                    data-account-disconnect={p}
                  >
                    {busyProvider === p ? t("common.working") : t("account.disconnect")}
                  </Button>
                ) : (
                  <Button
                    size="sm"
                    nativeButton={false}
                    render={<a href={socialHref(p, here, "link")} />}
                    data-account-connect={p}
                  >
                    {t("account.connect")}
                  </Button>
                )}
              </li>
            )
          })}
        </ul>
      </section>

      <DeleteAccount
        identities={identities}
        reauthed={reauth === "apple" || reauth === "google"}
        here={here}
      />
    </PublicFrame>
  )
}

function DeleteAccount({
  identities,
  reauthed,
  here,
}: {
  identities: Identity[]
  reauthed: boolean
  here: string
}) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const { mutate } = useSWRConfig()
  const [open, setOpen] = useState(reauthed)
  const [typed, setTyped] = useState("")
  const [password, setPassword] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const preview = useSWR(
    open ? "/api/me/account/deletion" : null,
    (p: string) => api<Deletion>(p),
    {
      revalidateOnFocus: false,
    },
  )
  const d = preview.data
  const blocking = d?.blocking_workspaces ?? []
  const deleting = d?.delete_workspaces ?? []
  const needsPassword = !!d && !d.fresh && d.has_password
  const needsReauth = !!d && !d.fresh && !d.has_password
  const canSubmit =
    !!d &&
    blocking.length === 0 &&
    typed.trim().toLowerCase() === d.email.toLowerCase() &&
    (!needsPassword || password.length > 0) &&
    !needsReauth

  async function remove(e: FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    setBusy(true)
    setErr(null)
    try {
      await api("/api/me/account/delete", {
        json: { confirm: typed.trim(), password: needsPassword ? password : undefined },
      })
      resetCsrf()
      await mutate(() => true, undefined, { revalidate: false })
      toast.success(t("account.deleted"))
      router.replace(`/${locale}/login`)
    } catch (error) {
      const code = error instanceof ApiError ? error.code : undefined
      setErr(
        code &&
          ["wrong_password", "reauth_required", "sole_owner", "confirm_mismatch"].includes(code)
          ? t(`account.deleteError.${code}`)
          : error instanceof Error
            ? error.message
            : t("common.networkError"),
      )
      if (code === "sole_owner" || code === "reauth_required") void preview.mutate()
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="border border-nq-danger-text/40 p-4" data-delete-account>
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div className="max-w-xl">
          <h2 className="text-base font-medium text-nq-danger-text">{t("account.danger")}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{t("account.dangerHint")}</p>
        </div>
        <Button
          variant="danger"
          onClick={() => {
            setTyped("")
            setPassword("")
            setErr(null)
            setOpen(true)
          }}
          data-delete-account-open
        >
          <Trash2Icon />
          {t("account.deleteButton")}
        </Button>
      </div>
      <AlertDialog open={open} onOpenChange={setOpen}>
        <AlertDialogContent>
          <form onSubmit={remove} noValidate className="grid gap-4">
            <AlertDialogHeader>
              <AlertDialogTitle>{t("account.deleteTitle")}</AlertDialogTitle>
              <AlertDialogDescription>{t("account.dangerHint")}</AlertDialogDescription>
            </AlertDialogHeader>
            {!d ? (
              preview.error ? (
                <ErrorState error={preview.error} />
              ) : (
                <Skeleton className="h-20 w-full" />
              )
            ) : (
              <>
                {blocking.length > 0 ? (
                  <div className="space-y-2 text-sm" data-delete-blocking>
                    <p className="text-nq-danger-text">{t("account.deleteBlocking")}</p>
                    <ul className="list-disc ps-5">
                      {blocking.map((w) => (
                        <li key={w.id}>
                          <Link
                            className="underline underline-offset-4"
                            href={`/${locale}/w/${w.slug}/settings/general`}
                          >
                            <BdiBlock className="inline">{w.name}</BdiBlock>
                          </Link>
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : null}
                {deleting.length > 0 ? (
                  <div className="space-y-2 text-sm" data-delete-workspaces>
                    <p>{t("account.deleteWorkspaces")}</p>
                    <ul className="list-disc ps-5">
                      {deleting.map((w) => (
                        <li key={w.id}>
                          <BdiBlock className="inline">{w.name}</BdiBlock>
                        </li>
                      ))}
                    </ul>
                  </div>
                ) : null}
                <p className="text-sm text-muted-foreground">{t("account.deleteLeave")}</p>
                {d.fresh ? (
                  <p className="flex items-center gap-2 text-sm">
                    <CheckCircle2Icon className="size-4 text-primary" />
                    {t("account.deleteFresh")}
                  </p>
                ) : null}
                {needsReauth ? (
                  <div className="space-y-2" data-delete-reauth>
                    <p className="text-sm">{t("account.deleteReauth")}</p>
                    <div className="flex flex-wrap gap-2">
                      {identities.map((i) => (
                        <Button
                          key={i.provider}
                          variant="secondary"
                          nativeButton={false}
                          render={<a href={socialHref(i.provider, here, "reauth")} />}
                        >
                          <ProviderGlyph provider={i.provider} />
                          {t("account.deleteReauthWith", { provider: SOCIAL_LABEL[i.provider] })}
                        </Button>
                      ))}
                    </div>
                  </div>
                ) : null}
                {needsPassword ? (
                  <Field>
                    <FieldLabel htmlFor="delete-password">{t("account.deletePassword")}</FieldLabel>
                    <Input
                      id="delete-password"
                      type="password"
                      dir="ltr"
                      autoComplete="current-password"
                      value={password}
                      onChange={(e) => setPassword(e.target.value)}
                    />
                  </Field>
                ) : null}
                <Field>
                  <FieldLabel htmlFor="delete-account-confirm">
                    {t("account.deleteConfirm")} <Ltr mono>{d.email}</Ltr>
                  </FieldLabel>
                  <Input
                    id="delete-account-confirm"
                    dir="ltr"
                    autoComplete="off"
                    value={typed}
                    onChange={(e) => setTyped(e.target.value)}
                    data-delete-account-input
                  />
                </Field>
              </>
            )}
            {err ? <FieldError role="alert">{err}</FieldError> : null}
            <AlertDialogFooter>
              <AlertDialogCancel type="button">{t("common.cancel")}</AlertDialogCancel>
              <Button
                type="submit"
                variant="danger"
                disabled={busy || !canSubmit}
                data-delete-account-confirm
              >
                {busy ? t("common.working") : t("account.deleteNow")}
              </Button>
            </AlertDialogFooter>
          </form>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}
