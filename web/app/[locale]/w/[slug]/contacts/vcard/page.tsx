"use client"

import Link from "next/link"
import { UploadIcon } from "lucide-react"
import { useRef, useState } from "react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { EmptyState, ErrorState } from "@/components/states"
import { SectionHeader } from "@/components/public-frame"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, downloadFile } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

/*
Contacts → import/export vCard (MH-584): upload or paste a .vcf, preview
which cards are new/will-merge/invalid, commit, and export contacts back out
as one .vcf. Its own page rather than an addition to contacts/import (MH-562,
which is "import from a connection") since a .vcf file is not a connection.
*/

type PreviewRow = {
  index: number
  name: string
  phones: string[]
  emails: string[]
  status: "new" | "merge" | "invalid"
  invalid?: string
  match_contact_ids?: string[]
  local_phone?: boolean
}

type VcardPreview = {
  card_count: number
  new_count: number
  merge_count: number
  invalid_count: number
  rows: PreviewRow[]
  file_hash: string
}

type CommitResult = {
  card_count: number
  created_count: number
  merged_count: number
  invalid_count: number
  contact_ids: string[]
}

export default function VcardPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("vcard.title")} · ${ws.summary.name}`)
  const base = `/${locale}/w/${ws.slug}`
  const canWrite = ws.can("contacts:write")

  const fileInput = useRef<HTMLInputElement>(null)
  const [text, setText] = useState("")
  const [preview, setPreview] = useState<VcardPreview | null>(null)
  const [previewError, setPreviewError] = useState<ApiError | Error | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const [committing, setCommitting] = useState(false)
  const [result, setResult] = useState<CommitResult | null>(null)
  const [exporting, setExporting] = useState(false)
  const [fileName, setFileName] = useState<string | null>(null)

  async function onFilePicked(f: File | null) {
    setFileName(f?.name ?? null)
    if (!f) return
    const content = await f.text()
    setText(content)
    setPreview(null)
    setResult(null)
  }

  async function runPreview() {
    if (!text.trim()) return
    setPreviewing(true)
    setPreviewError(null)
    setResult(null)
    try {
      const res = await api<VcardPreview>("/api/w/contacts/vcard/preview", {
        json: { vcard: text },
        workspaceId: ws.id,
      })
      setPreview(res)
    } catch (e) {
      setPreviewError(e instanceof Error ? e : new Error(String(e)))
    } finally {
      setPreviewing(false)
    }
  }

  async function runCommit() {
    if (!text.trim()) return
    setCommitting(true)
    try {
      const res = await api<CommitResult>("/api/w/contacts/vcard/commit", {
        json: { vcard: text },
        workspaceId: ws.id,
      })
      setResult(res)
      toast.success(t("vcard.commitDone", { created: res.created_count, merged: res.merged_count }))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setCommitting(false)
    }
  }

  async function runExportAll() {
    setExporting(true)
    try {
      await downloadFile("/api/w/contacts/vcard/export", { workspaceId: ws.id, fallbackName: "contacts.vcf" })
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setExporting(false)
    }
  }

  const statusVariant: Record<PreviewRow["status"], "default" | "secondary" | "destructive"> = {
    new: "default",
    merge: "secondary",
    invalid: "destructive",
  }

  return (
    <>
      <SectionHeader
        micro={t("nav.contacts")}
        title={t("vcard.title")}
        description={t("vcard.hint")}
        action={
          <Link className="text-sm underline underline-offset-4" href={`${base}/contacts`}>
            {t("contacts.backToList")}
          </Link>
        }
      />

      <div className="px-6 pb-4">
        <div className="mb-6 flex flex-wrap items-center gap-4 border-b border-line pb-6">
          <Button type="button" variant="outline" onClick={runExportAll} disabled={exporting} data-action="export-vcard">
            {exporting ? t("vcard.exporting") : t("vcard.exportAll")}
          </Button>
          <FieldDescription>{t("vcard.exportHint")}</FieldDescription>
        </div>

        {!canWrite ? <FieldDescription className="pb-4">{t("contacts.readOnly")}</FieldDescription> : null}

        <Field className="mb-4">
          <FieldLabel htmlFor="vcard-file">{t("vcard.pickFile")}</FieldLabel>
          {/* The native picker's own button and "no file chosen" are in the
              browser's language, not the page's — keep the input for the
              file dialog and drive it from a translated button. */}
          <input
            ref={fileInput}
            id="vcard-file"
            type="file"
            accept=".vcf,text/vcard"
            className="sr-only"
            disabled={!canWrite}
            onChange={(e) => onFilePicked(e.target.files?.[0] ?? null)}
            data-testid="vcard-file-input"
          />
          <div className="flex min-w-0 flex-wrap items-center gap-3">
            <Button
              type="button"
              variant="outline"
              disabled={!canWrite}
              onClick={() => fileInput.current?.click()}
              data-action="pick-vcard-file"
            >
              <UploadIcon />
              {t("vcard.chooseFile")}
            </Button>
            <span className="min-w-0 truncate text-sm text-grid-muted" data-vcard-file-name>
              {fileName ?? t("vcard.noFile")}
            </span>
          </div>
        </Field>

        <Field className="mb-4">
          <FieldLabel htmlFor="vcard-text">{t("vcard.orPaste")}</FieldLabel>
          <Textarea
            id="vcard-text"
            rows={8}
            value={text}
            disabled={!canWrite}
            onChange={(e) => {
              setText(e.target.value)
              setPreview(null)
              setResult(null)
            }}
            placeholder="BEGIN:VCARD..."
            data-testid="vcard-text-input"
          />
        </Field>

        <div className="mb-6 flex flex-wrap items-center gap-4">
          <Button type="button" onClick={runPreview} disabled={!canWrite || !text.trim() || previewing} data-action="preview-vcard">
            {previewing ? t("vcard.previewing") : t("vcard.preview")}
          </Button>
          {preview ? (
            <Button
              type="button"
              variant="default"
              onClick={runCommit}
              disabled={!canWrite || committing || preview.card_count === 0}
              data-action="commit-vcard"
            >
              {committing ? t("vcard.committing") : t("vcard.commit", { count: preview.new_count + preview.merge_count })}
            </Button>
          ) : null}
        </div>

        {previewError ? <ErrorState error={previewError} /> : null}

        {result ? (
          <div className="mb-6 rounded-lg border border-line p-4 text-sm" data-testid="vcard-commit-result">
            {t("vcard.commitDone", { created: result.created_count, merged: result.merged_count })}
          </div>
        ) : null}

        {preview ? (
          preview.rows.length === 0 ? (
            <EmptyState title={t("vcard.empty")} body={t("vcard.emptyBody")} />
          ) : (
            <div className="border-y border-line" data-testid="vcard-preview-table">
              <div className="flex flex-wrap gap-4 py-4 text-sm text-grid-muted">
                <span data-testid="vcard-new-count">{t("vcard.newCount", { count: preview.new_count })}</span>
                <span data-testid="vcard-merge-count">{t("vcard.mergeCount", { count: preview.merge_count })}</span>
                <span data-testid="vcard-invalid-count">{t("vcard.invalidCount", { count: preview.invalid_count })}</span>
              </div>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="ps-6">{t("vcard.name")}</TableHead>
                    <TableHead>{t("vcard.phones")}</TableHead>
                    <TableHead>{t("vcard.emails")}</TableHead>
                    <TableHead className="pe-6">{t("vcard.status")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {preview.rows.map((row) => (
                    <TableRow key={row.index} data-vcard-row={row.index}>
                      <TableCell className="ps-6">
                        <Bdi>{row.name || t("vcard.noName")}</Bdi>
                      </TableCell>
                      <TableCell className="text-grid-muted">{row.phones.join(", ")}</TableCell>
                      <TableCell className="text-grid-muted">{row.emails.join(", ")}</TableCell>
                      <TableCell className="pe-6">
                        <Badge variant={statusVariant[row.status]} data-vcard-status={row.status}>
                          {row.status === "invalid" ? row.invalid || t(`vcard.status.${row.status}`) : t(`vcard.status.${row.status}`)}
                        </Badge>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )
        ) : null}
      </div>
    </>
  )
}
