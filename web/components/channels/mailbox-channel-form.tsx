"use client"

import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { SetupGuide, type SetupGuideStep } from "@/components/channels/setup-guide"
import { PickSelect } from "@/components/crm/shared"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Mailbox, type MailboxInput } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useSmtpServiceOptions } from "@/lib/queries"

const MAILBOX_GUIDE_STEPS: SetupGuideStep[] = [
  {
    titleKey: "channels.email.guide.step1.title",
    bodyKey: "channels.email.guide.step1.body",
  },
  {
    titleKey: "channels.email.guide.step2.title",
    bodyKey: "channels.email.guide.step2.body",
    links: [
      {
        href: "https://myaccount.google.com/apppasswords",
        text: "myaccount.google.com/apppasswords",
      },
    ],
  },
  {
    titleKey: "channels.email.guide.step3.title",
    bodyKey: "channels.email.guide.step3.body",
  },
  {
    titleKey: "channels.email.guide.step4.title",
    warningKey: "channels.email.guide.step4.warning",
  },
  {
    titleKey: "channels.email.guide.step5.title",
    bodyKey: "channels.email.guide.step5.body",
  },
]

/**
 * The mailbox connect/edit form (extracted from inbox/email's MailboxCard for
 * MH-571). The old page keeps its own read-only display card for an existing
 * mailbox and only borrows this component for the actual fields; the new
 * Channels page uses it for both Add and Edit sheets.
 */
export function MailboxChannelForm({
  mailbox,
  onSaved,
  onCancel,
  defaultGuideOpen,
}: {
  mailbox?: Mailbox
  onSaved: () => void
  onCancel?: () => void
  defaultGuideOpen?: boolean
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const smtp = useSmtpServiceOptions(ws.id, true)
  const isNew = !mailbox
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
  const [err, setErr] = useState<string | null>(null)

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
        await api(`/api/w/mailboxes/${encodeURIComponent(mailbox.id)}`, {
          method: "PUT",
          json: body,
          workspaceId: ws.id,
        })
        toast.success(t("mailbox.saved"))
      } else {
        await api("/api/w/mailboxes", { method: "POST", json: body, workspaceId: ws.id })
        toast.success(t("mailbox.created"))
      }
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const smtpOptions = smtp.data?.services ?? []

  return (
    <div className="flex flex-col gap-4">
      <SetupGuide
        id="email"
        titleKey="channels.email.setupTitle"
        steps={MAILBOX_GUIDE_STEPS}
        defaultOpen={defaultGuideOpen ?? isNew}
      />
      <form
        onSubmit={save}
        noValidate
        className="flex flex-col gap-4"
        data-form={isNew ? "new-mailbox" : "edit-mailbox"}
      >
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="mb-name">{t("mailbox.name")}</FieldLabel>
            <Input
              id="mb-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("mailbox.namePlaceholder")}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="mb-host">{t("mailbox.host")}</FieldLabel>
            <Input
              id="mb-host"
              dir="ltr"
              value={host}
              onChange={(e) => setHost(e.target.value)}
              placeholder="imap.gmail.com"
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="mb-port">{t("mailbox.port")}</FieldLabel>
            <Input
              id="mb-port"
              dir="ltr"
              inputMode="numeric"
              value={port}
              onChange={(e) => setPort(e.target.value)}
            />
            <FieldDescription>{t("mailbox.portHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="mb-user">{t("mailbox.username")}</FieldLabel>
            <Input
              id="mb-user"
              dir="ltr"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="support@yourdomain.com"
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="mb-pass">{t("mailbox.password")}</FieldLabel>
            <Input
              id="mb-pass"
              type="password"
              dir="ltr"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={mailbox?.has_password ? t("mailbox.passwordStored") : ""}
              autoComplete="new-password"
            />
            <FieldDescription>
              {mailbox?.has_password ? t("mailbox.passwordKeep") : t("mailbox.passwordHint")}
            </FieldDescription>
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
            {tls === "none" ? (
              <FieldDescription className="text-grid-danger-text">
                {t("mailbox.noneWarning")}
              </FieldDescription>
            ) : null}
          </Field>
          <Field>
            <FieldLabel htmlFor="mb-folder">{t("mailbox.folder")}</FieldLabel>
            <Input
              id="mb-folder"
              dir="ltr"
              value={folder}
              onChange={(e) => setFolder(e.target.value)}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="mb-smtp">{t("mailbox.smtpService")}</FieldLabel>
            <PickSelect
              id="mb-smtp"
              value={smtpServiceId}
              onChange={setSmtpServiceId}
              noneLabel={t("mailbox.smtpServiceDefault")}
              options={smtpOptions.map((s) => ({
                value: s.id,
                label: s.is_default ? `${s.name} (${t("mailbox.smtpServiceDefault")})` : s.name,
              }))}
            />
            <FieldDescription>{t("mailbox.smtpServiceHint")}</FieldDescription>
          </Field>
          <label className="flex items-center gap-2 text-sm">
            <Switch
              checked={enabled}
              onCheckedChange={setEnabled}
              aria-label={t("mailbox.collecting")}
            />
            {t("mailbox.collectEnabled")}
          </label>
        </FieldGroup>
        {err ? <FieldError>{err}</FieldError> : null}
        <div className="flex items-center gap-2">
          <Button
            type="submit"
            disabled={busy || !host.trim() || !username.trim() || (isNew && !password)}
          >
            {busy ? t("common.working") : t("common.save")}
          </Button>
          {onCancel ? (
            <Button type="button" variant="ghost" onClick={onCancel}>
              {t("common.cancel")}
            </Button>
          ) : null}
        </div>
        <FieldDescription>{t("mailbox.replyHint")}</FieldDescription>
      </form>
    </div>
  )
}
