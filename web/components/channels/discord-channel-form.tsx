"use client"

import { useState, type FormEvent } from "react"
import { toast } from "@fadymondy/nasaq/web"

import { SetupGuide, type SetupGuideStep } from "@/components/channels/setup-guide"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type DiscordChannel } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

const DISCORD_GUIDE_STEPS: SetupGuideStep[] = [
  {
    titleKey: "channels.discord.guide.step1.title",
    bodyKey: "channels.discord.guide.step1.body",
    links: [
      {
        href: "https://discord.com/developers/applications",
        text: "discord.com/developers/applications",
      },
    ],
  },
  {
    titleKey: "channels.discord.guide.step2.title",
    warningKey: "channels.discord.guide.step2.warning",
  },
  {
    titleKey: "channels.discord.guide.step3.title",
    bodyKey: "channels.discord.guide.step3.body",
  },
  {
    titleKey: "channels.discord.guide.step4.title",
    bodyKey: "channels.discord.guide.step4.body",
  },
  {
    titleKey: "channels.discord.guide.step5.title",
    bodyKey: "channels.discord.guide.step5.body",
  },
]

/** The Discord bot connection form (extracted from inbox/discord for MH-571). */
export function DiscordChannelForm({
  channel,
  onSaved,
  onCancel,
  defaultGuideOpen,
}: {
  channel?: DiscordChannel
  onSaved: () => void
  onCancel?: () => void
  /** Open by default only when this type has no connections yet (MH-594) — the Channels page knows that count, this form does not. */
  defaultGuideOpen?: boolean
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState(channel?.name ?? "")
  const [token, setToken] = useState("")
  const [publicKey, setPublicKey] = useState(channel?.public_key ?? "")
  const [allowedGuilds, setAllowedGuilds] = useState(channel?.allowed_guild_ids ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      if (channel) {
        await api(`/api/w/discord-channels/${encodeURIComponent(channel.id)}`, {
          method: "PUT",
          json: {
            name: name.trim(),
            public_key: publicKey.trim(),
            allowed_guild_ids: allowedGuilds,
            ...(token.trim() ? { bot_token: token.trim() } : {}),
          },
          workspaceId: ws.id,
        })
        toast.success(t("channels.saved"))
      } else {
        await api("/api/w/discord-channels", {
          json: { name: name.trim(), bot_token: token.trim(), public_key: publicKey.trim() },
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
    <div className="flex flex-col gap-4">
      <SetupGuide
        id="discord"
        titleKey="channels.discord.setupTitle"
        steps={DISCORD_GUIDE_STEPS}
        defaultOpen={defaultGuideOpen ?? !channel}
      />
      <form onSubmit={submit} className="flex flex-col gap-4" data-form="discord-channel">
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="dc-name">{t("channels.name")}</FieldLabel>
            <Input
              id="dc-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={80}
              placeholder={t("channels.namePlaceholder")}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="dc-token">
              {t("channels.discord.botToken")}
              {channel ? <span className="text-muted-foreground"> ({t("common.optional")})</span> : null}
            </FieldLabel>
            <Input
              id="dc-token"
              dir="ltr"
              autoComplete="off"
              value={token}
              onChange={(e) => setToken(e.target.value)}
            />
            {channel ? <FieldDescription>{t("channels.tokenKeepHint")}</FieldDescription> : null}
          </Field>
          <Field>
            <FieldLabel htmlFor="dc-pubkey">
              {t("channels.discord.publicKey")}{" "}
              <span className="text-muted-foreground">({t("common.optional")})</span>
            </FieldLabel>
            <Input
              id="dc-pubkey"
              dir="ltr"
              autoComplete="off"
              value={publicKey}
              onChange={(e) => setPublicKey(e.target.value)}
            />
            <FieldDescription>{t("channels.discord.publicKeyHint")}</FieldDescription>
          </Field>
          <Field>
            <FieldLabel htmlFor="dc-guilds">{t("channels.discord.allowedGuilds")}</FieldLabel>
            <Input
              id="dc-guilds"
              dir="ltr"
              value={allowedGuilds}
              onChange={(e) => setAllowedGuilds(e.target.value)}
            />
            <FieldDescription>{t("channels.discord.allowedHint")}</FieldDescription>
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
    </div>
  )
}
