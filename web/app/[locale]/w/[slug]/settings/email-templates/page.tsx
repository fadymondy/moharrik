"use client"

import { useState, type FormEvent } from "react"
import { PencilIcon, PlusIcon, SparklesIcon } from "lucide-react"
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Field, FieldDescription, FieldLabel, Input, Textarea, toast } from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError } from "@/components/form-layout"

import { Bdi, isolate } from "@/components/bidi"
import { ConfirmButton } from "@/components/confirm-button"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type EmailTemplate } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useEmailTemplates } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

/**
 * Settings › Email templates (MH-661). Canned replies' bilingual,
 * subject-carrying sibling for email: every member sees them and uses them
 * from the reply box's template picker; only a manager adds, edits or
 * deletes them — the API refuses the rest, and the page does not offer what
 * would be refused.
 */
export default function EmailTemplatesPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("inbox.emailTemplates.title")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")
  const templates = useEmailTemplates(ws.id)
  const [editing, setEditing] = useState<EmailTemplate | "new" | null>(null)
  const list = templates.data?.templates ?? []

  return (
    <>
      <SectionHeader
        micro={t("nav.settings")}
        title={t("inbox.emailTemplates.title")}
        description={t("inbox.emailTemplates.hint")}
        action={
          canManage ? (
            <Button
              onClick={() => setEditing("new")}
              disabled={!templates.data}
              data-action="add-email-template"
            >
              <PlusIcon />
              {t("inbox.emailTemplates.add")}
            </Button>
          ) : undefined
        }
      />
      {!canManage ? (
        <p className="px-6 pb-4 text-sm text-muted-foreground">{t("inbox.emailTemplates.readOnly")}</p>
      ) : null}

      {templates.error ? (
        <ErrorState error={templates.error} />
      ) : !templates.data ? (
        <LoadingRows />
      ) : list.length === 0 ? (
        <EmptyState
          title={t("inbox.emailTemplates.empty")}
          body={canManage ? t("inbox.emailTemplates.emptyBody") : undefined}
        />
      ) : (
        <ul className="divide-y divide-nq-line border-y border-nq-line" data-email-template-list>
          {list.map((tpl) => (
            <EmailTemplateRow
              key={tpl.id}
              tpl={tpl}
              canManage={canManage}
              onEdit={() => setEditing(tpl)}
              onChange={() => templates.mutate()}
            />
          ))}
        </ul>
      )}
      <div className="pb-8" />

      <Dialog open={editing !== null} onOpenChange={(v) => (v ? null : setEditing(null))}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              {editing === "new" ? t("inbox.emailTemplates.add") : t("inbox.emailTemplates.editTitle")}
            </DialogTitle>
            <DialogDescription>{t("inbox.emailTemplates.bodyHint")}</DialogDescription>
          </DialogHeader>
          {editing !== null ? (
            <EmailTemplateForm
              template={editing === "new" ? null : editing}
              onSaved={() => {
                setEditing(null)
                templates.mutate()
              }}
            />
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  )
}

function EmailTemplateRow({
  tpl,
  canManage,
  onEdit,
  onChange,
}: {
  tpl: EmailTemplate
  canManage: boolean
  onEdit: () => void
  onChange: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()

  async function remove() {
    await api(`/api/w/email_templates/${encodeURIComponent(tpl.id)}`, {
      method: "DELETE",
      workspaceId: ws.id,
    })
    toast.success(t("inbox.emailTemplates.deleted"))
    onChange()
  }

  return (
    <li className="px-6 py-5" data-email-template-row={tpl.name}>
      <div className="flex flex-wrap items-center gap-3">
        <p className="text-sm font-medium">
          <Bdi>{tpl.name}</Bdi>
        </p>
        {canManage ? (
          <div className="ms-auto flex items-center gap-2">
            <Button variant="secondary" size="sm" onClick={onEdit} data-action="edit-email-template">
              <PencilIcon />
              {t("inbox.emailTemplates.edit")}
            </Button>
            <ConfirmButton
              label={t("common.delete")}
              title={t("inbox.emailTemplates.deleteTitle")}
              description={t("inbox.emailTemplates.deleteBody", { title: isolate(tpl.name) })}
              confirmLabel={t("common.delete")}
              onConfirm={remove}
            />
          </div>
        ) : null}
      </div>
      <div className="mt-3 grid gap-3 md:grid-cols-2">
        <Variant
          label={t("inbox.emailTemplates.subjectEn")}
          subject={tpl.subject_en}
          text={tpl.body_en}
          dir="ltr"
          lang="en"
        />
        <Variant
          label={t("inbox.emailTemplates.subjectAr")}
          subject={tpl.subject_ar}
          text={tpl.body_ar}
          dir="rtl"
          lang="ar"
        />
      </div>
    </li>
  )
}

function Variant({
  label,
  subject,
  text,
  dir,
  lang,
}: {
  label: string
  subject: string
  text: string
  dir: "ltr" | "rtl"
  lang: "en" | "ar"
}) {
  const { t } = useTranslations()
  return (
    <div className="min-w-0 space-y-1" data-email-template-variant={lang}>
      <p className="eyebrow">{label}</p>
      {text || subject ? (
        <div
          dir={dir}
          lang={lang}
          className="space-y-1 rounded-md border border-nq-line bg-card px-3 py-2 text-sm"
        >
          {subject ? <p className="font-medium">{subject}</p> : null}
          {text ? <p className="break-words whitespace-pre-wrap text-muted-foreground">{text}</p> : null}
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">
          {t("inbox.emailTemplates.missing", { language: t(`inbox.canned.language.${lang}`) })}
        </p>
      )}
    </div>
  )
}

function EmailTemplateForm({
  template,
  onSaved,
}: {
  template: EmailTemplate | null
  onSaved: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState(template?.name ?? "")
  const [subjectEn, setSubjectEn] = useState(template?.subject_en ?? "")
  const [subjectAr, setSubjectAr] = useState(template?.subject_ar ?? "")
  const [bodyEn, setBodyEn] = useState(template?.body_en ?? "")
  const [bodyAr, setBodyAr] = useState(template?.body_ar ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [preview, setPreview] = useState<{ subject: string; body: string } | null>(null)
  const [previewing, setPreviewing] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    const json = {
      name: name.trim(),
      subject_en: subjectEn.trim(),
      subject_ar: subjectAr.trim(),
      body_en: bodyEn.trim(),
      body_ar: bodyAr.trim(),
    }
    try {
      if (template) {
        await api(`/api/w/email_templates/${encodeURIComponent(template.id)}`, {
          method: "PATCH",
          json,
          workspaceId: ws.id,
        })
      } else {
        await api("/api/w/email_templates", { json, workspaceId: ws.id })
      }
      toast.success(t("inbox.emailTemplates.saved"))
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      setBusy(false)
    }
  }

  // MH-661: a live preview with sample data, saved or not — the same
  // placeholder resolver the send path uses, so what's shown here is exactly
  // what a customer would see. Only meaningful once the template exists.
  async function runPreview() {
    if (!template) return
    setPreviewing(true)
    try {
      const res = await api<{ subject: string; body: string }>(
        `/api/w/email_templates/${encodeURIComponent(template.id)}/render`,
        { json: {}, workspaceId: ws.id },
      )
      setPreview(res)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setPreviewing(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-4" data-form="email-template">
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="email-template-name">{t("inbox.emailTemplates.nameLabel")}</FieldLabel>
          <Input
            id="email-template-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={80}
          />
          <FieldDescription>{t("inbox.emailTemplates.nameHint")}</FieldDescription>
        </Field>
        <div className="grid gap-4 md:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="email-template-subject-en">{t("inbox.emailTemplates.subjectEn")}</FieldLabel>
            <Input
              id="email-template-subject-en"
              dir="ltr"
              lang="en"
              value={subjectEn}
              onChange={(e) => setSubjectEn(e.target.value)}
              maxLength={500}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="email-template-subject-ar">{t("inbox.emailTemplates.subjectAr")}</FieldLabel>
            <Input
              id="email-template-subject-ar"
              dir="rtl"
              lang="ar"
              value={subjectAr}
              onChange={(e) => setSubjectAr(e.target.value)}
              maxLength={500}
            />
          </Field>
        </div>
        <div className="grid gap-4 md:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="email-template-body-en">{t("inbox.emailTemplates.bodyEn")}</FieldLabel>
            <Textarea
              id="email-template-body-en"
              dir="ltr"
              lang="en"
              rows={6}
              value={bodyEn}
              onChange={(e) => setBodyEn(e.target.value)}
              maxLength={4000}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="email-template-body-ar">{t("inbox.emailTemplates.bodyAr")}</FieldLabel>
            <Textarea
              id="email-template-body-ar"
              dir="rtl"
              lang="ar"
              rows={6}
              value={bodyAr}
              onChange={(e) => setBodyAr(e.target.value)}
              maxLength={4000}
            />
          </Field>
        </div>
        <p className="text-xs text-muted-foreground">{t("inbox.emailTemplates.placeholdersHint")}</p>
      </FieldGroup>

      {template ? (
        <div className="space-y-2">
          <Button type="button" variant="secondary" size="sm" onClick={runPreview} disabled={previewing} data-action="preview-email-template">
            <SparklesIcon />
            {previewing ? t("common.working") : t("inbox.emailTemplates.preview")}
          </Button>
          {preview ? (
            <div
              className="space-y-1 rounded-md border border-nq-line bg-card px-3 py-2 text-sm"
              dir="auto"
              data-email-template-preview
            >
              <p className="font-medium">{preview.subject}</p>
              <p className="break-words whitespace-pre-wrap text-muted-foreground">{preview.body}</p>
            </div>
          ) : null}
        </div>
      ) : null}

      {err ? <FieldError>{err}</FieldError> : null}
      <DialogFooter>
        <Button
          type="submit"
          disabled={busy || !name.trim() || (!bodyEn.trim() && !bodyAr.trim())}
        >
          {busy ? t("common.working") : t("inbox.emailTemplates.save")}
        </Button>
      </DialogFooter>
    </form>
  )
}
