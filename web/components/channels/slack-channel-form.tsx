"use client"

import { useState, type FormEvent } from "react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type SlackChannel } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

/** The Slack app connection form (extracted from inbox/slack for MH-571). */
export function SlackChannelForm({ channel, onSaved, onCancel }: { channel?: SlackChannel; onSaved: () => void; onCancel?: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState(channel?.name ?? "")
  const [token, setToken] = useState("")
  const [signingSecret, setSigningSecret] = useState("")
  const [allowed, setAllowed] = useState(channel?.allowed_channel_ids ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      if (channel) {
        await api(`/api/w/slack-channels/${encodeURIComponent(channel.id)}`, {
          method: "PUT",
          json: {
            name: name.trim(),
            allowed_channel_ids: allowed,
            ...(token.trim() ? { bot_token: token.trim() } : {}),
            ...(signingSecret.trim() ? { signing_secret: signingSecret.trim() } : {}),
          },
          workspaceId: ws.id,
        })
        toast.success(t("channels.saved"))
      } else {
        await api("/api/w/slack-channels", {
          json: { name: name.trim(), bot_token: token.trim(), signing_secret: signingSecret.trim() },
          workspaceId: ws.id,
        })
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
    <form onSubmit={submit} className="flex flex-col gap-4" data-form="slack-channel">
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="slack-name">{t("channels.name")}</FieldLabel>
          <Input id="slack-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} placeholder={t("channels.namePlaceholder")} />
        </Field>
        <Field>
          <FieldLabel htmlFor="slack-token">
            {t("channels.slack.botToken")}
            {channel ? <span className="text-grid-muted"> ({t("common.optional")})</span> : null}
          </FieldLabel>
          <Input id="slack-token" dir="ltr" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} placeholder="xoxb-..." />
          {channel ? <FieldDescription>{t("channels.tokenKeepHint")}</FieldDescription> : null}
        </Field>
        <Field>
          <FieldLabel htmlFor="slack-signing">
            {t("channels.slack.signingSecret")}
            {channel ? <span className="text-grid-muted"> ({t("common.optional")})</span> : null}
          </FieldLabel>
          <Input id="slack-signing" dir="ltr" autoComplete="off" value={signingSecret} onChange={(e) => setSigningSecret(e.target.value)} />
          <FieldDescription>{channel ? t("channels.tokenKeepHint") : t("channels.slack.signingSecretHint")}</FieldDescription>
        </Field>
        <Field>
          <FieldLabel htmlFor="slack-allowed">{t("channels.allowedChannels")}</FieldLabel>
          <Input id="slack-allowed" dir="ltr" value={allowed} onChange={(e) => setAllowed(e.target.value)} />
          <FieldDescription>{t("channels.allowedChannelsHint")}</FieldDescription>
        </Field>
      </FieldGroup>
      {err ? <FieldError>{err}</FieldError> : null}
      <div className="flex items-center gap-2">
        <Button type="submit" disabled={busy || !name.trim() || (!channel && (!token.trim() || !signingSecret.trim()))}>
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
