"use client"

// Shared pieces of the superadmin panel (MH-1184): the action dialog every risky change goes through,
// the status badge, the facts list on a detail page, and the audit table.
import { useState, type FormEvent, type ReactNode } from "react"
import { CircleCheckIcon, CircleSlashIcon, ShieldIcon } from "lucide-react"
import {
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
  Textarea,
} from "@fadymondy/nasaq/web"

import { Ltr } from "@/components/copy-field"
import { FieldError } from "@/components/form-layout"
import { RowsTable } from "@/components/nq-table"
import { useFormat } from "@/components/overview/overview-cards"
import { errorText, type AdminAuditEntry } from "@/lib/admin"
import { useTranslations } from "@/lib/i18n"

/**
 * A confirmation for one admin action. `reason` adds an optional note (shown to the account or
 * workspace); `typeToConfirm` makes the admin type a value (a slug) before the button unlocks. Errors
 * stay in the dialog.
 */
export function ActionDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  danger = false,
  reason = false,
  typeToConfirm,
  onSubmit,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description: ReactNode
  confirmLabel: string
  danger?: boolean
  reason?: boolean
  typeToConfirm?: string
  onSubmit: (reason: string) => Promise<unknown>
  /** Extra fields between the description and the buttons. */
  children?: ReactNode
}) {
  const { t } = useTranslations()
  const [note, setNote] = useState("")
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  function close(next: boolean) {
    if (!next) {
      setNote("")
      setTyped("")
      setError(null)
    }
    onOpenChange(next)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (typeToConfirm && typed.trim() !== typeToConfirm) return
    setBusy(true)
    setError(null)
    try {
      await onSubmit(note.trim())
      close(false)
    } catch (err) {
      setError(errorText(err, t("common.networkError")))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={submit} noValidate className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{title}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>
          {children}
          {reason ? (
            <Field>
              <FieldLabel htmlFor="admin-reason">{t("admin.reason")}</FieldLabel>
              <Textarea
                id="admin-reason"
                value={note}
                onChange={(e) => setNote(e.target.value)}
                rows={3}
                maxLength={500}
                autoFocus
              />
              <FieldDescription>{t("admin.reasonHint")}</FieldDescription>
            </Field>
          ) : null}
          {typeToConfirm ? (
            <Field>
              <FieldLabel htmlFor="admin-confirm">
                {t("admin.typeToConfirm", { value: typeToConfirm })}
              </FieldLabel>
              <Input
                id="admin-confirm"
                dir="ltr"
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                autoComplete="off"
                autoFocus
              />
            </Field>
          ) : null}
          {error ? <FieldError>{error}</FieldError> : null}
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={() => close(false)}>
              {t("common.cancel")}
            </Button>
            <Button
              type="submit"
              variant={danger ? "danger" : "primary"}
              disabled={busy || (!!typeToConfirm && typed.trim() !== typeToConfirm)}
            >
              {busy ? t("common.working") : confirmLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Active or suspended, for an account or a workspace. Never colour alone: an icon and a word. */
export function StatusBadge({ status }: { status: string }) {
  const { t } = useTranslations()
  return status === "suspended" ? (
    <Badge variant="danger" data-status={status}>
      <CircleSlashIcon />
      {t("admin.status.suspended")}
    </Badge>
  ) : (
    <Badge variant="success" data-status={status}>
      <CircleCheckIcon />
      {t("admin.status.active")}
    </Badge>
  )
}

export function AdminBadge() {
  const { t } = useTranslations()
  return (
    <Badge variant="brand">
      <ShieldIcon />
      {t("admin.badge")}
    </Badge>
  )
}

/** Label/value pairs at the top of a detail page. */
export function Facts({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-3 rounded-lg border border-nq-line p-4 sm:grid-cols-2 lg:grid-cols-3">
      {items.map((it) => (
        <div key={it.label} className="flex min-w-0 flex-col gap-0.5">
          <dt className="text-xs text-muted-foreground">{it.label}</dt>
          <dd className="min-w-0 truncate text-sm text-foreground">{it.value}</dd>
        </div>
      ))}
    </dl>
  )
}

/** The admin audit log as a table; a row with changes expands to show them. */
export function AuditTable({
  entries,
  loading,
  error,
  onRetry,
}: {
  entries: AdminAuditEntry[]
  loading?: boolean
  error?: ReactNode
  onRetry?: () => void
}) {
  const { t } = useTranslations()
  const f = useFormat()
  return (
    <RowsTable
      label={t("admin.nav.audit")}
      data={entries}
      getRowId={(e) => e.id}
      rowLabel={(e) => `${e.action} ${e.target_label}`}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty={t("admin.audit.empty")}
      pageSize={50}
      defaultSort={{ id: "at", direction: "desc" }}
      canExpand={(e) => !!e.changes}
      renderExpanded={(e) => (
        <pre
          dir="ltr"
          className="max-h-80 overflow-auto px-4 py-3 text-start font-mono text-xs whitespace-pre-wrap"
        >
          {JSON.stringify(e.changes, null, 2)}
        </pre>
      )}
      data-admin-audit
      columns={[
        {
          id: "at",
          header: t("admin.audit.at"),
          cell: (e) => <span title={f.exact(e.at)}>{f.ago(e.at)}</span>,
          sortValue: (e) => e.at,
        },
        {
          id: "actor",
          header: t("admin.audit.actor"),
          cell: (e) => <Ltr>{e.actor_email || "—"}</Ltr>,
          sortValue: (e) => e.actor_email,
        },
        {
          id: "action",
          header: t("admin.audit.action"),
          cell: (e) => (
            <span className="font-mono text-xs" dir="ltr">
              {e.action}
            </span>
          ),
          sortValue: (e) => e.action,
        },
        {
          id: "target",
          header: t("admin.audit.target"),
          cell: (e) => (
            <span className="flex min-w-0 items-baseline gap-1.5">
              <span className="text-xs text-muted-foreground">
                {t(`admin.target.${e.target_type}`)}
              </span>
              <Ltr className="truncate">{e.target_label || e.target_id}</Ltr>
            </span>
          ),
          sortValue: (e) => e.target_label,
        },
        {
          id: "via",
          header: t("admin.audit.via"),
          cell: (e) => t(`admin.via.${e.via}`),
          sortValue: (e) => e.via,
        },
        {
          id: "ip",
          header: t("admin.audit.ip"),
          cell: (e) => <Ltr className="font-mono text-xs text-muted-foreground">{e.ip || "—"}</Ltr>,
          defaultHidden: true,
        },
      ]}
    />
  )
}
