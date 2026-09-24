"use client"

/*
The OAuth consent screen (MH-391): where claude.ai, ChatGPT or any MCP client
that signs in with OAuth is let into ONE workspace.

The client sends the member here (the authorization server metadata names
/oauth/authorize; the proxy adds the locale) with the authorization request in
the query. The page asks the API to validate it, then shows who is asking, the
workspace picker, and the scopes — capped by the member's role in the workspace
they pick — and sends Approve or Deny. The API re-validates everything on the
decision; the page only chooses. Either way the browser goes back to the
client's redirect URI, which the API has checked against the registration.

The client's name comes from its own registration, so it is shown as claimed,
with the host the member will be sent back to beside it.

Not behind the proxy's sign-in redirect: signed out, the page links to sign-in
with the whole address — the authorization request included — to come back to.
*/

import Link from "next/link"
import { useEffect, useMemo, useState, type FormEvent } from "react"
import useSWR from "swr"
import { CheckCircle2Icon, ExternalLinkIcon, PlugZapIcon, ShieldAlertIcon, ShieldCheckIcon, XCircleIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel, FieldLegend, FieldSet } from "@/components/ui/field"
import { Skeleton } from "@/components/ui/skeleton"
import { PickSelect } from "@/components/crm/shared"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import { Bdi, isolate } from "@/components/bidi"
import { api, ApiError, type ConsentRequest } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { scopeText } from "@/lib/scope-text"
import { useMe } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

type Problem = { title: string; body: string; back?: string }

