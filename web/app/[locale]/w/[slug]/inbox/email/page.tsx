"use client"

import Link from "next/link"
import { useEffect, useState, type FormEvent } from "react"
import { ArrowLeftIcon, MailIcon, PlusIcon, RefreshCwIcon, ShieldCheckIcon, StarIcon, TriangleAlertIcon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Mailbox, type MailboxInput } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMailboxes, useSmtpServiceOptions } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

export default function MailboxesPage() {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("mailbox.title")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")
  const mailboxes = useMailboxes(ws.id)
  const smtp = useSmtpServiceOptions(ws.id, canManage)
  const [adding, setAdding] = useState(false)

  const list = mailboxes.data?.mailboxes ?? []

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("mailbox.title")}
        description={t("mailbox.hint")}
        action={
          <div className="flex items-center gap-2">
            {canManage && list.length > 0 ? (
              <Button size="sm" onClick={() => setAdding(true)} disabled={adding} data-add-mailbox>
                <PlusIcon />
                {t("mailbox.addMailbox")}
              </Button>
            ) : null}
            <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/inbox`} />}>
              <ArrowLeftIcon className="rtl:rotate-180" />
              {t("mailbox.backToInbox")}
            </Button>
          </div>
        }
      />

      <div className="flex flex-col gap-4 px-6 pb-8">
        {mailboxes.error ? (
          <ErrorState error={mailboxes.error} />
        ) : !mailboxes.data ? (
          <LoadingRows rows={3} />
        ) : list.length === 0 && !adding ? (
          <EmptyState
            title={t("mailbox.notConfigured")}
            body={canManage ? t("mailbox.hint") : t("mailbox.managerOnly")}
            action={
              canManage ? (
                <Button onClick={() => setAdding(true)}>
                  <PlusIcon />
                  {t("mailbox.addMailbox")}
                </Button>
              ) : undefined
            }
          />
        ) : (
          <>
            {adding ? (
              <MailboxCard
                smtpOptions={smtp.data?.services ?? []}
                onSaved={() => {
                  setAdding(false)
                  void mailboxes.mutate()
                }}
                onCancel={() => setAdding(false)}
              />
            ) : null}
            {list.map((m) => (
              <MailboxCard
                key={m.id}
                mailbox={m}
                smtpOptions={smtp.data?.services ?? []}
                canManage={canManage}
                onSaved={() => void mailboxes.mutate()}
                formatDate={formatDate}
              />
            ))}
          </>
        )}
      </div>
    </>
  )
}

function MailboxCard({
  mailbox,
  smtpOptions,
  canManage,
  onSaved,
  onCancel,
  formatDate,
}: {
  mailbox?: Mailbox
  smtpOptions: { id: string; name: string; is_default: boolean }[]
  canManage?: boolean
  onSaved: () => void
  onCancel?: () => void
  formatDate?: (d: string) => string
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const isNew = !mailbox
  const [editing, setEditing] = useState(isNew)
  const [name, setName] = useState(mailbox?.name ?? "")
  const [host, setHost] = useState(mailbox?.host ?? "")
  const [port, setPort] = useState(String(mailbox?.port || 993))
  const [username, setUsername] = useState(mailbox?.username ?? "")
  const [password, setPassword] = useState("")
  const [tls, setTls] = useState<"tls" | "starttls" | "none">(mailbox?.tls_mode || "tls")
  const [folder, setFolder] = useState(mailbox?.folder || "INBOX")
  const [smtpServiceId, setSmtpServiceId] = useState(mailbox?.smtp_service_id ?? "")
  const [enabled, setEnabled] = useState(mailbox?.enabled ?? true)
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)
  const [checking, setChecking] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    if (!mailbox) return
    setName(mailbox.name)
    setHost(mailbox.host)
    setPort(String(mailbox.port || 993))
    setUsername(mailbox.username)
    setTls(mailbox.tls_mode || "tls")
    setFolder(mailbox.folder || "INBOX")
    setSmtpServiceId(mailbox.smtp_service_id ?? "")
    setEnabled(mailbox.enabled)
  }, [mailbox])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const body: MailboxInput = {
        name: name.trim() || undefined,
        host: host.trim(),
        port: Number(port) || 993,
        username: username.trim(),
        password,
        tls_mode: tls,
        folder: folder.trim() || "INBOX",
        smtp_service_id: smtpServiceId || "",
        enabled,
      }
      if (mailbox) {
        await api(`/api/w/mailboxes/${encodeURIComponent(mailbox.id)}`, { method: "PUT", json: body, workspaceId: ws.id })
        toast.success(t("mailbox.saved"))
        setEditing(false)
      } else {
        await api("/api/w/mailboxes", { method: "POST", json: body, workspaceId: ws.id })
        toast.success(t("mailbox.created"))
      }
      setPassword("")
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function testConnection() {
    if (!mailbox) return
    setTesting(true)
    try {
      const res = await api<{ mailbox: Mailbox; ok: boolean }>(`/api/w/mailboxes/${encodeURIComponent(mailbox.id)}/test`, {
        method: "POST",
        workspaceId: ws.id,
      })
      if (res.ok) toast.success(t("mailbox.testOk"))
      else toast.error(res.mailbox.last_test_error || t("mailbox.testFailed"))
      onSaved()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setTesting(false)
    }
  }

  async function checkNow() {
    if (!mailbox) return
    setChecking(true)
    try {
      const res = await api<{ filed: number; error?: string }>(`/api/w/mailboxes/${encodeURIComponent(mailbox.id)}/check`, {
        method: "POST",
        workspaceId: ws.id,
      })
      if (res.error) toast.error(res.error)
      else toast.success(t("mailbox.checked", { n: res.filed }))
      onSaved()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setChecking(false)
    }
  }

  async function remove() {
    if (!mailbox) return
    await api(`/api/w/mailboxes/${encodeURIComponent(mailbox.id)}`, { method: "DELETE", workspaceId: ws.id })
    toast.success(t("mailbox.removed"))
    onSaved()
  }

  if (!editing && mailbox) {
    return (
      <div className="rounded-lg border border-grid-border bg-grid-surface p-4" data-mailbox={mailbox.id}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <MailIcon className="size-4 text-grid-muted" />
            <span className="font-medium">
              <Bdi>{mailbox.name}</Bdi>
            </span>
            {mailbox.is_primary ? (
              <Badge variant="secondary" className="gap-1">
                <StarIcon className="size-3" /> {t("mailbox.primary")}
              </Badge>
            ) : null}
            <Badge variant={mailbox.enabled ? "default" : "outline"}>{mailbox.enabled ? t("mailbox.collecting") : t("mailbox.paused")}</Badge>
          </div>
          {canManage ? (
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="outline" size="sm" onClick={testConnection} disabled={testing} data-test-mailbox>
                <ShieldCheckIcon />
                {testing ? t("common.working") : t("mailbox.testConnection")}
              </Button>
              <Button variant="outline" size="sm" onClick={checkNow} disabled={checking} data-check-mailbox>
                <RefreshCwIcon />
                {checking ? t("common.working") : t("mailbox.checkNow")}
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setEditing(true)} data-edit-mailbox>
                {t("common.edit")}
              </Button>
              <ConfirmButton
                label={t("mailbox.remove")}
                title={t("mailbox.removeTitle")}
                description={t("mailbox.removeBody")}
                confirmLabel={t("mailbox.remove")}
                onConfirm={remove}
              />
            </div>
          ) : null}
        </div>
        <div className="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-grid-muted">
          <span dir="ltr">
            {mailbox.username}@{mailbox.host}
          </span>
          <span>{t("mailbox.collected", { n: mailbox.message_count })}</span>
          {mailbox.last_polled_at && formatDate ? <span>{t("mailbox.lastChecked", { date: formatDate(mailbox.last_polled_at) })}</span> : null}
          {mailbox.smtp_service_name ? <span>{t("mailbox.replyVia", { name: mailbox.smtp_service_name })}</span> : null}
        </div>
        {mailbox.last_error ? (
          <p className="mt-2 flex items-start gap-2 border border-grid-danger-text/40 bg-grid-danger-text/5 px-3 py-2 text-sm" role="status" data-mailbox-error>
            <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-danger-text" />
            <span>{mailbox.last_error}</span>
          </p>
        ) : null}
        {mailbox.last_test_at ? (
          <p className={`mt-1 text-xs ${mailbox.last_test_ok ? "text-grid-muted" : "text-grid-danger-text"}`} data-mailbox-test-result>
            {mailbox.last_test_ok ? t("mailbox.testOk") : mailbox.last_test_error || t("mailbox.testFailed")}
          </p>
        ) : null}
      </div>
    )
  }

  return (
    <form onSubmit={save} noValidate className="rounded-lg border border-grid-border bg-grid-surface p-4" data-form={isNew ? "new-mailbox" : "edit-mailbox"}>
      <FieldGroup>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor={`mb-name-${mailbox?.id ?? "new"}`}>{t("mailbox.name")}</FieldLabel>
            <Input id={`mb-name-${mailbox?.id ?? "new"}`} value={name} onChange={(e) => setName(e.target.value)} placeholder={t("mailbox.namePlaceholder")} />
          </Field>
          <Field>
            <FieldLabel htmlFor={`mb-host-${mailbox?.id ?? "new"}`}>{t("mailbox.host")}</FieldLabel>
            <Input id={`mb-host-${mailbox?.id ?? "new"}`} dir="ltr" value={host} onChange={(e) => setHost(e.target.value)} placeholder="imap.gmail.com" />
          </Field>
          <Field>
            <FieldLabel htmlFor={`mb-port-${mailbox?.id ?? "new"}`}>{t("mailbox.port")}</FieldLabel>
            <Input id={`mb-port-${mailbox?.id ?? "new"}`} dir="ltr" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value)} />
            <FieldDescription>{t("mailbox.portHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor={`mb-user-${mailbox?.id ?? "new"}`}>{t("mailbox.username")}</FieldLabel>
            <Input id={`mb-user-${mailbox?.id ?? "new"}`} dir="ltr" value={username} onChange={(e) => setUsername(e.target.value)} placeholder="support@yourdomain.com" />
          </Field>
          <Field>
            <FieldLabel htmlFor={`mb-pass-${mailbox?.id ?? "new"}`}>{t("mailbox.password")}</FieldLabel>
            <Input
              id={`mb-pass-${mailbox?.id ?? "new"}`}
              type="password"
              dir="ltr"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={mailbox?.has_password ? t("mailbox.passwordStored") : ""}
              autoComplete="new-password"
            />
            <FieldDescription>{mailbox?.has_password ? t("mailbox.passwordKeep") : t("mailbox.passwordHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor={`mb-tls-${mailbox?.id ?? "new"}`}>{t("mailbox.security")}</FieldLabel>
            <PickSelect
              id={`mb-tls-${mailbox?.id ?? "new"}`}
              value={tls}
              onChange={(v) => setTls(v as "tls" | "starttls" | "none")}
              options={[
                { value: "tls", label: t("mailbox.tls") },
                { value: "starttls", label: t("mailbox.starttls") },
                { value: "none", label: t("mailbox.none") },
              ]}
            />
            {tls === "none" ? <FieldDescription className="text-grid-danger-text">{t("mailbox.noneWarning")}</FieldDescription> : null}
          </Field>
          <Field>
            <FieldLabel htmlFor={`mb-folder-${mailbox?.id ?? "new"}`}>{t("mailbox.folder")}</FieldLabel>
            <Input id={`mb-folder-${mailbox?.id ?? "new"}`} dir="ltr" value={folder} onChange={(e) => setFolder(e.target.value)} />
          </Field>
          <Field>
            <FieldLabel htmlFor={`mb-smtp-${mailbox?.id ?? "new"}`}>{t("mailbox.smtpService")}</FieldLabel>
            <PickSelect
              id={`mb-smtp-${mailbox?.id ?? "new"}`}
              value={smtpServiceId}
              onChange={setSmtpServiceId}
              noneLabel={t("mailbox.smtpServiceDefault")}
              options={smtpOptions.map((s) => ({ value: s.id, label: s.is_default ? `${s.name} (${t("mailbox.smtpServiceDefault")})` : s.name }))}
            />
            <FieldDescription>{t("mailbox.smtpServiceHint")}</FieldDescription>
          </Field>
        </div>

        <div className="flex flex-wrap items-center gap-3 pt-2">
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={enabled} onCheckedChange={setEnabled} aria-label={t("mailbox.collecting")} />
            {t("mailbox.collectEnabled")}
          </label>
          <Button type="submit" disabled={busy || !host.trim() || !username.trim() || (isNew && !password)}>
            {busy ? t("common.working") : t("common.save")}
          </Button>
          <Button
            type="button"
            variant="ghost"
            onClick={() => {
              if (isNew) onCancel?.()
              else setEditing(false)
            }}
          >
            {t("common.cancel")}
          </Button>
        </div>
        <FieldDescription>{t("mailbox.replyHint")}</FieldDescription>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}
