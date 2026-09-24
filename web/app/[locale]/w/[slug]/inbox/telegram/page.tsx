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
import { api, ApiError, type TelegramChannel } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useTelegramChannels } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

/*
Settings → Inbox → Telegram (MH-563): as many bot connections as the
workspace wants. Each one's webhook is registered with Telegram the moment a
token is saved (channels.CreateTelegram), so there is no second "activate"
step — Test just re-proves it and repairs a stale registration.
*/
export default function TelegramChannelsPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("channels.telegram.title")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")
  const channels = useTelegramChannels(ws.id)
  const [creating, setCreating] = useState(false)
  const list = channels.data?.channels ?? []

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("channels.telegram.title")}
        description={t("channels.telegram.hint")}
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
        titleKey="channels.telegram.setupTitle"
        steps={["channels.telegram.step1", "channels.telegram.step2", "channels.telegram.step3", "channels.telegram.step4"]}
      />

      {!canManage ? (
        <p className="px-6 py-6 text-sm text-grid-muted">{t("channels.managerOnly")}</p>
      ) : channels.error ? (
        <ErrorState error={channels.error} />
      ) : !channels.data ? (
        <LoadingRows rows={2} />
      ) : list.length === 0 ? (
        <EmptyState title={t("channels.telegram.emptyTitle")} body={t("channels.telegram.emptyBody")} />
      ) : (
        <ul className="border-y border-line" data-telegram-channels>
          {list.map((c) => (
            <TelegramRow key={c.id} channel={c} onChange={() => channels.mutate()} />
          ))}
        </ul>
      )}
      <div className="pb-8" />

      <CreateDialog open={creating} onOpenChange={setCreating} onCreated={() => channels.mutate()} />
    </>
  )
}

function TelegramRow({ channel, onChange }: { channel: TelegramChannel; onChange: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [allowed, setAllowed] = useState(channel.allowed_chat_ids)
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)

  async function save(json: Record<string, unknown>, done?: string) {
    setBusy(true)
    try {
      await api(`/api/w/telegram-channels/${encodeURIComponent(channel.id)}`, {
        method: "PUT",
        json: { name: channel.name, allowed_chat_ids: channel.allowed_chat_ids, ...json },
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
      await api(`/api/w/telegram-channels/${encodeURIComponent(channel.id)}/test`, { method: "POST", json: {}, workspaceId: ws.id })
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
    await api(`/api/w/telegram-channels/${encodeURIComponent(channel.id)}`, { method: "DELETE", workspaceId: ws.id })
    toast.success(t("channels.removed"))
    onChange()
  }

  return (
    <li className="px-6 py-5" data-channel-row={channel.name}>
      <div className="flex flex-wrap items-center gap-3">
        <p className="text-sm font-medium">
          <Bdi>{channel.name}</Bdi>
        </p>
        {channel.bot_username ? <Badge variant="secondary">@{channel.bot_username}</Badge> : null}
        <Badge variant={channel.enabled ? "default" : "outline"}>{channel.enabled ? t("channels.enabled") : t("channels.disabled")}</Badge>
        <ChannelTestBadge ok={channel.last_test_ok} at={channel.last_test_at} error={channel.last_error} />
        <div className="ms-auto flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={test} disabled={testing || !channel.has_token} data-test-connection>
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
          <p className="grid-micro">{t("channels.telegram.webhookUrl")}</p>
          <CopyField value={channel.webhook_url} label={t("channels.telegram.webhookUrl")} />
        </div>
        <Field>
          <FieldLabel htmlFor={`allowed-${channel.id}`}>{t("channels.allowedChats")}</FieldLabel>
          <div className="flex gap-2">
            <Input id={`allowed-${channel.id}`} dir="ltr" value={allowed} onChange={(e) => setAllowed(e.target.value)} placeholder="123456789, -100987654321" />
            <Button variant="outline" disabled={busy} onClick={() => save({ allowed_chat_ids: allowed }, t("channels.saved"))}>
              {t("common.save")}
            </Button>
          </div>
          <FieldDescription>{t("channels.allowedChatsHint")}</FieldDescription>
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
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  function handleOpenChange(next: boolean) {
    if (next) {
      setName("")
      setToken("")
      setErr(null)
    }
    onOpenChange(next)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/telegram-channels", { json: { name: name.trim(), bot_token: token.trim() }, workspaceId: ws.id })
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
        <form onSubmit={submit} className="flex flex-col gap-4" data-form="telegram-channel">
          <DialogHeader>
            <DialogTitle>{t("channels.telegram.addTitle")}</DialogTitle>
            <DialogDescription>{t("channels.telegram.addHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="tg-name">{t("channels.name")}</FieldLabel>
              <Input id="tg-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} placeholder={t("channels.namePlaceholder")} />
            </Field>
            <Field>
              <FieldLabel htmlFor="tg-token">{t("channels.telegram.botToken")}</FieldLabel>
              <Input id="tg-token" dir="ltr" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value)} placeholder="123456:ABC-DEF..." />
              <FieldDescription>{t("channels.telegram.botTokenHint")}</FieldDescription>
            </Field>
          </FieldGroup>
          {err ? <FieldError>{err}</FieldError> : null}
          <DialogFooter>
            <Button type="submit" disabled={busy || !name.trim() || !token.trim()}>
              {busy ? t("common.working") : t("channels.connect")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
