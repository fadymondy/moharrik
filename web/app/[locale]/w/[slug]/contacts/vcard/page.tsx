"use client"

import Link from "next/link"
import { RefreshCwIcon, TriangleAlertIcon, UploadIcon } from "lucide-react"
import { useRef, useState } from "react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { SectionHeader } from "@/components/public-frame"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  downloadFile,
  type GoogleContactsConnection,
  type GoogleContactsSyncResult,
  type GoogleDisconnected,
  type ReparsePhonesResult,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import {
  useGoogleConnections,
  useGoogleContactsConnections,
  useGoogleContactsSyncStatus,
} from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

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
  needs_country?: boolean
}

type VcardPreview = {
  card_count: number
  new_count: number
  merge_count: number
  invalid_count: number
  needs_country_count: number
  rows: PreviewRow[]
  file_hash: string
}

type CommitResult = {
  card_count: number
  created_count: number
  merged_count: number
  invalid_count: number
  needs_country_count: number
  contact_ids: string[]
}

/**
 * NeedsCountryBanner (MH-646): every place a preview/commit/sync result
 * reports phones kept raw because no country could be resolved — shared so
 * the vCard flow and the Google panel say the same thing the same way, with
 * a link to Settings → General to fix it.
 */
function NeedsCountryBanner({ count, settingsHref }: { count: number; settingsHref: string }) {
  const { t } = useTranslations()
  if (count <= 0) return null
  return (
    <p
      className="mb-4 flex items-start gap-2 rounded-lg border border-grid-warn/40 bg-grid-warn/10 p-3 text-sm"
      data-testid="needs-country-banner"
    >
      <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" />
      <span>
        {t("vcard.needsCountry", { count })}{" "}
        <Link className="underline underline-offset-4" href={settingsHref}>
          {t("vcard.needsCountry.link")}
        </Link>
      </span>
    </p>
  )
}

// MH-585: a Google Contacts CSV export (contacts.google.com -> Export) needs
// no OAuth, so it rides the same preview -> commit flow as pasted vCard text
// -- only the request's "source"/"csv" vs "vcard" field differs (see
// internal/vcard/routes.go's importBody). A connected Google account (OAuth +
// People API, sync-now) is the GoogleContactsPanel below -- its own flow, not
// a third ImportSource, since a sync writes straight in with no
// preview/paste step.
type ImportSource = "vcard" | "google_csv"

