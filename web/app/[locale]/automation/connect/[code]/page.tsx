"use client"

/*
Connect a browser by approval (MH-478).

The extension's "Connect to the site" button opens this page in a new tab:
/{locale}/automation/connect/{code}#k={key}. The signed-in member sees what is
asking, picks the workspace from their own memberships, names the browser,
lists where it may be driven, and approves or refuses. The extension collects
its token by itself — nothing is copied from one screen to another.

The key after # never reaches a server as part of the address (browsers do not
send fragments); this page reads it and sends it back only with the decision.
It is what stops someone who merely saw the code from approving this browser
into THEIR workspace. Without it the page shows no details and no Approve.

Everything about the browser — its name, platform, address — was said by an
unauthenticated caller, so it is labelled as claimed. The question the member
is answering is not "is this description true" but "did I just press Connect
in an extension".

Not behind the proxy's sign-in redirect on purpose: that redirect would drop
the #key. Signed out, the page links to sign-in with the whole address —
fragment included — as the place to come back to.
*/

import Link from "next/link"
import { useEffect, useMemo, useState, type FormEvent } from "react"
import { useParams } from "next/navigation"
import useSWR from "swr"
import { CheckCircle2Icon, MonitorSmartphoneIcon, ShieldAlertIcon, ShieldCheckIcon, XCircleIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { PickSelect } from "@/components/crm/shared"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import { Bdi, isolate } from "@/components/bidi"
import { api, ApiError, type BrowserAgent, type ConnectRequest, type ConnectWorkspace } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMe } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

type Done = { kind: "approved"; workspace: ConnectWorkspace; browser: BrowserAgent; replaced: boolean } | { kind: "denied" }

