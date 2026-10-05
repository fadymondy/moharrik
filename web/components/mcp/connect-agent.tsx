"use client"

import { useState, type FormEvent } from "react"
import Link from "next/link"
import {
  Button,
  Field,
  FieldLabel,
  Input,
  McpLogo,
  McpConnectApps,
  McpConnectSheet,
  RadioCard,
  RadioGroup,
} from "@fadymondy/nasaq/web"

import { FieldError } from "@/components/form-layout"
import { api, ApiError, type McpToken } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMcpTokens, useOAuthGrants } from "@/lib/queries"

/*
"Connect your agent" in the app header (MH-1115): Nasaq's ConnectButton opens
McpConnectSheet, and the MCP panel is McpConnectApps, written for people who
have never heard of MCP. Claude, ChatGPT and Claude Code sign in over OAuth
(internal/mcp/oauth.go), so they need only the link; Cursor, VS Code and
"another app" take a key, minted here with KeyForm.

Only MCP is offered: the REST API takes a browser session, not a key, so an
"API" tile would have nothing to connect with.
*/

const SERVER_NAME = "moharrik"

export function ConnectAgent({ workspaceId, slug }: { workspaceId: string; slug: string }) {
  const { t } = useTranslations()
  const [open, setOpen] = useState(false)
  // Asked only once the panel opens, so the header costs no request on every page.
  const grants = useOAuthGrants(open ? workspaceId : null)
  // The URL Settings → MCP shows too (MH-516): the dedicated MCP host once there is one.
  const url =
    grants.data?.server_url ??
    (typeof window === "undefined" ? "" : `${window.location.origin}/api/mcp`)

  return (
    <McpConnectSheet
      open={open}
      onOpenChange={setOpen}
      types={[
        {
          value: "mcp",
          label: "MCP",
          icon: <McpLogo />,
          content: (
            <McpConnectApps
              product="Moharrik"
              server={{ name: SERVER_NAME, url }}
              oauth
              tryHint={t("connect.tryHint")}
              renderKeyForm={(done) => (
                <KeyForm workspaceId={workspaceId} slug={slug} onMinted={done} />
              )}
            />
          ),
        },
      ]}
    />
  )
}

/** Mints a key for an app that cannot sign in: read-only unless the person asks for more. */
function KeyForm({
  workspaceId,
  slug,
  onMinted,
}: {
  workspaceId: string
  slug: string
  onMinted: (token: string) => void
}) {
  const { t, locale } = useTranslations()
  const tokens = useMcpTokens(workspaceId)
  const [name, setName] = useState(() => t("connect.keyDefaultName"))
  const [access, setAccess] = useState<"read" | "write">("read")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const allowed = (tokens.data?.scopes ?? []).filter((s) => s.allowed).map((s) => s.scope)
  const scopes = access === "read" ? allowed.filter((s) => s.endsWith(":read")) : allowed

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const res = await api<{ token: McpToken; plaintext: string }>("/api/w/mcp_tokens", {
        json: { name: name.trim(), scopes, expires_in_days: 90 },
        workspaceId,
      })
      void tokens.mutate()
      onMinted(res.plaintext)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-3" data-connect-key-form>
      <Field>
        <FieldLabel htmlFor="connect-key-name">{t("connect.keyName")}</FieldLabel>
        <Input
          id="connect-key-name"
          dir="auto"
          required
          maxLength={80}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </Field>
      <Field>
        <FieldLabel id="connect-key-access">{t("connect.access")}</FieldLabel>
        <RadioGroup
          aria-labelledby="connect-key-access"
          value={access}
          onValueChange={(v) => setAccess(v === "write" ? "write" : "read")}
          className="grid gap-2 sm:grid-cols-2"
        >
          <RadioCard
            value="read"
            title={t("connect.readOnly")}
            description={t("connect.readOnlyHint")}
          />
          <RadioCard
            value="write"
            title={t("connect.readWrite")}
            description={t("connect.readWriteHint")}
          />
        </RadioGroup>
      </Field>
      <p className="text-sm text-muted-foreground">
        {t("connect.keyExpires")}{" "}
        <Link
          href={`/${locale}/w/${slug}/settings/mcp`}
          className="text-nq-action underline-offset-4 hover:underline"
        >
          {t("connect.manageKeys")}
        </Link>
      </p>
      {error ? <FieldError>{error}</FieldError> : null}
      <Button
        type="submit"
        disabled={busy || !tokens.data || !name.trim() || scopes.length === 0}
        className="self-start"
      >
        {busy ? t("common.working") : t("connect.createKey")}
      </Button>
    </form>
  )
}
