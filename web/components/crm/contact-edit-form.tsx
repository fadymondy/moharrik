"use client"

import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { CompanyPicker } from "@/components/crm/company-picker"
import { PickSelect, useMemberNames } from "@/components/crm/shared"
import { Button } from "@/components/ui/button"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

export type ContactEditable = {
  id: string
  name: string
  email: string
  phone: string
  handle: string
  title: string
  company: { id: string; name: string } | null
  owner_user_id: string | null
}

/**
 * The contact edit fields (MH-595): shared by the contact detail page and the
 * inbox's per-row contact dialog so both write through the SAME `PATCH
 * /api/w/contacts/{id}` call and never drift into two field sets. `idPrefix`
 * keeps element ids unique when more than one instance can exist on the page
 * at once (a dialog opened from a list row, for example).
 */
export function ContactEditForm({ contact, onSaved, idPrefix = "edit-contact" }: { contact: ContactEditable; onSaved: () => void; idPrefix?: string }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const members = useMemberNames(ws.id)
  const [name, setName] = useState(contact.name)
  const [email, setEmail] = useState(contact.email)
  const [phone, setPhone] = useState(contact.phone)
  const [handle, setHandle] = useState(contact.handle)
  const [title, setTitle] = useState(contact.title)
  const [companyId, setCompanyId] = useState(contact.company?.id ?? "")
  const [ownerId, setOwnerId] = useState(contact.owner_user_id ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const hasIdentity = !!(email.trim() || phone.trim() || handle.trim())

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/contacts/${encodeURIComponent(contact.id)}`, {
        method: "PATCH",
        json: {
          name: name.trim(),
          email: email.trim(),
          phone: phone.trim(),
          handle: handle.trim(),
          title: title.trim(),
          company_id: companyId,
          owner_user_id: ownerId,
        },
        workspaceId: ws.id,
      })
      toast.success(t("contacts.saved"))
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-4" data-form="edit-contact">
      <FieldGroup className="grid gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-name`}>{t("contacts.name")}</FieldLabel>
          <Input id={`${idPrefix}-name`} value={name} onChange={(e) => setName(e.target.value)} maxLength={120} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-email`}>{t("contacts.email")}</FieldLabel>
          <Input id={`${idPrefix}-email`} type="email" dir="ltr" value={email} onChange={(e) => setEmail(e.target.value)} maxLength={254} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-phone`}>{t("contacts.phone")}</FieldLabel>
          <Input id={`${idPrefix}-phone`} dir="ltr" value={phone} onChange={(e) => setPhone(e.target.value)} maxLength={40} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-handle`}>{t("contacts.handle")}</FieldLabel>
          <Input id={`${idPrefix}-handle`} dir="ltr" value={handle} onChange={(e) => setHandle(e.target.value)} maxLength={120} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-title`}>{t("contacts.jobTitle")}</FieldLabel>
          <Input id={`${idPrefix}-title`} value={title} onChange={(e) => setTitle(e.target.value)} maxLength={120} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-company`}>{t("companies.title")}</FieldLabel>
          <CompanyPicker id={`${idPrefix}-company`} value={companyId} selected={contact.company} onChange={setCompanyId} />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-owner`}>{t("crm.owner")}</FieldLabel>
          <PickSelect id={`${idPrefix}-owner`} value={ownerId} onChange={setOwnerId} options={members.options} noneLabel={t("crm.unassigned")} />
        </Field>
      </FieldGroup>
      <div className="flex items-center gap-3">
        <Button type="submit" disabled={busy || !hasIdentity}>
          {busy ? t("common.working") : t("common.save")}
        </Button>
        {!hasIdentity ? <FieldError>{t("contacts.needsIdentity")}</FieldError> : err ? <FieldError>{err}</FieldError> : null}
      </div>
    </form>
  )
}
