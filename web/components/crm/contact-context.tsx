"use client"

import { useRef, useState } from "react"
import {
  FileTextIcon,
  MessagesSquareIcon,
  SearchIcon,
  SparklesIcon,
  Trash2Icon,
  UploadIcon,
} from "lucide-react"
import { Badge, Button, Field, FieldDescription, FieldLabel, Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, Textarea, toast } from "@fadymondy/nasaq/web"
import { FormHint } from "@/components/form-layout"

import { ConfirmButton } from "@/components/confirm-button"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Bdi } from "@/components/bidi"
import { Ltr } from "@/components/copy-field"
import { PickSelect } from "@/components/crm/shared"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  type ContactReport,
  type ContextAnswer,
  type WhatsAppExportPreview,
  type WhatsAppImportResult,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"
import { useContactContext } from "@/lib/queries"

/*
"Ask about this contact" (MH-586): a searchable context index over a
contact's notes, imported WhatsApp history, all-channel conversations, deals
and MH-583 meta fields — plus the WhatsApp export upload flow and versioned
relationship reports. Every path here works with zero AI provider configured
(see internal/contactcontext/ask.go and report.go's doc comments): the UI
never blocks on that, it just shows `no_provider` plainly instead of a
generated sentence.
*/
function readAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const result = String(reader.result ?? "")
      resolve(result.slice(result.indexOf(",") + 1))
    }
    reader.onerror = () => reject(reader.error ?? new Error("read failed"))
    reader.readAsDataURL(file)
  })
}