export default function ConnectBrowserPage() {
  const { code } = useParams<{ code: string }>()
  const { t, locale, formatDate } = useTranslations()
  useDocumentTitle(t("automation.connect.title"))
  const me = useMe()

  // The key lives in the fragment. Read once on the client; it is never part
  // of anything this page fetches except the headers and bodies below.
  const [key, setKey] = useState<string | null>(null)
  const [here, setHere] = useState(`/${locale}/automation/connect/${code}`)
  useEffect(() => {
    const k = new URLSearchParams(window.location.hash.replace(/^#/, "")).get("k") ?? ""
    setKey(k)
    setHere(window.location.pathname + window.location.hash)
  }, [])

  const req = useSWR(
    me.data && key !== null && code ? [`/api/agents/connect/${encodeURIComponent(code)}`, key] : null,
    ([p, k]: [string, string]) => api<ConnectRequest>(p, { headers: k ? { "X-Connect-Key": k } : {} }),
    { revalidateOnFocus: false, shouldRetryOnError: false },
  )

  const approvable = useMemo(() => (req.data?.workspaces ?? []).filter((w) => w.can_approve), [req.data])
  const [workspace, setWorkspace] = useState("")
  const [name, setName] = useState("")
  const [hosts, setHosts] = useState("")
  const [busy, setBusy] = useState<"approve" | "deny" | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [done, setDone] = useState<Done | null>(null)

  useEffect(() => {
    const data = req.data
    if (!data) return
    setName((n) => n || data.requested_name)
    setWorkspace((w) => w || approvable[0]?.id || "")
  }, [req.data, approvable])

  async function decide(approve: boolean, e?: FormEvent) {
    e?.preventDefault()
    setBusy(approve ? "approve" : "deny")
    setError(null)
    try {
      if (approve) {
        const res = await api<{ browser: BrowserAgent; workspace: ConnectWorkspace; replaced: boolean }>("/api/agents/connect/approve", {
          json: {
            code,
            key,
            workspace,
            name: name.trim(),
            allowed_hosts: hosts
              .split(/[\s,]+/)
              .map((h) => h.trim())
              .filter(Boolean),
          },
        })
        setDone({ kind: "approved", workspace: res.workspace, browser: res.browser, replaced: res.replaced })
      } else {
        await api("/api/agents/connect/deny", { json: { code, key } })
        setDone({ kind: "denied" })
      }
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(null)
    }
  }

  const r = req.data

  return (
    <PublicFrame
      eyebrow={t("automation.connect.micro")}
      title={done?.kind === "approved" ? t("automation.connect.doneTitle") : done?.kind === "denied" ? t("automation.connect.refusedTitle") : t("automation.connect.title")}
      description={done ? undefined : t("automation.connect.hint")}
    >
      <PublicPanel className="flex justify-center py-10">
        <div className="w-full max-w-md" data-connect-page data-connect-state={done?.kind ?? r?.status ?? "loading"}>
          {me.isLoading || (me.data && key === null) ? (
            <Loading />
          ) : !me.data ? (
            // Signed out: sign in, then come straight back — the whole address,
            // key included, is where the sign-in form returns to.
            <div className="space-y-4" data-connect-signin>
              <p className="text-sm text-grid-body">{t("automation.connect.signInFirst")}</p>
              <div className="flex flex-wrap gap-2">
                <Button nativeButton={false} render={<Link href={`/${locale}/login?next=${encodeURIComponent(here)}`} />}>
                  {t("auth.signIn")}
                </Button>
                <Button variant="outline" nativeButton={false} render={<Link href={`/${locale}/register?next=${encodeURIComponent(here)}`} />}>
                  {t("auth.createAccount")}
                </Button>
              </div>
            </div>
          ) : done?.kind === "approved" ? (
            <div className="space-y-4" data-connect-done="approved">
              <p className="flex items-start gap-2 text-sm">
                <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-primary" />
                <span>
                  {t(done.replaced ? "automation.connect.doneReplaced" : "automation.connect.doneBody", { name: isolate(done.browser.name), workspace: isolate(done.workspace.name) })}
                </span>
              </p>
              <p className="text-sm text-grid-muted">{t("automation.connect.closeTab")}</p>
              {done.browser.allowed_hosts.length === 0 ? <p className="text-sm text-grid-warn-text">{t("automation.connect.noSitesYet")}</p> : null}
              <Button nativeButton={false} render={<Link href={`/${locale}/w/${done.workspace.slug}/automation`} />} data-open-automation>
                {t("automation.connect.openAutomation")}
              </Button>
            </div>
          ) : done?.kind === "denied" ? (
            <div className="space-y-4" data-connect-done="denied">
              <p className="flex items-start gap-2 text-sm">
                <XCircleIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" />
                {t("automation.connect.refusedBody")}
              </p>
            </div>
          ) : req.error ? (
            <Problem
              title={t("automation.connect.notFoundTitle")}
              body={req.error instanceof ApiError && req.error.status === 404 ? t("automation.connect.notFoundBody") : req.error.message}
            />
          ) : !r ? (
            <Loading />
          ) : r.status === "expired" ? (
            <Problem title={t("automation.connect.expiredTitle")} body={t("automation.connect.expiredBody")} />
          ) : r.status !== "pending" ? (
            <Problem
              title={t("automation.connect.decidedTitle")}
              body={r.workspace ? t("automation.connect.decidedBody", { workspace: isolate(r.workspace.name) }) : t(`automation.connect.status.${r.status}`)}
              action={
                r.workspace ? (
                  <Button variant="outline" nativeButton={false} render={<Link href={`/${locale}/w/${r.workspace.slug}/automation`} />}>
                    {t("automation.connect.openAutomation")}
                  </Button>
                ) : undefined
              }
            />
          ) : !r.key_ok ? (
            // The code alone: show that it exists and nothing to act on.
            <Problem title={t("automation.connect.noKeyTitle")} body={t("automation.connect.noKeyBody")} />
          ) : (
            <form onSubmit={(e) => decide(true, e)} noValidate className="space-y-6" data-connect-form>
              <div className="flex flex-wrap items-center justify-between gap-3">
                <p className="flex items-center gap-2 text-sm font-medium">
                  <MonitorSmartphoneIcon className="size-4 text-grid-muted" />
                  {t("automation.connect.checkCode")}
                </p>
                {/* Latin, read character by character: LTR in an Arabic page too. */}
                <Badge variant="secondary" className="font-mono text-sm tracking-widest" dir="ltr" data-connect-code>
                  {r.code}
                </Badge>
              </div>

              <div>
                <dl className="divide-y divide-line border border-line text-sm" data-connect-claims>
                  <Claim label={t("automation.connect.claimName")} value={r.requested_name} />
                  <Claim label={t("automation.connect.claimPlatform")} value={r.platform} ltr />
                  <Claim label={t("automation.connect.claimBrowser")} value={r.browser_version} ltr />
                  <Claim label={t("automation.connect.claimExtension")} value={r.extension_version} ltr />
                  <Claim label={t("automation.connect.claimIp")} value={r.ip} ltr />
                </dl>
                <p className="mt-2 text-xs text-grid-muted">{t("automation.connect.claimsHint", { date: formatDate(r.expires_at) })}</p>
              </div>

              <p className="text-sm text-grid-body">
                {t("automation.connect.signedInAs")} <Ltr className="font-medium text-grid-fg">{me.data.email}</Ltr>
              </p>

              {approvable.length === 0 ? (
                <Problem
                  title={t("automation.connect.noWorkspaceTitle")}
                  body={t(r.workspaces.length > 0 ? "automation.connect.notAdminBody" : "automation.connect.noWorkspaceBody")}
                  action={
                    <Button variant="outline" nativeButton={false} render={<Link href={`/${locale}/workspaces`} />}>
                      {t("automation.connect.toWorkspaces")}
                    </Button>
                  }
                />
              ) : (
                <FieldGroup>
                  <Field>
                    <FieldLabel htmlFor="connect-workspace">{t("automation.connect.workspace")}</FieldLabel>
                    <PickSelect id="connect-workspace" value={workspace} onChange={setWorkspace} options={approvable.map((w) => ({ value: w.id, label: w.name }))} />
                    <FieldDescription>
                      {r.workspaces.length > approvable.length ? t("automation.connect.workspaceHintSome") : t("automation.connect.workspaceHint")}
                    </FieldDescription>
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="connect-name">{t("automation.connect.name")}</FieldLabel>
                    <Input id="connect-name" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} placeholder={t("automation.connect.namePlaceholder")} />
                  </Field>
                  <Field>
                    <FieldLabel htmlFor="connect-hosts">{t("automation.allowedHosts")}</FieldLabel>
                    <Input id="connect-hosts" dir="ltr" value={hosts} onChange={(e) => setHosts(e.target.value)} placeholder="example.com, mail.google.com" />
                    <FieldDescription className="flex items-start gap-1">
                      <ShieldCheckIcon className="mt-0.5 size-3 shrink-0" />
                      <span>{t("automation.connect.hostsHint")}</span>
                    </FieldDescription>
                  </Field>
                </FieldGroup>
              )}

              {error ? <FieldError data-connect-error>{error}</FieldError> : null}

              <div className="flex flex-wrap gap-2">
                {approvable.length > 0 ? (
                  <Button type="submit" disabled={busy !== null || !workspace || !name.trim()} data-approve>
                    {busy === "approve" ? t("common.working") : t("automation.connect.approve")}
                  </Button>
                ) : null}
                <Button type="button" variant="outline" disabled={busy !== null} onClick={() => decide(false)} data-deny>
                  {busy === "deny" ? t("common.working") : t("automation.connect.deny")}
                </Button>
              </div>
            </form>
          )}
        </div>
      </PublicPanel>
    </PublicFrame>
  )
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

function Claim({ label, value, ltr }: { label: string; value: string; ltr?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-4 px-4 py-2.5">
      <dt className="shrink-0 text-grid-muted">{label}</dt>
      <dd className="min-w-0 truncate text-end">{value ? ltr ? <Ltr>{value}</Ltr> : <Bdi>{value}</Bdi> : "—"}</dd>
    </div>
  )
}

function Problem({ title, body, action }: { title: string; body: string; action?: React.ReactNode }) {
  return (
    <div className="space-y-3" data-connect-problem>
      <p className="flex items-center gap-2 text-sm font-medium">
        <ShieldAlertIcon className="size-4 text-grid-muted" />
        {title}
      </p>
      <p className="text-sm text-grid-muted">{body}</p>
      {action}
    </div>
  )
}
