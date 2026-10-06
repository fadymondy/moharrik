"use client"

// One workspace (MH-1184): status, plan, owner, record counts, its members and what admins have done to
// it. Members can be added straight in (an existing account, no invitation), re-roled or removed; the API
// keeps the last owner. Suspending keeps the data and turns members away with 403. Deleting needs the slug
// typed out, and removes everything in it.
import { useState, type FormEvent } from "react"
import { useParams, useRouter } from "next/navigation"
import {
  BarChart3Icon,
  PlayIcon,
  ScrollTextIcon,
  TagIcon,
  Trash2Icon,
  UserMinusIcon,
  UserPlusIcon,
  UsersIcon,
  UserXIcon,
} from "lucide-react"
import {
  AdminPage,
  Button,
  Field,
  FieldLabel,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  StatCard,
  StatGrid,
  toast,
} from "@fadymondy/nasaq/web"

import {
  ActionDialog,
  AdminBadge,
  AuditTable,
  Facts,
  StatusBadge,
} from "@/components/admin/admin-bits"
import { Ltr } from "@/components/copy-field"
import { FieldError } from "@/components/form-layout"
import { RowsTable } from "@/components/nq-table"
import { NotFoundState } from "@/components/not-found-state"
import { Panel, useFormat } from "@/components/overview/overview-cards"
import { ErrorState, LoadingRows } from "@/components/states"
import {
  adminActions,
  errorText,
  ROLES,
  useAdminAudit,
  useAdminWorkspace,
  type AdminMember,
  type AdminWorkspaceDetail,
  type Role,
} from "@/lib/admin"
import { ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

type Pending = "suspend" | "reactivate" | "plan" | "delete" | null

export default function AdminWorkspacePage() {
  const { t, locale } = useTranslations()
  const { id } = useParams<{ id: string }>()
  const ws = useAdminWorkspace(id)
  useDocumentTitle(`${ws.data?.name ?? t("admin.nav.workspaces")} · ${t("admin.title")}`)

  if (ws.error instanceof ApiError && ws.error.status === 404) return <NotFoundState />
  return (
    <AdminPage
      title={ws.data?.name ?? t("admin.nav.workspaces")}
      description={ws.data ? <Ltr>{ws.data.slug}</Ltr> : undefined}
      breadcrumbs={[
        { label: t("admin.title"), href: `/${locale}/admin` },
        { label: t("admin.nav.workspaces"), href: `/${locale}/admin/workspaces` },
        { label: ws.data?.name ?? "…" },
      ]}
      actions={ws.data ? <Actions w={ws.data} /> : null}
      data-admin-page="workspace"
    >
      {ws.error ? (
        <ErrorState error={ws.error} />
      ) : !ws.data ? (
        <LoadingRows rows={4} layout="detail" />
      ) : (
        <Detail w={ws.data} />
      )}
    </AdminPage>
  )
}

function Actions({ w }: { w: AdminWorkspaceDetail }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const [pending, setPending] = useState<Pending>(null)
  const open = (p: Pending) => () => setPending(p)
  const onOpenChange = (o: boolean) => !o && setPending(null)
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button variant="secondary" onClick={open("plan")}>
        <TagIcon />
        {t("admin.workspaces.setPlan")}
      </Button>
      {w.status === "suspended" ? (
        <Button variant="secondary" onClick={open("reactivate")}>
          <PlayIcon />
          {t("admin.workspaces.reactivate")}
        </Button>
      ) : (
        <Button variant="secondary" onClick={open("suspend")}>
          <UserXIcon />
          {t("admin.workspaces.suspend")}
        </Button>
      )}
      <Button variant="danger" onClick={open("delete")}>
        <Trash2Icon />
        {t("admin.workspaces.delete")}
      </Button>

      <PlanDialog open={pending === "plan"} onOpenChange={onOpenChange} w={w} />
      <ActionDialog
        open={pending === "suspend"}
        onOpenChange={onOpenChange}
        title={t("admin.workspaces.suspend")}
        description={t("admin.workspaces.suspendBody")}
        confirmLabel={t("admin.workspaces.suspend")}
        danger
        reason
        onSubmit={(reason) => adminActions.suspendWorkspace(w.id, reason)}
      />
      <ActionDialog
        open={pending === "reactivate"}
        onOpenChange={onOpenChange}
        title={t("admin.workspaces.reactivate")}
        description={t("admin.workspaces.reactivateBody")}
        confirmLabel={t("admin.workspaces.reactivate")}
        onSubmit={() => adminActions.reactivateWorkspace(w.id)}
      />
      <ActionDialog
        open={pending === "delete"}
        onOpenChange={onOpenChange}
        title={t("admin.workspaces.delete")}
        description={t("admin.workspaces.deleteBody", { name: w.name })}
        confirmLabel={t("admin.workspaces.delete")}
        danger
        typeToConfirm={w.slug}
        onSubmit={async () => {
          await adminActions.deleteWorkspace(w.id, w.slug)
          toast.success(t("admin.workspaces.deleted", { name: w.name }))
          router.replace(`/${locale}/admin/workspaces`)
        }}
      />
    </div>
  )
}

