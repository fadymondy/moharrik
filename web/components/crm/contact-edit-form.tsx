"use client"

import { useRef, useState, type ChangeEvent, type FormEvent } from "react"
import { Button, Field, FieldError, FieldLabel, Input, Textarea, toast } from "@fadymondy/nasaq/web"
import { FieldGroup } from "@/components/form-layout"
import { useSWRConfig } from "swr"

import { PersonAvatar } from "@/components/avatar-with-fallback"
import { CompanyPicker } from "@/components/crm/company-picker"
import { PickSelect, useMemberNames } from "@/components/crm/shared"
import { DatePicker } from "@/components/date-picker"
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
  avatar_url?: string
  // MH-583: Google-People-parity fields.
  given_name?: string
  family_name?: string
  middle_name?: string
  name_prefix?: string
  name_suffix?: string
  phonetic_given_name?: string
  phonetic_family_name?: string
  nickname?: string
  birthday?: string | null
  biography?: string
}

// MH-602: server-validated too (internal/crm/avatar.go), this is only so a
// bad file never leaves the browser and wastes a round trip.
const AVATAR_TYPES = ["image/png", "image/jpeg", "image/webp"]
const AVATAR_MAX_BYTES = 2 * 1024 * 1024

function readAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      // "data:image/png;base64,AAAA..." — only the part after the comma is
      // what the API's `data` field wants.
      const result = String(reader.result ?? "")
      resolve(result.slice(result.indexOf(",") + 1))
    }
    reader.onerror = () => reject(reader.error ?? new Error("read failed"))
    reader.readAsDataURL(file)
  })
}

/**
 * Upload/replace/remove a contact's photo (MH-602). Kept separate from the
 * name/email/etc. PATCH below: each avatar action is its own request against
 * /api/w/contacts/{id}/avatar and takes effect immediately, rather than
 * waiting on the surrounding form's Save.
 */
