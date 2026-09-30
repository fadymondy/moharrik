"use client"

import { InfoIcon } from "lucide-react"

import { CopyField, Ltr } from "@/components/copy-field"
import { Tabs, TabsPanel, TabsList, TabsTab } from "@fadymondy/nasaq/web"
import { useTranslations } from "@/lib/i18n"

/**
 * How to connect each MCP client to this workspace (MH-396, MH-391).
 *
 * claude.ai and ChatGPT connect by URL and sign in with OAuth: no token is
 * pasted, the member picks the workspace on Moharrik's consent page. Claude
 * Code, Claude Desktop and Cursor are told how to send a pasted token.
 * `token` is the real plaintext right after creating one, a placeholder
 * otherwise. `url` and `oauthUrl` are the one server URL the page computes
 * (MCPServerURL, MH-516) — passed twice because the two kinds of client are
 * told about it differently, not because they can ever point somewhere else.
 */
export function ConnectClients({
  url,
  token,
  oauthUrl,
  where = "settings",
}: {
  url: string
  token?: string
  oauthUrl?: string
  /** "docs": the public reference (MH-428), which is not the page that lists Connected apps. */
  where?: "settings" | "docs"
}) {
  const { t } = useTranslations()
  const lastOAuthStep =
    where === "docs" ? t("mcp.client.claudeAi.step4Docs") : t("mcp.client.claudeAi.step4")
  const tok = token ?? "<your token>"

  const claudeCode = `claude mcp add --transport http moharrik ${url} --header "Authorization: Bearer ${tok}"`
  const claudeDesktop = JSON.stringify(
    {
      mcpServers: {
        moharrik: {
          command: "npx",
          args: ["-y", "mcp-remote", url, "--header", "Authorization:${MOHARRIK_AUTH}"],
          env: { MOHARRIK_AUTH: `Bearer ${tok}` },
        },
      },
    },
    null,
    2,
  )
  const cursor = JSON.stringify(
    { mcpServers: { moharrik: { url, headers: { Authorization: `Bearer ${tok}` } } } },
    null,
    2,
  )

  const clients = [
    { id: "claude-code", label: "Claude Code" },
    { id: "claude-desktop", label: "Claude Desktop" },
    { id: "claude-ai", label: "claude.ai" },
    { id: "chatgpt", label: "ChatGPT" },
    { id: "cursor", label: "Cursor" },
  ]

  return (
    <Tabs defaultValue="claude-code" className="gap-3">
      <div className="-mx-1 overflow-x-auto overflow-y-hidden px-1 pb-1.5">
        <TabsList variant="underline" className="h-9">
          {clients.map((c) => (
            <TabsTab key={c.id} value={c.id} className="px-2.5">
              <Ltr>{c.label}</Ltr>
            </TabsTab>
          ))}
        </TabsList>
      </div>

      <TabsPanel value="claude-code" className="space-y-3">
        <Step n={1}>{t("mcp.client.claudeCode.step1")}</Step>
        <CopyField value={claudeCode} label="Claude Code" />
        <Step n={2}>{t("mcp.client.claudeCode.step2")}</Step>
      </TabsPanel>

      <TabsPanel value="claude-desktop" className="space-y-3">
        <Step n={1}>{t("mcp.client.claudeDesktop.step1")}</Step>
        <Step n={2}>{t("mcp.client.claudeDesktop.step2")}</Step>
        <CopyField value={claudeDesktop} label="claude_desktop_config.json" />
        <Step n={3}>{t("mcp.client.claudeDesktop.step3")}</Step>
      </TabsPanel>

      <TabsPanel value="claude-ai" className="space-y-3" data-connect-oauth="claude-ai">
        <Note>{t("mcp.client.oauthNote")}</Note>
        <Step n={1}>{t("mcp.client.claudeAi.step1")}</Step>
        <Step n={2}>{t("mcp.client.claudeAi.step2")}</Step>
        <CopyField value={oauthUrl ?? url} label={t("mcp.client.serverUrl")} />
        <Step n={3}>{t("mcp.client.claudeAi.step3")}</Step>
        <Step n={4}>{lastOAuthStep}</Step>
      </TabsPanel>

      <TabsPanel value="chatgpt" className="space-y-3" data-connect-oauth="chatgpt">
        <Note>{t("mcp.client.oauthNote")}</Note>
        <Step n={1}>{t("mcp.client.chatgpt.step1")}</Step>
        <Step n={2}>{t("mcp.client.chatgpt.step2")}</Step>
        <CopyField value={oauthUrl ?? url} label={t("mcp.client.serverUrl")} />
        <Step n={3}>{t("mcp.client.chatgpt.step3")}</Step>
        <Step n={4}>{lastOAuthStep}</Step>
      </TabsPanel>

      <TabsPanel value="cursor" className="space-y-3">
        <Step n={1}>{t("mcp.client.cursor.step1")}</Step>
        <CopyField value={cursor} label="mcp.json" />
        <Step n={2}>{t("mcp.client.cursor.step2")}</Step>
      </TabsPanel>
    </Tabs>
  )
}

function Step({ n, children }: { n: number; children: React.ReactNode }) {
  return (
    <p className="flex gap-2.5 text-sm text-nq-fg-body">
      <span className="grid size-5 shrink-0 place-items-center border border-nq-line font-mono text-[11px] text-muted-foreground">
        {n}
      </span>
      <span className="min-w-0">{children}</span>
    </p>
  )
}

function Note({ children }: { children: React.ReactNode }) {
  return (
    <p className="flex items-start gap-2 border border-nq-line bg-card p-3 text-sm text-nq-fg-body">
      <InfoIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <span className="min-w-0">{children}</span>
    </p>
  )
}
