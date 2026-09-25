"use client"

import { PlugZapIcon } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { Bdi, isolate } from "@/components/bidi"
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

  return (
    <section className="border-t border-line py-8" aria-labelledby="mcp-apps" data-connected-apps>
      <div className="mb-4 max-w-2xl space-y-1.5 px-6">
        <h2 id="mcp-apps" className="grid-title flex items-center gap-2 text-lg">
          <PlugZapIcon className="size-4 text-grid-muted" />
          {t("oauth.apps.title")}
        </h2>
        <p className="grid-body text-sm">{all ? t("oauth.apps.hintAll") : t("oauth.apps.hint")}</p>
      </div>
      {grants.error ? (
        <ErrorState error={grants.error} />
      ) : grants.isLoading ? (
        <LoadingRows rows={2} />
      ) : list.length === 0 ? (
        <EmptyState title={t("oauth.apps.empty")} body={t("oauth.apps.emptyBody")} />
      ) : (
        <div className="border-y border-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("oauth.apps.app")}</TableHead>
                {all ? <TableHead>{t("mcp.holder")}</TableHead> : null}
                <TableHead>{t("mcp.scopes")}</TableHead>
                <TableHead>{t("oauth.apps.connected")}</TableHead>
                <TableHead>{t("mcp.lastUsed")}</TableHead>
                <TableHead className="pe-6 text-end">
                  <span className="sr-only">{t("common.actions")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((g) => (
                <TableRow
                  key={g.id}
                  className={g.revoked_at ? "opacity-60" : undefined}
                  data-grant={g.id}
                  data-grant-revoked={!!g.revoked_at}
                >
                  <TableCell className="ps-6">
                    <span className="block font-medium">
                      <Bdi>{g.client_name || t("oauth.unnamed")}</Bdi>
                    </span>
                    {g.client_uri ? (
                      <Ltr className="block max-w-56 truncate text-xs text-grid-muted">
                        {g.client_uri}
                      </Ltr>
                    ) : null}
                  </TableCell>
                  {all ? (
                    <TableCell>
                      <span className="flex flex-wrap items-center gap-1.5">
                        <Ltr className="text-sm">{g.user_email || g.user_id}</Ltr>
                        {g.mine ? <Badge variant="secondary">{t("mcp.you")}</Badge> : null}
                      </span>
                    </TableCell>
                  ) : null}
                  <TableCell>
                    <div className="flex max-w-xs flex-wrap gap-1">
                      {g.scopes.map((s) => (
                        <Badge key={s} variant="outline" className="font-mono" dir="ltr">
                          {s}
                        </Badge>
                      ))}
                    </div>
                  </TableCell>
                  <TableCell className="text-grid-muted">
                    <Ltr>{formatDate(g.created_at)}</Ltr>
                  </TableCell>
                  <TableCell className="text-grid-muted">
                    {g.last_used_at ? <Ltr>{formatDate(g.last_used_at)}</Ltr> : t("mcp.never")}
                  </TableCell>
                  <TableCell className="pe-6 text-end">
                    {g.revoked_at ? (
                      <Badge variant="secondary">{t("oauth.apps.disconnectedBadge")}</Badge>
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
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </section>
  )
}
