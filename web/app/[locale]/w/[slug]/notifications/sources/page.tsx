"use client"

import { useState, type FormEvent, type ReactNode } from "react"
import {
  KeyRoundIcon,
  PlusIcon,
  RefreshCwIcon,
  ShieldCheckIcon,
  TriangleAlertIcon,
} from "lucide-react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  Switch,
  toast,
} from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError } from "@/components/form-layout"

import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField, Ltr } from "@/components/copy-field"
import { RowsTable } from "@/components/nq-table"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type WebhookSource, type WebhookSourceKind } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWebhookSources } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { CollectedSources } from "@/components/notifications/collected-sources"
import { Bdi, isolate } from "@/components/bidi"

export default function WebhookSourcesPage() {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("notifications.sources")} · ${ws.summary.name}`)
  const canManage = ws.can("notifications:manage")
  const sources = useWebhookSources(ws.id)
  const [creating, setCreating] = useState(false)
  const [secretDialog, setSecretDialog] = useState<{
    source: WebhookSource
    secret: string
  } | null>(null)
  const [clientSecretFor, setClientSecretFor] = useState<WebhookSource | null>(null)

  const list = sources.data?.sources ?? []

  return (
    <>
      <SectionHeader
        title={t("notifications.sources")}
        description={t("notifications.sourcesPageHint")}
      />

      {/* What the center polls (MH-384), then what posts to it. */}
      <CollectedSources />

      <div
        className="flex flex-wrap items-end justify-between gap-3 px-6 pt-6 pb-4"
        data-webhook-sources
      >
        <div className="max-w-2xl">
          <h2 className="text-lg font-semibold">{t("notifications.webhooks")}</h2>
          <p className="mt-1 text-sm text-muted-foreground">{t("notifications.sourcesHint")}</p>
        </div>
        {canManage ? (
          <Button onClick={() => setCreating(true)} disabled={!sources.data}>
            <PlusIcon />
            {t("notifications.addSource")}
          </Button>
        ) : null}
      </div>

      {sources.error ? (
        <ErrorState error={sources.error} />
      ) : sources.isLoading ? (
        <LoadingRows />
      ) : list.length === 0 ? (
        <EmptyState
          title={t("notifications.noSources")}
          body={canManage ? t("notifications.noSourcesBody") : undefined}
        />
      ) : (
        <div className="px-6">
          <RowsTable<WebhookSource>
            data={list}
            getRowId={(src) => src.id}
            rowLabel={(src) => src.name}
            label={t("notifications.sources")}
            data-sources
            columns={[
              {
                id: "name",
                header: t("notifications.sourceName"),
                hideable: false,
                sortValue: (src) => src.name,
                cell: (src) => (
                  <SourceName
                    source={src}
                    canManage={canManage}
                    onChange={() => sources.mutate()}
                  />
                ),
              },
              {
                id: "kind",
                header: t("notifications.sourceKind"),
                sortValue: (src) => src.kind,
                cell: (src) => (
                  <span className="text-muted-foreground">
                    {t(`notifications.kind.${src.kind}`)}
                  </span>
                ),
              },
              {
                id: "status",
                header: t("notifications.sourceStatus"),
                cell: (src) => (
                  <SourceToggle
                    source={src}
                    canManage={canManage}
                    onChange={() => sources.mutate()}
                  />
                ),
              },
              {
                id: "deliveries",
                header: t("notifications.deliveries"),
                sortValue: (src) => src.delivery_count,
                cell: (src) => (
                  <span className="text-muted-foreground">
                    {src.delivery_count}
                    {src.last_delivery_at ? (
                      <span className="block text-xs">{formatDate(src.last_delivery_at)}</span>
                    ) : null}
                  </span>
                ),
              },
              ...(canManage
                ? [
                    {
                      id: "actions",
                      header: <span className="sr-only">{t("common.actions")}</span>,
                      label: t("common.actions"),
                      align: "end" as const,
                      hideable: false,
                      cell: (src: WebhookSource) => (
                        <SourceActions
                          source={src}
                          onChange={() => sources.mutate()}
                          onRotated={(secret) => setSecretDialog({ source: src, secret })}
                          onSetClientSecret={() => setClientSecretFor(src)}
                        />
                      ),
                    },
                  ]
                : []),
            ]}
          />
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
      <SecretDialog
        state={secretDialog}
        onClose={() => setSecretDialog(null)}
        onSaved={() => sources.mutate()}
      />
      <ClientSecretDialog
        source={clientSecretFor}
        onClose={() => setClientSecretFor(null)}
        onSaved={() => {
          sources.mutate()
          setClientSecretFor(null)
        }}
      />
    </>
  )
}

/** The source's name and URL, with whatever needs the owner's attention beneath them. */
function SourceName({
  source,
  canManage,
  onChange,
}: {
  source: WebhookSource
  canManage: boolean
  onChange: () => void
}) {
  const { t } = useTranslations()
  return (
    <div data-source-row={source.name}>
      <div className="font-medium">
        <Bdi>{source.name}</Bdi>
      </div>
      <Ltr className="text-xs text-muted-foreground" mono>
        {source.url}
      </Ltr>
      {source.legacy_signing ? (
        <LegacyNotice source={source} canManage={canManage} onChange={onChange} />
      ) : null}
      {source.kind === "sentry" && !source.has_secret ? (
        <p
          className="mt-1 flex max-w-[min(28rem,75vw)] items-start gap-1 text-xs whitespace-normal text-nq-warning-text"
          data-no-secret
        >
          <KeyRoundIcon className="mt-0.5 size-3 shrink-0" />
          {t("notifications.noSecretYet")}
        </p>
      ) : null}
      {source.last_error ? (
        <p className="mt-1 flex items-start gap-1 text-xs text-nq-danger-text">
          <TriangleAlertIcon className="mt-0.5 size-3 shrink-0" />
          <Bdi>{source.last_error}</Bdi>
        </p>
      ) : null}
    </div>
  )
}

function SourceToggle({
  source,
  canManage,
  onChange,
}: {
  source: WebhookSource
  canManage: boolean
  onChange: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()

  async function toggleEnabled() {
    try {
      await api(`/api/w/webhook_sources/${encodeURIComponent(source.id)}`, {
        method: "PATCH",
        json: { enabled: !source.enabled },
        workspaceId: ws.id,
      })
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return canManage ? (
    <Switch
      checked={source.enabled}
      onCheckedChange={toggleEnabled}
      aria-label={source.enabled ? t("notifications.enabled") : t("notifications.disabled")}
    />
  ) : (
    <Badge variant={source.enabled ? "brand" : "outline"}>
      {source.enabled ? t("notifications.enabled") : t("notifications.disabled")}
    </Badge>
  )
}

function SourceActions({
  source,
  onChange,
  onRotated,
  onSetClientSecret,
}: {
  source: WebhookSource
  onChange: () => void
  onRotated: (secret: string) => void
  onSetClientSecret: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)

  async function rotate() {
    setBusy(true)
    try {
      const res = await api<{ source: WebhookSource; secret: string }>(
        `/api/w/webhook_sources/${encodeURIComponent(source.id)}/rotate`,
        {
          method: "POST",
          workspaceId: ws.id,
        },
      )
      onChange()
      onRotated(res.secret)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function remove() {
    await api(`/api/w/webhook_sources/${encodeURIComponent(source.id)}`, {
      method: "DELETE",
      workspaceId: ws.id,
    })
    toast.success(t("notifications.sourceDeleted"))
    onChange()
  }

  return (
    <div className="flex justify-end gap-1">
      {source.kind === "sentry" ? (
        // Sentry generates this secret, so there is nothing to rotate here:
        // it is rotated in Sentry and pasted in again.
        <Button variant="ghost" size="sm" onClick={onSetClientSecret}>
          <KeyRoundIcon />
          {t("notifications.setClientSecret")}
        </Button>
      ) : (
        <Button variant="ghost" size="sm" onClick={rotate} disabled={busy}>
          <RefreshCwIcon />
          {t("notifications.rotate")}
        </Button>
      )}
      <ConfirmButton
        label={t("common.delete")}
        title={t("notifications.deleteSourceTitle")}
        description={t("notifications.deleteSourceBody", { name: isolate(source.name) })}
        confirmLabel={t("common.delete")}
        onConfirm={remove}
      />
    </div>
  )
}

/**
 * A source from before timestamped signing (MH-381). It still accepts the old
 * body-only signature so nothing broke on deploy — but that signature can be
 * replayed, so it is flagged, with when it was last actually used, and the
 * owner can switch it off (one way).
 */
function LegacyNotice({
  source,
  canManage,
  onChange,
}: {
  source: WebhookSource
  canManage: boolean
  onChange: () => void
}) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)

  async function switchOff() {
    setBusy(true)
    try {
      await api(`/api/w/webhook_sources/${encodeURIComponent(source.id)}`, {
        method: "PATCH",
        json: { legacy_signing: false },
        workspaceId: ws.id,
      })
      toast.success(t("notifications.switchedSigning", { name: isolate(source.name) }))
      setOpen(false)
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mt-1.5 space-y-1" data-legacy-signing>
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline" className="border-nq-warning text-nq-warning-text">
          <TriangleAlertIcon />
          {t("notifications.legacySigning")}
        </Badge>
        {canManage ? (
          <Button
            variant="link"
            size="sm"
            className="h-auto p-0 text-xs"
            onClick={() => setOpen(true)}
          >
            <ShieldCheckIcon />
            {t("notifications.switchSigning")}
          </Button>
        ) : null}
      </div>
      <p className="max-w-[min(28rem,75vw)] text-xs whitespace-normal text-muted-foreground">
        {source.kind === "generic"
          ? t("notifications.legacySigningHint")
          : t("notifications.legacySigningHintPlatform")}{" "}
        {source.last_legacy_at
          ? t("notifications.lastLegacyDelivery", { date: formatDate(source.last_legacy_at) })
          : t("notifications.noLegacyDelivery")}
      </p>
      <AlertDialog open={open} onOpenChange={setOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("notifications.switchSigningTitle", { name: isolate(source.name) })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {source.kind === "generic"
                ? t("notifications.switchSigningBody")
                : t("notifications.switchSigningBodyPlatform")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction disabled={busy} onClick={switchOff}>
              {busy ? t("common.working") : t("notifications.switchSigningConfirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
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
      const res = await api<{ source: WebhookSource; secret: string }>("/api/w/webhook_sources", {
        json: { name: name.trim(), kind },
        workspaceId: ws.id,
      })
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
              <Input
                id="source-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={80}
                placeholder={t("notifications.sourceNamePlaceholder")}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="source-kind">{t("notifications.sourceKind")}</FieldLabel>
              <PickSelect
                id="source-kind"
                value={kind}
                onChange={(v) => setKind(v as WebhookSourceKind)}
                options={kindOpts}
              />
              <FieldDescription data-kind-hint={kind}>
                {t(`notifications.kindHint.${kind}`)}
              </FieldDescription>
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

/**
 * A copy-paste test for a generic source, in the timestamped scheme: the
 * signature covers timestamp + "." + body, and the server refuses anything more
 * than 5 minutes old. openssl prints "SHA2-256(stdin)= <hex>" (or "(stdin)= "),
 * hence the sed.
 */
function genericCurl(url: string, secret: string): string {
  return [
    `SECRET='${secret}'`,
    `BODY='{"title":"Hello from curl"}'`,
    `TS=$(date +%s)`,
    `SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | sed 's/^.*= //')`,
    `curl -X POST '${url}' \\`,
    `  -H "Content-Type: application/json" \\`,
    `  -H "X-Webhook-Timestamp: $TS" \\`,
    `  -H "X-Webhook-Signature: sha256=$SIG" \\`,
    `  -d "$BODY"`,
  ].join("\n")
}

function SecretDialog({
  state,
  onClose,
  onSaved,
}: {
  state: { source: WebhookSource; secret: string } | null
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslations()
  if (!state) return null
  const { source, secret } = state
  const kind = source.kind

  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="sm:max-w-xl">
        <div className="flex flex-col gap-4" data-secret-dialog={kind}>
          <DialogHeader>
            <DialogTitle>
              {kind === "sentry" ? t("notifications.sentryTitle") : t("notifications.secretTitle")}
            </DialogTitle>
            <DialogDescription>
              {kind === "sentry" ? t("notifications.sentryHint") : t("notifications.secretHint")}
            </DialogDescription>
          </DialogHeader>
          {kind !== "sentry" ? (
            <p
              className="flex items-start gap-2 border border-nq-line bg-card p-3 text-sm text-nq-warning-text"
              role="alert"
            >
              <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
              {t("notifications.shownOnce")}
            </p>
          ) : null}
          <div className="space-y-1.5">
            <p className="eyebrow">
              {kind === "sentry" ? t("notifications.webhookUrl") : t("notifications.url")}
            </p>
            <CopyField value={source.url} label={t("notifications.url")} />
          </div>
          {kind !== "sentry" ? (
            <div className="space-y-1.5">
              <p className="eyebrow">{t("notifications.secret")}</p>
              <CopyField value={secret} label={t("notifications.secret")} />
            </div>
          ) : null}
          <div className="space-y-1.5">
            <p className="eyebrow">{t("notifications.howToTitle")}</p>
            <p className="text-sm text-muted-foreground" data-how-to={kind}>
              {t(`notifications.howTo.${kind}`)}
            </p>
          </div>
          {kind === "generic" ? (
            <div className="space-y-1.5">
              <p className="eyebrow">{t("notifications.curlExample")}</p>
              <CopyField
                value={genericCurl(source.url, secret)}
                label={t("notifications.curlExample")}
              />
            </div>
          ) : null}
          {kind === "sentry" ? (
            <ClientSecretForm
              source={source}
              onSaved={() => {
                onSaved()
                onClose()
              }}
              secondary={
                <Button type="button" variant="secondary" onClick={onClose}>
                  {t("notifications.later")}
                </Button>
              }
            />
          ) : (
            <DialogFooter>
              <Button onClick={onClose}>{t("notifications.savedIt")}</Button>
            </DialogFooter>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}

/** Paste the Client Secret Sentry generated for its integration. Write-only. */
function ClientSecretForm({
  source,
  onSaved,
  secondary,
}: {
  source: WebhookSource
  onSaved: () => void
  secondary?: ReactNode
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [value, setValue] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const inputId = `client-secret-${source.id}`

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/webhook_sources/${encodeURIComponent(source.id)}/secret`, {
        method: "PUT",
        json: { secret: value.trim() },
        workspaceId: ws.id,
      })
      toast.success(t("notifications.clientSecretSaved"))
      setValue("")
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <Field>
        <FieldLabel htmlFor={inputId}>{t("notifications.clientSecret")}</FieldLabel>
        <Input
          id={inputId}
          type="password"
          autoComplete="off"
          dir="ltr"
          className="font-mono"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder={t("notifications.clientSecretPlaceholder")}
        />
        {err ? <FieldError>{err}</FieldError> : null}
      </Field>
      <DialogFooter>
        {secondary}
        <Button type="submit" disabled={busy || value.trim().length < 16}>
          {busy ? t("common.working") : t("notifications.saveSecret")}
        </Button>
      </DialogFooter>
    </form>
  )
}

function ClientSecretDialog({
  source,
  onClose,
  onSaved,
}: {
  source: WebhookSource | null
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslations()
  if (!source) return null
  return (
    <Dialog open onOpenChange={(next) => !next && onClose()}>
      <DialogContent className="sm:max-w-md">
        <div className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("notifications.setClientSecret")}</DialogTitle>
            <DialogDescription>
              {t("notifications.clientSecretHint", { name: isolate(source.name) })}
            </DialogDescription>
          </DialogHeader>
          <ClientSecretForm source={source} onSaved={onSaved} />
        </div>
      </DialogContent>
    </Dialog>
  )
}