/** A plan is a free label until the pricing catalog lands (MH-429). */
function PlanDialog({
  open,
  onOpenChange,
  w,
}: {
  open: boolean
  onOpenChange: (o: boolean) => void
  w: AdminWorkspaceDetail
}) {
  const { t } = useTranslations()
  const [plan, setPlan] = useState(w.plan)
  return (
    <ActionDialog
      open={open}
      onOpenChange={(o) => {
        if (o) setPlan(w.plan)
        onOpenChange(o)
      }}
      title={t("admin.workspaces.setPlan")}
      description={t("admin.workspaces.planBody")}
      confirmLabel={t("common.save")}
      onSubmit={() => adminActions.setPlan(w.id, plan.trim() || w.plan)}
    >
      <Field>
        <FieldLabel htmlFor="admin-plan">{t("admin.col.plan")}</FieldLabel>
        <Input
          id="admin-plan"
          dir="ltr"
          value={plan}
          onChange={(e) => setPlan(e.target.value)}
          maxLength={40}
          autoFocus
        />
      </Field>
    </ActionDialog>
  )
}

function Detail({ w }: { w: AdminWorkspaceDetail }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const f = useFormat()
  const audit = useAdminAudit({ target: w.id })
  const [removing, setRemoving] = useState<AdminMember | null>(null)
  const when = (v: string | null) =>
    v ? <span title={f.exact(v)}>{f.ago(v)}</span> : t("admin.never")
  const c = (k: string) => w.counts[k] ?? 0

  async function setRole(m: AdminMember, role: Role) {
    try {
      await adminActions.setMemberRole(w.id, m.user_id, role)
      toast.success(t("admin.members.roleChanged", { email: m.email, role: t(`role.${role}`) }))
    } catch (err) {
      toast.error(errorText(err, t("common.networkError")))
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <Facts
        items={[
          { label: t("admin.col.status"), value: <StatusBadge status={w.status} /> },
          ...(w.status === "suspended"
            ? [{ label: t("admin.reason"), value: w.suspended_reason || "—" }]
            : []),
          { label: t("admin.col.plan"), value: <Ltr>{w.plan}</Ltr> },
          { label: t("admin.col.owner"), value: <Ltr>{w.owner_email || "—"}</Ltr> },
          { label: t("admin.col.created"), value: when(w.created_at) },
          { label: t("admin.col.lastActive"), value: when(w.last_active_at) },
        ]}
      />

      <Panel id="admin-ws-counts" title={t("admin.workspaces.records")} icon={<BarChart3Icon />}>
        <div className="p-4">
          <StatGrid data-admin-ws-counts>
            <StatCard label={t("admin.stats.contacts")} value={c("contacts")} />
            <StatCard label={t("admin.stats.companies")} value={c("companies")} />
            <StatCard label={t("admin.stats.deals")} value={c("deals")} />
            <StatCard label={t("admin.stats.leads")} value={c("leads")} />
            <StatCard label={t("admin.stats.conversations")} value={c("conversations")} />
            <StatCard label={t("admin.stats.messages30d")} value={w.messages_30d} />
            <StatCard label={t("admin.stats.campaigns")} value={c("campaigns")} />
            <StatCard label={t("admin.stats.workflows")} value={c("automation_workflows")} />
            <StatCard label={t("admin.stats.rules")} value={c("automation_rules")} />
            <StatCard label={t("admin.users.mcpTokens")} value={c("mcp_tokens")} />
          </StatGrid>
        </div>
      </Panel>

      <Panel
        id="admin-ws-members"
        title={t("admin.col.members")}
        icon={<UsersIcon />}
        meta={f.n(w.member_list.length)}
      >
        <div className="flex flex-col gap-4 p-4">
          <AddMember w={w} />
          <RowsTable
            label={t("admin.col.members")}
            data={w.member_list}
            getRowId={(m) => m.user_id}
            rowLabel={(m) => m.email}
            onRowClick={(m) => router.push(`/${locale}/admin/users/${m.user_id}`)}
            data-admin-members
            rowActions={(m) => [
              ...ROLES.filter((r) => r !== m.role).map((r) => ({
                id: `role-${r}`,
                label: t("admin.members.makeRole", { role: t(`role.${r}`) }),
                group: "role",
                onSelect: () => void setRole(m, r),
              })),
              {
                id: "remove",
                label: t("admin.members.remove"),
                icon: <UserMinusIcon />,
                danger: true,
                group: "danger",
                onSelect: () => setRemoving(m),
              },
            ]}
            columns={[
              {
                id: "email",
                header: t("admin.col.account"),
                cell: (m) => (
                  <span className="flex min-w-0 items-center gap-1.5">
                    <Ltr className="truncate font-medium text-foreground">{m.email}</Ltr>
                    {m.is_admin ? <AdminBadge /> : null}
                  </span>
                ),
                sortValue: (m) => m.email,
              },
              {
                id: "role",
                header: t("admin.col.role"),
                cell: (m) => t(`role.${m.role}`),
                sortValue: (m) => ROLES.indexOf(m.role),
              },
              {
                id: "status",
                header: t("admin.col.status"),
                cell: (m) => <StatusBadge status={m.status} />,
                sortValue: (m) => m.status,
              },
              {
                id: "seen",
                header: t("admin.col.lastSeen"),
                cell: (m) => when(m.last_seen_at),
                sortValue: (m) => m.last_seen_at ?? "",
              },
              {
                id: "joined",
                header: t("admin.col.joined"),
                cell: (m) => when(m.joined_at),
                sortValue: (m) => m.joined_at,
              },
            ]}
          />
        </div>
      </Panel>

      <Panel id="admin-ws-audit" title={t("admin.nav.audit")} icon={<ScrollTextIcon />}>
        <AuditTable
          entries={audit.data ?? []}
          loading={!audit.data && !audit.error}
          error={audit.error ? t("common.networkError") : undefined}
          onRetry={() => audit.mutate()}
        />
      </Panel>

      <ActionDialog
        open={!!removing}
        onOpenChange={(o) => !o && setRemoving(null)}
        title={t("admin.members.remove")}
        description={t("admin.members.removeBody", { email: removing?.email ?? "", name: w.name })}
        confirmLabel={t("admin.members.remove")}
        danger
        onSubmit={() => adminActions.removeMember(w.id, removing!.user_id)}
      />
    </div>
  )
}

/** Put an existing account straight in, by email, with a role. */
function AddMember({ w }: { w: AdminWorkspaceDetail }) {
  const { t } = useTranslations()
  const [email, setEmail] = useState("")
  const [role, setRole] = useState<Role>("member")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const items = ROLES.map((r) => ({ value: r, label: t(`role.${r}`) }))

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!email.trim()) return
    setBusy(true)
    setError(null)
    try {
      await adminActions.addMember(w.id, email.trim(), role)
      toast.success(t("admin.members.added", { email: email.trim() }))
      setEmail("")
    } catch (err) {
      setError(errorText(err, t("common.networkError")))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-2" data-admin-add-member>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
        <Field className="sm:flex-1">
          <FieldLabel htmlFor="admin-add-email">{t("admin.members.addLabel")}</FieldLabel>
          <Input
            id="admin-add-email"
            type="email"
            dir="ltr"
            placeholder="name@company.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            autoComplete="off"
          />
        </Field>
        <Field className="sm:w-44">
          <FieldLabel htmlFor="admin-add-role">{t("admin.col.role")}</FieldLabel>
          <Select
            items={items}
            value={role}
            onValueChange={(v) => setRole((v as Role) ?? "member")}
          >
            <SelectTrigger id="admin-add-role" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {items.map((i) => (
                <SelectItem key={i.value} value={i.value}>
                  {i.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Button type="submit" disabled={busy || !email.trim()}>
          <UserPlusIcon />
          {busy ? t("common.working") : t("admin.members.add")}
        </Button>
      </div>
      {error ? (
        <FieldError>{error}</FieldError>
      ) : (
        <p className="text-caption text-muted-foreground">{t("admin.members.addHint")}</p>
      )}
    </form>
  )
}