export default function OAuthAuthorizePage() {
  const { t, locale } = useTranslations()
  useDocumentTitle(t("oauth.title"))
  const me = useMe()

  // The authorization request, read once on the client.
  const [search, setSearch] = useState<string | null>(null)
  const [here, setHere] = useState(`/${locale}/oauth/authorize`)
  useEffect(() => {
    setSearch(window.location.search.replace(/^\?/, ""))
    setHere(window.location.pathname + window.location.search)
  }, [])
  const params = useMemo(() => Object.fromEntries(new URLSearchParams(search ?? "")), [search])

  const req = useSWR(me.data && search !== null ? `/api/oauth/authorize/request?${search}` : null, fetchRequest, {
    revalidateOnFocus: false,
    shouldRetryOnError: false,
  })

  const [workspace, setWorkspace] = useState("")
  const [chosen, setChosen] = useState<string[]>([])
  const [busy, setBusy] = useState<"approve" | "deny" | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [leaving, setLeaving] = useState<"approved" | "denied" | null>(null)

  const r = req.data
  const ws = r?.workspaces.find((w) => w.id === workspace)
  const allowed = useMemo(() => ws?.allowed_scopes ?? [], [ws])

  useEffect(() => {
    if (r && !workspace) setWorkspace(r.workspaces[0]?.id ?? "")
  }, [r, workspace])
  // A new workspace: everything the role there allows, ticked.
  useEffect(() => setChosen(allowed), [allowed])

  async function decide(approve: boolean, e?: FormEvent) {
    e?.preventDefault()
    setBusy(approve ? "approve" : "deny")
    setError(null)
    try {
      const res = await api<{ redirect_to: string }>("/api/oauth/authorize/decision", {
        json: { ...params, approve, workspace, granted_scopes: chosen },
      })
      setLeaving(approve ? "approved" : "denied")
      window.location.assign(res.redirect_to)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.networkError"))
      setBusy(null)
    }
  }

  const problem = requestProblem(req.error, t)

  return (
    <PublicFrame
      eyebrow={t("oauth.micro")}
      title={r ? t("oauth.titleNamed", { client: isolate(r.client.name || t("oauth.unnamed")) }) : t("oauth.title")}
      description={leaving ? undefined : t("oauth.hint")}
    >
      <PublicPanel className="flex justify-center py-10">
        <div className="w-full max-w-lg" data-oauth-page data-oauth-state={leaving ?? (r ? "consent" : req.error ? "error" : "loading")}>
          {me.isLoading || (me.data && search === null) ? (
            <Loading />
          ) : !me.data ? (
            <div className="space-y-4" data-oauth-signin>
              <p className="text-sm text-grid-body">{t("oauth.signInFirst")}</p>
              <div className="flex flex-wrap gap-2">
                <Button nativeButton={false} render={<Link href={`/${locale}/login?next=${encodeURIComponent(here)}`} />}>
                  {t("auth.signIn")}
                </Button>
                <Button variant="outline" nativeButton={false} render={<Link href={`/${locale}/register?next=${encodeURIComponent(here)}`} />}>
                  {t("auth.createAccount")}
                </Button>
              </div>
            </div>
          ) : leaving ? (
            <p className="flex items-start gap-2 text-sm" data-oauth-leaving={leaving}>
              {leaving === "approved" ? <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-primary" /> : <XCircleIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" />}
              <span>{t(leaving === "approved" ? "oauth.returningApproved" : "oauth.returningDenied", { host: isolate(r?.redirect_host ?? "") })}</span>
            </p>
          ) : problem ? (
            <ProblemView problem={problem} />
          ) : !r ? (
            <Loading />
          ) : (
            <form onSubmit={(e) => decide(true, e)} noValidate className="space-y-6" data-oauth-form>
              <div className="space-y-3">
                <p className="flex items-center gap-2 text-sm font-medium">
                  <PlugZapIcon className="size-4 text-grid-muted" />
                  {t("oauth.wantsAccess", { client: isolate(r.client.name || t("oauth.unnamed")) })}
                </p>
                <dl className="divide-y divide-line border border-line text-sm" data-oauth-claims>
                  <Claim label={t("oauth.claimClient")} value={<Bdi>{r.client.name || t("oauth.unnamed")}</Bdi>} />
                  {r.client.uri ? <Claim label={t("oauth.claimSite")} value={<Ltr>{r.client.uri}</Ltr>} /> : null}
                  <Claim label={t("oauth.claimReturnsTo")} value={<Ltr>{r.redirect_host}</Ltr>} />
                  <Claim label={t("oauth.claimServer")} value={<Ltr>{r.resource}</Ltr>} />
                </dl>
                <p className="text-xs text-grid-muted">
                  {r.loopback_redirect ? t("oauth.loopbackHint") : t("oauth.claimsHint", { host: isolate(r.redirect_host) })}
                </p>
              </div>

              <p className="text-sm text-grid-body">
                {t("oauth.signedInAs")} <Ltr className="font-medium text-grid-fg">{me.data.email}</Ltr>
              </p>

              {r.workspaces.length === 0 ? (
                <ProblemView
                  problem={{ title: t("oauth.noWorkspaceTitle"), body: t("oauth.noWorkspaceBody") }}
                  action={
                    <Button variant="outline" nativeButton={false} render={<Link href={`/${locale}/workspaces`} />}>
                      {t("oauth.toWorkspaces")}
                    </Button>
                  }
                />
              ) : (
                <FieldGroup>
                  <Field>
                    <FieldLabel htmlFor="oauth-workspace">{t("oauth.workspace")}</FieldLabel>
                    <PickSelect
                      id="oauth-workspace"
                      value={workspace}
                      onChange={setWorkspace}
                      options={r.workspaces.map((w) => ({ value: w.id, label: `${isolate(w.name)} · ${t(`role.${w.role}`)}` }))}
                    />
                    <FieldDescription>{t("oauth.workspaceHint")}</FieldDescription>
                  </Field>

                  <FieldSet>
                    <FieldLegend variant="label">{t("oauth.scopes")}</FieldLegend>
                    <FieldDescription>{t("oauth.scopesHint")}</FieldDescription>
                    <ul className="max-h-[28rem] divide-y divide-line overflow-y-auto border border-line" data-oauth-scopes>
                      {r.requested_scopes.map((s) => {
                        const ok = allowed.includes(s.scope)
                        const id = `oauth-scope-${s.scope}`
                        return (
                          <li key={s.scope} className={ok ? "" : "bg-grid-soft/50"} data-scope={s.scope} data-allowed={ok}>
                            <label htmlFor={id} className={`flex items-start gap-3 px-3 py-2.5 ${ok ? "cursor-pointer" : "cursor-not-allowed"}`}>
                              <input
                                id={id}
                                type="checkbox"
                                className="mt-0.5 size-4 shrink-0 accent-(--grid-action)"
                                disabled={!ok}
                                checked={ok && chosen.includes(s.scope)}
                                onChange={(e) => setChosen((prev) => (e.target.checked ? [...prev, s.scope] : prev.filter((x) => x !== s.scope)))}
                              />
                              <span className="min-w-0">
                                <span className="block">
                                  <span dir="ltr" className="ltr-isolate font-mono text-xs text-grid-fg">
                                    {s.scope}
                                  </span>
                                </span>
                                <span className="block text-sm text-grid-body">{scopeText(t, s.scope, s.description)}</span>
                                {!ok ? <span className="mt-0.5 block text-xs text-grid-muted">{t("mcp.notAllowed", { role: t(`role.${s.min_role}`) })}</span> : null}
                              </span>
                            </label>
                          </li>
                        )
                      })}
                    </ul>
                  </FieldSet>
                  <p className="flex items-start gap-1.5 text-xs text-grid-muted">
                    <ShieldCheckIcon className="mt-0.5 size-3 shrink-0" />
                    <span>{t("oauth.boundHint", { hours: Math.round(r.access_ttl_seconds / 3600) })}</span>
                  </p>
                </FieldGroup>
              )}

              {error ? <FieldError data-oauth-error>{error}</FieldError> : null}

              <div className="flex flex-wrap gap-2">
                {r.workspaces.length > 0 ? (
                  <Button type="submit" disabled={busy !== null || !workspace || chosen.length === 0} data-approve>
                    {busy === "approve" ? t("common.working") : t("oauth.approve")}
                  </Button>
                ) : null}
                <Button type="button" variant="outline" disabled={busy !== null} onClick={() => decide(false)} data-deny>
                  {busy === "deny" ? t("common.working") : t("oauth.deny")}
                </Button>
              </div>
            </form>
          )}
        </div>
      </PublicPanel>
    </PublicFrame>
  )
}

