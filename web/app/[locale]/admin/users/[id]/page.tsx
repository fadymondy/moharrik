"use client"

// One account (MH-1184): its status, the sessions and tokens it holds, the workspaces it belongs to, and
// what admins have done to it. Suspend with a reason, sign it out everywhere, or grant or revoke
// platform admin. The API refuses an admin acting on their own account; the error shows in the dialog.
import { useState } from "react"
import { useParams, useRouter } from "next/navigation"
import {
  Building2Icon,
  LogOutIcon,
  PlayIcon,
  ScrollTextIcon,
  ShieldCheckIcon,
  ShieldOffIcon,
  UserXIcon,
} from "lucide-react"
import { AdminPage, Badge, Button } from "@fadymondy/nasaq/web"

import {
  ActionDialog,
  AdminBadge,
  AuditTable,
  Facts,
  StatusBadge,
} from "@/components/admin/admin-bits"
import { Ltr } from "@/components/copy-field"
import { RowsTable } from "@/components/nq-table"
import { NotFoundState } from "@/components/not-found-state"
import { Panel, useFormat } from "@/components/overview/overview-cards"
import { ErrorState, LoadingRows } from "@/components/states"
import { adminActions, useAdminAudit, useAdminUser, type AdminUserDetail } from "@/lib/admin"
import { ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

type Pending = "suspend" | "reactivate" | "signout" | "grant" | "revoke" | null

export default function AdminUserPage() {
  const { t, locale } = useTranslations()
  const { id } = useParams<{ id: string }>()
  const user = useAdminUser(id)
  useDocumentTitle(`${user.data?.email ?? t("admin.nav.users")} · ${t("admin.title")}`)

  if (user.error instanceof ApiError && user.error.status === 404) return <NotFoundState />
  return (
    <AdminPage
      title={user.data ? <Ltr>{user.data.email}</Ltr> : t("admin.nav.users")}
      breadcrumbs={[
        { label: t("admin.title"), href: `/${locale}/admin` },
        { label: t("admin.nav.users"), href: `/${locale}/admin/users` },
        { label: user.data?.email ?? "…" },
      ]}
      actions={user.data ? <Actions u={user.data} /> : null}
      data-admin-page="user"
    >
      {user.error ? (
        <ErrorState error={user.error} />
      ) : !user.data ? (
        <LoadingRows rows={4} layout="detail" />
      ) : (
        <Detail u={user.data} />
      )}
    </AdminPage>
  )
}

function Actions({ u }: { u: AdminUserDetail }) {
  const { t } = useTranslations()
  const [pending, setPending] = useState<Pending>(null)
  const open = (p: Pending) => () => setPending(p)
  const onOpenChange = (o: boolean) => !o && setPending(null)
  return (
    <div className="flex flex-wrap items-center gap-2">
      {u.status === "suspended" ? (
        <Button variant="secondary" onClick={open("reactivate")}>
          <PlayIcon />
          {t("admin.users.reactivate")}
        </Button>
      ) : (
        <Button variant="secondary" onClick={open("suspend")}>
          <UserXIcon />
          {t("admin.users.suspend")}
        </Button>
      )}
      <Button variant="secondary" onClick={open("signout")}>
        <LogOutIcon />
        {t("admin.users.signOut")}
      </Button>
      {u.is_admin ? (
        <Button variant="secondary" onClick={open("revoke")}>
          <ShieldOffIcon />
          {t("admin.users.revokeAdmin")}
        </Button>
      ) : (
        <Button variant="secondary" onClick={open("grant")}>
          <ShieldCheckIcon />
          {t("admin.users.grantAdmin")}
        </Button>
      )}

      <ActionDialog
        open={pending === "suspend"}
        onOpenChange={onOpenChange}
        title={t("admin.users.suspend")}
        description={t("admin.users.suspendBody")}
        confirmLabel={t("admin.users.suspend")}
        danger
        reason
        onSubmit={(reason) => adminActions.suspendUser(u.id, reason)}
      />
      <ActionDialog
        open={pending === "reactivate"}
        onOpenChange={onOpenChange}
        title={t("admin.users.reactivate")}
        description={t("admin.users.reactivateBody")}
        confirmLabel={t("admin.users.reactivate")}
        onSubmit={() => adminActions.reactivateUser(u.id)}
      />
      <ActionDialog
        open={pending === "signout"}
        onOpenChange={onOpenChange}
        title={t("admin.users.signOut")}
        description={t("admin.users.signOutBody")}
        confirmLabel={t("admin.users.signOut")}
        danger
        onSubmit={() => adminActions.signOutUser(u.id)}
      />
      <ActionDialog
        open={pending === "grant"}
        onOpenChange={onOpenChange}
        title={t("admin.users.grantAdmin")}
        description={t("admin.users.grantBody")}
        confirmLabel={t("admin.users.grantAdmin")}
        onSubmit={() => adminActions.setAdmin(u.id, true)}
      />
      <ActionDialog
        open={pending === "revoke"}
        onOpenChange={onOpenChange}
        title={t("admin.users.revokeAdmin")}
        description={t("admin.users.revokeBody")}
        confirmLabel={t("admin.users.revokeAdmin")}
        danger
        onSubmit={() => adminActions.setAdmin(u.id, false)}
      />
    </div>
  )
}

function Detail({ u }: { u: AdminUserDetail }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const f = useFormat()
  const audit = useAdminAudit({ target: u.id })
  const when = (v: string | null) =>
    v ? <span title={f.exact(v)}>{f.ago(v)}</span> : t("admin.never")
  return (
    <div className="flex flex-col gap-6">
      <Facts
        items={[
          {
            label: t("admin.col.status"),
            value: (
              <span className="flex flex-wrap items-center gap-1.5">
                <StatusBadge status={u.status} />
                {u.is_admin ? <AdminBadge /> : null}
              </span>
            ),
          },
          ...(u.status === "suspended"
            ? [{ label: t("admin.reason"), value: u.suspended_reason || "—" }]
            : []),
          { label: t("admin.col.joined"), value: when(u.created_at) },
          { label: t("admin.col.lastSeen"), value: when(u.last_seen_at) },
          { label: t("admin.users.sessionsSince"), value: when(u.sessions_valid_after) },
          {
            label: t("admin.users.mcpTokens"),
            value: <span className="tabular-nums">{f.n(u.mcp_tokens)}</span>,
          },
        ]}
      />

      <Panel
        id="admin-user-workspaces"
        title={t("admin.nav.workspaces")}
        icon={<Building2Icon />}
        meta={f.n(u.workspaces.length)}
      >
        <RowsTable
          label={t("admin.nav.workspaces")}
          data={u.workspaces}
          getRowId={(m) => m.workspace_id}
          rowLabel={(m) => m.name}
          onRowClick={(m) => router.push(`/${locale}/admin/workspaces/${m.slug}`)}
          empty={t("admin.users.noWorkspaces")}
          frame={false}
          data-admin-memberships
          columns={[
            {
              id: "name",
              header: t("admin.col.workspace"),
              cell: (m) => (
                <span className="flex min-w-0 flex-col">
                  <span className="truncate font-medium text-foreground">{m.name}</span>
                  <Ltr className="truncate text-xs text-muted-foreground">{m.slug}</Ltr>
                </span>
              ),
              sortValue: (m) => m.name,
            },
            {
              id: "role",
              header: t("admin.col.role"),
              cell: (m) => <Badge variant="neutral">{t(`role.${m.role}`)}</Badge>,
              sortValue: (m) => m.role,
            },
            {
              id: "status",
              header: t("admin.col.status"),
              cell: (m) => <StatusBadge status={m.status} />,
              sortValue: (m) => m.status,
            },
          ]}
        />
      </Panel>

      <Panel id="admin-user-audit" title={t("admin.nav.audit")} icon={<ScrollTextIcon />}>
        <AuditTable
          entries={audit.data ?? []}
          loading={!audit.data && !audit.error}
          error={audit.error ? t("common.networkError") : undefined}
          onRetry={() => audit.mutate()}
        />
      </Panel>
    </div>
  )
}
