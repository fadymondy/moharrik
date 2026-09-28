"use client"

import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { SetupGuide, type SetupGuideStep } from "@/components/channels/setup-guide"
import { parseOrigins } from "@/components/inbox/widget-settings"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

const WIDGET_GUIDE_STEPS: SetupGuideStep[] = [
  {
    titleKey: "channels.chat.guide.step1.title",
    bodyKey: "channels.chat.guide.step1.body",
  },
  {
    titleKey: "channels.chat.guide.step2.title",
    bodyKey: "channels.chat.guide.step2.body",
  },
  {
    titleKey: "channels.chat.guide.step3.title",
    bodyKey: "channels.chat.guide.step3.body",
  },
  {
    titleKey: "channels.chat.guide.step4.title",
    bodyKey: "channels.chat.guide.step4.body",
  },
]

/**
 * The chat widget's create form (extracted from inbox/widgets for MH-571).
 * Create-only: editing an existing widget — name, allowed origins, look, behaviour — has its
 * own full settings sheet (`WidgetSettingsSheet`, `@/components/inbox/widget-settings`)
 * which the Channels page's row "edit" action opens directly instead of
 * duplicating that form here.
 */
export function WidgetChannelForm({
  onSaved,
  onCancel,
  defaultGuideOpen,
}: {
  onSaved: () => void
  onCancel?: () => void
  defaultGuideOpen?: boolean
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [origins, setOrigins] = useState("")
  const [greeting, setGreeting] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    const allowed = parseOrigins(origins)
    if (!allowed) {
      setErr(t("inbox.originsInvalid"))
      return
    }
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/chat_widgets", {
        json: {
          name: name.trim(),
          allowed_origins: allowed,
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
    <div className="flex flex-col gap-4">
      <SetupGuide
        id="chat"
        titleKey="channels.chat.setupTitle"
        steps={WIDGET_GUIDE_STEPS}
        defaultOpen={defaultGuideOpen ?? true}
      />
      <form onSubmit={submit} className="flex flex-col gap-4" data-form="chat-widget">
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="widget-name">{t("inbox.widgetName")}</FieldLabel>
            <Input
              id="widget-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={80}
              placeholder={t("inbox.widgetNamePlaceholder")}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="widget-origins">{t("inbox.allowedOrigins")}</FieldLabel>
            <Input
              id="widget-origins"
              dir="ltr"
              value={origins}
              onChange={(e) => setOrigins(e.target.value)}
              placeholder="https://example.com"
            />
            <FieldDescription>{t("inbox.allowedOriginsHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="widget-greeting">
              {t("inbox.greeting")}{" "}
              <span className="text-grid-muted">({t("common.optional")})</span>
            </FieldLabel>
            <Input
              id="widget-greeting"
              value={greeting}
              onChange={(e) => setGreeting(e.target.value)}
              maxLength={500}
            />
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
    </div>
  )
}
