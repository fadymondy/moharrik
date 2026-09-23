"use client"

import { useState, type FormEvent } from "react"
import { PlusIcon, TriangleAlertIcon } from "lucide-react"
import { toast } from "sonner"

import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type ChatWidget } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useChatWidgets } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

export default function ChatWidgetsPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("inbox.widgets")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")
  const widgets = useChatWidgets(ws.id)
  const [creating, setCreating] = useState(false)
  const list = widgets.data?.widgets ?? []

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("inbox.widgets")}
        description={t("inbox.widgetsHint")}
        action={
          canManage ? (
            <Button onClick={() => setCreating(true)} disabled={!widgets.data}>
              <PlusIcon />
              {t("inbox.addWidget")}
            </Button>
          ) : undefined
        }
      />

      {widgets.error ? (
        <ErrorState error={widgets.error} />
      ) : widgets.isLoading ? (
        <LoadingRows />
      ) : list.length === 0 ? (
        <EmptyState title={t("inbox.noWidgets")} body={canManage ? t("inbox.noWidgetsBody") : undefined} />
      ) : (
        <ul className="divide-y divide-line border-y border-line">
          {list.map((w) => (
            <WidgetRow key={w.id} widget={w} canManage={canManage} onChange={() => widgets.mutate()} />
          ))}
        </ul>
      )}
      <div className="pb-8" />

      <CreateWidgetDialog open={creating} onOpenChange={setCreating} onCreated={() => widgets.mutate()} />
    </>
  )
}

function WidgetRow({ widget, canManage, onChange }: { widget: ChatWidget; canManage: boolean; onChange: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [origins, setOrigins] = useState(widget.allowed_origins.join(", "))
  const [saving, setSaving] = useState(false)

  async function patch(json: Record<string, unknown>, done: string) {
    setSaving(true)
    try {
      await api(`/api/w/chat_widgets/${encodeURIComponent(widget.id)}`, { method: "PATCH", json, workspaceId: ws.id })
      toast.success(done)
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setSaving(false)
    }
  }

  async function remove() {
    await api(`/api/w/chat_widgets/${encodeURIComponent(widget.id)}`, { method: "DELETE", workspaceId: ws.id })
    toast.success(t("inbox.widgetDeleted"))
    onChange()
  }

  const blocked = widget.allowed_origins.length === 0

  return (
    <li className="px-6 py-5" data-widget-row={widget.name}>
      <div className="flex flex-wrap items-center gap-3">
        <p className="text-sm font-medium">
          <Bdi>{widget.name}</Bdi>
        </p>
        <Badge variant={widget.enabled ? "default" : "outline"}>{widget.enabled ? t("inbox.enabled") : t("inbox.disabled")}</Badge>
        {blocked ? (
          <span className="flex items-center gap-1 text-xs text-grid-warn-text">
            <TriangleAlertIcon className="size-3.5" />
            {t("inbox.noOrigins")}
          </span>
        ) : null}
        <div className="ms-auto flex items-center gap-2">
          {canManage ? (
            <>
              <Switch
                checked={widget.enabled}
                onCheckedChange={() => patch({ enabled: !widget.enabled }, widget.enabled ? t("inbox.widgetDisabled") : t("inbox.widgetEnabled"))}
                aria-label={widget.enabled ? t("inbox.enabled") : t("inbox.disabled")}
              />
              <ConfirmButton
                label={t("common.delete")}
                title={t("inbox.deleteWidgetTitle")}
                description={t("inbox.deleteWidgetBody", { name: isolate(widget.name) })}
                confirmLabel={t("common.delete")}
                onConfirm={remove}
              />
            </>
          ) : null}
        </div>
      </div>

      <div className="mt-3 space-y-3">
        <div className="space-y-1.5">
          <p className="grid-micro">{t("inbox.embed")}</p>
          <CopyField value={widget.embed} label={t("inbox.embed")} />
        </div>
        {canManage ? (
          <Field>
            <FieldLabel htmlFor={`origins-${widget.id}`}>{t("inbox.allowedOrigins")}</FieldLabel>
            <div className="flex gap-2">
              <Input
                id={`origins-${widget.id}`}
                dir="ltr"
                value={origins}
                onChange={(e) => setOrigins(e.target.value)}
                placeholder="https://example.com, https://www.example.com"
              />
              <Button
                variant="outline"
                disabled={saving}
                onClick={() =>
                  patch(
                    { allowed_origins: origins.split(",").map((o) => o.trim()).filter(Boolean) },
                    t("inbox.originsSaved"),
                  )
                }
              >
                {t("common.save")}
              </Button>
            </div>
            <FieldDescription>{t("inbox.allowedOriginsHint")}</FieldDescription>
          </Field>
        ) : (
          <p className="text-xs text-grid-muted">
            {t("inbox.allowedOrigins")}: {widget.allowed_origins.join(", ") || t("inbox.noOrigins")}
          </p>
        )}
      </div>
    </li>
  )
}

function CreateWidgetDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (v: boolean) => void; onCreated: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [origins, setOrigins] = useState("")
  const [greeting, setGreeting] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  function handleOpenChange(next: boolean) {
    if (next) {
      setName("")
      setOrigins("")
      setGreeting("")
      setErr(null)
    }
    onOpenChange(next)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/chat_widgets", {
        json: {
          name: name.trim(),
          allowed_origins: origins.split(",").map((o) => o.trim()).filter(Boolean),
          greeting: greeting.trim(),
        },
        workspaceId: ws.id,
      })
      toast.success(t("inbox.widgetCreated"))
      onCreated()
      handleOpenChange(false)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("inbox.addWidget")}</DialogTitle>
            <DialogDescription>{t("inbox.addWidgetHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="widget-name">{t("inbox.widgetName")}</FieldLabel>
              <Input id="widget-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} placeholder={t("inbox.widgetNamePlaceholder")} />
            </Field>
            <Field>
              <FieldLabel htmlFor="widget-origins">{t("inbox.allowedOrigins")}</FieldLabel>
              <Input id="widget-origins" dir="ltr" value={origins} onChange={(e) => setOrigins(e.target.value)} placeholder="https://example.com" />
              <FieldDescription>{t("inbox.allowedOriginsHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="widget-greeting">
                {t("inbox.greeting")} <span className="text-grid-muted">({t("common.optional")})</span>
              </FieldLabel>
              <Input id="widget-greeting" value={greeting} onChange={(e) => setGreeting(e.target.value)} maxLength={500} />
            </Field>
          </FieldGroup>
          {err ? <FieldError>{err}</FieldError> : null}
          <DialogFooter>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("inbox.createWidget")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