/** An OAuth problem from the request endpoint, with where to send the member back when the API trusts it. */
class RequestProblem extends Error {
  constructor(
    public code: string,
    public description: string,
    public back?: string,
  ) {
    super(description || code)
  }
}

// Not api(): an OAuth error body is {error, error_description, redirect_to},
// and all three are needed here, not only the first.
async function fetchRequest(path: string): Promise<ConsentRequest> {
  const res = await fetch(path, { credentials: "same-origin", headers: { Accept: "application/json" } })
  const body = await res.json().catch(() => ({}))
  if (!res.ok) throw new RequestProblem(body.error ?? String(res.status), body.error_description ?? "", body.redirect_to)
  return body as ConsentRequest
}

function requestProblem(err: unknown, t: ReturnType<typeof useTranslations>["t"]): Problem | null {
  if (!err) return null
  if (err instanceof RequestProblem) {
    const known = `oauth.error.${err.code}`
    const text = t(known)
    return { title: t("oauth.badRequestTitle"), body: text === known ? err.description || err.code : `${text} (${err.description})`, back: err.back }
  }
  return { title: t("oauth.badRequestTitle"), body: t("common.networkError") }
}

function Loading() {
  return (
    <div className="space-y-3">
      <Skeleton className="h-6 w-2/3" />
      <Skeleton className="h-4 w-1/2" />
      <Skeleton className="h-24 w-full" />
    </div>
  )
}

function Claim({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 px-4 py-2.5">
      <dt className="shrink-0 text-grid-muted">{label}</dt>
      <dd className="min-w-0 truncate text-end">{value}</dd>
    </div>
  )
}

function ProblemView({ problem, action }: { problem: Problem; action?: React.ReactNode }) {
  const { t } = useTranslations()
  return (
    <div className="space-y-3" data-oauth-problem>
      <p className="flex items-center gap-2 text-sm font-medium">
        <ShieldAlertIcon className="size-4 text-grid-muted" />
        {problem.title}
      </p>
      <p className="text-sm text-grid-muted">
        <Bdi>{problem.body}</Bdi>
      </p>
      {problem.back ? (
        <Button variant="outline" nativeButton={false} render={<a href={problem.back} />}>
          <ExternalLinkIcon />
          {t("oauth.backToClient")}
        </Button>
      ) : null}
      {action}
      {!problem.back && !action ? <Badge variant="secondary">{t("oauth.closeTab")}</Badge> : null}
    </div>
  )
}
