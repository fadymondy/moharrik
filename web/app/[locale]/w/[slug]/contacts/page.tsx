"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { MailWarningIcon } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Contact, type MailList, type SubscriptionStatus } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useContacts, useMailLists, useSmtpSettings } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

const statusVariant: Record<SubscriptionStatus, "default" | "secondary" | "outline" | "destructive"> = {
  subscribed: "default",
  pending: "secondary",
  unsubscribed: "outline",
  bounced: "destructive",
}

export default function ContactsPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.contacts")} · ${ws.summary.name}`)

  const canWrite = ws.can("contacts:write")
  const lists = useMailLists(ws.id)
  const contacts = useContacts(ws.id)
  const listItems = lists.data?.lists ?? []

  return (
    <>
      <SectionHeader micro={ws.summary.name} title={t("nav.contacts")} description={t("contacts.hint")} />
      <ListsSection lists={listItems} loading={lists.isLoading} error={lists.error} onChange={() => { lists.mutate(); contacts.mutate() }} />

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />

      {canWrite ? (
        <AddContact lists={listItems} onAdded={() => { contacts.mutate(); lists.mutate() }} />
      ) : ws.current ? (
        <p className="px-6 py-6 text-sm text-grid-muted">{t("contacts.readOnly")}</p>
      ) : null}

      {contacts.error ? (
        <ErrorState error={contacts.error} />
      ) : contacts.isLoading ? (
        <LoadingRows />
      ) : (contacts.data?.contacts ?? []).length === 0 ? (
        <EmptyState title={t("contacts.empty")} body={canWrite ? t("contacts.emptyBody") : undefined} />
      ) : (
        <ContactsTable items={contacts.data?.contacts ?? []} canWrite={canWrite} onChange={() => { contacts.mutate(); lists.mutate() }} />
      )}
      <div className="pb-8" />
    </>
  )
}

