"use client"

import { useEffect, useState, type FormEvent } from "react"
import { CheckCircle2Icon, EllipsisIcon, MailWarningIcon, PencilIcon, PlusIcon, SendIcon, StarIcon, XCircleIcon } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { SectionHeader } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type SmtpService } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMe, useSmtpServices } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

/*
Settings → Email (MH-412): the workspace's SMTP services.

One is the DEFAULT — confirmation emails, inbox replies and tests go through
it — and one is the CAMPAIGN DEFAULT, for a campaign that names none. With
two services, a big campaign and the double-opt-in confirmations no longer
share one provider's rate limits and sender reputation. Passwords are write-
only: the API only ever says whether one is stored.
*/
export default function EmailSettingsPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.email")} · ${ws.summary.name}`)
  const isAdmin = ws.can("mail:settings")
  const services = useSmtpServices(ws.id, isAdmin)
  // null = closed; "new" = adding; a service = editing it.
  const [editing, setEditing] = useState<SmtpService | "new" | null>(null)
  const list = services.data?.services ?? []

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.email")}
        description={t("smtp.hintMany")}
        action={
          isAdmin && services.data ? (
            <Button size="sm" onClick={() => setEditing("new")} data-add-service>
              <PlusIcon />
              {t("smtp.add")}
            </Button>
          ) : undefined
        }
      />
      {!ws.current ? (
        <LoadingRows rows={2} />
      ) : !isAdmin ? (
        <p className="px-6 py-6 text-sm text-grid-muted">{t("smtp.adminOnly")}</p>
      ) : services.error ? (
        <ErrorState error={services.error} />
      ) : !services.data ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          <p className="flex items-start gap-2 px-6 pb-4 text-sm text-grid-muted" data-smtp-state={list.length ? "configured" : "missing"}>
            {list.length ? (
              <>
                <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-grid-brand" />
                <span>{t("smtp.countConfigured", { count: list.length })}</span>
              </>
            ) : (
              <>
                <MailWarningIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" />
                <span>{t("smtp.notConfigured")}</span>
              </>
            )}
          </p>
          {list.length === 0 ? (
            <EmptyState title={t("smtp.emptyTitle")} body={t("smtp.emptyBody")} />
          ) : (
            <ServiceList services={list} onEdit={(s) => setEditing(s)} onChange={() => services.mutate()} />
          )}
          {list.length > 0 ? (
            <>
              <div className="grid-separator-contained hatch-band mt-2" aria-hidden />
              <TestSection services={list} onTested={() => services.mutate()} />
            </>
          ) : null}
        </>
      )}
      <ServiceSheet
        editing={editing}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null)
          services.mutate()
        }}
      />
    </>
  )
}

function TestBadge({ s }: { s: SmtpService }) {
  const { t, formatDate } = useTranslations()
  if (s.last_test_ok === null) {
    return <span className="text-xs text-grid-muted">{t("smtp.neverTested")}</span>
  }
  return (
    <span
      className={`inline-flex items-center gap-1 text-xs ${s.last_test_ok ? "text-grid-muted" : "text-grid-danger-text"}`}
      title={s.last_test_error || undefined}
      data-test-ok={s.last_test_ok ? "yes" : "no"}
    >
      {s.last_test_ok ? <CheckCircle2Icon className="size-3.5 text-grid-brand" /> : <XCircleIcon className="size-3.5" />}
      {s.last_test_ok ? t("smtp.testOk") : t("smtp.testFailed")}
      {s.last_test_at ? (
        <>
          {" · "}
          <Ltr>{formatDate(s.last_test_at)}</Ltr>
        </>
      ) : null}
    </span>
  )
}