export function ContactContextPanel({
  contact,
}: {
  contact: { id: string; name: string; email: string; phone: string }
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const canRead = ws.can("contacts:read")
  const canWrite = ws.can("contacts:write")
  const ctx = useContactContext(ws.id, contact.id, canRead)
  const when = dateFormat(locale, { dateStyle: "medium", timeStyle: "short" })
  const [importOpen, setImportOpen] = useState(false)

  if (!canRead) return null

  return (
    <section className="px-6 pb-6" data-contact-context-panel>
      <div className="border border-nq-line bg-card">
        <div className="flex flex-wrap items-center gap-3 border-b border-nq-line px-5 py-4">
          <h2 className="text-base font-medium">{t("contactContext.title")}</h2>
          {canWrite ? (
            <Button
              variant="secondary"
              size="sm"
              className="ms-auto"
              onClick={() => setImportOpen(true)}
              data-action="import-whatsapp"
            >
              <UploadIcon />
              {t("contactContext.importWhatsApp")}
            </Button>
          ) : null}
        </div>

        {ctx.error ? <div className="p-5">{<ErrorState error={ctx.error} />}</div> : null}
        {!ctx.error && !ctx.data ? (
          <div className="p-5">
            <LoadingRows rows={3} />
          </div>
        ) : null}

        {ctx.data ? (
          <div className="flex flex-col gap-6 px-5 py-5">
            <ImportsList
              imports={ctx.data.imports}
              contactId={contact.id}
              canWrite={canWrite}
              onChanged={() => ctx.mutate()}
              when={when}
            />
            <AskPanel contactId={contact.id} />
            <ReportsPanel contactId={contact.id} canWrite={canWrite} when={when} />
          </div>
        ) : null}
      </div>

      <WhatsAppImportSheet
        open={importOpen}
        onOpenChange={setImportOpen}
        contactId={contact.id}
        contactName={contact.name}
        onImported={() => ctx.mutate()}
      />
    </section>
  )
}

function ImportsList({
  imports,
  contactId,
  canWrite,
  onChanged,
  when,
}: {
  imports: {
    id: string
    filename: string
    contact_sender: string
    message_count: number
    media_count: number
    created_at: string
  }[]
  contactId: string
  canWrite: boolean
  onChanged: () => void
  when: { format: (d: Date | number) => string }
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()

  async function remove(importId: string) {
    try {
      await api(
        `/api/w/contacts/${encodeURIComponent(contactId)}/context/imports/${encodeURIComponent(importId)}`,
        {
          method: "DELETE",
          workspaceId: ws.id,
        },
      )
      toast.success(t("contactContext.importRemoved"))
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  if (imports.length === 0) return null

  return (
    <div data-contact-context-imports>
      <h3 className="mb-2 text-sm font-medium text-muted-foreground">{t("contactContext.imports")}</h3>
      <ul className="flex flex-col gap-2">
        {imports.map((imp) => (
          <li
            key={imp.id}
            className="flex flex-wrap items-center gap-3 border border-nq-line px-3 py-2 text-sm"
            data-whatsapp-import={imp.id}
          >
            <MessagesSquareIcon className="size-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 truncate">
              <Ltr>{imp.filename}</Ltr>
            </span>
            <span className="text-muted-foreground">
              {t("contactContext.importCounts", {
                messages: imp.message_count,
                media: imp.media_count,
              })}
            </span>
            <span className="text-xs text-muted-foreground">
              <Ltr>{when.format(new Date(imp.created_at))}</Ltr>
            </span>
            {canWrite ? (
              <span className="ms-auto" data-remove-import={imp.id}>
                <ConfirmButton
                  label={
                    <>
                      <Trash2Icon />
                      {t("contactContext.removeImport")}
                    </>
                  }
                  title={t("contactContext.removeImportTitle")}
                  description={t("contactContext.removeImportBody", { file: imp.filename })}
                  confirmLabel={t("common.delete")}
                  onConfirm={() => remove(imp.id)}
                />
              </span>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  )
}

function AskPanel({ contactId }: { contactId: string }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const when = dateFormat(locale, { dateStyle: "medium", timeStyle: "short" })
  const [question, setQuestion] = useState("")
  const [asking, setAsking] = useState(false)
  const [answer, setAnswer] = useState<ContextAnswer | null>(null)
  const [error, setError] = useState<ApiError | Error | null>(null)

  async function ask() {
    if (!question.trim()) return
    setAsking(true)
    setError(null)
    try {
      const res = await api<ContextAnswer>(
        `/api/w/contacts/${encodeURIComponent(contactId)}/context/ask`,
        {
          json: { question: question.trim() },
          workspaceId: ws.id,
        },
      )
      setAnswer(res)
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)))
    } finally {
      setAsking(false)
    }
  }

  return (
    <div data-contact-context-ask>
      <h3 className="mb-2 text-sm font-medium text-muted-foreground">{t("contactContext.askTitle")}</h3>
      <div className="flex flex-wrap items-end gap-3">
        <Field className="min-w-0 flex-1">
          <FieldLabel htmlFor="cc-question" className="sr-only">
            {t("contactContext.askTitle")}
          </FieldLabel>
          <Textarea
            id="cc-question"
            rows={2}
            value={question}
            onChange={(e) => setQuestion(e.target.value)}
            placeholder={t("contactContext.askPlaceholder")}
            data-testid="contact-context-question"
          />
        </Field>
        <Button
          type="button"
          onClick={ask}
          disabled={!question.trim() || asking}
          data-action="ask-contact-context"
        >
          <SearchIcon />
          {asking ? t("contactContext.asking") : t("contactContext.ask")}
        </Button>
      </div>

      {error ? <ErrorState error={error} /> : null}

      {answer ? (
        <div
          className="mt-3 border border-nq-line bg-nq-bg p-4 text-sm"
          data-testid="contact-context-answer"
          data-no-provider={answer.no_provider}
        >
          {answer.no_provider ? (
            <p
              className="mb-2 flex items-center gap-1.5 text-xs text-muted-foreground"
              data-answer-no-provider
            >
              <SparklesIcon className="size-3.5" />
              {t("contactContext.noProvider")}
            </p>
          ) : null}
          <p className="whitespace-pre-wrap">{answer.text}</p>
          {answer.citations.length > 0 ? (
            <ul
              className="mt-3 flex flex-col gap-1.5 border-t border-nq-line pt-3"
              data-testid="contact-context-citations"
            >
              {answer.citations.map((c, i) => (
                <li
                  key={i}
                  className="flex flex-wrap items-start gap-2 text-xs text-muted-foreground"
                  data-citation-kind={c.kind}
                >
                  <Badge variant="neutral">{t(`contactContext.citationKind.${c.kind}`)}</Badge>
                  {c.when ? (
                    <span>
                      <Ltr>{when.format(new Date(c.when.replace(" ", "T")))}</Ltr>
                    </span>
                  ) : null}
                  <span className="min-w-0 flex-1 truncate">{c.snippet}</span>
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}

function ReportsPanel({
  contactId,
  canWrite,
  when,
}: {
  contactId: string
  canWrite: boolean
  when: { format: (d: Date | number) => string }
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [reports, setReports] = useState<ContactReport[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [generating, setGenerating] = useState(false)
  const [error, setError] = useState<ApiError | Error | null>(null)
  const [open, setOpen] = useState<string | null>(null)

  async function load() {
    setLoading(true)
    setError(null)
    try {
      const res = await api<ContactReport[]>(
        `/api/w/contacts/${encodeURIComponent(contactId)}/context/reports`,
        { workspaceId: ws.id },
      )
      setReports(res ?? [])
    } catch (e) {
      setError(e instanceof Error ? e : new Error(String(e)))
    } finally {
      setLoading(false)
    }
  }

  async function generate() {
    setGenerating(true)
    try {
      const res = await api<ContactReport>(
        `/api/w/contacts/${encodeURIComponent(contactId)}/context/reports`,
        {
          method: "POST",
          workspaceId: ws.id,
        },
      )
      setReports((prev) => [res, ...(prev ?? [])])
      setOpen(res.id)
      toast.success(t("contactContext.reportGenerated", { version: res.version }))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setGenerating(false)
    }
  }

  function download(r: ContactReport) {
    const blob = new Blob([r.markdown], { type: "text/markdown;charset=utf-8" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = `contact-report-v${r.version}.md`
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 10_000)
  }

  if (reports === null && !loading && !error) {
    void load()
  }

  return (
    <div data-contact-context-reports>
      <div className="mb-2 flex flex-wrap items-center gap-3">
        <h3 className="text-sm font-medium text-muted-foreground">{t("contactContext.reportsTitle")}</h3>
        {canWrite ? (
          <Button
            variant="secondary"
            size="sm"
            className="ms-auto"
            onClick={generate}
            disabled={generating}
            data-action="generate-report"
          >
            <FileTextIcon />
            {generating ? t("contactContext.generating") : t("contactContext.generateReport")}
          </Button>
        ) : null}
      </div>

      {error ? <ErrorState error={error} /> : null}
      {loading ? <LoadingRows rows={1} /> : null}

      {reports && reports.length === 0 ? (
        <EmptyState title={t("contactContext.noReports")} />
      ) : null}

      {reports && reports.length > 0 ? (
        <ul className="flex flex-col gap-2" data-testid="contact-context-report-list">
          {reports.map((r) => (
            <li
              key={r.id}
              className="border border-nq-line"
              data-contact-report={r.id}
              data-report-version={r.version}
            >
              <button
                type="button"
                className="flex w-full items-center gap-3 px-3 py-2 text-start text-sm"
                onClick={() => setOpen(open === r.id ? null : r.id)}
                data-action="toggle-report"
              >
                <span className="font-medium">
                  {t("contactContext.reportVersion", { version: r.version })}
                </span>
                {r.no_provider ? (
                  <Badge variant="neutral" data-report-no-provider>
                    {t("contactContext.noProviderShort")}
                  </Badge>
                ) : null}
                <span className="ms-auto text-xs text-muted-foreground">{open === r.id ? "▲" : "▼"}</span>
              </button>
              {open === r.id ? (
                <div className="border-t border-nq-line px-3 py-3">
                  <pre
                    className="max-h-96 overflow-auto whitespace-pre-wrap font-sans text-sm"
                    data-testid="contact-report-markdown"
                  >
                    {r.markdown}
                  </pre>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="mt-2"
                    onClick={() => download(r)}
                    data-action="download-report"
                  >
                    {t("contactContext.downloadReport")}
                  </Button>
                </div>
              ) : null}
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  )
}

const WA_TYPES = [".txt", ".zip"]

function WhatsAppImportSheet({
  open,
  onOpenChange,
  contactId,
  contactName,
  onImported,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  contactId: string
  contactName: string
  onImported: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const fileInput = useRef<HTMLInputElement>(null)
  const [fileName, setFileName] = useState<string | null>(null)
  const [data, setData] = useState<string | null>(null)
  const [preview, setPreview] = useState<WhatsAppExportPreview | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const [previewError, setPreviewError] = useState<ApiError | Error | null>(null)
  const [sender, setSender] = useState("")
  const [committing, setCommitting] = useState(false)
  const [result, setResult] = useState<WhatsAppImportResult | null>(null)

  function reset() {
    setFileName(null)
    setData(null)
    setPreview(null)
    setPreviewError(null)
    setSender("")
    setResult(null)
  }

  async function onFilePicked(f: File | null) {
    reset()
    if (!f) return
    setFileName(f.name)
    setPreviewing(true)
    try {
      const b64 = await readAsBase64(f)
      setData(b64)
      const res = await api<WhatsAppExportPreview>(
        `/api/w/contacts/${encodeURIComponent(contactId)}/context/whatsapp/preview`,
        {
          json: { filename: f.name, data: b64 },
          workspaceId: ws.id,
        },
      )
      setPreview(res)
      setSender(res.guessed_contact || res.senders[0] || "")
    } catch (e) {
      setPreviewError(e instanceof Error ? e : new Error(String(e)))
    } finally {
      setPreviewing(false)
    }
  }

  async function commit() {
    if (!data || !fileName || !sender) return
    setCommitting(true)
    try {
      const res = await api<WhatsAppImportResult>(
        `/api/w/contacts/${encodeURIComponent(contactId)}/context/whatsapp/commit`,
        {
          json: { filename: fileName, data, contact_sender: sender },
          workspaceId: ws.id,
        },
      )
      setResult(res)
      toast.success(t("contactContext.importDone", { count: res.messages_added }))
      onImported()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setCommitting(false)
    }
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v)
        if (!v) reset()
      }}
    >
      <SheetContent data-testid="whatsapp-import-sheet">
        <SheetHeader>
          <SheetTitle>{t("contactContext.importWhatsApp")}</SheetTitle>
          <SheetDescription>
            {t("contactContext.importHint", { name: contactName })}
          </SheetDescription>
        </SheetHeader>

        <div className="flex flex-col gap-4 px-4 pb-4">
          <input
            ref={fileInput}
            type="file"
            accept={WA_TYPES.join(",")}
            className="sr-only"
            onChange={(e) => void onFilePicked(e.target.files?.[0] ?? null)}
            data-testid="whatsapp-file-input"
          />
          <div className="flex min-w-0 flex-wrap items-center gap-3">
            <Button
              type="button"
              variant="secondary"
              onClick={() => fileInput.current?.click()}
              data-action="pick-whatsapp-file"
            >
              <UploadIcon />
              {t("contactContext.chooseFile")}
            </Button>
            <span className="min-w-0 truncate text-sm text-muted-foreground" data-whatsapp-file-name>
              {fileName ?? t("contactContext.noFile")}
            </span>
          </div>
          <FormHint>{t("contactContext.importFormats")}</FormHint>

          {previewing ? <LoadingRows rows={2} /> : null}
          {previewError ? <ErrorState error={previewError} /> : null}

          {preview ? (
            <div className="flex flex-col gap-4" data-testid="whatsapp-preview">
              <div className="flex flex-wrap gap-3 text-sm text-muted-foreground">
                <span data-preview-message-count>
                  {t("contactContext.previewMessages", { count: preview.message_count })}
                </span>
                <span data-preview-media-count>
                  {t("contactContext.previewMedia", { count: preview.media_count })}
                </span>
                {preview.already_imported_estimate > 0 ? (
                  <span data-preview-already-imported>
                    {t("contactContext.previewAlready", {
                      count: preview.already_imported_estimate,
                    })}
                  </span>
                ) : null}
              </div>

              <Field>
                <FieldLabel htmlFor="cc-sender">
                  {t("contactContext.pickSender", { name: contactName })}
                </FieldLabel>
                <PickSelect
                  id="cc-sender"
                  value={sender}
                  onChange={setSender}
                  options={preview.senders.map((s) => ({ value: s, label: s }))}
                />
              </Field>

              {preview.sample.length > 0 ? (
                <ul
                  className="flex max-h-40 flex-col gap-1 overflow-auto border border-nq-line p-2 text-xs"
                  data-testid="whatsapp-sample"
                >
                  {preview.sample.slice(0, 5).map((s, i) => (
                    <li key={i} className="text-muted-foreground">
                      <Bdi>{s.sender ? `${s.sender}: ` : ""}</Bdi>
                      {s.text}
                    </li>
                  ))}
                </ul>
              ) : null}

              {result ? (
                <div
                  className="border border-nq-line p-3 text-sm"
                  data-testid="whatsapp-commit-result"
                >
                  {t("contactContext.importDone", { count: result.messages_added })}
                  {result.messages_skipped_duplicate > 0
                    ? ` ${t("contactContext.importSkipped", { count: result.messages_skipped_duplicate })}`
                    : null}
                </div>
              ) : (
                <Button
                  type="button"
                  onClick={commit}
                  disabled={!sender || committing}
                  data-action="commit-whatsapp-import"
                >
                  {committing ? t("contactContext.importing") : t("contactContext.commitImport")}
                </Button>
              )}
            </div>
          ) : null}
        </div>
      </SheetContent>
    </Sheet>
  )
}