function ListsSection({ lists, loading, error, onChange }: { lists: MailList[]; loading: boolean; error: unknown; onChange: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  const canDelete = ws.can("campaigns:write")
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [doi, setDoi] = useState(true)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function create(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/mail/lists", { json: { name: name.trim(), description: description.trim(), double_opt_in: doi }, workspaceId: ws.id })
      toast.success(t("lists.created"))
      setName("")
      setDescription("")
      onChange()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function remove(id: string) {
    try {
      await api(`/api/w/mail/lists/${encodeURIComponent(id)}`, { method: "DELETE", workspaceId: ws.id })
      toast.success(t("lists.deleted"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <section>
      <h2 className="px-6 pb-1 text-base font-medium">{t("lists.title")}</h2>
      <p className="px-6 pb-4 text-sm text-grid-muted">{t("lists.hint")}</p>
      {error ? (
        <ErrorState error={error} />
      ) : loading ? (
        <LoadingRows rows={2} />
      ) : lists.length === 0 ? (
        <EmptyState title={t("lists.empty")} body={canWrite ? t("lists.emptyBody") : undefined} />
      ) : (
        <div className="border-y border-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("lists.name")}</TableHead>
                <TableHead>{t("lists.doubleOptIn")}</TableHead>
                <TableHead>{t("lists.subscribed")}</TableHead>
                <TableHead>{t("lists.pending")}</TableHead>
                <TableHead className="pe-6 text-end">
                  <span className="sr-only">{t("common.actions")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {lists.map((l) => (
                <TableRow key={l.id} data-list-row={l.name}>
                  <TableCell className="ps-6">
                    <div className="font-medium">{l.name}</div>
                    {l.description ? <div className="text-xs text-grid-muted">{l.description}</div> : null}
                  </TableCell>
                  <TableCell className="text-grid-muted">{l.double_opt_in ? t("lists.on") : t("lists.off")}</TableCell>
                  <TableCell>{l.subscribed}</TableCell>
                  <TableCell className="text-grid-muted">{l.pending}</TableCell>
                  <TableCell className="pe-6 text-end">
                    {canDelete ? (
                      <ConfirmButton
                        label={t("common.delete")}
                        title={t("lists.deleteTitle")}
                        description={t("lists.deleteBody", { name: l.name })}
                        confirmLabel={t("common.delete")}
                        onConfirm={() => remove(l.id)}
                      />
                    ) : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {canWrite ? (
        <form onSubmit={create} noValidate className="px-6 pt-6" data-form="create-list">
          <FieldGroup className="sm:flex-row sm:items-end">
            <Field className="sm:flex-1">
              <FieldLabel htmlFor="list-name">{t("lists.name")}</FieldLabel>
              <Input id="list-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
            </Field>
            <Field className="sm:flex-1">
              <FieldLabel htmlFor="list-description">
                {t("lists.description")} <span className="text-grid-muted">({t("common.optional")})</span>
              </FieldLabel>
              <Input id="list-description" value={description} onChange={(e) => setDescription(e.target.value)} maxLength={500} />
            </Field>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("lists.create")}
            </Button>
          </FieldGroup>
          <Field orientation="horizontal" className="mt-4">
            <Switch id="list-doi" checked={doi} onCheckedChange={(v) => setDoi(!!v)} />
            <div>
              <FieldLabel htmlFor="list-doi">{t("lists.doubleOptIn")}</FieldLabel>
              <FieldDescription>{t("lists.doubleOptInHint")}</FieldDescription>
            </div>
          </Field>
          {err ? <FieldError className="mt-3">{err}</FieldError> : null}
        </form>
      ) : null}
    </section>
  )
}

function AddContact({ lists, onAdded }: { lists: MailList[]; onAdded: () => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const isAdmin = ws.can("mail:settings")
  const smtp = useSmtpSettings(ws.id, isAdmin)
  const [email, setEmail] = useState("")
  const [name, setName] = useState("")
  const [lang, setLang] = useState<string>(locale === "ar" ? "ar" : "en")
  const [listId, setListId] = useState<string>("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const chosen = listId || lists[0]?.id || ""
  const listItems = lists.map((l) => ({ value: l.id, label: l.name }))
  const langItems = [
    { value: "en", label: t("lang.en") },
    { value: "ar", label: t("lang.ar") },
  ]
  const needsSmtp = isAdmin && smtp.data && !smtp.data.configured && lists.some((l) => l.double_opt_in)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{ status: string; confirmation: string; error?: string; contact: Contact }>("/api/w/contacts", {
        json: { email: email.trim(), name: name.trim(), locale: lang, list_id: chosen },
        workspaceId: ws.id,
      })
      if (res.confirmation === "sent") toast.success(t("contacts.addedConfirmSent"))
      else if (res.confirmation === "not_sent") toast.warning(t("contacts.addedConfirmNotSent", { error: res.error ?? "" }))
      else if (res.status === "subscribed") toast.success(t("contacts.addedSubscribed"))
      setEmail("")
      setName("")
      onAdded()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="px-6 py-6">
      <h2 className="text-base font-medium">{t("contacts.addTitle")}</h2>
      {needsSmtp ? (
        <p className="mt-1 flex items-start gap-2 text-sm text-grid-muted">
          <MailWarningIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" />
          <span>
            {t("contacts.smtpMissing")}{" "}
            <Link className="underline underline-offset-4" href={`/${locale}/w/${ws.slug}/settings/email`}>
              {t("contacts.smtpLink")}
            </Link>
          </span>
        </p>
      ) : null}
      {lists.length === 0 ? (
        <p className="mt-2 text-sm text-grid-muted">{t("contacts.needList")}</p>
      ) : (
        <form onSubmit={onSubmit} noValidate className="mt-5" data-form="add-contact">
          <FieldGroup className="sm:flex-row sm:items-end">
            <Field className="sm:flex-1">
              <FieldLabel htmlFor="contact-email">{t("contacts.email")}</FieldLabel>
              <Input id="contact-email" type="email" dir="ltr" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="name@company.com" />
            </Field>
            <Field className="sm:flex-1">
              <FieldLabel htmlFor="contact-name">
                {t("contacts.name")} <span className="text-grid-muted">({t("common.optional")})</span>
              </FieldLabel>
              <Input id="contact-name" value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field className="sm:w-36">
              <FieldLabel htmlFor="contact-lang">{t("contacts.language")}</FieldLabel>
              <Select items={langItems} value={lang} onValueChange={(v) => setLang(String(v ?? "en"))}>
                <SelectTrigger id="contact-lang" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {langItems.map((i) => (
                    <SelectItem key={i.value} value={i.value}>
                      {i.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field className="sm:w-44">
              <FieldLabel htmlFor="contact-list">{t("contacts.list")}</FieldLabel>
              <Select items={listItems} value={chosen} onValueChange={(v) => setListId(String(v ?? ""))}>
                <SelectTrigger id="contact-list" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {listItems.map((i) => (
                    <SelectItem key={i.value} value={i.value}>
                      {i.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Button type="submit" disabled={busy || !email.trim() || !chosen}>
              {busy ? t("common.working") : t("contacts.add")}
            </Button>
          </FieldGroup>
          {err ? <FieldError className="mt-3">{err}</FieldError> : null}
        </form>
      )}
    </section>
  )
}

function ContactsTable({ items, canWrite, onChange }: { items: Contact[]; canWrite: boolean; onChange: () => void }) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()

  async function resend(subId: string) {
    try {
      await api(`/api/w/subscriptions/${encodeURIComponent(subId)}/resend`, { method: "POST", workspaceId: ws.id })
      toast.success(t("contacts.resent"))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function remove(id: string) {
    try {
      await api(`/api/w/contacts/${encodeURIComponent(id)}`, { method: "DELETE", workspaceId: ws.id })
      toast.success(t("contacts.deleted"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <div className="border-y border-line">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="ps-6">{t("contacts.email")}</TableHead>
            <TableHead>{t("contacts.name")}</TableHead>
            <TableHead>{t("contacts.lists")}</TableHead>
            <TableHead>{t("contacts.added")}</TableHead>
            <TableHead className="pe-6 text-end">
              <span className="sr-only">{t("common.actions")}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((c) => (
            <TableRow key={c.id} data-contact-row={c.email}>
              <TableCell className="ps-6">
                <Ltr>{c.email}</Ltr>
              </TableCell>
              <TableCell>{c.name || <span className="text-grid-muted">—</span>}</TableCell>
              <TableCell>
                <div className="flex flex-wrap gap-1.5">
                  {(c.subscriptions ?? []).map((s) => (
                    <span key={s.id} className="inline-flex items-center gap-1">
                      <Badge variant={statusVariant[s.status]} title={t(`sub.${s.status}`)}>
                        {s.list_name} · {t(`sub.${s.status}`)}
                      </Badge>
                      {canWrite && s.status === "pending" ? (
                        <Button variant="link" size="sm" className="h-auto px-1 text-xs" onClick={() => resend(s.id)}>
                          {t("contacts.resend")}
                        </Button>
                      ) : null}
                    </span>
                  ))}
                </div>
              </TableCell>
              <TableCell className="text-grid-muted">
                <Ltr>{formatDate(c.created_at)}</Ltr>
              </TableCell>
              <TableCell className="pe-6 text-end">
                {canWrite ? (
                  <ConfirmButton
                    label={t("common.delete")}
                    title={t("contacts.deleteTitle")}
                    description={t("contacts.deleteBody", { email: c.email })}
                    confirmLabel={t("common.delete")}
                    onConfirm={() => remove(c.id)}
                  />
                ) : null}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