export default function VcardPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("vcard.title")} · ${ws.summary.name}`)
  const base = `/${locale}/w/${ws.slug}`
  const canWrite = ws.can("contacts:write")
  // Exporting the list is a manager's call, as on Contacts (MH-690); the API
  // refuses anyone below, so the button is not offered to them.
  const canExport = ws.can("contacts:export")

  const [source, setSource] = useState<ImportSource>("vcard")
  const fileInput = useRef<HTMLInputElement>(null)
  const [text, setText] = useState("")
  const [preview, setPreview] = useState<VcardPreview | null>(null)
  const [previewError, setPreviewError] = useState<ApiError | Error | null>(null)
  const [previewing, setPreviewing] = useState(false)
  const [committing, setCommitting] = useState(false)
  const [result, setResult] = useState<CommitResult | null>(null)
  const [exporting, setExporting] = useState(false)
  const [fileName, setFileName] = useState<string | null>(null)
  const [reparsing, setReparsing] = useState(false)

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
        json: source === "google_csv" ? { source, csv: text } : { vcard: text },
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
        json: source === "google_csv" ? { source, csv: text } : { vcard: text },
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

  async function runReparse() {
    setReparsing(true)
    try {
      const res = await api<ReparsePhonesResult>("/api/w/contacts/vcard/reparse-phones", {
        method: "POST",
        workspaceId: ws.id,
      })
      toast.success(
        res.resolved > 0 ? t("vcard.reparseDone", { count: res.resolved }) : t("vcard.reparseNone"),
      )
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setReparsing(false)
    }
  }

  async function runExportAll() {
    setExporting(true)
    try {
      await downloadFile("/api/w/contacts/vcard/export", {
        workspaceId: ws.id,
        fallbackName: "contacts.vcf",
      })
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
        <div
          className="mb-6 flex flex-wrap items-center gap-4 border-b border-line pb-6"
          hidden={!canExport && !canWrite}
        >
          {canExport ? (
            <>
              <Button
                type="button"
                variant="outline"
                onClick={runExportAll}
                disabled={exporting}
                data-action="export-vcard"
              >
                {exporting ? t("vcard.exporting") : t("vcard.exportAll")}
              </Button>
              <FieldDescription>{t("vcard.exportHint")}</FieldDescription>
            </>
          ) : null}
          {canWrite ? (
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={runReparse}
              disabled={reparsing}
              data-action="reparse-vcard-phones"
            >
              {reparsing ? t("vcard.reparsing") : t("vcard.reparse")}
            </Button>
          ) : null}
        </div>

        {/* Import only, and it reads the connected Google account's address book. */}
        {canWrite ? <GoogleContactsPanel /> : null}

        {!canWrite ? (
          <FieldDescription className="pb-4">{t("contacts.readOnly")}</FieldDescription>
        ) : null}

        <Field className="mb-4">
          <FieldLabel>{t("vcard.source")}</FieldLabel>
          <div className="flex flex-wrap gap-2" role="radiogroup" aria-label={t("vcard.source")}>
            <Button
              type="button"
              variant={source === "vcard" ? "default" : "outline"}
              aria-pressed={source === "vcard"}
              onClick={() => {
                setSource("vcard")
                setText("")
                setFileName(null)
                setPreview(null)
                setResult(null)
              }}
              data-action="source-vcard"
            >
              {t("vcard.source.vcard")}
            </Button>
            <Button
              type="button"
              variant={source === "google_csv" ? "default" : "outline"}
              aria-pressed={source === "google_csv"}
              onClick={() => {
                setSource("google_csv")
                setText("")
                setFileName(null)
                setPreview(null)
                setResult(null)
              }}
              data-action="source-google-csv"
            >
              {t("vcard.source.googleCsv")}
            </Button>
          </div>
          {source === "google_csv" ? (
            <FieldDescription>{t("vcard.source.googleCsvHint")}</FieldDescription>
          ) : null}
        </Field>

        <Field className="mb-4">
          <FieldLabel htmlFor="vcard-file">
            {source === "google_csv" ? t("vcard.pickFile.googleCsv") : t("vcard.pickFile")}
          </FieldLabel>
          {/* The native picker's own button and "no file chosen" are in the
              browser's language, not the page's — keep the input for the
              file dialog and drive it from a translated button. */}
          <input
            ref={fileInput}
            id="vcard-file"
            type="file"
            accept={source === "google_csv" ? ".csv,text/csv" : ".vcf,text/vcard"}
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
          <FieldLabel htmlFor="vcard-text">
            {source === "google_csv" ? t("vcard.orPaste.googleCsv") : t("vcard.orPaste")}
          </FieldLabel>
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
            placeholder={source === "google_csv" ? "Name,Given Name,...," : "BEGIN:VCARD..."}
            data-testid="vcard-text-input"
          />
        </Field>

        <div className="mb-6 flex flex-wrap items-center gap-4">
          <Button
            type="button"
            onClick={runPreview}
            disabled={!canWrite || !text.trim() || previewing}
            data-action="preview-vcard"
          >
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
              {committing
                ? t("vcard.committing")
                : t("vcard.commit", { count: preview.new_count + preview.merge_count })}
            </Button>
          ) : null}
        </div>

        {previewError ? <ErrorState error={previewError} /> : null}

        {result ? (
          <div
            className="mb-6 rounded-lg border border-line p-4 text-sm"
            data-testid="vcard-commit-result"
          >
            {t("vcard.commitDone", { created: result.created_count, merged: result.merged_count })}
          </div>
        ) : null}

        {result ? (
          <NeedsCountryBanner
            count={result.needs_country_count}
            settingsHref={`${base}/settings/general`}
          />
        ) : preview ? (
          <NeedsCountryBanner
            count={preview.needs_country_count}
            settingsHref={`${base}/settings/general`}
          />
        ) : null}

        {preview ? (
          preview.rows.length === 0 ? (
            <EmptyState title={t("vcard.empty")} body={t("vcard.emptyBody")} />
          ) : (
            <div className="border-y border-line" data-testid="vcard-preview-table">
              <div className="flex flex-wrap gap-4 py-4 text-sm text-grid-muted">
                <span data-testid="vcard-new-count">
                  {t("vcard.newCount", { count: preview.new_count })}
                </span>
                <span data-testid="vcard-merge-count">
                  {t("vcard.mergeCount", { count: preview.merge_count })}
                </span>
                <span data-testid="vcard-invalid-count">
                  {t("vcard.invalidCount", { count: preview.invalid_count })}
                </span>
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
                          {row.status === "invalid"
                            ? row.invalid || t(`vcard.status.${row.status}`)
                            : t(`vcard.status.${row.status}`)}
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

/**
 * "Import from Google" (MH-585): connect a Google account with Contacts
 * access (admin only, reusing the MH-439 connect flow with product=contacts)
 * and sync it in. Listing + "Sync now" are member-visible, same as the
 * REST routes in internal/googlecontacts/routes.go -- a member who did not
 * connect the account can still run the sync it feeds. Connect/disconnect
 * stay admin-only because they reuse internal/google's integrations:manage
 * routes as-is (MH-439), unchanged here.
 */
function GoogleContactsPanel() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const canManage = ws.can("integrations:manage")
  // Only an admin needs "configured" (whether this server even has a Google
  // OAuth client) -- it drives the connect button, which only an admin sees.
  const admin = useGoogleConnections(ws.id, canManage)
  const contacts = useGoogleContactsConnections(ws.id)
  const [connecting, setConnecting] = useState(false)

  const list = contacts.data?.connections ?? []

  async function connect() {
    setConnecting(true)
    try {
      const q = new URLSearchParams({ product: "contacts", locale })
      const res = await api<{ url: string }>(`/api/w/google/connect?${q}`, { workspaceId: ws.id })
      // A full navigation: Google's consent screen, then back to this page.
      window.location.assign(res.url)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
      setConnecting(false)
    }
  }

  return (
    <div className="mb-6 border-y border-line py-4" data-testid="google-contacts-panel">
      <h2 className="text-base font-medium">{t("vcard.google.title")}</h2>
      <p className="mt-1 max-w-3xl text-sm text-grid-muted">{t("vcard.google.hint")}</p>

      {canManage ? (
        admin.data && !admin.data.configured ? (
          <p
            className="mt-3 text-sm text-grid-danger-text"
            data-testid="google-contacts-not-configured"
          >
            {t("vcard.google.notConfigured")}
          </p>
        ) : (
          <Button
            type="button"
            variant="outline"
            className="mt-3"
            onClick={connect}
            disabled={connecting || (admin.data ? !admin.data.configured : false)}
            data-action="connect-google-contacts"
          >
            {connecting ? t("vcard.google.connecting") : t("vcard.google.connect")}
          </Button>
        )
      ) : null}

      {!contacts.data ? (
        contacts.error ? (
          <ErrorState error={contacts.error} />
        ) : (
          <LoadingRows rows={1} />
        )
      ) : list.length === 0 ? (
        <p className="mt-3 text-sm text-grid-muted" data-testid="google-contacts-empty">
          {canManage ? t("vcard.google.none") : t("vcard.google.adminOnly")}
        </p>
      ) : (
        <ul className="mt-4 divide-y divide-line border-y border-line">
          {list.map((c) => (
            <GoogleContactsConnectionRow
              key={c.id}
              connection={c}
              canManage={canManage}
              onChange={() => contacts.mutate()}
            />
          ))}
        </ul>
      )}
    </div>
  )
}

function GoogleContactsConnectionRow({
  connection: c,
  canManage,
  onChange,
}: {
  connection: GoogleContactsConnection
  canManage: boolean
  onChange: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [syncing, setSyncing] = useState(false)
  const [fullConfirmOpen, setFullConfirmOpen] = useState(false)
  const [result, setResult] = useState<GoogleContactsSyncResult | null>(null)
  const status = useGoogleContactsSyncStatus(ws.id, c.id, { refreshWhileSyncing: syncing })
  const reauth = c.status === "reauth"

  async function sync(full = false) {
    setSyncing(true)
    setResult(null)
    try {
      const q = full ? "?full=true" : ""
      const res = await api<GoogleContactsSyncResult>(
        `/api/w/google/contacts/${encodeURIComponent(c.id)}/sync${q}`,
        {
          method: "POST",
          workspaceId: ws.id,
        },
      )
      setResult(res)
      status.mutate()
      toast.success(
        t("vcard.google.syncResult", {
          added: res.added,
          updated: res.updated,
          deleted: res.flagged_deleted,
        }),
      )
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setSyncing(false)
    }
  }

  async function fullResync() {
    setFullConfirmOpen(false)
    await sync(true)
  }

  async function disconnect() {
    const res = await api<GoogleDisconnected>(
      `/api/w/google/connections/${encodeURIComponent(c.id)}`,
      {
        method: "DELETE",
        workspaceId: ws.id,
      },
    )
    if (res.revoked) toast.success(t("integrations.disconnected"))
    else if (res.shared) toast.success(t("integrations.disconnectedShared"))
    else toast.warning(t("integrations.disconnectedNoRevoke"))
    onChange()
  }

  return (
    <li
      className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between"
      data-google-contacts-connection={c.email}
      data-status={c.status || "ok"}
    >
      <div className="min-w-0">
        <p className="text-sm font-medium">
          <Ltr>{c.email || "—"}</Ltr>
        </p>
        <p className="mt-1 text-xs text-grid-muted">
          {reauth ? (
            <Badge variant="destructive">{t("vcard.google.statusReauth")}</Badge>
          ) : status.data?.last_synced_at ? (
            t("vcard.google.lastSynced", {
              when: new Date(status.data.last_synced_at).toLocaleString(locale),
            })
          ) : (
            t("vcard.google.neverSynced")
          )}
        </p>
        {status.data?.last_error ? (
          <p className="mt-1 text-xs text-grid-danger-text">
            {t("vcard.google.lastError", { error: status.data.last_error })}
          </p>
        ) : null}
        {result ? (
          <p className="mt-1 text-xs text-grid-muted" data-testid="google-contacts-sync-result">
            {t("vcard.google.syncResult", {
              added: result.added,
              updated: result.updated,
              deleted: result.flagged_deleted,
            })}
          </p>
        ) : null}
        <NeedsCountryBanner
          count={result?.needs_country ?? 0}
          settingsHref={`/${locale}/w/${ws.slug}/settings/general`}
        />
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => void sync()}
          disabled={syncing || reauth}
          data-action="sync-google-contacts"
        >
          <RefreshCwIcon />
          {syncing ? t("vcard.google.syncing") : t("vcard.google.sync")}
        </Button>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setFullConfirmOpen(true)}
          disabled={syncing || reauth}
          data-action="full-resync-google-contacts"
        >
          {t("vcard.google.fullResync")}
        </Button>
        <AlertDialog open={fullConfirmOpen} onOpenChange={setFullConfirmOpen}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t("vcard.google.fullResyncTitle")}</AlertDialogTitle>
              <AlertDialogDescription>{t("vcard.google.fullResyncBody")}</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
              <AlertDialogAction disabled={syncing} onClick={() => void fullResync()}>
                {syncing ? t("vcard.google.fullResyncing") : t("vcard.google.fullResync")}
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
        {canManage ? (
          <ConfirmButton
            label={t("vcard.google.disconnect")}
            title={t("vcard.google.disconnectTitle", { email: isolate(c.email) })}
            description={t("vcard.google.disconnectBody")}
            confirmLabel={t("vcard.google.disconnect")}
            onConfirm={disconnect}
          />
        ) : null}
      </div>
    </li>
  )
}
