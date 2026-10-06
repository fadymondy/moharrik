"use client"

import { useState, type FormEvent } from "react"
import { PencilIcon, PlusIcon } from "lucide-react"
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Field, FieldDescription, FieldLabel, Input, Textarea, toast } from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError } from "@/components/form-layout"

import { Bdi, isolate } from "@/components/bidi"
import { ConfirmButton } from "@/components/confirm-button"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type CannedReply } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCannedReplies } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

/**
 * Settings › Canned replies (MH-383). Every member sees them (and inserts
 * them from the reply box); only a manager adds, edits or deletes them — the
 * API refuses the rest, and the page does not offer what would be refused.
 */
export default function CannedRepliesPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("inbox.canned")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")
  const canned = useCannedReplies(ws.id)
  const [editing, setEditing] = useState<CannedReply | "new" | null>(null)
  const list = canned.data?.replies ?? []

  return (
    <>
      <SectionHeader
        micro={t("nav.settings")}
        title={t("inbox.canned")}
        description={t("inbox.canned.hint")}
        action={
          canManage ? (
            <Button
              onClick={() => setEditing("new")}
              disabled={!canned.data}
              data-action="add-canned"
            >
              <PlusIcon />
              {t("inbox.canned.add")}
            </Button>
          ) : undefined
        }
      />
      {!canManage ? (
        <p className="px-6 pb-4 text-sm text-muted-foreground">{t("inbox.canned.readOnly")}</p>
      ) : null}

      {canned.error ? (
        <ErrorState error={canned.error} />
      ) : !canned.data ? (
        <LoadingRows />
      ) : list.length === 0 ? (
        <EmptyState
          title={t("inbox.canned.empty")}
          body={canManage ? t("inbox.canned.emptyBody") : undefined}
        />
      ) : (
        <ul className="divide-y divide-nq-line border-y border-nq-line" data-canned-list>
          {list.map((r) => (
            <CannedRow
              key={r.id}
              r={r}
              canManage={canManage}
              onEdit={() => setEditing(r)}
              onChange={() => canned.mutate()}
            />
          ))}
        </ul>
      )}
      <div className="pb-8" />

      <Dialog open={editing !== null} onOpenChange={(v) => (v ? null : setEditing(null))}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>
              {editing === "new" ? t("inbox.canned.add") : t("inbox.canned.editTitle")}
            </DialogTitle>
            <DialogDescription>{t("inbox.canned.bodyHint")}</DialogDescription>
          </DialogHeader>
          {editing !== null ? (
            <CannedForm
              reply={editing === "new" ? null : editing}
              onSaved={() => {
                setEditing(null)
                canned.mutate()
              }}
            />
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  )
}

function CannedRow({
  r,
  canManage,
  onEdit,
  onChange,
}: {
  r: CannedReply
  canManage: boolean
  onEdit: () => void
  onChange: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()

  async function remove() {
    await api(`/api/w/canned_replies/${encodeURIComponent(r.id)}`, {
      method: "DELETE",
      workspaceId: ws.id,
    })
    toast.success(t("inbox.canned.deleted"))
    onChange()
  }

  return (
    <li className="px-6 py-5" data-canned-row={r.title}>
      <div className="flex flex-wrap items-center gap-3">
        <p className="text-sm font-medium">
          <Bdi>{r.title}</Bdi>
        </p>
        {canManage ? (
          <div className="ms-auto flex items-center gap-2">
            <Button variant="secondary" size="sm" onClick={onEdit} data-action="edit-canned">
              <PencilIcon />
              {t("inbox.canned.edit")}
            </Button>
            <ConfirmButton
              label={t("common.delete")}
              title={t("inbox.canned.deleteTitle")}
              description={t("inbox.canned.deleteBody", { title: isolate(r.title) })}
              confirmLabel={t("common.delete")}
              onConfirm={remove}
            />
          </div>
        ) : null}
      </div>
      <div className="mt-3 grid gap-3 md:grid-cols-2">
        <Variant label={t("inbox.canned.bodyEn")} text={r.body_en} dir="ltr" lang="en" />
        <Variant label={t("inbox.canned.bodyAr")} text={r.body_ar} dir="rtl" lang="ar" />
      </div>
    </li>
  )
}

function Variant({
  label,
  text,
  dir,
  lang,
}: {
  label: string
  text: string
  dir: "ltr" | "rtl"
  lang: "en" | "ar"
}) {
  const { t } = useTranslations()
  return (
    <div className="min-w-0 space-y-1" data-canned-variant={lang}>
      <p className="eyebrow">{label}</p>
      {text ? (
        <p
          dir={dir}
          lang={lang}
          className="rounded-md border border-nq-line bg-card px-3 py-2 text-sm break-words whitespace-pre-wrap"
        >
          {text}
        </p>
      ) : (
        <p className="text-sm text-muted-foreground">
          {t("inbox.canned.missing", { language: t(`inbox.canned.language.${lang}`) })}
        </p>
      )}
    </div>
  )
}

function CannedForm({ reply, onSaved }: { reply: CannedReply | null; onSaved: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [title, setTitle] = useState(reply?.title ?? "")
  const [bodyEn, setBodyEn] = useState(reply?.body_en ?? "")
  const [bodyAr, setBodyAr] = useState(reply?.body_ar ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    const json = { title: title.trim(), body_en: bodyEn.trim(), body_ar: bodyAr.trim() }
    try {
      if (reply) {
        await api(`/api/w/canned_replies/${encodeURIComponent(reply.id)}`, {
          method: "PATCH",
          json,
          workspaceId: ws.id,
        })
      } else {
        await api("/api/w/canned_replies", { json, workspaceId: ws.id })
      }
      toast.success(t("inbox.canned.saved"))
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-4" data-form="canned">
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="canned-title">{t("inbox.canned.titleLabel")}</FieldLabel>
          <Input
            id="canned-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            maxLength={80}
          />
          <FieldDescription>{t("inbox.canned.titleHint")}</FieldDescription>
        </Field>
        <div className="grid gap-4 md:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="canned-en">{t("inbox.canned.bodyEn")}</FieldLabel>
            <Textarea
              id="canned-en"
              dir="ltr"
              lang="en"
              rows={6}
              value={bodyEn}
              onChange={(e) => setBodyEn(e.target.value)}
              maxLength={4000}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="canned-ar">{t("inbox.canned.bodyAr")}</FieldLabel>
            <Textarea
              id="canned-ar"
              dir="rtl"
              lang="ar"
              rows={6}
              value={bodyAr}
              onChange={(e) => setBodyAr(e.target.value)}
              maxLength={4000}
            />
          </Field>
        </div>
      </FieldGroup>
      {err ? <FieldError>{err}</FieldError> : null}
      <DialogFooter>
        <Button
          type="submit"
          disabled={busy || !title.trim() || (!bodyEn.trim() && !bodyAr.trim())}
        >
          {busy ? t("common.working") : t("inbox.canned.save")}
        </Button>
      </DialogFooter>
    </form>
  )
}
