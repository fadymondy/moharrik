"use client"

import { useEffect, useState, type FormEvent } from "react"
import { CheckCircle2Icon, MailWarningIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type SmtpSettings } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMe, useSmtpSettings } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

export default function EmailSettingsPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.email")} · ${ws.summary.name}`)
  const isAdmin = ws.can("mail:settings")
  const smtp = useSmtpSettings(ws.id, isAdmin)

  return (
    <>
      <SectionHeader micro={<Bdi>{ws.summary.name}</Bdi>} title={t("nav.email")} description={t("smtp.hint")} />
      {!ws.current ? (
        <LoadingRows rows={2} />
      ) : !isAdmin ? (
        <p className="px-6 py-6 text-sm text-grid-muted">{t("smtp.adminOnly")}</p>
      ) : smtp.error ? (
        <ErrorState error={smtp.error} />
      ) : !smtp.data ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          <SmtpForm current={smtp.data} onSaved={(s) => smtp.mutate(s, { revalidate: true })} />
          {smtp.data.configured ? (
            <>
              <div className="grid-separator-contained hatch-band mt-2" aria-hidden />
              <TestSection />
            </>
          ) : null}
        </>
      )}
    </>
  )
}

function SmtpForm({ current, onSaved }: { current: SmtpSettings; onSaved: (s?: SmtpSettings) => void }) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const [host, setHost] = useState(current.host)
  const [port, setPort] = useState(String(current.port || 587))
  const [tls, setTls] = useState<string>(current.tls_mode || "starttls")
  const [username, setUsername] = useState(current.username)
  const [password, setPassword] = useState("")
  const [fromName, setFromName] = useState(current.from_name)
  const [fromEmail, setFromEmail] = useState(current.from_email)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    setHost(current.host)
    setPort(String(current.port || 587))
    setTls(current.tls_mode || "starttls")
    setUsername(current.username)
    setFromName(current.from_name)
    setFromEmail(current.from_email)
  }, [current])

  const tlsItems = (["starttls", "tls", "none"] as const).map((v) => ({ value: v, label: t(`smtp.tls.${v}`) }))

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const payload: Record<string, unknown> = {
        host: host.trim(),
        port: Number(port),
        tls_mode: tls,
        username: username.trim(),
        from_name: fromName.trim(),
        from_email: fromEmail.trim(),
      }
      // Empty keeps the stored password; typing one replaces it.
      if (password !== "" || !current.has_password) payload.password = password
      const res = await api<SmtpSettings>("/api/w/mail/smtp", { method: "PUT", json: payload, workspaceId: ws.id })
      setPassword("")
      toast.success(t("smtp.saved"))
      onSaved(res)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    try {
      await api("/api/w/mail/smtp", { method: "DELETE", workspaceId: ws.id })
      toast.success(t("smtp.removed"))
      onSaved(undefined)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <section className="px-6 pb-8">
      <p className="mb-5 flex items-start gap-2 text-sm text-grid-muted" data-smtp-state={current.configured ? "configured" : "missing"}>
        {current.configured ? (
          <>
            <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-grid-brand" />
            <span>
              {t("smtp.configured")}
              {current.updated_at ? (
                <>
                  {" · "}
                  <Ltr>{formatDate(current.updated_at)}</Ltr>
                </>
              ) : null}
            </span>
          </>
        ) : (
          <>
            <MailWarningIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" />
            <span>{t("smtp.notConfigured")}</span>
          </>
        )}
      </p>
      <form onSubmit={onSubmit} noValidate className="max-w-3xl" data-form="smtp">
        <FieldGroup>
          <div className="flex flex-col gap-4 sm:flex-row">
            <Field className="sm:flex-1">
              <FieldLabel htmlFor="smtp-host">{t("smtp.host")}</FieldLabel>
              <Input id="smtp-host" dir="ltr" value={host} onChange={(e) => setHost(e.target.value)} placeholder="smtp.example.com" />
            </Field>
            <Field className="sm:w-28">
              <FieldLabel htmlFor="smtp-port">{t("smtp.port")}</FieldLabel>
              <Input id="smtp-port" dir="ltr" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value.replace(/\D/g, ""))} />
            </Field>
            <Field className="sm:w-56">
              <FieldLabel htmlFor="smtp-tls">{t("smtp.security")}</FieldLabel>
              <Select items={tlsItems} value={tls} onValueChange={(v) => setTls(String(v ?? "starttls"))}>
                <SelectTrigger id="smtp-tls" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {tlsItems.map((i) => (
                    <SelectItem key={i.value} value={i.value}>
                      {i.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </div>
          <div className="flex flex-col gap-4 sm:flex-row">
            <Field className="sm:flex-1">
              <FieldLabel htmlFor="smtp-username">
                {t("smtp.username")} <span className="text-grid-muted">({t("common.optional")})</span>
              </FieldLabel>
              <Input id="smtp-username" dir="ltr" autoComplete="off" value={username} onChange={(e) => setUsername(e.target.value)} />
            </Field>
            <Field className="sm:flex-1">
              <FieldLabel htmlFor="smtp-password">{t("smtp.password")}</FieldLabel>
              <Input
                id="smtp-password"
                type="password"
                dir="ltr"
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={current.has_password ? t("smtp.passwordKeep") : ""}
              />
            </Field>
          </div>
          <div className="flex flex-col gap-4 sm:flex-row">
            <Field className="sm:flex-1">
              <FieldLabel htmlFor="smtp-from-name">
                {t("smtp.fromName")} <span className="text-grid-muted">({t("common.optional")})</span>
              </FieldLabel>
              <Input id="smtp-from-name" value={fromName} onChange={(e) => setFromName(e.target.value)} />
            </Field>
            <Field className="sm:flex-1">
              <FieldLabel htmlFor="smtp-from-email">{t("smtp.fromEmail")}</FieldLabel>
              <Input id="smtp-from-email" type="email" dir="ltr" value={fromEmail} onChange={(e) => setFromEmail(e.target.value)} placeholder="news@company.com" />
            </Field>
          </div>
          <div className="flex items-center gap-2">
            <Button type="submit" disabled={busy || !host.trim() || !port || !fromEmail.trim()}>
              {busy ? t("common.working") : t("smtp.save")}
            </Button>
            {current.configured ? (
              <ConfirmButton
                label={t("smtp.remove")}
                title={t("smtp.removeTitle")}
                description={t("smtp.removeBody")}
                confirmLabel={t("smtp.remove")}
                onConfirm={remove}
              />
            ) : null}
          </div>
        </FieldGroup>
        {err ? <FieldError className="mt-3">{err}</FieldError> : null}
      </form>
    </section>
  )
}

function TestSection() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const me = useMe()
  const [to, setTo] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const target = to || me.data?.email || ""

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/mail/smtp/test", { json: { to: target.trim() }, workspaceId: ws.id })
      toast.success(t("smtp.testSent", { email: target.trim() }))
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="px-6 py-6 pb-10">
      <h2 className="text-base font-medium">{t("smtp.testTitle")}</h2>
      <form onSubmit={onSubmit} noValidate className="mt-4 max-w-3xl" data-form="smtp-test">
        <FieldGroup className="sm:flex-row sm:items-end">
          <Field className="sm:flex-1">
            <FieldLabel htmlFor="smtp-test-to">{t("smtp.testTo")}</FieldLabel>
            <Input id="smtp-test-to" type="email" dir="ltr" value={target} onChange={(e) => setTo(e.target.value)} />
          </Field>
          <Button type="submit" variant="outline" disabled={busy || !target.trim()}>
            {busy ? t("common.working") : t("smtp.test")}
          </Button>
        </FieldGroup>
        {err ? <FieldError className="mt-3">{err}</FieldError> : null}
      </form>
    </section>
  )
}
