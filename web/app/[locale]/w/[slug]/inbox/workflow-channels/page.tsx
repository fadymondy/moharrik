"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { PlusIcon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type WorkflowChannel } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWorkflowChannels, useWorkflows } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

const NETWORKS = ["linkedin", "facebook", "instagram", "x"] as const

/*
Settings → Inbox → Workflow channels (MH-564): an inbox channel fed by a
browser workflow's "Push to inbox" step on a schedule, for a network with no
usable API. Reading is done by pointing the channel at a workflow built on
the matching read preset (build it on the Automation → Workflows page); this
settings page owns the channel's identity, its allow-replies switch and its
on/off switch, and shows the last run's own numbers.
*/
export default function WorkflowChannelsPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("channels.workflow.title")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")
  const channels = useWorkflowChannels(ws.id)
  const workflows = useWorkflows(ws.id)
  const [creating, setCreating] = useState(false)
  const list = channels.data?.channels ?? []
  const workflowName = (id: string | null) => (id ? workflows.data?.workflows?.find((w) => w.id === id)?.name ?? id : "")

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("channels.workflow.title")}
        description={t("channels.workflow.hint")}
        action={
          canManage ? (
            <Button onClick={() => setCreating(true)} disabled={!channels.data}>
              <PlusIcon />
              {t("channels.addConnection")}
            </Button>
          ) : undefined
        }
      />

      <div className="mx-6 mb-6 border border-line bg-grid-card px-4 py-3 text-sm" data-setup-instructions>
        <p className="font-medium">{t("channels.workflow.setupTitle")}</p>
        <ol className="mt-2 list-decimal space-y-1 ps-5 text-grid-muted">
          <li>{t("channels.workflow.step1")}</li>
          <li>{t("channels.workflow.step2")}</li>
          <li>{t("channels.workflow.step3")}</li>
        </ol>
        <Button variant="ghost" size="sm" className="mt-2" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation/workflows`} />}>
          {t("channels.workflow.openWorkflows")}
        </Button>
      </div>

      {!canManage ? (
        <p className="px-6 py-6 text-sm text-grid-muted">{t("channels.managerOnly")}</p>
      ) : channels.error ? (
        <ErrorState error={channels.error} />
      ) : !channels.data ? (
        <LoadingRows rows={2} />
      ) : list.length === 0 ? (
        <EmptyState title={t("channels.workflow.emptyTitle")} body={t("channels.workflow.emptyBody")} />
      ) : (
        <ul className="border-y border-line" data-workflow-channels>
          {list.map((c) => (
            <WorkflowChannelRow key={c.id} channel={c} workflowName={workflowName(c.workflow_id)} onChange={() => channels.mutate()} />
          ))}
        </ul>
      )}
      <div className="pb-8" />

      <CreateDialog open={creating} onOpenChange={setCreating} onCreated={() => channels.mutate()} />
    </>
  )
}

function WorkflowChannelRow({ channel, workflowName, onChange }: { channel: WorkflowChannel; workflowName: string; onChange: () => void }) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)

  async function save(json: Record<string, unknown>, done?: string) {
    setBusy(true)
    try {
      await api(`/api/w/workflow-channels/${encodeURIComponent(channel.id)}`, {
        method: "PUT",
        json: { name: channel.name, network: channel.network, ...json },
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

  async function remove() {
    await api(`/api/w/workflow-channels/${encodeURIComponent(channel.id)}`, { method: "DELETE", workspaceId: ws.id })
    toast.success(t("channels.removed"))
    onChange()
  }

  return (
    <li className="px-6 py-5" data-channel-row={channel.name}>
      <div className="flex flex-wrap items-center gap-3">
        <p className="text-sm font-medium">
          <Bdi>{channel.name}</Bdi>
        </p>
        <Badge variant="secondary">{t(`channels.workflow.network.${channel.network}`)}</Badge>
        <Badge variant={channel.enabled ? "default" : "outline"}>{channel.enabled ? t("channels.enabled") : t("channels.disabled")}</Badge>
        <div className="ms-auto flex items-center gap-2">
          <Switch
            checked={channel.allow_replies}
            onCheckedChange={() => save({ allow_replies: !channel.allow_replies }, channel.allow_replies ? t("channels.workflow.repliesOff") : t("channels.workflow.repliesOn"))}
            aria-label={t("channels.workflow.allowReplies")}
            disabled={busy}
          />
          <span className="text-xs text-grid-muted">{t("channels.workflow.allowReplies")}</span>
          <Switch
            checked={channel.enabled}
            onCheckedChange={() => save({ enabled: !channel.enabled }, channel.enabled ? t("channels.turnedOff") : t("channels.turnedOn"))}
            aria-label={channel.enabled ? t("channels.enabled") : t("channels.disabled")}
            disabled={busy}
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
      <p className="mt-2 text-sm text-grid-muted">
        {channel.workflow_id ? t("channels.workflow.readsWith", { name: isolate(workflowName) }) : t("channels.workflow.noWorkflow")}
      </p>
      <p className="mt-1 text-xs text-grid-muted" data-workflow-channel-stats>
        {channel.last_run_at
          ? t("channels.workflow.lastRun", { date: formatDate(channel.last_run_at), count: channel.last_pushed_count })
          : t("channels.workflow.neverRun")}
        {channel.last_error ? ` · ${channel.last_error}` : ""}
      </p>
    </li>
  )
}

function CreateDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (v: boolean) => void; onCreated: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const workflows = useWorkflows(ws.id)
  const [name, setName] = useState("")
  const [network, setNetwork] = useState<(typeof NETWORKS)[number]>("linkedin")
  const [workflowId, setWorkflowId] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  function handleOpenChange(next: boolean) {
    if (next) {
      setName("")
      setNetwork("linkedin")
      setWorkflowId("")
      setErr(null)
    }
    onOpenChange(next)
  }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/workflow-channels", {
        json: { name: name.trim(), network, workflow_id: workflowId || undefined },
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

  const workflowItems = (workflows.data?.workflows ?? []).map((w) => ({ value: w.id, label: w.name }))
  const networkItems = NETWORKS.map((n) => ({ value: n, label: t(`channels.workflow.network.${n}`) }))

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} className="flex flex-col gap-4" data-form="workflow-channel">
          <DialogHeader>
            <DialogTitle>{t("channels.workflow.addTitle")}</DialogTitle>
            <DialogDescription>{t("channels.workflow.addHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="wfc-name">{t("channels.name")}</FieldLabel>
              <Input id="wfc-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} placeholder={t("channels.namePlaceholder")} />
            </Field>
            <Field>
              <FieldLabel htmlFor="wfc-network">{t("channels.workflow.network.label")}</FieldLabel>
              <Select items={networkItems} value={network} onValueChange={(v) => setNetwork((v as (typeof NETWORKS)[number]) ?? "linkedin")}>
                <SelectTrigger id="wfc-network" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {networkItems.map((i) => (
                    <SelectItem key={i.value} value={i.value}>
                      {i.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="wfc-workflow">
                {t("channels.workflow.pickWorkflow")} <span className="text-grid-muted">({t("common.optional")})</span>
              </FieldLabel>
              <Select items={workflowItems} value={workflowId} onValueChange={(v) => setWorkflowId(String(v ?? ""))}>
                <SelectTrigger id="wfc-workflow" className="w-full">
                  <SelectValue placeholder={t("channels.workflow.noWorkflowOption")} />
                </SelectTrigger>
                <SelectContent>
                  {workflowItems.map((i) => (
                    <SelectItem key={i.value} value={i.value}>
                      <Bdi>{i.label}</Bdi>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <FieldDescription>{t("channels.workflow.pickWorkflowHint")}</FieldDescription>
            </Field>
          </FieldGroup>
          {err ? <FieldError>{err}</FieldError> : null}
          <DialogFooter>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("channels.connect")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
