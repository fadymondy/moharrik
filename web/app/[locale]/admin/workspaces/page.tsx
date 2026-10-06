"use client"

// Every workspace on the platform (MH-1184), in Nasaq's AdminWorkspaces: owner, plan, members and status,
// with suspend or reactivate and a plan change. Moharrik has no pricing catalog yet (MH-429), so a plan is
// a label: the choices are the labels in use plus "free", and a new label is set on the workspace's page.
// Nasaq's own stat tiles include MRR, which Moharrik cannot compute, so they are hidden for ours.
import { useMemo } from "react"
import { useRouter } from "next/navigation"
import { Building2Icon, CircleSlashIcon, UserPlusIcon, UsersIcon } from "lucide-react"
import {
  AdminPage,
  AdminWorkspaces,
  StatCard,
  StatGrid,
  type AdminPlan,
  type AdminWorkspace,
} from "@fadymondy/nasaq/web"

import { adminActions, errorText, useAdminWorkspaces } from "@/lib/admin"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

export default function AdminWorkspacesPage() {
  const { t, locale } = useTranslations()
  useDocumentTitle(`${t("admin.nav.workspaces")} · ${t("admin.title")}`)
  const router = useRouter()
  const list = useAdminWorkspaces()
  const rows = useMemo(() => list.data ?? [], [list.data])

  const plans = useMemo<AdminPlan[]>(() => {
    const count = new Map<string, number>([["free", 0]])
    for (const w of rows) count.set(w.plan, (count.get(w.plan) ?? 0) + 1)
    return [...count].map(([id, n]) => ({
      id,
      name: id,
      priceMonthly: 0,
      seats: null,
      storageGb: null,
      features: [],
      visible: true,
      subscribers: n,
    }))
  }, [rows])

  const workspaces = useMemo<AdminWorkspace[]>(
    () =>
      rows.map((w) => ({
        id: w.id,
        name: w.name,
        slug: w.slug,
        owner: { name: w.owner_email.split("@")[0] || w.owner_email, email: w.owner_email },
        planId: w.plan,
        status: w.status,
        seatsUsed: w.members,
        createdAt: w.created_at,
      })),
    [rows],
  )

  const suspended = rows.filter((w) => w.status === "suspended").length
  const members = rows.reduce((n, w) => n + w.members, 0)
  const month = Date.now() - 30 * 24 * 3600 * 1000
  const fresh = rows.filter((w) => new Date(w.created_at).getTime() >= month).length
  const fail = (err: unknown) => ({ error: errorText(err, t("common.networkError")) })

  return (
    <AdminPage
      title={t("admin.nav.workspaces")}
      description={t("admin.workspaces.hint")}
      breadcrumbs={[
        { label: t("admin.title"), href: `/${locale}/admin` },
        { label: t("admin.nav.workspaces") },
      ]}
      data-admin-page="workspaces"
    >
      <div className="flex flex-col gap-6">
        <StatGrid data-admin-ws-stats>
          <StatCard
            label={t("admin.stats.workspaces")}
            value={rows.length}
            icon={<Building2Icon />}
            loading={!list.data && !list.error}
          />
          <StatCard
            label={t("admin.stats.new30d")}
            value={fresh}
            icon={<UserPlusIcon />}
            loading={!list.data && !list.error}
          />
          <StatCard
            label={t("admin.col.members")}
            value={members}
            icon={<UsersIcon />}
            loading={!list.data && !list.error}
          />
          <StatCard
            label={t("admin.status.suspended")}
            value={suspended}
            icon={<CircleSlashIcon />}
            loading={!list.data && !list.error}
          />
        </StatGrid>
        <AdminWorkspaces
          workspaces={workspaces}
          plans={plans}
          hideStats
          loading={!list.data && !list.error}
          error={list.error ? errorText(list.error, t("common.networkError")) : undefined}
          onRetry={() => list.mutate()}
          pageSize={25}
          onChangePlan={async (w, plan) => {
            try {
              await adminActions.setPlan(w.id, plan)
            } catch (err) {
              return fail(err)
            }
          }}
          onSetSuspended={async (w, suspend) => {
            try {
              if (suspend) await adminActions.suspendWorkspace(w.id, "")
              else await adminActions.reactivateWorkspace(w.id)
            } catch (err) {
              return fail(err)
            }
          }}
          onOpen={(w) => router.push(`/${locale}/admin/workspaces/${w.slug}`)}
        />
      </div>
    </AdminPage>
  )
}
