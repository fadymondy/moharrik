"use client"

import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { PickSelect } from "@/components/crm/shared"
import { Ltr } from "@/components/copy-field"
import { api, ApiError, CONTACT_CHANNELS, type ContactChannel, type ContactIdentity } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWorkspace } from "@/components/workspace/workspace-context"

/**
 * MH-583: every account this contact answers on — several WhatsApp numbers,
 * a second email, Telegram/Slack/Discord/Messenger/Instagram/LinkedIn/X, the
 * chat widget's visitor id — linked or unlinked here. Every inbound filer
 * (email, WhatsApp today; see internal/inbox/crm_link.go) matches ANY row in
 * this list, not just the contact's single primary email/phone.
 */
export function IdentitiesPanel({ contactId, identities, canWrite, onChanged }: { contactId: string; identities: ContactIdentity[]; canWrite: boolean; onChanged: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [channel, setChannel] = useState<ContactChannel>("whatsapp")
  const [externalId, setExternalId] = useState("")
  const [label, setLabel] = useState("")
  const [primary, setPrimary] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const sorted = [...identities].sort((a, b) => (b.is_primary ? 1 : 0) - (a.is_primary ? 1 : 0) || a.channel.localeCompare(b.channel))

  async function link(e: FormEvent) {
    e.preventDefault()
    const value = externalId.trim()
    if (!value) return
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/contacts/${encodeURIComponent(contactId)}/identities`, {
        method: "POST",
        json: { channel, external_id: value, label: label.trim(), is_primary: primary },
        workspaceId: ws.id,
      })
      toast.success(t("contacts.identityAdded"))
      setExternalId("")
      setLabel("")
      setPrimary(false)
      onChanged()
    } catch (e) {
      const msg = e instanceof ApiError && e.status === 409 ? t("contacts.identityDuplicate") : e instanceof ApiError ? e.message : t("common.networkError")
      setErr(msg)
      toast.error(t("contacts.identityLinkFailed"))
    } finally {
      setBusy(false)
    }
  }

  async function unlink(identity: ContactIdentity) {
    await api(`/api/w/contacts/${encodeURIComponent(contactId)}/identities/${encodeURIComponent(identity.id)}`, {
      method: "DELETE",
      workspaceId: ws.id,
    })
    toast.success(t("contacts.identityUnlinked"))
    onChanged()
  }

  return (
    <section className="border border-line bg-grid-card px-5 py-4" data-identities-panel>
      <h2 className="text-base font-medium">{t("contacts.identities")}</h2>
      <p className="pt-1 text-xs text-grid-muted">{t("contacts.identitiesHint")}</p>

      {sorted.length === 0 ? (
        <p className="py-4 text-sm text-grid-muted">{t("contacts.identitiesEmpty")}</p>
      ) : (
        <ul className="divide-y divide-line py-2" data-identity-list>
          {sorted.map((i) => (
            <li key={i.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2" data-identity={`${i.channel}:${i.external_id}`}>
              <Badge variant="secondary">{t(`channel.${i.channel}`)}</Badge>
              <Ltr className="text-sm">{i.external_id}</Ltr>
              {i.label ? <span className="text-xs text-grid-muted">{i.label}</span> : null}
              {i.is_primary ? <Badge variant="outline">{t("contacts.identityPrimary")}</Badge> : null}
              {canWrite ? (
                <ConfirmButton
                  label={t("contacts.identityUnlink")}
                  title={t("contacts.identityUnlinkTitle")}
                  description={t("contacts.identityUnlinkBody", { value: i.external_id })}
                  confirmLabel={t("contacts.identityUnlink")}
                  onConfirm={() => unlink(i)}
                />
              ) : null}
            </li>
          ))}
        </ul>
      )}

      {canWrite ? (
        <form onSubmit={link} noValidate className="mt-3 border-t border-line pt-3" data-form="link-identity">
          <FieldGroup className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <Field>
              <FieldLabel htmlFor="identity-channel">{t("contacts.identityChannel")}</FieldLabel>
              <PickSelect id="identity-channel" value={channel} onChange={(v) => setChannel(v as ContactChannel)} options={CONTACT_CHANNELS.map((c) => ({ value: c, label: t(`channel.${c}`) }))} />
            </Field>
            <Field>
              <FieldLabel htmlFor="identity-value">{t("contacts.identityValue")}</FieldLabel>
              <Input id="identity-value" dir="ltr" value={externalId} onChange={(e) => setExternalId(e.target.value)} maxLength={254} />
            </Field>
            <Field>
              <FieldLabel htmlFor="identity-label">{t("contacts.identityLabel")}</FieldLabel>
              <Input id="identity-label" value={label} onChange={(e) => setLabel(e.target.value)} maxLength={60} />
            </Field>
            {/* Horizontal: the vertical default gives every child w-full, which
                stretched the checkbox and pushed "Primary" off-screen at 390. */}
            <Field orientation="horizontal" className="self-end pb-2">
              <Checkbox id="identity-primary" checked={primary} onCheckedChange={(v) => setPrimary(!!v)} />
              <FieldLabel htmlFor="identity-primary" className="font-normal">
                {t("contacts.identityPrimary")}
              </FieldLabel>
            </Field>
          </FieldGroup>
          <div className="mt-3 flex items-center gap-3">
            <Button type="submit" size="sm" disabled={busy || !externalId.trim()}>
              {busy ? t("common.working") : t("contacts.identityAdd")}
            </Button>
            {err ? <span className="text-sm text-grid-danger-text">{err}</span> : null}
          </div>
        </form>
      ) : null}
    </section>
  )
}

/**
 * MH-583: Fady's own words — "add a meta value that i can add key and value
 * of each contact." Free-form key/value pairs, edited as a whole map (the
 * REST PATCH replaces it wholesale, unlike Upsert's additive merge on import).
 */
export function MetaPanel({ contactId, meta, canWrite, onChanged }: { contactId: string; meta: Record<string, string>; canWrite: boolean; onChanged: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [rows, setRows] = useState<{ key: string; value: string }[]>(() => Object.entries(meta ?? {}).map(([key, value]) => ({ key, value })))
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  function setRow(i: number, patch: Partial<{ key: string; value: string }>) {
    setRows((rs) => rs.map((r, idx) => (idx === i ? { ...r, ...patch } : r)))
  }
  function removeRow(i: number) {
    setRows((rs) => rs.filter((_, idx) => idx !== i))
  }
  function addRow() {
    setRows((rs) => [...rs, { key: "", value: "" }])
  }

  async function save() {
    setBusy(true)
    setErr(null)
    try {
      const clean: Record<string, string> = {}
      for (const r of rows) {
        const k = r.key.trim()
        if (!k) continue
        clean[k] = r.value
      }
      await api(`/api/w/contacts/${encodeURIComponent(contactId)}`, { method: "PATCH", json: { meta: clean }, workspaceId: ws.id })
      toast.success(t("contacts.metaSaved"))
      onChanged()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="border border-line bg-grid-card px-5 py-4" data-meta-panel>
      <h2 className="text-base font-medium">{t("contacts.meta")}</h2>
      <p className="pt-1 text-xs text-grid-muted">{t("contacts.metaHint")}</p>

      {rows.length === 0 ? <p className="py-4 text-sm text-grid-muted">{t("contacts.metaEmpty")}</p> : null}

      <div className="flex flex-col gap-2 py-2" data-meta-rows>
        {rows.map((r, i) => (
          <div key={i} className="flex flex-wrap items-center gap-2" data-meta-row>
            <Input aria-label={t("contacts.metaKey")} value={r.key} onChange={(e) => setRow(i, { key: e.target.value })} maxLength={200} disabled={!canWrite} className="max-w-48" />
            <Input aria-label={t("contacts.metaValue")} value={r.value} onChange={(e) => setRow(i, { value: e.target.value })} maxLength={200} disabled={!canWrite} className="max-w-64 flex-1" />
            {canWrite ? (
              <Button type="button" variant="ghost" size="sm" onClick={() => removeRow(i)}>
                {t("contacts.metaRemove")}
              </Button>
            ) : null}
          </div>
        ))}
      </div>

      {canWrite ? (
        <div className="mt-2 flex items-center gap-3">
          <Button type="button" variant="outline" size="sm" onClick={addRow}>
            {t("contacts.metaAdd")}
          </Button>
          <Button type="button" size="sm" disabled={busy} onClick={() => void save()}>
            {busy ? t("common.working") : t("common.save")}
          </Button>
          {err ? <span className="text-sm text-grid-danger-text">{err}</span> : null}
        </div>
      ) : null}
    </section>
  )
}
