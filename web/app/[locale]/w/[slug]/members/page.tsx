"use client"

import { useState, type FormEvent } from "react"
import { MailWarningIcon } from "lucide-react"
import { Badge, Button, Field, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, toast } from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError } from "@/components/form-layout"
import { mutate } from "swr"

import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField, Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, INVITABLE_ROLES, type Invitation, type Member, type Role } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useInvitations, useMe, useMembers } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { isolate } from "@/components/bidi"

export default function MembersPage() {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.members")} · ${ws.summary.name}`)

  const canInvite = ws.can("members:invite")
  const members = useMembers(ws.id)
  const invitations = useInvitations(ws.id, canInvite)
  const myId = String(useMe().data?.id ?? "")
  const myRole = ws.summary.role
  // Admins and owners manage the others (MH-679); only an owner touches an
  // owner. The server enforces all of it — this just hides what would 403.
  const canManage = ws.can("members:manage")
  const manageable = (m: Member) =>
    canManage && m.user_id !== myId && (m.role !== "owner" || myRole === "owner")

  async function remove(m: Member) {
    try {
      const res = await api<{ members: Member[] | null }>(
        `/api/w/members/${encodeURIComponent(m.user_id)}`,
        { method: "DELETE", workspaceId: ws.id },
      )
      await members.mutate({ members: res.members ?? [] }, false)
      void mutate("/api/me/workspaces")
      toast.success(t("members.removed", { email: isolate(m.email) }))
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    }
  }

  return (
    <>
      <SectionHeader
        title={t("nav.members")}
        description={t("members.hint")}
      />

      {members.error ? (
        <ErrorState error={members.error} />
      ) : members.isLoading ? (
        <LoadingRows />
      ) : (members.data?.members ?? []).length === 0 ? (
        <EmptyState title={t("members.empty")} />
      ) : (
        <div className="border-y border-nq-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("members.email")}</TableHead>
                <TableHead>{t("members.role")}</TableHead>
                <TableHead>{t("members.status")}</TableHead>
                <TableHead className={canManage ? undefined : "pe-6"}>
                  {t("members.joined")}
                </TableHead>
                {canManage ? (
                  <TableHead className="pe-6 text-end">
                    <span className="sr-only">{t("common.actions")}</span>
                  </TableHead>
                ) : null}
              </TableRow>
            </TableHeader>
            <TableBody>
              {(members.data?.members ?? []).map((m) => (
                <TableRow key={m.user_id} data-member={m.email}>
                  <TableCell className="ps-6">
                    <Ltr>{m.email}</Ltr>
                    {m.user_id === myId ? (
                      <span className="ms-2 text-xs text-muted-foreground">{t("members.you")}</span>
                    ) : null}
                  </TableCell>
                  <TableCell>
                    {manageable(m) ? (
                      <MemberRoleSelect
                        member={m}
                        onChanged={(list) => members.mutate({ members: list }, false)}
                      />
                    ) : (
                      <Badge variant="outline">{t(`role.${m.role}`)}</Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-muted-foreground">{t(`status.${m.status}`)}</TableCell>
                  <TableCell className={canManage ? "text-muted-foreground" : "pe-6 text-muted-foreground"}>
                    <Ltr>{formatDate(m.joined_at)}</Ltr>
                  </TableCell>
                  {canManage ? (
                    <TableCell className="pe-6 text-end">
                      {manageable(m) ? (
                        <ConfirmButton
                          label={t("members.remove")}
                          title={t("members.removeTitle")}
                          description={t("members.removeBody", { email: isolate(m.email) })}
                          confirmLabel={t("members.remove")}
                          onConfirm={() => remove(m)}
                        />
                      ) : null}
                    </TableCell>
                  ) : null}
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {canInvite ? (
        <>
          <div className="hatch h-8 border-y border-border mt-8" aria-hidden />
          <InviteSection />
          <PendingInvitations
            loading={invitations.isLoading}
            error={invitations.error}
            items={invitations.data?.invitations ?? []}
            onRevoked={() => invitations.mutate()}
            formatDate={formatDate}
          />
        </>
      ) : ws.current ? (
        <p className="px-6 py-6 text-sm text-muted-foreground">{t("members.noInvitePermission")}</p>
      ) : null}
    </>
  )
}

const ROLE_ORDER: Role[] = ["owner", "admin", "manager", "member", "client"]

/** A member's role, changeable in place — offering only the roles the
 * signed-in user may grant: never above their own, and owner only by an owner. */
function MemberRoleSelect({
  member,
  onChanged,
}: {
  member: Member
  onChanged: (list: Member[]) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)
  const mine = ROLE_ORDER.indexOf(ws.summary.role)
  const items = ROLE_ORDER.filter((r) => ROLE_ORDER.indexOf(r) >= mine).map((r) => ({
    value: r,
    label: t(`role.${r}`),
  }))

  async function change(role: string) {
    if (role === member.role) return
    setBusy(true)
    try {
      const res = await api<{ members: Member[] | null }>(
        `/api/w/members/${encodeURIComponent(member.user_id)}`,
        { method: "PUT", json: { role }, workspaceId: ws.id },
      )
      onChanged(res.members ?? [])
      toast.success(
        t("members.roleChanged", { email: isolate(member.email), role: t(`role.${role}`) }),
      )
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Select
      items={items}
      value={member.role}
      onValueChange={(v) => void change(String(v ?? member.role))}
    >
      <SelectTrigger
        className="w-36"
        disabled={busy}
        aria-label={t("members.roleFor", { email: member.email })}
        data-member-role
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {items.map((r) => (
          <SelectItem key={r.value} value={r.value}>
            {r.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

function InviteSection() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const invitations = useInvitations(ws.id)
  const [email, setEmail] = useState("")
  const [role, setRole] = useState<string>("member")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [link, setLink] = useState<{ url: string; email: string } | null>(null)

  const roleItems = INVITABLE_ROLES.map((r) => ({ value: r, label: t(`role.${r}`) }))

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const res = await api<{ invitation: Invitation; accept_url: string }>("/api/w/invitations", {
        json: { email: email.trim(), role },
        workspaceId: ws.id,
      })
      const path = res.accept_url.startsWith("/") ? res.accept_url : `/${res.accept_url}`
      setLink({ url: `${window.location.origin}/${locale}${path}`, email: email.trim() })
      setEmail("")
      await invitations.mutate()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="px-6 py-6">
      <h2 className="text-base font-medium">{t("members.inviteTitle")}</h2>
      {/* P0 sends no email — say so plainly instead of implying it does. */}
      <p className="mt-1 mb-5 flex items-start gap-2 text-sm text-muted-foreground">
        <MailWarningIcon className="mt-0.5 size-4 shrink-0 text-nq-warning" />
        {t("members.noEmail")}
      </p>
      <form onSubmit={onSubmit} noValidate>
        <FieldGroup className="sm:flex-row sm:items-end">
          <Field className="sm:flex-1">
            <FieldLabel htmlFor="invite-email">{t("members.email")}</FieldLabel>
            <Input
              id="invite-email"
              type="email"
              dir="ltr"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="teammate@company.com"
            />
          </Field>
          <Field className="sm:w-44">
            <FieldLabel htmlFor="invite-role">{t("members.role")}</FieldLabel>
            <Select
              items={roleItems}
              value={role}
              onValueChange={(v) => setRole(String(v ?? "member"))}
            >
              <SelectTrigger id="invite-role" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {roleItems.map((r) => (
                  <SelectItem key={r.value} value={r.value}>
                    {r.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Button type="submit" disabled={busy || !email.trim()}>
            {busy ? t("common.working") : t("members.invite")}
          </Button>
        </FieldGroup>
        {error ? <FieldError className="mt-3">{error}</FieldError> : null}
      </form>

      {link ? (
        <div className="mt-6 space-y-2 border border-nq-line bg-card p-4" role="status">
          <p className="text-sm font-medium">
            {t("members.linkReady")} <Ltr>{link.email}</Ltr>
          </p>
          <p className="text-xs text-nq-warning-text">{t("members.linkOnce")}</p>
          <CopyField value={link.url} label={t("members.acceptLink")} />
          <div className="flex justify-end">
            <Button variant="ghost" size="sm" onClick={() => setLink(null)}>
              {t("common.done")}
            </Button>
          </div>
        </div>
      ) : null}
    </section>
  )
}

function PendingInvitations({
  loading,
  error,
  items,
  onRevoked,
  formatDate,
}: {
  loading: boolean
  error: unknown
  items: Invitation[]
  onRevoked: () => void
  formatDate: (v: string | null | undefined) => string
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()

  async function revoke(id: string) {
    try {
      await api(`/api/w/invitations/${encodeURIComponent(id)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      toast.success(t("members.revoked"))
      onRevoked()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    }
  }

  return (
    <section className="pb-8">
      <h2 className="px-6 pb-3 text-base font-medium">{t("members.pending")}</h2>
      {error ? (
        <ErrorState error={error} />
      ) : loading ? (
        <LoadingRows rows={2} />
      ) : items.length === 0 ? (
        <EmptyState title={t("members.noPending")} />
      ) : (
        <div className="border-y border-nq-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("members.email")}</TableHead>
                <TableHead>{t("members.role")}</TableHead>
                <TableHead>{t("members.expires")}</TableHead>
                <TableHead className="pe-6 text-end">
                  <span className="sr-only">{t("common.actions")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((inv) => (
                <TableRow key={inv.id}>
                  <TableCell className="ps-6">
                    <Ltr>{inv.email}</Ltr>
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">{t(`role.${inv.role}`)}</Badge>
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    <Ltr>{formatDate(inv.expires_at)}</Ltr>
                  </TableCell>
                  <TableCell className="pe-6 text-end">
                    <ConfirmButton
                      label={t("common.revoke")}
                      title={t("members.revokeTitle")}
                      description={t("members.revokeBody", { email: isolate(inv.email) })}
                      confirmLabel={t("common.revoke")}
                      onConfirm={() => revoke(inv.id)}
                    />
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
