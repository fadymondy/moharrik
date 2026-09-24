"use client"

import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

/**
 * The chat widget's create form (extracted from inbox/widgets for MH-571).
 * Create-only: editing an existing widget's look/behaviour already has its
 * own full settings sheet (`WidgetSettingsSheet`, `@/components/inbox/widget-settings`)
 * which the Channels page's row "edit" action opens directly instead of
 * duplicating that form here.
 */
export function WidgetChannelForm({ onSaved, onCancel }: { onSaved: () => void; onCancel?: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [origins, setOrigins] = useState("")
  const [greeting, setGreeting] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

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
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4" data-form="chat-widget">
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
      <div className="flex items-center gap-2">
        <Button type="submit" disabled={busy || !name.trim()}>
          {busy ? t("common.working") : t("inbox.createWidget")}
        </Button>
        {onCancel ? (
          <Button type="button" variant="ghost" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
        ) : null}
      </div>
    </form>
  )
}
