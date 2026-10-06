"use client"

// The platform overview (MH-1184): accounts, workspaces and records across every workspace, the last
// 30 days as series, member roles, and the busiest workspaces.
import { useRouter } from "next/navigation"
import {
  ActivityIcon,
  Building2Icon,
  MessagesSquareIcon,
  ShieldIcon,
  TrendingUpIcon,
  UserPlusIcon,
  UsersIcon,
  MegaphoneIcon,
  ZapIcon,
} from "lucide-react"
import { AdminPage, StatCard, StatGrid } from "@fadymondy/nasaq/web"

import { BarList, TrendChart } from "@/components/charts/trend-chart"
import { Ltr } from "@/components/copy-field"
import { RowsTable } from "@/components/nq-table"
import { Panel, useFormat } from "@/components/overview/overview-cards"
import { ErrorState, LoadingRows } from "@/components/states"
import { ROLES, useAdminStats, type PlatformStats } from "@/lib/admin"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

export default function AdminOverviewPage() {
  const { t } = useTranslations()
  useDocumentTitle(`${t("admin.nav.dashboard")} · ${t("admin.title")}`)
  const stats = useAdminStats()
  return (
    <AdminPage
      title={t("admin.nav.dashboard")}
      description={t("admin.dashboard.hint")}
      data-admin-page="overview"
    >
      {stats.error ? (
        <ErrorState error={stats.error} />
      ) : !stats.data ? (
        <LoadingRows rows={4} layout="detail" />
      ) : (
        <Overview s={stats.data} />
      )}
    </AdminPage>
  )
}

function Overview({ s }: { s: PlatformStats }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const f = useFormat()
  const days = s.signups.map((d) => d.date)
  return (
    <div className="flex flex-col gap-6">
      <StatGrid data-admin-stats>
        <StatCard
          label={t("admin.stats.users")}
          value={s.users.total}
          icon={<UsersIcon />}
          deltaLabel={t("admin.stats.usersSub", {
            admins: f.n(s.users.admins),
            suspended: f.n(s.users.suspended),
          })}
          sparkline={s.signups.map((d) => d.value)}
          sparklineLabel={t("admin.dashboard.signups")}
        />
        <StatCard
          label={t("admin.stats.active7d")}
          value={s.users.active_7d}
          icon={<ActivityIcon />}
          deltaLabel={t("admin.stats.last30", { n: f.n(s.users.active_30d) })}
        />
        <StatCard
          label={t("admin.stats.workspaces")}
          value={s.workspaces.total}
          icon={<Building2Icon />}
          deltaLabel={t("admin.stats.wsSub", {
            active: f.n(s.workspaces.active_30d),
            suspended: f.n(s.workspaces.suspended),
          })}
          sparkline={s.new_workspaces.map((d) => d.value)}
          sparklineLabel={t("admin.dashboard.newWorkspaces")}
        />
        <StatCard
          label={t("admin.stats.messages30d")}
          value={s.records.messages_30d}
          icon={<MessagesSquareIcon />}
          sparkline={s.messages.map((d) => d.value)}
          sparklineLabel={t("admin.dashboard.messages")}
        />
      </StatGrid>

      <StatGrid data-admin-records>
        <StatCard
          label={t("admin.stats.contacts")}
          value={s.records.contacts}
          icon={<UsersIcon />}
        />
        <StatCard
          label={t("admin.stats.companies")}
          value={s.records.companies}
          icon={<Building2Icon />}
        />
        <StatCard
          label={t("admin.stats.deals")}
          value={s.records.deals}
          icon={<TrendingUpIcon />}
        />
        <StatCard label={t("admin.stats.leads")} value={s.records.leads} icon={<UserPlusIcon />} />
        <StatCard
          label={t("admin.stats.conversations")}
          value={s.records.conversations}
          icon={<MessagesSquareIcon />}
        />
        <StatCard
          label={t("admin.stats.campaigns")}
          value={s.records.campaigns}
          icon={<MegaphoneIcon />}
        />
        <StatCard
          label={t("admin.stats.runs30d")}
          value={s.records.automation_runs_30d}
          icon={<ZapIcon />}
        />
        <StatCard label={t("admin.stats.new30d")} value={s.users.new_30d} icon={<UserPlusIcon />} />
      </StatGrid>

      <Panel id="admin-growth" title={t("admin.dashboard.growth")} icon={<TrendingUpIcon />}>
        <div className="px-5 py-4">
          <TrendChart
            title={t("admin.dashboard.growth")}
            dates={days}
            series={[
              {
                key: "signups",
                label: t("admin.dashboard.signups"),
                values: s.signups.map((d) => d.value),
                slot: 1,
              },
              {
                key: "workspaces",
                label: t("admin.dashboard.newWorkspaces"),
                values: s.new_workspaces.map((d) => d.value),
                slot: 2,
                dashed: true,
              },
            ]}
            format={f.n}
            formatDate={f.date}
          />
        </div>
      </Panel>

      <Panel
        id="admin-messages"
        title={t("admin.dashboard.messages")}
        icon={<MessagesSquareIcon />}
      >
        <div className="px-5 py-4">
          <TrendChart
            title={t("admin.dashboard.messages")}
            dates={s.messages.map((d) => d.date)}
            series={[
              {
                key: "messages",
                label: t("admin.dashboard.messages"),
                values: s.messages.map((d) => d.value),
                slot: 1,
              },
            ]}
            format={f.n}
            formatDate={f.date}
          />
        </div>
      </Panel>

      <div className="grid gap-6 lg:grid-cols-[1fr_2fr]">
        <Panel id="admin-roles" title={t("admin.dashboard.roles")} icon={<ShieldIcon />}>
          <BarList
            label={t("admin.dashboard.roles")}
            format={f.n}
            rows={ROLES.map((r) => ({ key: r, label: t(`role.${r}`), value: s.roles[r] ?? 0 }))}
          />
        </Panel>
        <RowsTable
          label={t("admin.dashboard.top")}
          data={s.top_workspaces}
          getRowId={(w) => w.id}
          rowLabel={(w) => w.name}
          onRowClick={(w) => router.push(`/${locale}/admin/workspaces/${w.slug}`)}
          data-admin-top
          columns={[
            {
              id: "name",
              header: t("admin.col.workspace"),
              cell: (w) => (
                <span className="flex min-w-0 flex-col">
                  <span className="truncate font-medium text-foreground">{w.name}</span>
                  <Ltr className="truncate text-xs text-muted-foreground">{w.slug}</Ltr>
                </span>
              ),
              sortValue: (w) => w.name,
            },
            {
              id: "messages",
              header: t("admin.col.messages30d"),
              align: "end",
              cell: (w) => <span className="tabular-nums">{f.n(w.messages_30d)}</span>,
              sortValue: (w) => w.messages_30d,
            },
            {
              id: "members",
              header: t("admin.col.members"),
              align: "end",
              cell: (w) => <span className="tabular-nums">{f.n(w.members)}</span>,
              sortValue: (w) => w.members,
            },
          ]}
        />
      </div>
    </div>
  )
}
