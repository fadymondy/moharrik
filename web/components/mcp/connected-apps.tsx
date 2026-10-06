"use client"

import { PlugZapIcon } from "lucide-react"
import type { ReactNode } from "react"
import { Badge, toast } from "@fadymondy/nasaq/web"

import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { Bdi, isolate } from "@/components/bidi"
import { RowsTable, type RowsColumn } from "@/components/nq-table"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type OAuthGrant } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useOAuthGrants } from "@/lib/queries"

/**
 * Apps connected over OAuth (MH-391): claude.ai, ChatGPT or any MCP client a
 * member signed into this workspace from its consent screen. Each is bound to
 * this workspace alone. Disconnecting one stops its tokens at once. Owners and
 * admins see every member's; a member sees their own, as with tokens.
 */
export function ConnectedApps({ grants }: { grants: ReturnType<typeof useOAuthGrants> }) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const list = grants.data?.grants ?? []
  const all = !!grants.data?.all

  async function revoke(g: OAuthGrant) {
    try {
      await api(`/api/w/oauth_grants/${encodeURIComponent(g.id)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      toast.success(t("oauth.apps.revoked", { name: isolate(g.client_name) }))
      await grants.mutate()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    }
  }

  // A disconnected app stays listed, dimmed, so its history still reads.
  const dim = (g: OAuthGrant, node: ReactNode) => (
    <span
      className={g.revoked_at ? "opacity-60" : undefined}
      data-grant={g.id}
      data-grant-revoked={!!g.revoked_at}
    >
      {node}
    </span>
  )
  const columns: RowsColumn<OAuthGrant>[] = [
    {
      id: "app",
      header: t("oauth.apps.app"),
      hideable: false,
      sortValue: (g) => g.client_name,
      cell: (g) =>
        dim(
          g,
          <>
            <span className="block font-medium">
              <Bdi>{g.client_name || t("oauth.unnamed")}</Bdi>
            </span>
            {g.client_uri ? (
              <Ltr className="block max-w-56 truncate text-xs text-muted-foreground">
                {g.client_uri}
              </Ltr>
            ) : null}
          </>,
        ),
    },
    ...(all
      ? [
          {
            id: "holder",
            header: t("mcp.holder"),
            sortValue: (g: OAuthGrant) => g.user_email,
            cell: (g: OAuthGrant) => (
              <span className="flex flex-wrap items-center gap-1.5">
                <Ltr className="text-sm">{g.user_email || g.user_id}</Ltr>
                {g.mine ? <Badge variant="neutral">{t("mcp.you")}</Badge> : null}
              </span>
            ),
          },
        ]
      : []),
    {
      id: "scopes",
      header: t("mcp.scopes"),
      cell: (g) => (
        <span className="flex max-w-xs flex-wrap gap-1">
          {g.scopes.map((s) => (
            <Badge key={s} variant="outline" className="font-mono" dir="ltr">
              {s}
            </Badge>
          ))}
        </span>
      ),
    },
    {
      id: "connected",
      header: t("oauth.apps.connected"),
      sortValue: (g) => g.created_at,
      cell: (g) => <Ltr className="text-muted-foreground">{formatDate(g.created_at)}</Ltr>,
    },
    {
      id: "lastUsed",
      header: t("mcp.lastUsed"),
      sortValue: (g) => g.last_used_at,
      cell: (g) => (
        <span className="text-muted-foreground">
          {g.last_used_at ? <Ltr>{formatDate(g.last_used_at)}</Ltr> : t("mcp.never")}
        </span>
      ),
    },
    {
      id: "actions",
      header: <span className="sr-only">{t("common.actions")}</span>,
      label: t("common.actions"),
      align: "end",
      hideable: false,
      cell: (g) =>
        g.revoked_at ? (
          <Badge variant="neutral">{t("oauth.apps.disconnectedBadge")}</Badge>
        ) : (
          <ConfirmButton
            label={t("oauth.apps.disconnect")}
            title={t("oauth.apps.disconnectTitle")}
            description={t("oauth.apps.disconnectBody", {
              name: isolate(g.client_name || t("oauth.unnamed")),
            })}
            confirmLabel={t("oauth.apps.disconnect")}
            onConfirm={() => revoke(g)}
          />
        ),
    },
  ]

  return (
    <section
      className="border-t border-nq-line py-8"
      aria-labelledby="mcp-apps"
      data-connected-apps
    >
      <div className="mb-4 max-w-2xl space-y-1.5 px-6">
        <h2
          id="mcp-apps"
          className="font-medium tracking-tight text-foreground flex items-center gap-2 text-lg"
        >
          <PlugZapIcon className="size-4 text-muted-foreground" />
          {t("oauth.apps.title")}
        </h2>
        <p className="leading-relaxed text-nq-fg-body text-sm">
          {all ? t("oauth.apps.hintAll") : t("oauth.apps.hint")}
        </p>
      </div>
      {grants.error ? (
        <ErrorState error={grants.error} />
      ) : grants.isLoading ? (
        <LoadingRows rows={2} />
      ) : list.length === 0 ? (
        <EmptyState title={t("oauth.apps.empty")} body={t("oauth.apps.emptyBody")} />
      ) : (
        <div className="px-6">
          <RowsTable<OAuthGrant>
            data={list}
            getRowId={(g) => g.id}
            rowLabel={(g) => g.client_name || t("oauth.unnamed")}
            label={t("oauth.apps.title")}
            columns={columns}
          />
        </div>
      )}
    </section>
  )
}
