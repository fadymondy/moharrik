"use client"

import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type TelegramChannel } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

/**
 * The Telegram bot connection form (extracted from the old inbox/telegram
 * page for MH-571, so the unified Channels page's Add sheet and Edit sheet
 * can reuse it instead of a rewrite). Create needs a bot token; edit leaves
 * it blank to keep the one already stored (same pattern as the mailbox
 * password field).
 */
export function TelegramChannelForm({ channel, onSaved, onCancel }: { channel?: TelegramChannel; onSaved: () => void; onCancel?: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState(channel?.name ?? "")
  const [token, setToken] = useState("")
  const [allowed, setAllowed] = useState(channel?.allowed_chat_ids ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      if (channel) {
        await api(`/api/w/telegram-channels/${encodeURIComponent(channel.id)}`, {
          method: "PUT",
          json: { name: name.trim(), allowed_chat_ids: allowed, ...(token.trim() ? { bot_token: token.trim() } : {}) },
          workspaceId: ws.id,
        })
        toast.success(t("channels.saved"))
      } else {
        await api("/api/w/telegram-channels", { json: { name: name.trim(), bot_token: token.trim() }, workspaceId: ws.id })
        toast.success(t("channels.connected"))
      }
      onSaved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4" data-form="telegram-channel">
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="tg-name">{t("channels.name")}</FieldLabel>
          <Input id="tg-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} placeholder={t("channels.namePlaceholder")} />
        </Field>
        <Field>
          <FieldLabel htmlFor="tg-token">
            {t("channels.telegram.botToken")}
            {channel ? <span className="text-grid-muted"> ({t("common.optional")})</span> : null}
          </FieldLabel>
          <Input id="tg-token" dir="ltr" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} placeholder="123456:ABC-DEF..." />
          <FieldDescription>{channel ? t("channels.tokenKeepHint") : t("channels.telegram.botTokenHint")}</FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor="tg-allowed">{t("channels.allowedChats")}</FieldLabel>
          <Input id="tg-allowed" dir="ltr" value={allowed} onChange={(e) => setAllowed(e.target.value)} placeholder="123456789, -100987654321" />
          <FieldDescription>{t("channels.allowedChatsHint")}</FieldDescription>
        </Field>
      </FieldGroup>
      {err ? <FieldError>{err}</FieldError> : null}
      <div className="flex items-center gap-2">
        <Button type="submit" disabled={busy || !name.trim() || (!channel && !token.trim())}>
          {busy ? t("common.working") : channel ? t("common.save") : t("channels.connect")}
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
