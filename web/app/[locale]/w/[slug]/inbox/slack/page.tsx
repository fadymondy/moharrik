"use client"

import { useState, type FormEvent } from "react"
import { PlusIcon, RefreshCwIcon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { CopyField } from "@/components/copy-field"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { ChannelInstructions, ChannelTestBadge } from "@/components/channels/channel-common"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type SlackChannel } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useSlackChannels } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

/*
Settings → Inbox → Slack (MH-563): as many app connections as the workspace
wants. Unlike Telegram, Slack has no register-webhook call — the Request URL
is pasted into the Slack app's own Event Subscriptions page, which then
verifies it with a challenge our webhook answers automatically.
*/
export default function SlackChannelsPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("channels.slack.title")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")
  const channels = useSlackChannels(ws.id)
  const [creating, setCreating] = useState(false)
  const list = channels.data?.channels ?? []

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("channels.slack.title")}
        description={t("channels.slack.hint")}
        action={
          canManage ? (
            <Button onClick={() => setCreating(true)} disabled={!channels.data}>
              <PlusIcon />
              {t("channels.addConnection")}
            </Button>
          ) : undefined
        }
      />

      <ChannelInstructions
        titleKey="channels.slack.setupTitle"
        steps={["channels.slack.step1", "channels.slack.step2", "channels.slack.step3", "channels.slack.step4", "channels.slack.step5"]}
      />

      {!canManage ? (
        <p className="px-6 py-6 text-sm text-grid-muted">{t("channels.managerOnly")}</p>
      ) : channels.error ? (
        <ErrorState error={channels.error} />
      ) : !channels.data ? (
        <LoadingRows rows={2} />
      ) : list.length === 0 ? (
        <EmptyState title={t("channels.slack.emptyTitle")} body={t("channels.slack.emptyBody")} />
      ) : (
        <ul className="border-y border-line" data-slack-channels>
          {list.map((c) => (
            <SlackRow key={c.id} channel={c} onChange={() => channels.mutate()} />
          ))}
        </ul>
      )}
      <div className="pb-8" />

      <CreateDialog open={creating} onOpenChange={setCreating} onCreated={() => channels.mutate()} />
    </>
  )
}

function SlackRow({ channel, onChange }: { channel: SlackChannel; onChange: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [allowed, setAllowed] = useState(channel.allowed_channel_ids)
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)

  async function save(json: Record<string, unknown>, done?: string) {
    setBusy(true)
    try {
      await api(`/api/w/slack-channels/${encodeURIComponent(channel.id)}`, {
        method: "PUT",
        json: { name: channel.name, allowed_channel_ids: channel.allowed_channel_ids, ...json },
        workspaceId: ws.id,
      })
      if (done) toast.success(done)
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function test() {
    setTesting(true)
    try {
      await api(`/api/w/slack-channels/${encodeURIComponent(channel.id)}/test`, { method: "POST", json: {}, workspaceId: ws.id })
      toast.success(t("channels.testOk"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
      onChange()
    } finally {
      setTesting(false)
    }
  }

  async function remove() {
    await api(`/api/w/slack-channels/${encodeURIComponent(channel.id)}`, { method: "DELETE", workspaceId: ws.id })
    toast.success(t("channels.removed"))
    onChange()
  }

  return (
    <li className="px-6 py-5" data-channel-row={channel.name}>
      <div className="flex flex-wrap items-center gap-3">
        <p className="text-sm font-medium">
          <Bdi>{channel.name}</Bdi>
        </p>
        {channel.team_name ? <Badge variant="secondary">{channel.team_name}</Badge> : null}
        <Badge variant={channel.enabled ? "default" : "outline"}>{channel.enabled ? t("channels.enabled") : t("channels.disabled")}</Badge>
        <ChannelTestBadge ok={channel.last_test_ok} at={channel.last_test_at} error={channel.last_error} />
        <div className="ms-auto flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={test} disabled={testing || !channel.has_bot_token} data-test-connection>
            <RefreshCwIcon className={testing ? "animate-spin" : undefined} />
            {t("channels.test")}
          </Button>
          <Switch
            checked={channel.enabled}
            onCheckedChange={() => save({ enabled: !channel.enabled }, channel.enabled ? t("channels.turnedOff") : t("channels.turnedOn"))}
            aria-label={channel.enabled ? t("channels.enabled") : t("channels.disabled")}
          />
          <ConfirmButton
            label={t("common.delete")}
            title={t("channels.deleteTitle")}
            description={t("channels.deleteBody", { name: isolate(channel.name) })}
            confirmLabel={t("common.delete")}
            onConfirm={remove}
          />
        </div>
      </div>

      <div className="mt-3 space-y-3">
        <div className="space-y-1.5">
          <p className="grid-micro">{t("channels.slack.requestUrl")}</p>
          <CopyField value={channel.webhook_url} label={t("channels.slack.requestUrl")} />
        </div>
        <Field>
          <FieldLabel htmlFor={`allowed-${channel.id}`}>{t("channels.allowedChannels")}</FieldLabel>
          <div className="flex gap-2">
            <Input id={`allowed-${channel.id}`} dir="ltr" value={allowed} onChange={(e) => setAllowed(e.target.value)} placeholder="C0123456, C0987654" />
            <Button variant="outline" disabled={busy} onClick={() => save({ allowed_channel_ids: allowed }, t("channels.saved"))}>
              {t("common.save")}
            </Button>
          </div>
          <FieldDescription>{t("channels.allowedChannelsHint")}</FieldDescription>
        </Field>
      </div>
    </li>
  )
}

function CreateDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (v: boolean) => void; onCreated: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [token, setToken] = useState("")
  const [signingSecret, setSigningSecret] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  function handleOpenChange(next: boolean) {
    if (next) {
      setName("")
      setToken("")
      setSigningSecret("")
      setErr(null)
    }
    onOpenChange(next)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/slack-channels", {
        json: { name: name.trim(), bot_token: token.trim(), signing_secret: signingSecret.trim() },
        workspaceId: ws.id,
      })
      toast.success(t("channels.connected"))
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
        <form onSubmit={submit} className="flex flex-col gap-4" data-form="slack-channel">
          <DialogHeader>
            <DialogTitle>{t("channels.slack.addTitle")}</DialogTitle>
            <DialogDescription>{t("channels.slack.addHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="slack-name">{t("channels.name")}</FieldLabel>
              <Input id="slack-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} placeholder={t("channels.namePlaceholder")} />
            </Field>
            <Field>
              <FieldLabel htmlFor="slack-token">{t("channels.slack.botToken")}</FieldLabel>
              <Input id="slack-token" dir="ltr" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} placeholder="xoxb-..." />
            </Field>
            <Field>
              <FieldLabel htmlFor="slack-signing">{t("channels.slack.signingSecret")}</FieldLabel>
              <Input id="slack-signing" dir="ltr" autoComplete="off" value={signingSecret} onChange={(e) => setSigningSecret(e.target.value)} />
              <FieldDescription>{t("channels.slack.signingSecretHint")}</FieldDescription>
            </Field>
          </FieldGroup>
          {err ? <FieldError>{err}</FieldError> : null}
          <DialogFooter>
            <Button type="submit" disabled={busy || !name.trim() || !token.trim() || !signingSecret.trim()}>
              {busy ? t("common.working") : t("channels.connect")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
