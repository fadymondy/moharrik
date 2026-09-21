"use client"

import { useState, type FormEvent } from "react"
import { PlusIcon, RefreshCwIcon, TriangleAlertIcon } from "lucide-react"
import { toast } from "sonner"

import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField, Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type WebhookSource, type WebhookSourceKind } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWebhookSources } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function WebhookSourcesPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("notifications.sources")} · ${ws.summary.name}`)
  const canManage = ws.can("notifications:manage")
  const sources = useWebhookSources(ws.id)
  const [creating, setCreating] = useState(false)
  const [secretDialog, setSecretDialog] = useState<{ source: WebhookSource; secret: string } | null>(null)

  const list = sources.data?.sources ?? []

  return (
    <>
      <SectionHeader
        micro={ws.summary.name}
        title={t("notifications.sources")}
        description={t("notifications.sourcesHint")}
        action={
          canManage ? (
            <Button onClick={() => setCreating(true)} disabled={!sources.data}>
              <PlusIcon />
              {t("notifications.addSource")}
            </Button>
          ) : undefined
        }
      />

      {sources.error ? (
        <ErrorState error={sources.error} />
      ) : sources.isLoading ? (
        <LoadingRows />
      ) : list.length === 0 ? (
        <EmptyState title={t("notifications.noSources")} body={canManage ? t("notifications.noSourcesBody") : undefined} />
      ) : (
        <div className="border-y border-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("notifications.sourceName")}</TableHead>
                <TableHead>{t("notifications.sourceKind")}</TableHead>
                <TableHead>{t("notifications.sourceStatus")}</TableHead>
                <TableHead>{t("notifications.deliveries")}</TableHead>
                <TableHead className="pe-6 text-end">
                  <span className="sr-only">{t("common.actions")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((s) => (
                <SourceRow
                  key={s.id}
                  source={s}
                  canManage={canManage}
                  onChange={() => sources.mutate()}
                  onRotated={(secret) => setSecretDialog({ source: s, secret })}
                />
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <div className="pb-8" />

      <CreateSourceDialog
        open={creating}
        onOpenChange={setCreating}
        onCreated={(source, secret) => {
          sources.mutate()
          setSecretDialog({ source, secret })
        }}
      />
      <SecretDialog state={secretDialog} onClose={() => setSecretDialog(null)} />
    </>
  )
}

function SourceRow({
  source,
  canManage,
  onChange,
  onRotated,
}: {
  source: WebhookSource
  canManage: boolean
  onChange: () => void
  onRotated: (secret: string) => void
}) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)

  async function toggleEnabled() {
    try {
      await api(`/api/w/webhook_sources/${encodeURIComponent(source.id)}`, { method: "PATCH", json: { enabled: !source.enabled }, workspaceId: ws.id })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function rotate() {
    setBusy(true)
    try {
      const res = await api<{ source: WebhookSource; secret: string }>(`/api/w/webhook_sources/${encodeURIComponent(source.id)}/rotate`, {
        method: "POST",
        workspaceId: ws.id,
      })
      onChange()
      onRotated(res.secret)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    await api(`/api/w/webhook_sources/${encodeURIComponent(source.id)}`, { method: "DELETE", workspaceId: ws.id })
    toast.success(t("notifications.sourceDeleted"))
    onChange()
  }

  return (
    <TableRow data-source-row={source.name}>
      <TableCell className="ps-6">
        <div className="font-medium">{source.name}</div>
        <Ltr className="text-xs text-grid-muted" mono>
          {source.url}
        </Ltr>
        {source.last_error ? (
          <p className="mt-1 flex items-start gap-1 text-xs text-grid-danger-text">
            <TriangleAlertIcon className="mt-0.5 size-3 shrink-0" />
            {source.last_error}
          </p>
        ) : null}
      </TableCell>
      <TableCell className="text-grid-muted">{t(`notifications.kind.${source.kind}`)}</TableCell>
      <TableCell>
        {canManage ? (
          <Switch checked={source.enabled} onCheckedChange={toggleEnabled} aria-label={source.enabled ? t("notifications.enabled") : t("notifications.disabled")} />
        ) : (
          <Badge variant={source.enabled ? "default" : "outline"}>{source.enabled ? t("notifications.enabled") : t("notifications.disabled")}</Badge>
        )}
      </TableCell>
      <TableCell className="text-grid-muted">
        {source.delivery_count}
        {source.last_delivery_at ? <span className="block text-xs">{formatDate(source.last_delivery_at)}</span> : null}
      </TableCell>
      <TableCell className="pe-6 text-end">
        {canManage ? (
          <div className="flex justify-end gap-1">
            <Button variant="ghost" size="sm" onClick={rotate} disabled={busy}>
              <RefreshCwIcon />
              {t("notifications.rotate")}
            </Button>
            <ConfirmButton
              label={t("common.delete")}
              title={t("notifications.deleteSourceTitle")}
              description={t("notifications.deleteSourceBody", { name: source.name })}
              confirmLabel={t("common.delete")}
              onConfirm={remove}
            />
          </div>
        ) : null}
      </TableCell>
    </TableRow>
  )
}

function CreateSourceDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (source: WebhookSource, secret: string) => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [kind, setKind] = useState<WebhookSourceKind>("generic")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  function handleOpenChange(next: boolean) {
    if (next) {
      setName("")
      setKind("generic")
      setErr(null)
    }
    onOpenChange(next)
  }

  const kindOpts: { value: string; label: string }[] = [
    { value: "generic", label: t("notifications.kind.generic") },
    { value: "github", label: t("notifications.kind.github") },
    { value: "sentry", label: t("notifications.kind.sentry") },
  ]

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{ source: WebhookSource; secret: string }>("/api/w/webhook_sources", { json: { name: name.trim(), kind }, workspaceId: ws.id })
      onCreated(res.source, res.secret)
      handleOpenChange(false)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("notifications.addSource")}</DialogTitle>
            <DialogDescription>{t("notifications.addSourceHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="source-name">{t("notifications.sourceName")}</FieldLabel>
              <Input id="source-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} placeholder={t("notifications.sourceNamePlaceholder")} />
            </Field>
            <Field>
              <FieldLabel htmlFor="source-kind">{t("notifications.sourceKind")}</FieldLabel>
              <PickSelect id="source-kind" value={kind} onChange={(v) => setKind(v as WebhookSourceKind)} options={kindOpts} />
            </Field>
          </FieldGroup>
          {err ? <FieldError>{err}</FieldError> : null}
          <DialogFooter>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("notifications.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function SecretDialog({ state, onClose }: { state: { source: WebhookSource; secret: string } | null; onClose: () => void }) {
  const { t } = useTranslations()
  if (!state) return null
  const curl = `curl -X POST ${state.source.url} \\\n  -H "Content-Type: application/json" \\\n  -H "X-Webhook-Signature: sha256=<hmac-sha256 of the body, hex>" \\\n  -d '{"title":"Hello"}'`
  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <div className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("notifications.secretTitle")}</DialogTitle>
            <DialogDescription>{t("notifications.secretHint")}</DialogDescription>
          </DialogHeader>
          <p className="flex items-start gap-2 border border-line bg-grid-card p-3 text-sm text-grid-warn-text" role="alert">
            <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
            {t("notifications.shownOnce")}
          </p>
          <div className="space-y-1.5">
            <p className="grid-micro">{t("notifications.url")}</p>
            <CopyField value={state.source.url} label={t("notifications.url")} />
          </div>
          <div className="space-y-1.5">
            <p className="grid-micro">{t("notifications.secret")}</p>
            <CopyField value={state.secret} label={t("notifications.secret")} />
          </div>
          <div className="space-y-1.5">
            <p className="grid-micro">{t("notifications.curlExample")}</p>
            <CopyField value={curl} label={t("notifications.curlExample")} />
          </div>
          <DialogFooter>
            <Button onClick={onClose}>{t("notifications.savedIt")}</Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>
  )
}
