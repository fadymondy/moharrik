"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { MessageSquareTextIcon, PencilIcon, PlusIcon, TriangleAlertIcon } from "lucide-react"
import { toast } from "sonner"

import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField, Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type FormKind, type MailList, type SubscribeForm } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMailLists, useSubscribeForms } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

/** Origins are typed one per line or comma-separated; the API stores them normalised. */
const parseOrigins = (v: string) =>
  v
    .split(/[\n,]/)
    .map((o) => o.trim())
    .filter(Boolean)

export default function SubscribeFormsPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.subscribeForms")} · ${ws.summary.name}`)
  const canManage = ws.can("campaigns:write")
  const forms = useSubscribeForms(ws.id)
  const lists = useMailLists(ws.id)
  // "new-subscribe" / "new-inquiry" open the dialog for that kind (MH-390).
  const [editing, setEditing] = useState<SubscribeForm | `new-${FormKind}` | null>(null)
  const items = forms.data?.forms ?? []
  const listItems = lists.data?.lists ?? []

  return (
    <>
      <SectionHeader
        micro={ws.summary.name}
        title={t("nav.subscribeForms")}
        description={t("forms.hint")}
        action={
          canManage ? (
            <div className="flex flex-wrap items-center gap-2">
              {/* An inquiry form needs no list, so it is available from the start. */}
              <Button variant="outline" onClick={() => setEditing("new-inquiry")} disabled={!forms.data} data-new-form="inquiry">
                <MessageSquareTextIcon />
                {t("forms.addInquiry")}
              </Button>
              {listItems.length > 0 ? (
                <Button onClick={() => setEditing("new-subscribe")} disabled={!forms.data} data-new-form="subscribe">
                  <PlusIcon />
                  {t("forms.add")}
                </Button>
              ) : null}
            </div>
          ) : undefined
        }
      />

      {canManage && lists.data && listItems.length === 0 ? (
        <p className="px-6 pb-4 text-sm text-grid-muted">
          <Link className="underline underline-offset-4" href={`/${locale}/w/${ws.slug}/contacts`}>
            {t("forms.needList")}
          </Link>
        </p>
      ) : null}

      {forms.error ? (
        <ErrorState error={forms.error} />
      ) : forms.isLoading ? (
        <LoadingRows />
      ) : items.length === 0 ? (
        <EmptyState title={t("forms.empty")} body={canManage ? t("forms.emptyBody") : t("forms.readOnly")} />
      ) : (
        <ul className="divide-y divide-line border-y border-line">
          {items.map((f) => (
            <FormRow key={f.id} form={f} canManage={canManage} onEdit={() => setEditing(f)} onChange={() => forms.mutate()} />
          ))}
        </ul>
      )}
      <div className="pb-8" />

      {editing ? (
        <FormDialog
          key={typeof editing === "string" ? editing : editing.id}
          form={typeof editing === "string" ? null : editing}
          kind={typeof editing === "string" ? (editing === "new-inquiry" ? "inquiry" : "subscribe") : editing.kind}
          lists={listItems}
          onClose={() => setEditing(null)}
          onSaved={() => forms.mutate()}
        />
      ) : null}
    </>
  )
}