function ContactAvatarControl({
  contactId,
  name,
  avatarUrl,
  onChanged,
  idPrefix,
}: {
  contactId: string
  name: string
  avatarUrl?: string
  onChanged: (avatarUrl: string) => void
  idPrefix: string
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const { mutate } = useSWRConfig()
  const fileRef = useRef<HTMLInputElement>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  // The dialog this control lives in re-fetches the contact fresh every time
  // it opens (useContact keys off `open`), but SWR's own cache for that same
  // key can still be the one from a moment ago — so reopening right after an
  // upload/remove served the STALE avatar_url and the "Remove" button never
  // appeared. Updating this cache entry in place, the instant the server
  // confirms the change, means the next open reads the truth immediately
  // instead of racing SWR's dedupe window.
  function syncContactCache(nextAvatarUrl: string) {
    void mutate(
      [`/api/w/contacts/${encodeURIComponent(contactId)}`, ws.id],
      (cur: { contact: { avatar_url?: string } } | undefined) =>
        cur ? { ...cur, contact: { ...cur.contact, avatar_url: nextAvatarUrl } } : cur,
      { revalidate: false },
    )
  }

  async function onPick(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = ""
    if (!file) return
    setErr(null)
    if (!AVATAR_TYPES.includes(file.type)) {
      setErr(t("contacts.avatar.badType"))
      return
    }
    if (file.size > AVATAR_MAX_BYTES) {
      setErr(t("contacts.avatar.tooLarge"))
      return
    }
    setBusy(true)
    try {
      const data = await readAsBase64(file)
      const res = await api<{ contact: { avatar_url?: string } }>(
        `/api/w/contacts/${encodeURIComponent(contactId)}/avatar`,
        {
          method: "POST",
          json: { data, content_type: file.type, filename: file.name },
          workspaceId: ws.id,
        },
      )
      onChanged(res.contact.avatar_url ?? "")
      syncContactCache(res.contact.avatar_url ?? "")
      toast.success(t("contacts.avatar.uploaded"))
    } catch (err) {
      setErr(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{ contact: { avatar_url?: string } }>(
        `/api/w/contacts/${encodeURIComponent(contactId)}/avatar`,
        {
          method: "DELETE",
          workspaceId: ws.id,
        },
      )
      onChanged(res.contact.avatar_url ?? "")
      syncContactCache(res.contact.avatar_url ?? "")
      toast.success(t("contacts.avatar.removed"))
    } catch (err) {
      setErr(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex items-center gap-3" data-contact-avatar-control>
      <PersonAvatar id={contactId} name={name} photoUrl={avatarUrl} size="lg" />
      <div className="flex flex-col gap-1">
        <div className="flex items-center gap-2">
          <input
            ref={fileRef}
            id={`${idPrefix}-avatar-file`}
            type="file"
            accept={AVATAR_TYPES.join(",")}
            className="hidden"
            onChange={onPick}
            data-avatar-input
          />
          <Button
            type="button"
            variant="secondary"
            size="sm"
            disabled={busy}
            onClick={() => fileRef.current?.click()}
            data-avatar-upload
          >
            {busy
              ? t("common.working")
              : avatarUrl
                ? t("contacts.avatar.replace")
                : t("contacts.avatar.upload")}
          </Button>
          {avatarUrl ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={busy}
              onClick={remove}
              data-avatar-remove
            >
              {t("contacts.avatar.remove")}
            </Button>
          ) : null}
        </div>
        {err ? (
          <FieldError>{err}</FieldError>
        ) : (
          <p className="text-xs text-muted-foreground">{t("contacts.avatar.hint")}</p>
        )}
      </div>
    </div>
  )
}

/**
 * The contact edit fields (MH-595): shared by the contact detail page and the
 * inbox's per-row contact dialog so both write through the SAME `PATCH
 * /api/w/contacts/{id}` call and never drift into two field sets. `idPrefix`
 * keeps element ids unique when more than one instance can exist on the page
 * at once (a dialog opened from a list row, for example).
 */
export function ContactEditForm({
  contact,
  onSaved,
  idPrefix = "edit-contact",
}: {
  contact: ContactEditable
  onSaved: () => void
  idPrefix?: string
}) {
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
  const [avatarUrl, setAvatarUrl] = useState(contact.avatar_url ?? "")
  // MH-583: Google-People-parity scalar fields the identities/meta panels don't cover.
  const [givenName, setGivenName] = useState(contact.given_name ?? "")
  const [familyName, setFamilyName] = useState(contact.family_name ?? "")
  const [middleName, setMiddleName] = useState(contact.middle_name ?? "")
  const [namePrefix, setNamePrefix] = useState(contact.name_prefix ?? "")
  const [nameSuffix, setNameSuffix] = useState(contact.name_suffix ?? "")
  const [phoneticGivenName, setPhoneticGivenName] = useState(contact.phonetic_given_name ?? "")
  const [phoneticFamilyName, setPhoneticFamilyName] = useState(contact.phonetic_family_name ?? "")
  const [nickname, setNickname] = useState(contact.nickname ?? "")
  const [birthday, setBirthday] = useState(contact.birthday ?? "")
  const [biography, setBiography] = useState(contact.biography ?? "")
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
          given_name: givenName.trim(),
          family_name: familyName.trim(),
          middle_name: middleName.trim(),
          name_prefix: namePrefix.trim(),
          name_suffix: nameSuffix.trim(),
          phonetic_given_name: phoneticGivenName.trim(),
          phonetic_family_name: phoneticFamilyName.trim(),
          nickname: nickname.trim(),
          birthday: birthday.trim(),
          biography: biography.trim(),
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
      <ContactAvatarControl
        contactId={contact.id}
        name={name || contact.name}
        avatarUrl={avatarUrl}
        onChanged={setAvatarUrl}
        idPrefix={idPrefix}
      />
      <FieldGroup className="grid gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-name`}>{t("contacts.name")}</FieldLabel>
          <Input
            id={`${idPrefix}-name`}
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={120}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-email`}>{t("contacts.email")}</FieldLabel>
          <Input
            id={`${idPrefix}-email`}
            type="email"
            dir="ltr"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            maxLength={254}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-phone`}>{t("contacts.phone")}</FieldLabel>
          <Input
            id={`${idPrefix}-phone`}
            dir="ltr"
            value={phone}
            onChange={(e) => setPhone(e.target.value)}
            maxLength={40}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-handle`}>{t("contacts.handle")}</FieldLabel>
          <Input
            id={`${idPrefix}-handle`}
            dir="ltr"
            value={handle}
            onChange={(e) => setHandle(e.target.value)}
            maxLength={120}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-title`}>{t("contacts.jobTitle")}</FieldLabel>
          <Input
            id={`${idPrefix}-title`}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            maxLength={120}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-company`}>{t("companies.title")}</FieldLabel>
          <CompanyPicker
            id={`${idPrefix}-company`}
            value={companyId}
            selected={contact.company}
            onChange={setCompanyId}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-owner`}>{t("crm.owner")}</FieldLabel>
          <PickSelect
            id={`${idPrefix}-owner`}
            value={ownerId}
            onChange={setOwnerId}
            options={members.options}
            noneLabel={t("crm.unassigned")}
          />
        </Field>
      </FieldGroup>
      <h3 className="text-sm font-medium text-muted-foreground">{t("contacts.personDetails")}</h3>
      <FieldGroup className="grid gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-name-prefix`}>{t("contacts.namePrefix")}</FieldLabel>
          <Input
            id={`${idPrefix}-name-prefix`}
            value={namePrefix}
            onChange={(e) => setNamePrefix(e.target.value)}
            maxLength={40}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-given-name`}>{t("contacts.givenName")}</FieldLabel>
          <Input
            id={`${idPrefix}-given-name`}
            value={givenName}
            onChange={(e) => setGivenName(e.target.value)}
            maxLength={120}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-middle-name`}>{t("contacts.middleName")}</FieldLabel>
          <Input
            id={`${idPrefix}-middle-name`}
            value={middleName}
            onChange={(e) => setMiddleName(e.target.value)}
            maxLength={120}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-family-name`}>{t("contacts.familyName")}</FieldLabel>
          <Input
            id={`${idPrefix}-family-name`}
            value={familyName}
            onChange={(e) => setFamilyName(e.target.value)}
            maxLength={120}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-name-suffix`}>{t("contacts.nameSuffix")}</FieldLabel>
          <Input
            id={`${idPrefix}-name-suffix`}
            value={nameSuffix}
            onChange={(e) => setNameSuffix(e.target.value)}
            maxLength={40}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-nickname`}>{t("contacts.nickname")}</FieldLabel>
          <Input
            id={`${idPrefix}-nickname`}
            value={nickname}
            onChange={(e) => setNickname(e.target.value)}
            maxLength={120}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-phonetic-given`}>
            {t("contacts.phoneticGivenName")}
          </FieldLabel>
          <Input
            id={`${idPrefix}-phonetic-given`}
            value={phoneticGivenName}
            onChange={(e) => setPhoneticGivenName(e.target.value)}
            maxLength={120}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-phonetic-family`}>
            {t("contacts.phoneticFamilyName")}
          </FieldLabel>
          <Input
            id={`${idPrefix}-phonetic-family`}
            value={phoneticFamilyName}
            onChange={(e) => setPhoneticFamilyName(e.target.value)}
            maxLength={120}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={`${idPrefix}-birthday`}>{t("contacts.birthday")}</FieldLabel>
          <DatePicker
            id={`${idPrefix}-birthday`}
            value={birthday}
            onValueChange={setBirthday}
            clearable
            max={new Date().toISOString().slice(0, 10)}
          />
        </Field>
        <Field className="sm:col-span-2">
          <FieldLabel htmlFor={`${idPrefix}-biography`}>{t("contacts.biography")}</FieldLabel>
          <Textarea
            id={`${idPrefix}-biography`}
            value={biography}
            onChange={(e) => setBiography(e.target.value)}
            maxLength={5000}
            rows={3}
          />
        </Field>
      </FieldGroup>
      <div className="flex items-center gap-3">
        <Button type="submit" disabled={busy || !hasIdentity}>
          {busy ? t("common.working") : t("common.save")}
        </Button>
        {!hasIdentity ? (
          <FieldError>{t("contacts.needsIdentity")}</FieldError>
        ) : err ? (
          <FieldError>{err}</FieldError>
        ) : null}
      </div>
    </form>
  )
}
