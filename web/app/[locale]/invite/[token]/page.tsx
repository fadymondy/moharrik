"use client"

import Link from "next/link"
import { useState } from "react"
import { useParams, useRouter } from "next/navigation"
import useSWR, { useSWRConfig } from "swr"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
import { ErrorState } from "@/components/states"
import { Ltr } from "@/components/copy-field"
import { api, ApiError, type InvitationPreview, type WorkspaceSummary } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMe } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function InvitePage() {
  const { token } = useParams<{ token: string }>()
  const { t, locale } = useTranslations()
  const router = useRouter()
  const { mutate } = useSWRConfig()
  useDocumentTitle(t("invite.title"))

  const me = useMe()
  const preview = useSWR(
    token ? `/api/invitations/${encodeURIComponent(token)}` : null,
    (p: string) => api<InvitationPreview>(p),
    { revalidateOnFocus: false, shouldRetryOnError: false },
  )

  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const here = `/${locale}/invite/${token}`

  async function accept() {
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ workspace: WorkspaceSummary }>("/api/invitations/accept", {
        json: { token },
      })
      await mutate("/api/me/workspaces")
      router.push(`/${locale}/w/${res.workspace.slug}`)
    } catch (err) {
      if (err instanceof ApiError && err.status === 403)
        setError(err.message || t("invite.mismatch"))
      else if (err instanceof ApiError && err.status === 404) setError(t("invite.invalid"))
      else setError(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  if (preview.error instanceof ApiError && preview.error.status === 404) {
    return (
      <PublicFrame
        eyebrow="404"
        title={t("invite.notFoundTitle")}
        description={t("invite.notFoundBody")}
      >
        <PublicPanel className="flex justify-center py-10">
          <Button variant="outline" nativeButton={false} render={<Link href={`/${locale}`} />}>
            {t("invite.goHome")}
          </Button>
        </PublicPanel>
      </PublicFrame>
    )
  }

  const inv = preview.data
  const signedInEmail = me.data?.email
  const emailMismatch =
    !!inv && !!signedInEmail && signedInEmail.toLowerCase() !== inv.email.toLowerCase()

  return (
    <PublicFrame
      eyebrow={t("invite.micro")}
      title={t("invite.title")}
      description={inv ? inv.workspace_name : undefined}
    >
      <PublicPanel className="flex justify-center py-10">
        <div className="w-full max-w-sm">
          {preview.error ? (
            <ErrorState error={preview.error} />
          ) : !inv ? (
            <div className="space-y-3">
              <Skeleton className="h-6 w-2/3" />
              <Skeleton className="h-4 w-1/2" />
            </div>
          ) : (
            <>
              <dl className="divide-y divide-nq-line border border-nq-line text-sm">
                <div className="flex items-center justify-between gap-4 px-4 py-3">
                  <dt className="text-muted-foreground">{t("invite.workspace")}</dt>
                  <dd className="font-medium">{inv.workspace_name}</dd>
                </div>
                <div className="flex items-center justify-between gap-4 px-4 py-3">
                  <dt className="text-muted-foreground">{t("invite.invitedEmail")}</dt>
                  <dd>
                    <Ltr>{inv.email}</Ltr>
                  </dd>
                </div>
                <div className="flex items-center justify-between gap-4 px-4 py-3">
                  <dt className="text-muted-foreground">{t("invite.role")}</dt>
                  <dd>
                    <Badge variant="outline">{t(`role.${inv.role}`)}</Badge>
                  </dd>
                </div>
              </dl>

              <div className="space-y-4 pt-6">
                {inv.accepted ? (
                  <p className="text-sm">{t("invite.alreadyAccepted")}</p>
                ) : inv.expired ? (
                  <p className="text-sm text-nq-danger-text">{t("invite.expired")}</p>
                ) : me.isLoading ? (
                  <Skeleton className="h-9 w-40" />
                ) : !me.data ? (
                  <>
                    <p className="text-sm text-nq-fg-body">{t("invite.signInFirst")}</p>
                    <div className="flex flex-wrap gap-2">
                      <Button
                        nativeButton={false}
                        render={<Link href={`/${locale}/login?next=${encodeURIComponent(here)}`} />}
                      >
                        {t("auth.signIn")}
                      </Button>
                      <Button
                        variant="outline"
                        nativeButton={false}
                        render={
                          <Link href={`/${locale}/register?next=${encodeURIComponent(here)}`} />
                        }
                      >
                        {t("auth.createAccount")}
                      </Button>
                    </div>
                  </>
                ) : (
                  <>
                    <p className="text-sm text-nq-fg-body">
                      {t("invite.signedInAs")}{" "}
                      <Ltr className="font-medium text-foreground">{signedInEmail}</Ltr>
                    </p>
                    {emailMismatch ? (
                      <p className="text-sm text-nq-warning-text">{t("invite.mismatchHint")}</p>
                    ) : null}
                    <Button onClick={accept} disabled={busy}>
                      {busy ? t("common.working") : t("invite.accept")}
                    </Button>
                  </>
                )}
                {inv.accepted ? (
                  <Button
                    variant="outline"
                    nativeButton={false}
                    render={<Link href={`/${locale}/workspaces`} />}
                  >
                    {t("invite.toWorkspaces")}
                  </Button>
                ) : null}
                {error ? (
                  <p role="alert" className="text-sm text-nq-danger-text">
                    {error}
                  </p>
                ) : null}
              </div>
            </>
          )}
        </div>
      </PublicPanel>
    </PublicFrame>
  )
}
