"use client"

import { useState, type FormEvent } from "react"
import { PlusIcon, TriangleAlertIcon } from "lucide-react"
import { Badge, Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Field, FieldDescription, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, toast } from "@fadymondy/nasaq/web"
import { FieldGroup, FieldLegend, FieldSet, FieldError, FormHint } from "@/components/form-layout"

import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField, Ltr } from "@/components/copy-field"
import Link from "next/link"
import { ConnectClients } from "@/components/mcp/connect-clients"
import { ConnectedApps } from "@/components/mcp/connected-apps"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type McpScope, type McpToken } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { scopeText } from "@/lib/scope-text"
import { useMcpTokens, useOAuthGrants } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"
import { mcpDocsHref } from "@/lib/mcp-docs"

const MCP_ORIGIN = process.env.NEXT_PUBLIC_MCP_ORIGIN ?? "http://localhost:8120"

export default function McpTokensPage() {
  const { t, formatDate, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.mcpTokens")} · ${ws.summary.name}`)
  const tokens = useMcpTokens(ws.id)
  const grants = useOAuthGrants(ws.id)
  // The one URL every client is shown, pasted-token or OAuth alike: the
  // dedicated mcp.<domain> host once MCP_PUBLIC_URL names one, else the
  // legacy /api/mcp resource — server.MCPServerURL() (internal/mcp/oauth.go)
  // is the shared source, oauth_grants.go's server_url field carries it here
  // (MH-516). Falls back to the old NEXT_PUBLIC_MCP_ORIGIN-built shape only
  // while the grants call hasn't answered yet.
  const serverUrl = grants.data?.server_url ?? `${MCP_ORIGIN}/api/mcp`
  // Owners and admins can open each token's calls in the audit log (MH-392).
  const auditHref = ws.can("audit:read")
    ? (actor: string) =>
        `/${locale}/w/${ws.summary.slug}/settings/audit?actor=${encodeURIComponent(actor)}`
    : undefined
  const [creating, setCreating] = useState(false)

  async function revoke(id: string) {
    try {
      await api(`/api/w/mcp_tokens/${encodeURIComponent(id)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      toast.success(t("mcp.revoked"))
      await tokens.mutate()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    }
  }

  const list = tokens.data?.tokens ?? []
  // Owners and admins see every member's tokens (MH-396), so the table says whose each one is.
  const all = !!tokens.data?.all

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.mcpTokens")}
        description={all ? t("mcp.hintAll") : t("mcp.hint")}
        action={
          <Button onClick={() => setCreating(true)} disabled={!tokens.data}>
            <PlusIcon />
            {t("mcp.create")}
          </Button>
        }
      />

      {tokens.error ? (
        <ErrorState error={tokens.error} />
      ) : tokens.isLoading ? (
        <LoadingRows />
      ) : list.length === 0 ? (
        <EmptyState title={t("mcp.empty")} body={t("mcp.emptyBody")} />
      ) : (
        <div className="border-y border-nq-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("mcp.name")}</TableHead>
                {all ? <TableHead>{t("mcp.holder")}</TableHead> : null}
                <TableHead>{t("mcp.prefix")}</TableHead>
                <TableHead>{t("mcp.scopes")}</TableHead>
                <TableHead>{t("mcp.created")}</TableHead>
                <TableHead>{t("mcp.lastUsed")}</TableHead>
                <TableHead>{t("mcp.expires")}</TableHead>
                <TableHead className="pe-6 text-end">
                  <span className="sr-only">{t("common.actions")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((tok) => (
                <TokenRow
                  key={tok.id}
                  tok={tok}
                  showHolder={all}
                  formatDate={formatDate}
                  onRevoke={() => revoke(tok.id)}
                  activityHref={auditHref?.(tok.id)}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <ConnectedApps grants={grants} />

      <section className="border-t border-nq-line px-6 py-8" aria-labelledby="mcp-connect">
        <div className="mb-4 max-w-2xl space-y-1.5">
          <h2 id="mcp-connect" className="font-medium tracking-tight text-foreground text-lg">
            {t("mcp.connectTitle")}
          </h2>
          <p className="leading-relaxed text-nq-fg-body text-sm">{t("mcp.connectHint")}</p>
          <p className="text-sm">
            <Link
              href={mcpDocsHref(locale)}
              className="text-nq-action underline-offset-4 hover:underline"
              data-mcp-docs-link
            >
              {t("mcp.docsLink")}
            </Link>
          </p>
        </div>
        <div className="max-w-3xl">
          <ConnectClients url={serverUrl} oauthUrl={serverUrl} />
        </div>
      </section>

      {tokens.data ? (
        <CreateTokenDialog
          open={creating}
          onOpenChange={setCreating}
          scopes={tokens.data.scopes ?? []}
          serverUrl={serverUrl}
          onCreated={() => tokens.mutate()}
        />
      ) : null}
    </>
  )
}

function TokenRow({
  tok,
  showHolder,
  formatDate,
  onRevoke,
  activityHref,
}: {
  tok: McpToken
  showHolder: boolean
  formatDate: (v: string | null | undefined) => string
  onRevoke: () => Promise<void>
  /** Its calls in the audit log, for owners and admins (MH-392). */
  activityHref?: string
}) {
  const { t } = useTranslations()
  const expired = !!tok.expires_at && new Date(tok.expires_at).getTime() < Date.now()
  return (
    <TableRow className={tok.revoked_at || expired ? "opacity-60" : undefined}>
      <TableCell className="ps-6 font-medium">
        <Bdi>{tok.name}</Bdi>
      </TableCell>
      {showHolder ? (
        <TableCell>
          <span className="flex flex-wrap items-center gap-1.5">
            <Ltr className="text-sm">{tok.user_email || tok.user_id}</Ltr>
            {tok.mine ? <Badge variant="neutral">{t("mcp.you")}</Badge> : null}
          </span>
        </TableCell>
      ) : null}
      <TableCell>
        <Ltr mono className="text-xs">
          {tok.prefix}…
        </Ltr>
      </TableCell>
      <TableCell>
        <div className="flex max-w-xs flex-wrap gap-1">
          {(tok.scopes ?? []).map((s) => (
            <Badge key={s} variant="outline" className="font-mono" dir="ltr">
              {s}
            </Badge>
          ))}
        </div>
      </TableCell>
      <TableCell className="text-muted-foreground">
        <Ltr>{formatDate(tok.created_at)}</Ltr>
      </TableCell>
      <TableCell className="text-muted-foreground">
        {tok.last_used_at ? <Ltr>{formatDate(tok.last_used_at)}</Ltr> : t("mcp.never")}
        {activityHref && tok.last_used_at ? (
          <Link
            href={activityHref}
            className="block text-xs text-nq-action underline-offset-4 hover:underline"
            data-token-activity
          >
            {t("mcp.recentCalls")}
          </Link>
        ) : null}
      </TableCell>
      <TableCell className="text-muted-foreground">
        {tok.expires_at ? <Ltr>{formatDate(tok.expires_at)}</Ltr> : t("mcp.noExpiry")}
      </TableCell>
      <TableCell className="pe-6 text-end">
        {tok.revoked_at ? (
          <Badge variant="neutral">{t("mcp.revokedBadge")}</Badge>
        ) : expired ? (
          <Badge variant="neutral">{t("mcp.expiredBadge")}</Badge>
        ) : (
          <ConfirmButton
            label={t("common.revoke")}
            title={t("mcp.revokeTitle")}
            description={
              tok.mine
                ? t("mcp.revokeBody", { name: isolate(tok.name) })
                : t("mcp.revokeOtherBody", {
                    name: isolate(tok.name),
                    who: isolate(tok.user_email),
                  })
            }
            confirmLabel={t("common.revoke")}
            onConfirm={onRevoke}
          />
        )}
      </TableCell>
    </TableRow>
  )
}

const EXPIRY = ["30", "90", "365", "never"] as const

function CreateTokenDialog({
  open,
  onOpenChange,
  scopes,
  serverUrl,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  scopes: McpScope[]
  serverUrl: string
  onCreated: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [chosen, setChosen] = useState<string[]>([])
  const [expiry, setExpiry] = useState<string>("90")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [plaintext, setPlaintext] = useState<string | null>(null)

  const expiryItems = EXPIRY.map((v) => ({
    value: v,
    label: v === "never" ? t("mcp.expiryNever") : t("mcp.expiryDays", { n: v }),
  }))

  function reset() {
    setName("")
    setChosen([])
    setExpiry("90")
    setError(null)
    setPlaintext(null)
  }

  function handleOpenChange(next: boolean) {
    onOpenChange(next)
    if (!next) reset()
  }

  function toggle(scope: string, on: boolean) {
    setChosen((prev) => (on ? [...prev, scope] : prev.filter((s) => s !== scope)))
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (chosen.length === 0) {
      setError(t("mcp.pickScope"))
      return
    }
    setBusy(true)
    try {
      const body: Record<string, unknown> = { name: name.trim(), scopes: chosen }
      if (expiry !== "never") body.expires_in_days = Number(expiry)
      const res = await api<{ token: McpToken; plaintext: string }>("/api/w/mcp_tokens", {
        json: body,
        workspaceId: ws.id,
      })
      setPlaintext(res.plaintext)
      onCreated()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="max-h-[92dvh] overflow-y-auto sm:max-w-xl">
        {plaintext ? (
          <div className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{t("mcp.createdTitle")}</DialogTitle>
              <DialogDescription>{t("mcp.createdHint")}</DialogDescription>
            </DialogHeader>
            <p
              className="flex items-start gap-2 border border-nq-line bg-card p-3 text-sm text-nq-warning-text"
              role="alert"
            >
              <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
              {t("mcp.shownOnce")}
            </p>
            <div className="space-y-1.5">
              <p className="eyebrow">{t("mcp.token")}</p>
              <CopyField value={plaintext} label={t("mcp.token")} />
            </div>
            <div className="space-y-1.5">
              <p className="eyebrow">{t("mcp.connectTitle")}</p>
              <ConnectClients url={serverUrl} token={plaintext} />
            </div>
            <DialogFooter>
              <Button onClick={() => handleOpenChange(false)}>{t("mcp.savedIt")}</Button>
            </DialogFooter>
          </div>
        ) : (
          <form onSubmit={onSubmit} className="flex flex-col gap-4">
            <DialogHeader>
              <DialogTitle>{t("mcp.create")}</DialogTitle>
              <DialogDescription>{t("mcp.createHint")}</DialogDescription>
            </DialogHeader>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="mcp-name">{t("mcp.name")}</FieldLabel>
                <Input
                  id="mcp-name"
                  dir="auto"
                  required
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={t("mcp.namePlaceholder")}
                />
              </Field>

              <FieldSet>
                <FieldLegend>{t("mcp.scopes")}</FieldLegend>
                <FormHint>
                  {t("mcp.scopesHint")}{" "}
                  <Link
                    href={mcpDocsHref(locale, "scopes")}
                    target="_blank"
                    className="text-nq-action underline-offset-4 hover:underline"
                  >
                    {t("mcp.scopesDocsLink")}
                  </Link>
                </FormHint>
                {scopes.length === 0 ? (
                  <p className="text-sm text-muted-foreground">{t("mcp.noScopes")}</p>
                ) : (
                  <ul className="max-h-64 divide-y divide-nq-line overflow-y-auto border border-nq-line">
                    {scopes.map((s) => {
                      const id = `scope-${s.scope}`
                      return (
                        <li key={s.scope} className={s.allowed ? "" : "bg-nq-surface-soft/50"}>
                          <label
                            htmlFor={id}
                            className={`flex items-start gap-3 px-3 py-2.5 ${s.allowed ? "cursor-pointer" : "cursor-not-allowed"}`}
                          >
                            <input
                              id={id}
                              type="checkbox"
                              className="mt-0.5 size-4 shrink-0 accent-(--nq-action)"
                              disabled={!s.allowed}
                              checked={chosen.includes(s.scope)}
                              onChange={(e) => toggle(s.scope, e.target.checked)}
                            />
                            <span className="min-w-0">
                              {/* The block follows the page direction; only the inline name is LTR, so every
                                  row's name starts at the same edge in /ar (MH-485, as on the OAuth consent page). */}
                              <span className="block">
                                <span
                                  dir="ltr"
                                  className="ltr-isolate font-mono text-xs text-foreground"
                                >
                                  {s.scope}
                                </span>
                              </span>
                              <span className="block text-sm text-nq-fg-body">
                                {scopeText(t, s.scope, s.description)}
                              </span>
                              {!s.allowed ? (
                                <span className="mt-0.5 block text-xs text-muted-foreground">
                                  {t("mcp.notAllowed", { role: t(`role.${s.min_role}`) })}
                                </span>
                              ) : null}
                            </span>
                          </label>
                        </li>
                      )
                    })}
                  </ul>
                )}
              </FieldSet>

              <Field>
                <FieldLabel htmlFor="mcp-expiry">{t("mcp.expiry")}</FieldLabel>
                <Select
                  items={expiryItems}
                  value={expiry}
                  onValueChange={(v) => setExpiry(String(v ?? "90"))}
                >
                  <SelectTrigger id="mcp-expiry" className="w-full sm:w-56">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {expiryItems.map((e) => (
                      <SelectItem key={e.value} value={e.value}>
                        {e.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              {error ? <FieldError>{error}</FieldError> : null}
            </FieldGroup>
            <DialogFooter>
              <Button type="button" variant="secondary" onClick={() => handleOpenChange(false)}>
                {t("common.cancel")}
              </Button>
              <Button type="submit" disabled={busy || !name.trim() || chosen.length === 0}>
                {busy ? t("common.working") : t("mcp.create")}
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}
