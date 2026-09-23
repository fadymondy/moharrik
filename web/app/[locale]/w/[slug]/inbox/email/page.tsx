"use client"

import Link from "next/link"
import { useEffect, useState, type FormEvent } from "react"
import { ArrowLeftIcon, MailIcon, RefreshCwIcon, TriangleAlertIcon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type MailboxSettings } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMailbox } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

export default function MailboxPage() {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("mailbox.title")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")
  const mailbox = useMailbox(ws.id)

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("mailbox.title")}
        description={t("mailbox.hint")}
        action={
          <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/inbox`} />}>
            <ArrowLeftIcon className="rtl:rotate-180" />
            {t("mailbox.backToInbox")}
          </Button>
        }
      />

      {mailbox.error ? (
        <ErrorState error={mailbox.error} />
      ) : !mailbox.data ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2 px-6 pb-4 text-sm" data-mailbox-configured={mailbox.data.configured ? "true" : "false"}>
            <MailIcon className="size-4 text-grid-muted" />
            {mailbox.data.configured ? (
              <>
                <Badge variant={mailbox.data.enabled ? "default" : "outline"}>
                  {mailbox.data.enabled ? t("mailbox.collecting") : t("mailbox.paused")}
                </Badge>
                <span className="text-grid-muted">
                  {t("mailbox.collected", { n: mailbox.data.message_count })}
                  {mailbox.data.last_polled_at ? ` · ${t("mailbox.lastChecked", { date: formatDate(mailbox.data.last_polled_at) })}` : ""}
                </span>
              </>
            ) : (
              <span className="text-grid-muted">{t("mailbox.notConfigured")}</span>
            )}
          </div>

          {mailbox.data.last_error ? (
            <p className="mx-6 mb-4 flex items-start gap-2 border border-grid-danger-text/40 bg-grid-danger-text/5 px-4 py-3 text-sm" role="status" data-mailbox-error>
              <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-danger-text" />
              {/* The mail server's own words: "authentication failed" tells
                  someone what to change; "could not connect" does not. */}
              <span>{mailbox.data.last_error}</span>
            </p>
          ) : null}

          {canManage ? (
            <MailboxForm settings={mailbox.data} onSaved={() => mailbox.mutate()} />
          ) : (
            <p className="px-6 text-sm text-grid-muted">{t("mailbox.managerOnly")}</p>
          )}
        </>
      )}
      <div className="pb-8" />
    </>
  )
}

function MailboxForm({ settings, onSaved }: { settings: MailboxSettings; onSaved: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [host, setHost] = useState(settings.host)
  const [port, setPort] = useState(String(settings.port || 993))
  const [username, setUsername] = useState(settings.username)
  const [password, setPassword] = useState("")
  const [tls, setTls] = useState<"tls" | "starttls" | "none">(settings.tls_mode || "tls")
  const [folder, setFolder] = useState(settings.folder || "INBOX")
  const [enabled, setEnabled] = useState(settings.enabled)
  const [busy, setBusy] = useState(false)
  const [checking, setChecking] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  // Seeded from the server on every load: the fields must show what is stored,
  // not what this component happened to mount with.
  useEffect(() => {
    setHost(settings.host)
    setPort(String(settings.port || 993))
    setUsername(settings.username)
    setTls(settings.tls_mode || "tls")
    setFolder(settings.folder || "INBOX")
    setEnabled(settings.enabled)
  }, [settings])

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/mailbox", {
        method: "PUT",
        json: {
          host: host.trim(),
          port: Number(port) || 993,
          username: username.trim(),
          password, // empty keeps the stored one
          tls_mode: tls,
          folder: folder.trim() || "INBOX",
          enabled,
        },
        workspaceId: ws.id,
      })
      setPassword("")
      toast.success(t("mailbox.saved"))
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function checkNow() {
    setChecking(true)
    try {
      const res = await api<{ filed: number; error?: string }>("/api/w/mailbox/check", { method: "POST", workspaceId: ws.id })
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
    await api("/api/w/mailbox", { method: "DELETE", workspaceId: ws.id })
    toast.success(t("mailbox.removed"))
    onSaved()
  }

  return (
    <form onSubmit={save} noValidate className="px-6" data-form="mailbox">
      <FieldGroup>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="mb-host">{t("mailbox.host")}</FieldLabel>
            <Input id="mb-host" dir="ltr" value={host} onChange={(e) => setHost(e.target.value)} placeholder="imap.gmail.com" />
          </Field>
          <Field>
            <FieldLabel htmlFor="mb-port">{t("mailbox.port")}</FieldLabel>
            <Input id="mb-port" dir="ltr" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value)} />
            <FieldDescription>{t("mailbox.portHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="mb-user">{t("mailbox.username")}</FieldLabel>
            <Input id="mb-user" dir="ltr" value={username} onChange={(e) => setUsername(e.target.value)} placeholder="support@yourdomain.com" />
          </Field>
          <Field>
            <FieldLabel htmlFor="mb-pass">{t("mailbox.password")}</FieldLabel>
            <Input
              id="mb-pass"
              type="password"
              dir="ltr"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={settings.has_password ? t("mailbox.passwordStored") : ""}
              autoComplete="new-password"
            />
            <FieldDescription>{settings.has_password ? t("mailbox.passwordKeep") : t("mailbox.passwordHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="mb-tls">{t("mailbox.security")}</FieldLabel>
            <PickSelect
              id="mb-tls"
              value={tls}
              onChange={(v) => setTls(v as "tls" | "starttls" | "none")}
              options={[
                { value: "tls", label: t("mailbox.tls") },
                { value: "starttls", label: t("mailbox.starttls") },
                { value: "none", label: t("mailbox.none") },
              ]}
            />
            {/* Said out loud rather than left to the label: this sends the
                mailbox password in the clear. */}
            {tls === "none" ? <FieldDescription className="text-grid-danger-text">{t("mailbox.noneWarning")}</FieldDescription> : null}
          </Field>
          <Field>
            <FieldLabel htmlFor="mb-folder">{t("mailbox.folder")}</FieldLabel>
            <Input id="mb-folder" dir="ltr" value={folder} onChange={(e) => setFolder(e.target.value)} />
          </Field>
        </div>

        <div className="flex flex-wrap items-center gap-3 pt-2">
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={enabled} onCheckedChange={setEnabled} aria-label={t("mailbox.collecting")} />
            {t("mailbox.collectEnabled")}
          </label>
          <Button type="submit" disabled={busy || !host.trim() || !username.trim()}>
            {busy ? t("common.working") : t("common.save")}
          </Button>
          {settings.configured ? (
            <>
              <Button type="button" variant="outline" size="sm" onClick={checkNow} disabled={checking}>
                <RefreshCwIcon />
                {checking ? t("common.working") : t("mailbox.checkNow")}
              </Button>
              <ConfirmButton
                label={t("mailbox.remove")}
                title={t("mailbox.removeTitle")}
                description={t("mailbox.removeBody")}
                confirmLabel={t("mailbox.remove")}
                onConfirm={remove}
              />
            </>
          ) : null}
        </div>
        <FieldDescription>{t("mailbox.replyHint")}</FieldDescription>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}
