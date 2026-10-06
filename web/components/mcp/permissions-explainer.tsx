"use client"

import useSWR from "swr"
import { Badge } from "@fadymondy/nasaq/web"

import { ErrorState, LoadingRows } from "@/components/states"
import { Ltr } from "@/components/copy-field"
import { api } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import type { McpPermissions, McpPermTool } from "@/lib/mcp-docs"

/**
 * What a token holding each scope can read, change, change only after a
 * confirmation, and never do (MH-1069). Every list comes from
 * GET /api/mcp/permissions, which the API derives from its own tool registry
 * (internal/mcp/permissions.go): the tool names, the one-line annotations (the
 * tools' own descriptions), the write/confirm flags and the "never" entries.
 * Nothing here is a list kept by hand in the frontend.
 */
export function PermissionsExplainer() {
  const { t, locale } = useTranslations()
  const lang = locale === "ar" ? "ar" : "en"
  const perms = useSWR<McpPermissions>(["/api/mcp/permissions", lang], ([p, l]: [string, string]) =>
    api<McpPermissions>(`${p}?lang=${l}`),
  )
  const doc = perms.data

  return (
    <section
      className="border-t border-nq-line px-6 py-8"
      aria-labelledby="mcpx-title"
      data-mcp-permissions
    >
      <div className="mb-4 max-w-2xl space-y-1.5">
        <h2 id="mcpx-title" className="font-medium tracking-tight text-foreground text-lg">
          {t("mcpx.title")}
        </h2>
        <p className="leading-relaxed text-nq-fg-body text-sm">{t("mcpx.hint")}</p>
      </div>

      {perms.error ? (
        <ErrorState error={perms.error} />
      ) : !doc ? (
        <LoadingRows rows={3} />
      ) : (
        <div className="max-w-4xl space-y-4">
          <ul
            className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground"
            data-mcpx-legend
          >
            <li className="flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-nq-info" aria-hidden />
              {t("mcpx.read")}
            </li>
            <li className="flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-nq-success" aria-hidden />
              {t("mcpx.change")}
            </li>
            <li className="flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-nq-warning" aria-hidden />
              {t("mcpx.confirm")}
            </li>
            <li className="flex items-center gap-1.5">
              <span className="size-2 rounded-full bg-nq-danger" aria-hidden />
              {t("mcpx.never")}
            </li>
          </ul>

          <div className="border border-nq-line divide-y divide-nq-line">
            {doc.scopes.map((s) => (
              <details key={s.scope} className="group" data-mcpx-scope={s.scope}>
                <summary className="flex cursor-pointer list-none flex-wrap items-center gap-x-3 gap-y-1 px-4 py-3 marker:hidden [&::-webkit-details-marker]:hidden">
                  <Ltr mono className="text-xs text-foreground">
                    {s.scope}
                  </Ltr>
                  <span className="min-w-0 flex-1 basis-60 text-sm text-nq-fg-body">
                    {s.description}
                  </span>
                  <span className="flex flex-wrap gap-1">
                    {s.can_read.length > 0 ? (
                      <Badge variant="neutral" data-count="read">
                        {t("mcpx.nRead", { n: s.can_read.length })}
                      </Badge>
                    ) : null}
                    {s.can_change.length > 0 ? (
                      <Badge variant="neutral" data-count="change">
                        {t("mcpx.nChange", { n: s.can_change.length })}
                      </Badge>
                    ) : null}
                    {s.needs_confirm.length > 0 ? (
                      <Badge variant="brand" data-count="confirm">
                        {t("mcpx.nConfirm", { n: s.needs_confirm.length })}
                      </Badge>
                    ) : null}
                  </span>
                </summary>
                <div className="space-y-4 border-t border-nq-line bg-nq-surface-soft/40 px-4 py-4">
                  <ToolGroup tone="info" title={t("mcpx.read")} tools={s.can_read} />
                  <ToolGroup tone="success" title={t("mcpx.change")} tools={s.can_change} />
                  <ToolGroup
                    tone="warning"
                    title={t("mcpx.confirm")}
                    tools={s.needs_confirm}
                    showConfirm
                  />
                  {s.can_read.length + s.can_change.length + s.needs_confirm.length === 0 ? (
                    <p className="text-sm text-muted-foreground">{t("mcpx.noTools")}</p>
                  ) : null}
                </div>
              </details>
            ))}
          </div>

          <div data-mcpx-never>
            <h3 className="mb-2 flex items-center gap-1.5 text-sm font-medium">
              <span className="size-2 rounded-full bg-nq-danger" aria-hidden />
              {t("mcpx.never")}
            </h3>
            {doc.never.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t("mcpx.neverNone")}</p>
            ) : (
              <>
                <p className="mb-2 text-xs text-muted-foreground">{t("mcpx.neverHint")}</p>
                <ul className="list-disc space-y-1 ps-5 text-sm text-nq-fg-body">
                  {doc.never.map((n) => (
                    <li key={n.id} data-never={n.id}>
                      {n.label}
                    </li>
                  ))}
                </ul>
              </>
            )}
          </div>
        </div>
      )}
    </section>
  )
}

const dot: Record<string, string> = {
  info: "bg-nq-info",
  success: "bg-nq-success",
  warning: "bg-nq-warning",
}

function ToolGroup({
  tone,
  title,
  tools,
  showConfirm,
}: {
  tone: "info" | "success" | "warning"
  title: string
  tools: McpPermTool[]
  showConfirm?: boolean
}) {
  if (tools.length === 0) return null
  return (
    <div data-mcpx-group={tone}>
      <h3 className="mb-1.5 flex items-center gap-1.5 text-xs font-medium tracking-wide text-muted-foreground uppercase">
        <span className={`size-2 rounded-full ${dot[tone]}`} aria-hidden />
        {title}
        <span className="font-normal normal-case">({tools.length})</span>
      </h3>
      <ul className="space-y-1.5">
        {tools.map((tool) => (
          <li key={tool.name} className="text-sm" data-mcpx-tool={tool.name}>
            <Ltr mono className="text-xs text-foreground">
              {tool.name}
            </Ltr>
            <span className="text-nq-fg-body"> — {tool.summary}</span>
            {showConfirm && tool.confirm ? (
              <span className="mt-0.5 block text-xs text-nq-warning-text">{tool.confirm}</span>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  )
}
