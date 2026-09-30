"use client"

import { useState, type FormEvent } from "react"
import { Button, Field, FieldDescription, FieldError, FieldLabel, Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, toast } from "@fadymondy/nasaq/web"
import { FieldGroup } from "@/components/form-layout"

import { Bdi } from "@/components/bidi"
import { SetupGuide, type SetupGuideStep } from "@/components/channels/setup-guide"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type WorkflowChannel } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWorkflows } from "@/lib/queries"

export const WORKFLOW_NETWORKS = ["linkedin", "facebook", "instagram", "x"] as const

const WORKFLOW_CHANNEL_GUIDE_STEPS: SetupGuideStep[] = [
  {
    titleKey: "channels.workflow.guide.step1.title",
    bodyKey: "channels.workflow.guide.step1.body",
  },
  {
    titleKey: "channels.workflow.guide.step2.title",
    bodyKey: "channels.workflow.guide.step2.body",
  },
  {
    titleKey: "channels.workflow.guide.step3.title",
    bodyKey: "channels.workflow.guide.step3.body",
  },
]

/** The workflow-channel form (extracted from inbox/workflow-channels for MH-571). */
export function WorkflowChannelForm({
  channel,
  onSaved,
  onCancel,
  defaultGuideOpen,
}: {
  channel?: WorkflowChannel
  onSaved: () => void
  onCancel?: () => void
  defaultGuideOpen?: boolean
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const workflows = useWorkflows(ws.id)
  const [name, setName] = useState(channel?.name ?? "")
  const [network, setNetwork] = useState<(typeof WORKFLOW_NETWORKS)[number]>(
    channel?.network ?? "linkedin",
  )
  const [workflowId, setWorkflowId] = useState(channel?.workflow_id ?? "")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      if (channel) {
        await api(`/api/w/workflow-channels/${encodeURIComponent(channel.id)}`, {
          method: "PUT",
          json: { name: name.trim(), network, workflow_id: workflowId || undefined },
          workspaceId: ws.id,
        })
        toast.success(t("channels.saved"))
      } else {
        await api("/api/w/workflow-channels", {
          json: { name: name.trim(), network, workflow_id: workflowId || undefined },
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

  const workflowItems = (workflows.data?.workflows ?? []).map((w) => ({
    value: w.id,
    label: w.name,
  }))
  const networkItems = WORKFLOW_NETWORKS.map((n) => ({
    value: n,
    label: t(`channels.workflow.network.${n}`),
  }))

  return (
    <div className="flex flex-col gap-4">
      <SetupGuide
        id="workflow"
        titleKey="channels.workflow.setupTitle"
        steps={WORKFLOW_CHANNEL_GUIDE_STEPS}
        defaultOpen={defaultGuideOpen ?? !channel}
      />
      <form onSubmit={submit} className="flex flex-col gap-4" data-form="workflow-channel">
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="wfc-name">{t("channels.name")}</FieldLabel>
            <Input
              id="wfc-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              maxLength={80}
              placeholder={t("channels.namePlaceholder")}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="wfc-network">{t("channels.workflow.network.label")}</FieldLabel>
            <Select
              items={networkItems}
              value={network}
              onValueChange={(v) =>
                setNetwork((v as (typeof WORKFLOW_NETWORKS)[number]) ?? "linkedin")
              }
            >
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
              {t("channels.workflow.pickWorkflow")}{" "}
              <span className="text-muted-foreground">({t("common.optional")})</span>
            </FieldLabel>
            <Select
              items={workflowItems}
              value={workflowId}
              onValueChange={(v) => setWorkflowId(String(v ?? ""))}
            >
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
        <div className="flex items-center gap-2">
          <Button type="submit" disabled={busy || !name.trim()}>
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