function ServiceList({ services, onEdit, onChange }: { services: SmtpService[]; onEdit: (s: SmtpService) => void; onChange: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [removing, setRemoving] = useState<SmtpService | null>(null)
  const [busy, setBusy] = useState(false)

  async function makeDefault(s: SmtpService, role: "transactional" | "campaign") {
    try {
      await api(`/api/w/smtp-services/${encodeURIComponent(s.id)}/default`, { json: { role }, workspaceId: ws.id })
      toast.success(role === "campaign" ? t("smtp.nowCampaignDefault", { name: isolate(s.name) }) : t("smtp.nowDefault", { name: isolate(s.name) }))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function remove() {
    if (!removing) return
    setBusy(true)
    try {
      await api(`/api/w/smtp-services/${encodeURIComponent(removing.id)}`, { method: "DELETE", workspaceId: ws.id })
      toast.success(t("smtp.removedOne", { name: isolate(removing.name) }))
      setRemoving(null)
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  // The default can only go last: the API refuses, so the menu says why
  // instead of offering a delete that cannot happen.
  const defaultLocked = (s: SmtpService) => s.is_default && services.length > 1

  return (
    <ul className="border-y border-line" data-smtp-services>
      {services.map((s) => (
        <li key={s.id} className="flex flex-col gap-3 border-b border-line px-6 py-4 last:border-b-0 sm:flex-row sm:items-start" data-service={s.name}>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">
                <Bdi>{s.name}</Bdi>
              </span>
              {s.is_default ? (
                <Badge variant="default" data-role="default">
                  {t("smtp.roleDefault")}
                </Badge>
              ) : null}
              {s.is_campaign_default ? (
                <Badge variant="secondary" data-role="campaign">
                  {t("smtp.roleCampaign")}
                </Badge>
              ) : null}
            </div>
            <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-sm text-grid-muted">
              <Ltr>
                {s.host}:{s.port}
              </Ltr>
              <Ltr>{s.from_name ? `${s.from_name} <${s.from_email}>` : s.from_email}</Ltr>
              <span>{t(`smtp.tls.${s.tls_mode}`)}</span>
              {s.campaigns > 0 ? <span>{t("smtp.usedBy", { count: s.campaigns })}</span> : null}
            </div>
            <div className="mt-1">
              <TestBadge s={s} />
            </div>
          </div>
          <div className="flex shrink-0 items-center gap-1">
            <Button variant="ghost" size="sm" onClick={() => onEdit(s)} data-edit-service={s.name}>
              <PencilIcon />
              {t("smtp.edit")}
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger render={<Button variant="ghost" size="icon-sm" aria-label={t("common.actions")} data-service-menu={s.name} />}>
                <EllipsisIcon />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="min-w-60">
                <DropdownMenuItem disabled={s.is_default} onClick={() => makeDefault(s, "transactional")} data-make-default>
                  <StarIcon />
                  {t("smtp.makeDefault")}
                </DropdownMenuItem>
                <DropdownMenuItem disabled={s.is_campaign_default} onClick={() => makeDefault(s, "campaign")} data-make-campaign-default>
                  <SendIcon />
                  {t("smtp.makeCampaignDefault")}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" disabled={defaultLocked(s)} onClick={() => setRemoving(s)} data-remove-service>
                  <XCircleIcon />
                  {defaultLocked(s) ? t("smtp.removeDefaultLocked") : t("smtp.remove")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </li>
      ))}
      <AlertDialog open={!!removing} onOpenChange={(o) => (!o ? setRemoving(null) : undefined)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("smtp.removeOneTitle", { name: isolate(removing?.name ?? "") })}</AlertDialogTitle>
            <AlertDialogDescription>
              {removing?.is_default ? t("smtp.removeBody") : t("smtp.removeOneBody", { count: removing?.campaigns ?? 0 })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" disabled={busy} onClick={remove}>
              {busy ? t("common.working") : t("smtp.remove")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </ul>
  )
}

function ServiceSheet({ editing, onClose, onSaved }: { editing: SmtpService | "new" | null; onClose: () => void; onSaved: () => void }) {
  const { t, isRtl } = useTranslations()
  const current = editing && editing !== "new" ? editing : null
  return (
    <Sheet open={!!editing} onOpenChange={(open) => (!open ? onClose() : undefined)}>
      <SheetContent
        side={isRtl ? "left" : "right"}
        className="w-full overflow-y-auto data-[side=left]:w-full data-[side=right]:w-full data-[side=left]:sm:max-w-xl data-[side=right]:sm:max-w-xl"
        data-service-sheet
      >
        <SheetHeader className="pe-12">
          <SheetTitle>{current ? t("smtp.editTitle", { name: isolate(current.name) }) : t("smtp.addTitle")}</SheetTitle>
          <SheetDescription>{t("smtp.formHint")}</SheetDescription>
        </SheetHeader>
        <div className="px-4 pb-6">{editing ? <SmtpForm key={current?.id ?? "new"} current={current} onSaved={onSaved} onCancel={onClose} /> : null}</div>
      </SheetContent>
    </Sheet>
  )
}

function SmtpForm({ current, onSaved, onCancel }: { current: SmtpService | null; onSaved: () => void; onCancel: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState(current?.name ?? "")
  const [host, setHost] = useState(current?.host ?? "")
  const [port, setPort] = useState(String(current?.port || 587))
  const [tls, setTls] = useState<string>(current?.tls_mode || "starttls")
  const [username, setUsername] = useState(current?.username ?? "")
  const [password, setPassword] = useState("")
  const [fromName, setFromName] = useState(current?.from_name ?? "")
  const [fromEmail, setFromEmail] = useState(current?.from_email ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => setErr(null), [current])

  const tlsItems = (["starttls", "tls", "none"] as const).map((v) => ({ value: v, label: t(`smtp.tls.${v}`) }))

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const payload: Record<string, unknown> = {
        name: name.trim(),
        host: host.trim(),
        port: Number(port),
        tls_mode: tls,
        username: username.trim(),
        from_name: fromName.trim(),
        from_email: fromEmail.trim(),
      }
      // Empty keeps the stored password; typing one replaces it.
      if (password !== "" || !current?.has_password) payload.password = password
      await api(current ? `/api/w/smtp-services/${encodeURIComponent(current.id)}` : "/api/w/smtp-services", {
        method: current ? "PUT" : "POST",
        json: payload,
        workspaceId: ws.id,
      })
      setPassword("")
      toast.success(current ? t("smtp.saved") : t("smtp.added", { name: isolate(name.trim()) }))
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate data-form="smtp">
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="smtp-name">{t("smtp.name")}</FieldLabel>
          <Input id="smtp-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} placeholder={t("smtp.namePlaceholder")} />
          <FieldDescription>{t("smtp.nameHint")}</FieldDescription>
        </Field>
        <div className="flex flex-col gap-4 sm:flex-row">
          <Field className="sm:flex-1">
            <FieldLabel htmlFor="smtp-host">{t("smtp.host")}</FieldLabel>
            <Input id="smtp-host" dir="ltr" value={host} onChange={(e) => setHost(e.target.value)} placeholder="smtp.example.com" />
          </Field>
          <Field className="sm:w-28">
            <FieldLabel htmlFor="smtp-port">{t("smtp.port")}</FieldLabel>
            <Input id="smtp-port" dir="ltr" inputMode="numeric" value={port} onChange={(e) => setPort(e.target.value.replace(/\D/g, ""))} />
          </Field>
        </div>
        <Field>
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
              placeholder={current?.has_password ? t("smtp.passwordKeep") : ""}
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
          <Button type="submit" disabled={busy || !name.trim() || !host.trim() || !port || !fromEmail.trim()}>
            {busy ? t("common.working") : current ? t("smtp.save") : t("smtp.add")}
          </Button>
          <Button type="button" variant="ghost" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
        </div>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}

function TestSection({ services, onTested }: { services: SmtpService[]; onTested: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const me = useMe()
  const [to, setTo] = useState("")
  const [serviceId, setServiceId] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const target = to || me.data?.email || ""
  const chosen = services.find((s) => s.id === serviceId) ?? services[0]
  const items = services.map((s) => ({ value: s.id, label: s.name }))

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (!chosen) return
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/smtp-services/${encodeURIComponent(chosen.id)}/test`, { json: { to: target.trim() }, workspaceId: ws.id })
      toast.success(t("smtp.testSentVia", { email: target.trim(), name: isolate(chosen.name) }))
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
      onTested()
    }
  }

  return (
    <section className="px-6 py-6 pb-10">
      <h2 className="text-base font-medium">{t("smtp.testTitle")}</h2>
      <form onSubmit={onSubmit} noValidate className="mt-4 max-w-3xl" data-form="smtp-test">
        <FieldGroup className="sm:flex-row sm:items-end">
          <Field className="sm:w-56">
            <FieldLabel htmlFor="smtp-test-service">{t("smtp.testService")}</FieldLabel>
            <Select items={items} value={chosen?.id ?? ""} onValueChange={(v) => setServiceId(String(v ?? ""))}>
              <SelectTrigger id="smtp-test-service" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {items.map((i) => (
                  <SelectItem key={i.value} value={i.value}>
                    <Bdi>{i.label}</Bdi>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field className="sm:flex-1">
            <FieldLabel htmlFor="smtp-test-to">{t("smtp.testTo")}</FieldLabel>
            <Input id="smtp-test-to" type="email" dir="ltr" value={target} onChange={(e) => setTo(e.target.value)} />
          </Field>
          <Button type="submit" variant="outline" disabled={busy || !target.trim() || !chosen}>
            {busy ? t("common.working") : t("smtp.test")}
          </Button>
        </FieldGroup>
        {err ? <FieldError className="mt-3">{err}</FieldError> : null}
      </form>
    </section>
  )
}
