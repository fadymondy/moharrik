"use client"

import Link from "next/link"
import { useState } from "react"
import { BotIcon, LoaderCircleIcon, ServerIcon, SparklesIcon, XIcon } from "lucide-react"
import { Badge, Button, Toggle, ToggleGroup, toast } from "@fadymondy/nasaq/web"

import { Bdi } from "@/components/bidi"
import { CopyField } from "@/components/copy-field"
import { useFormatDateTime } from "@/components/research/shared"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AgentProviderOut, type AgentTask, type ProviderMode } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useAgentProvider } from "@/lib/queries"

const MODE_ICON = {
  auto: <SparklesIcon />,
  server: <ServerIcon />,
  agent: <BotIcon />,
} as const

/**
 * "Run via your AI assistant (MCP)": with no model key, a workspace's research
 * is thought through by the caller's own agent (Claude, Codex, …) over MCP.
 * Shows who does the research, the exact prompt to paste, and the tasks
 * waiting. `compact` drops the mode switch and task list (the run page).
 */
export function AgentPanel({
  compact = false,
  onChanged,
}: {
  compact?: boolean
  onChanged?: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const data = useAgentProvider(ws.id)
  const canRun = ws.can("research:run")
  const [saving, setSaving] = useState(false)

  if (!data.data) return null
  const d = data.data
  const p = d.provider
  const agent = p.effective === "agent"
  // Server mode with a model needs nothing from an assistant: say nothing.
  if (compact && !agent) return null

  async function setMode(mode: ProviderMode) {
    setSaving(true)
    try {
      const out = await api<AgentProviderOut>("/api/w/agent-provider", {
        method: "PUT",
        json: { mode },
        workspaceId: ws.id,
      })
      void data.mutate(out, { revalidate: false })
      onChanged?.()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setSaving(false)
    }
  }

  const open = (d.tasks ?? []).filter((x) => x.status === "pending" || x.status === "claimed")

  return (
    <section
      className="mx-6 mb-6 max-w-3xl space-y-4 border border-nq-line bg-card px-5 py-4 text-sm"
      data-agent-panel
      data-effective={p.effective}
    >
      <div className="flex flex-wrap items-start gap-3">
        <BotIcon className="mt-0.5 size-5 shrink-0 text-nq-brand" aria-hidden />
        <div className="min-w-0 flex-1 space-y-1">
          <h2 className="text-base font-medium">{t("agent.title")}</h2>
          <p className="text-muted-foreground">
            {agent
              ? p.can_server
                ? t("agent.effectiveCollect")
                : t("agent.effectiveAgent")
              : t("agent.effectiveServer")}
          </p>
        </div>
        {p.pending_tasks + p.claimed_tasks > 0 ? (
          <Badge variant="outline" data-agent-waiting>
            {t("agent.waiting", { n: p.pending_tasks + p.claimed_tasks })}
          </Badge>
        ) : null}
      </div>

      {!compact && canRun ? (
        <div className="space-y-2">
          <p className="font-medium">{t("agent.modeLabel")}</p>
          <ToggleGroup
            value={[p.mode]}
            onValueChange={(v) => v[0] && v[0] !== p.mode && void setMode(v[0] as ProviderMode)}
            aria-label={t("agent.modeLabel")}
            disabled={saving}
          >
            {(["auto", "server", "agent"] as const).map((m) => (
              <Toggle key={m} value={m} data-provider-mode={m}>
                {MODE_ICON[m]}
                {t(`agent.mode.${m}`)}
              </Toggle>
            ))}
          </ToggleGroup>
          <p className="text-xs text-muted-foreground">{t(`agent.modeHint.${p.mode}`)}</p>
        </div>
      ) : null}

      {agent ? (
        <>
          <ol className="list-decimal space-y-3 ps-5">
            <li className="space-y-2">
              <p>
                {t("agent.step1")}{" "}
                <Link
                  className="text-nq-brand hover:underline"
                  href={`/${locale}/w/${ws.slug}/settings/mcp`}
                >
                  {t("agent.step1Link")}
                </Link>
              </p>
              <CopyField value={d.mcp_url} label={t("agent.mcpUrl")} nowrap />
            </li>
            <li className="space-y-2">
              <p>{t("agent.step2")}</p>
              <CopyField value={d.prompt} label={t("agent.prompt")} />
            </li>
            <li>{t("agent.step3")}</li>
          </ol>
          {!compact && open.length > 0 ? (
            <TaskList tasks={open} onChanged={() => void data.mutate()} />
          ) : null}
        </>
      ) : null}
    </section>
  )
}

function TaskList({ tasks, onChanged }: { tasks: AgentTask[]; onChanged: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const fmt = useFormatDateTime()
  const [busy, setBusy] = useState("")

  async function cancel(id: string) {
    setBusy(id)
    try {
      await api(`/api/w/agent-tasks/${encodeURIComponent(id)}/cancel`, {
        method: "POST",
        workspaceId: ws.id,
      })
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy("")
    }
  }

  return (
    <div className="space-y-2" data-agent-tasks>
      <p className="font-medium">{t("agent.tasks")}</p>
      <ul className="divide-y divide-nq-line border-y border-nq-line">
        {tasks.map((x) => (
          <li
            key={x.id}
            className="flex flex-wrap items-center gap-2 py-2"
            data-agent-task={x.kind}
          >
            <Badge variant="outline">{t(`agent.kind.${x.kind}`)}</Badge>
            <span className="min-w-0 flex-1 truncate">
              <Bdi>{x.topic}</Bdi>
            </span>
            <span className="text-xs text-muted-foreground">
              {x.status === "claimed" && x.agent_name
                ? t("agent.claimedBy", { agent: x.agent_name })
                : t(`agent.status.${x.status}`)}{" "}
              · {fmt(x.created_at)}
            </span>
            {ws.can("research:run") ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => void cancel(x.id)}
                disabled={busy === x.id}
                aria-label={t("agent.cancelTask")}
                data-action="cancel-agent-task"
              >
                {busy === x.id ? <LoaderCircleIcon className="animate-spin" /> : <XIcon />}
              </Button>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  )
}
