"use client"

// Every account on the platform (MH-1184), in Nasaq's AdminUsers: suspend or reactivate, and grant or
// revoke platform admin through its roles dialog. A row opens the account. Moharrik has no admin-side
// "add user", password reset or impersonation, so those controls are not passed and do not render.
import { useMemo } from "react"
import { useRouter } from "next/navigation"
import { AdminPage, AdminUsers, type ManagedRole, type ManagedUser } from "@fadymondy/nasaq/web"

import { adminActions, errorText, useAdminUsers } from "@/lib/admin"
import { useTranslations } from "@/lib/i18n"
import { useMe } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

const PLATFORM_ADMIN = "platform_admin"
const ACCOUNT = "account"

export default function AdminUsersPage() {
  const { t, locale } = useTranslations()
  useDocumentTitle(`${t("admin.nav.users")} · ${t("admin.title")}`)
  const router = useRouter()
  const me = useMe()
  const list = useAdminUsers()

  const roles: ManagedRole[] = [
    { id: ACCOUNT, label: t("admin.role.account"), description: t("admin.role.accountHint") },
    {
      id: PLATFORM_ADMIN,
      label: t("admin.role.platformAdmin"),
      description: t("admin.role.platformAdminHint"),
    },
  ]

  const users = useMemo<ManagedUser[]>(
    () =>
      (list.data ?? []).map((u) => ({
        id: u.id,
        name: u.email.split("@")[0] || u.email,
        email: u.email,
        roles: u.is_admin ? [ACCOUNT, PLATFORM_ADMIN] : [ACCOUNT],
        status: u.status === "suspended" ? "disabled" : "active",
        verified: true,
        workspace: u.workspaces.map((w) => w.name).join(", ") || undefined,
        lastActive: u.last_seen_at,
        createdAt: u.created_at,
      })),
    [list.data],
  )

  const fail = (err: unknown) => ({ error: errorText(err, t("common.networkError")) })

  return (
    <AdminPage
      title={t("admin.nav.users")}
      description={t("admin.users.hint")}
      breadcrumbs={[
        { label: t("admin.title"), href: `/${locale}/admin` },
        { label: t("admin.nav.users") },
      ]}
      data-admin-page="users"
    >
      <AdminUsers
        users={users}
        roles={roles}
        currentUserId={me.data ? String(me.data.id) : undefined}
        loading={!list.data && !list.error}
        error={list.error ? errorText(list.error, t("common.networkError")) : undefined}
        onRetry={() => list.mutate()}
        pageSize={25}
        onSetDisabled={async (u, disabled) => {
          try {
            if (disabled) await adminActions.suspendUser(u.id, "")
            else await adminActions.reactivateUser(u.id)
          } catch (err) {
            return fail(err)
          }
        }}
        onUpdateRoles={async (u, next) => {
          const want = next.includes(PLATFORM_ADMIN)
          if (want === u.roles.includes(PLATFORM_ADMIN)) return
          try {
            await adminActions.setAdmin(u.id, want)
          } catch (err) {
            return fail(err)
          }
        }}
        onOpenUser={(u) => router.push(`/${locale}/admin/users/${u.id}`)}
        labels={{
          disabled: t("admin.status.suspended"),
          statusDisabled: t("admin.status.suspended"),
          disable: t("admin.users.suspend"),
          enable: t("admin.users.reactivate"),
          disableConfirm: t("admin.users.suspend"),
          disableBody: t("admin.users.suspendBody"),
          editRoles: t("admin.users.editAccess"),
          rolesBody: t("admin.users.accessBody"),
        }}
      />
    </AdminPage>
  )
}
