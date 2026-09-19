"use client"

import { useState, type FormEvent } from "react"
import { MailWarningIcon } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField, Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, INVITABLE_ROLES, type Invitation } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useInvitations, useMembers } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function MembersPage() {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.members")} · ${ws.summary.name}`)

  const canInvite = ws.can("members:invite")
  const members = useMembers(ws.id)
  const invitations = useInvitations(ws.id, canInvite)

  return (
    <>
      <SectionHeader micro={ws.summary.name} title={t("nav.members")} description={t("members.hint")} />

      {members.error ? (
        <ErrorState error={members.error} />
      ) : members.isLoading ? (
        <LoadingRows />
      ) : (members.data?.members ?? []).length === 0 ? (
        <EmptyState title={t("members.empty")} />
      ) : (
        <div className="border-y border-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("members.email")}</TableHead>
                <TableHead>{t("members.role")}</TableHead>
                <TableHead>{t("members.status")}</TableHead>
                <TableHead className="pe-6">{t("members.joined")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(members.data?.members ?? []).map((m) => (
                <TableRow key={m.user_id}>
                  <TableCell className="ps-6">
                    <Ltr>{m.email}</Ltr>
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline">{t(`role.${m.role}`)}</Badge>
                  </TableCell>
                  <TableCell className="text-grid-muted">{t(`status.${m.status}`)}</TableCell>
                  <TableCell className="pe-6 text-grid-muted">
                    <Ltr>{formatDate(m.joined_at)}</Ltr>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {canInvite ? (
        <>
          <div className="grid-separator-contained hatch-band mt-8" aria-hidden />
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
        <p className="px-6 py-6 text-sm text-grid-muted">{t("members.noInvitePermission")}</p>
      ) : null}
    </>
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
      <p className="mt-1 mb-5 flex items-start gap-2 text-sm text-grid-muted">
        <MailWarningIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" />
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
            <Select items={roleItems} value={role} onValueChange={(v) => setRole(String(v ?? "member"))}>
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
        <div className="mt-6 space-y-2 border border-line bg-grid-card p-4" role="status">
          <p className="text-sm font-medium">
            {t("members.linkReady")} <Ltr>{link.email}</Ltr>
          </p>
          <p className="text-xs text-grid-warn-text">{t("members.linkOnce")}</p>
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
      await api(`/api/w/invitations/${encodeURIComponent(id)}`, { method: "DELETE", workspaceId: ws.id })
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
        <div className="border-y border-line">
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
                  <TableCell className="text-grid-muted">
                    <Ltr>{formatDate(inv.expires_at)}</Ltr>
                  </TableCell>
                  <TableCell className="pe-6 text-end">
                    <ConfirmButton
                      label={t("common.revoke")}
                      title={t("members.revokeTitle")}
                      description={t("members.revokeBody", { email: inv.email })}
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