function FormRow({ form, canManage, onEdit, onChange }: { form: SubscribeForm; canManage: boolean; onEdit: () => void; onChange: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [saving, setSaving] = useState(false)
  const origins = form.allowed_origins ?? []
  const blocked = origins.length === 0
  const inquiry = form.kind === "inquiry"

  async function toggle() {
    setSaving(true)
    try {
      await api(`/api/w/subscribe-forms/${encodeURIComponent(form.id)}`, { method: "PATCH", json: { enabled: !form.enabled }, workspaceId: ws.id })
      toast.success(form.enabled ? t("forms.disabledToast") : t("forms.enabledToast"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setSaving(false)
    }
  }

  async function remove() {
    try {
      await api(`/api/w/subscribe-forms/${encodeURIComponent(form.id)}`, { method: "DELETE", workspaceId: ws.id })
      toast.success(t("forms.deleted"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <li className="px-6 py-5" data-form-row={form.name} data-form-kind={form.kind}>
      <div className="flex flex-wrap items-center gap-3">
        <p className="text-sm font-medium">{form.name}</p>
        <Badge variant="secondary">{inquiry ? t("forms.kind.inquiry") : t("forms.kind.subscribe")}</Badge>
        <Badge variant="outline">{inquiry ? t("forms.toLeads") : t("forms.toList", { list: form.list_name || "—" })}</Badge>
        <Badge variant={form.enabled ? "default" : "outline"}>{form.enabled ? t("forms.enabled") : t("forms.disabled")}</Badge>
        {blocked ? (
          <span className="flex items-center gap-1 text-xs text-grid-warn-text" data-no-origins>
            <TriangleAlertIcon className="size-3.5" />
            {t("forms.noOrigins")}
          </span>
        ) : null}
        {canManage ? (
          <div className="ms-auto flex items-center gap-2">
            <Switch
              checked={form.enabled}
              disabled={saving}
              onCheckedChange={toggle}
              aria-label={form.enabled ? t("forms.disable") : t("forms.enable")}
            />
            <Button variant="ghost" size="sm" onClick={onEdit}>
              <PencilIcon />
              {t("forms.edit")}
            </Button>
            <ConfirmButton
              label={t("common.delete")}
              title={t("forms.deleteTitle")}
              description={t(inquiry ? "forms.deleteBodyInquiry" : "forms.deleteBody", { name: form.name })}
              confirmLabel={t("common.delete")}
              onConfirm={remove}
            />
          </div>
        ) : null}
      </div>

      <dl className="mt-3 grid gap-1 text-xs text-grid-muted sm:grid-cols-[10rem_1fr]">
        <dt>{t("forms.publicKey")}</dt>
        <dd className="min-w-0 break-all">
          <Ltr mono className="text-grid-fg">
            {form.public_key}
          </Ltr>
        </dd>
        <dt>{t("forms.allowedOrigins")}</dt>
        <dd className="min-w-0 break-all">{blocked ? t("forms.noOriginsShort") : <Ltr>{origins.join(", ")}</Ltr>}</dd>
      </dl>

      <div className="mt-3 space-y-1.5">
        <p className="grid-micro">{t("forms.embed")}</p>
        <CopyField value={form.embed} label={t("forms.embed")} />
        <p className="text-xs text-grid-muted">{t(inquiry ? "forms.embedHintInquiry" : "forms.embedHint")}</p>
      </div>
    </li>
  )
}

function FormDialog({
  form,
  kind,
  lists,
  onClose,
  onSaved,
}: {
  form: SubscribeForm | null
  kind: FormKind
  lists: MailList[]
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState(form?.name ?? "")
  const inquiry = kind === "inquiry"
  const [listId, setListId] = useState(form?.list_id ?? lists[0]?.id ?? "")
  const [origins, setOrigins] = useState((form?.allowed_origins ?? []).join("\n"))
  const [thanksEn, setThanksEn] = useState(form?.thanks_en ?? "")
  const [thanksAr, setThanksAr] = useState(form?.thanks_ar ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const listItems = lists.map((l) => ({ value: l.id, label: l.name }))
  const parsed = parseOrigins(origins)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    // An inquiry form has no list; its kind is fixed when it is created.
    const common = { name: name.trim(), allowed_origins: parsed, thanks_en: thanksEn.trim(), thanks_ar: thanksAr.trim() }
    const json = inquiry ? (form ? common : { ...common, kind }) : { ...common, list_id: listId }
    try {
      if (form) {
        await api(`/api/w/subscribe-forms/${encodeURIComponent(form.id)}`, { method: "PATCH", json, workspaceId: ws.id })
        toast.success(t("forms.saved"))
      } else {
        await api("/api/w/subscribe-forms", { json, workspaceId: ws.id })
        toast.success(t("forms.created"))
      }
      onSaved()
      onClose()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(v) => (v ? null : onClose())}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} className="flex flex-col gap-4" data-form={inquiry ? "inquiry-form" : "subscribe-form"}>
          <DialogHeader>
            <DialogTitle>{form ? t(inquiry ? "forms.editTitleInquiry" : "forms.editTitle") : t(inquiry ? "forms.addInquiry" : "forms.add")}</DialogTitle>
            <DialogDescription>{t(inquiry ? "forms.dialogHintInquiry" : "forms.dialogHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="form-name">{t("forms.name")}</FieldLabel>
              <Input
                id="form-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={80}
                placeholder={t(inquiry ? "forms.namePlaceholderInquiry" : "forms.namePlaceholder")}
              />
            </Field>
            {inquiry ? null : (
            <Field>
              <FieldLabel htmlFor="form-list">{t("forms.list")}</FieldLabel>
              <Select items={listItems} value={listId} onValueChange={(v) => setListId(String(v ?? ""))}>
                <SelectTrigger id="form-list" className="w-full">
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
            )}
            <Field>
              <FieldLabel htmlFor="form-origins">{t("forms.allowedOrigins")}</FieldLabel>
              <Textarea
                id="form-origins"
                dir="ltr"
                rows={3}
                value={origins}
                onChange={(e) => setOrigins(e.target.value)}
                placeholder={"https://example.com\nhttps://www.example.com"}
              />
              <FieldDescription>{t("forms.allowedOriginsHint")}</FieldDescription>
              {parsed.length === 0 ? (
                <p className="flex items-center gap-1 text-xs text-grid-warn-text" role="status" data-no-origins-warning>
                  <TriangleAlertIcon className="size-3.5 shrink-0" />
                  {t("forms.noOrigins")}
                </p>
              ) : null}
            </Field>
            <Field>
              <FieldLabel htmlFor="form-thanks-en">
                {t("forms.thanksEn")} <span className="text-grid-muted">({t("common.optional")})</span>
              </FieldLabel>
              <Input id="form-thanks-en" dir="ltr" value={thanksEn} onChange={(e) => setThanksEn(e.target.value)} maxLength={300} />
            </Field>
            <Field>
              <FieldLabel htmlFor="form-thanks-ar">
                {t("forms.thanksAr")} <span className="text-grid-muted">({t("common.optional")})</span>
              </FieldLabel>
              <Input id="form-thanks-ar" dir="rtl" value={thanksAr} onChange={(e) => setThanksAr(e.target.value)} maxLength={300} />
              <FieldDescription>{t(inquiry ? "forms.thanksHintInquiry" : "forms.thanksHint")}</FieldDescription>
            </Field>
          </FieldGroup>
          {err ? <FieldError>{err}</FieldError> : null}
          <DialogFooter>
            <Button type="submit" disabled={busy || !name.trim() || (!inquiry && !listId)}>
              {busy ? t("common.working") : form ? t("common.save") : t("forms.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
