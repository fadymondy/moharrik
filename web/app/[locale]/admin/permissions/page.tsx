"use client"

// Who may do what (MH-1184). The workspace roles are fixed in code (internal/authz), so the matrix is
// read-only: each action and the lowest role that may take it. The one grant an admin can change is
// platform admin itself, listed below it; granting is done from an account's page.
import { useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { CheckIcon, KeyRoundIcon, MinusIcon, ShieldCheckIcon, ShieldOffIcon } from "lucide-react"
import { AdminPage, Badge, Input } from "@fadymondy/nasaq/web"

import { ActionDialog, StatusBadge } from "@/components/admin/admin-bits"
import { Ltr } from "@/components/copy-field"
import { RowsTable, type RowsColumn } from "@/components/nq-table"
import { Panel, useFormat } from "@/components/overview/overview-cards"
import {
  adminActions,
  useAdminPermissions,
  useAdminUsers,
  type AdminUser,
  type PlatformPermissions,
} from "@/lib/admin"
import { useTranslations } from "@/lib/i18n"
import { useMe } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

type Action = PlatformPermissions["actions"][number]

export default function AdminPermissionsPage() {
  const { t, locale } = useTranslations()
  useDocumentTitle(`${t("admin.nav.permissions")} · ${t("admin.title")}`)
  const router = useRouter()
  const f = useFormat()
  const me = useMe()
  const perms = useAdminPermissions()
  const admins = useAdminUsers({ status: "admin" })
  const [q, setQ] = useState("")
  const [revoking, setRevoking] = useState<AdminUser | null>(null)

  const roles = perms.data?.roles ?? []
  const actions = useMemo(() => {
    const needle = q.trim().toLowerCase()
    const all = perms.data?.actions ?? []
    return needle
      ? all.filter(
          (a) => a.action.toLowerCase().includes(needle) || a.group.toLowerCase().includes(needle),
        )
      : all
  }, [perms.data, q])

  // Roles run strongest first, so a role may act when it sits at or before the action's minimum.
  const allowed = (a: Action, role: string) =>
    a.min_role !== "" && roles.indexOf(role as never) <= roles.indexOf(a.min_role)

  const matrix: RowsColumn<Action>[] = [
    {
      id: "action",
      header: t("admin.permissions.action"),
      cell: (a) => (
        <span className="font-mono text-xs" dir="ltr">
          {a.action}
        </span>
      ),
      sortValue: (a) => a.action,
    },
    {
      id: "group",
      header: t("admin.permissions.group"),
      cell: (a) => <Badge variant="neutral">{a.group}</Badge>,
      sortValue: (a) => a.group,
    },
    ...roles.map<RowsColumn<Action>>((r) => ({
      id: r,
      header: t(`role.${r}`),
      align: "center",
      cell: (a) =>
        allowed(a, r) ? (
          <CheckIcon
            className="mx-auto size-4 text-nq-success-text"
            aria-label={t("admin.permissions.allowed")}
          />
        ) : (
          <MinusIcon
            className="mx-auto size-4 text-muted-foreground"
            aria-label={t("admin.permissions.denied")}
          />
        ),
    })),
  ]

  return (
    <AdminPage
      title={t("admin.nav.permissions")}
      description={t("admin.permissions.hint")}
      breadcrumbs={[
        { label: t("admin.title"), href: `/${locale}/admin` },
        { label: t("admin.nav.permissions") },
      ]}
      data-admin-page="permissions"
    >
      <div className="flex flex-col gap-6">
        <Panel
          id="admin-platform-admins"
          title={t("admin.permissions.admins")}
          icon={<ShieldCheckIcon />}
          meta={f.n(admins.data?.length ?? 0)}
        >
          <RowsTable
            label={t("admin.permissions.admins")}
            data={admins.data ?? []}
            getRowId={(u) => u.id}
            rowLabel={(u) => u.email}
            loading={!admins.data && !admins.error}
            error={admins.error ? t("common.networkError") : undefined}
            onRetry={() => admins.mutate()}
            onRowClick={(u) => router.push(`/${locale}/admin/users/${u.id}`)}
            frame={false}
            data-admin-admins
            rowActions={(u) =>
              me.data && u.id === String(me.data.id)
                ? []
                : [
                    {
                      id: "revoke",
                      label: t("admin.users.revokeAdmin"),
                      icon: <ShieldOffIcon />,
                      danger: true,
                      onSelect: () => setRevoking(u),
                    },
                  ]
            }
            columns={[
              {
                id: "email",
                header: t("admin.col.account"),
                cell: (u) => <Ltr className="font-medium text-foreground">{u.email}</Ltr>,
                sortValue: (u) => u.email,
              },
              {
                id: "status",
                header: t("admin.col.status"),
                cell: (u) => <StatusBadge status={u.status} />,
                sortValue: (u) => u.status,
              },
              {
                id: "seen",
                header: t("admin.col.lastSeen"),
                cell: (u) =>
                  u.last_seen_at ? (
                    <span title={f.exact(u.last_seen_at)}>{f.ago(u.last_seen_at)}</span>
                  ) : (
                    t("admin.never")
                  ),
                sortValue: (u) => u.last_seen_at ?? "",
              },
            ]}
          />
        </Panel>

        <Panel
          id="admin-matrix"
          title={t("admin.permissions.matrix")}
          icon={<KeyRoundIcon />}
          meta={f.n(actions.length)}
        >
          <div className="flex flex-col gap-3 p-4">
            <Input
              type="search"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder={t("admin.permissions.search")}
              aria-label={t("admin.permissions.search")}
              className="sm:max-w-xs"
            />
            <RowsTable
              label={t("admin.permissions.matrix")}
              data={actions}
              getRowId={(a) => a.action}
              rowLabel={(a) => a.action}
              loading={!perms.data && !perms.error}
              error={perms.error ? t("common.networkError") : undefined}
              onRetry={() => perms.mutate()}
              pageSize={50}
              data-admin-matrix
              columns={matrix}
            />
          </div>
        </Panel>
      </div>

      <ActionDialog
        open={!!revoking}
        onOpenChange={(o) => !o && setRevoking(null)}
        title={t("admin.users.revokeAdmin")}
        description={t("admin.users.revokeBody")}
        confirmLabel={t("admin.users.revokeAdmin")}
        danger
        onSubmit={() => adminActions.setAdmin(revoking!.id, false)}
      />
    </AdminPage>
  )
}
