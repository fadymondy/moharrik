"use client"

import { useState, type FormEvent } from "react"
import { EyeIcon, FlaskConicalIcon, PencilLineIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { CampaignPreview } from "@/components/campaigns/campaign-preview"
import { CampaignTestSend } from "@/components/campaigns/campaign-test-send"
import { MarkdownEditor } from "@/components/campaigns/markdown-editor"
import { WhatsAppMissing } from "@/components/campaigns/whatsapp-missing"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Campaign, type CampaignChannel, type MailList } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWhatsAppAudience } from "@/lib/queries"
import { Bdi } from "@/components/bidi"

/** The live WhatsApp audience of a list: the number a broadcast would reach now. */
function Reachable({ listId }: { listId: string }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const aud = useWhatsAppAudience(ws.id, listId)
  if (aud.error) return <p className="text-sm text-grid-danger-text">{t("campaigns.reachableError")}</p>
  return (
    <div className="text-sm" data-whatsapp-reachable={aud.data?.reachable ?? ""}>
      <p className="font-medium">{aud.data ? t("campaigns.reachable", { count: aud.data.reachable }) : t("common.working")}</p>
      <p className="text-grid-muted">{t("campaigns.reachableRule")}</p>
    </div>
  )
}

type Lang = "en" | "ar"

/*
One form for drafting and for editing (MH-407). Write / Preview / Test are tabs
over the same state, so the preview always shows what is in the form — saved or
not — and the test box knows whether what it would send is what you see.

Test appears only for a saved campaign (there is nothing on the server to test
before that) and only for someone who may send.
*/
export function CampaignForm({
  lists,
  whatsappLinked,
  smtpReady,
  canSend,
  campaign,
  onSaved,
  onCancel,
}: {
  lists: MailList[]
  whatsappLinked: boolean
  smtpReady: boolean
  canSend: boolean
  /** Editing this campaign; absent for a new draft. */
  campaign?: Campaign
  onSaved: (c: Campaign) => void
  onCancel?: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const editing = !!campaign
  const fresh = {
    listId: campaign?.list_id ?? "",
    subject: campaign?.subject ?? "",
    body: campaign?.body ?? "",
    lang: (campaign?.locale ?? (locale === "ar" ? "ar" : "en")) as Lang,
    channel: (campaign?.channel ?? "email") as CampaignChannel,
  }
  // What the server has, so "unsaved changes" is measured against the last save.
  const [saved, setSaved] = useState(fresh)
  const [listId, setListId] = useState(fresh.listId)
  const [subject, setSubject] = useState(fresh.subject)
  const [body, setBody] = useState(fresh.body)
  const [lang, setLang] = useState<Lang>(fresh.lang)
  const [channel, setChannel] = useState<CampaignChannel>(fresh.channel)
  const [tab, setTab] = useState<string>("write")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const chosen = listId || lists[0]?.id || ""
  const wa = channel === "whatsapp"
  const dirty =
    editing &&
    (chosen !== saved.listId || subject.trim() !== saved.subject || body !== saved.body || lang !== saved.lang || channel !== saved.channel)

  const channelItems = [
    { value: "email", label: t("campaigns.channel.email") },
    { value: "whatsapp", label: t("campaigns.channel.whatsapp") },
  ]
  const listItems = lists.map((l) => ({ value: l.id, label: l.name }))
  const langItems = [
    { value: "en", label: t("lang.en") },
    { value: "ar", label: t("lang.ar") },
  ]

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    const payload = { list_id: chosen, channel, subject: subject.trim(), body, locale: lang }
    try {
      const res = await api<{ campaign: Campaign }>(
        campaign ? `/api/w/campaigns/${encodeURIComponent(campaign.id)}` : "/api/w/campaigns",
        { method: editing ? "PUT" : "POST", json: payload, workspaceId: ws.id },
      )
      if (editing) {
        setSaved({ listId: chosen, subject: subject.trim(), body, lang, channel })
        toast.success(t("campaigns.saved"))
      } else {
        toast.success(t("campaigns.created"))
        setSubject("")
        setBody("")
        setTab("write")
      }
      onSaved(res.campaign)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  const dir = lang === "ar" ? "rtl" : "ltr"
  const showTest = editing && canSend

  return (
    <form onSubmit={onSubmit} noValidate className="max-w-3xl" data-form={editing ? "edit-campaign" : "new-campaign"}>
      <FieldGroup>
        <div className="flex flex-col gap-4 sm:flex-row">
          <Field className="sm:w-40">
            <FieldLabel htmlFor={`${editing ? "edit" : "campaign"}-channel`}>{t("campaigns.channel")}</FieldLabel>
            <Select items={channelItems} value={channel} onValueChange={(v) => setChannel(v === "whatsapp" ? "whatsapp" : "email")}>
              <SelectTrigger id={`${editing ? "edit" : "campaign"}-channel`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {channelItems.map((i) => (
                  <SelectItem key={i.value} value={i.value}>
                    {i.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field className="sm:flex-1">
            <FieldLabel htmlFor={`${editing ? "edit" : "campaign"}-list`}>{t("campaigns.list")}</FieldLabel>
            <Select items={listItems} value={chosen} onValueChange={(v) => setListId(String(v ?? ""))}>
              <SelectTrigger id={`${editing ? "edit" : "campaign"}-list`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {listItems.map((i) => (
                  <SelectItem key={i.value} value={i.value}>
                    <Bdi>{i.label}</Bdi>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field className="sm:w-40">
            <FieldLabel htmlFor={`${editing ? "edit" : "campaign"}-lang`}>{t("campaigns.language")}</FieldLabel>
            <Select items={langItems} value={lang} onValueChange={(v) => setLang(v === "ar" ? "ar" : "en")}>
              <SelectTrigger id={`${editing ? "edit" : "campaign"}-lang`} className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {langItems.map((i) => (
                  <SelectItem key={i.value} value={i.value}>
                    {i.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </div>
        {wa ? (
          <div className="flex flex-col gap-3 border border-line bg-grid-card px-4 py-3" data-whatsapp-panel>
            <Reachable listId={chosen} />
            {!whatsappLinked ? <WhatsAppMissing /> : null}
          </div>
        ) : null}
        <Field>
          <FieldLabel htmlFor={`${editing ? "edit" : "campaign"}-subject`}>{wa ? t("campaigns.internalName") : t("campaigns.subject")}</FieldLabel>
          <Input
            id={`${editing ? "edit" : "campaign"}-subject`}
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            maxLength={200}
            dir={dir}
          />
          {wa ? <FieldDescription>{t("campaigns.internalNameHint")}</FieldDescription> : null}
        </Field>

        <Tabs value={tab} onValueChange={(v) => setTab(String(v))} className="gap-3">
          <TabsList variant="line" aria-label={wa ? t("campaigns.message") : t("campaigns.body")}>
            <TabsTrigger value="write" data-tab="write">
              <PencilLineIcon />
              {wa ? t("campaigns.message") : t("campaigns.body")}
            </TabsTrigger>
            <TabsTrigger value="preview" data-tab="preview">
              <EyeIcon />
              {t("campaigns.preview")}
            </TabsTrigger>
            {showTest ? (
              <TabsTrigger value="test" data-tab="test">
                <FlaskConicalIcon />
                {t("campaigns.test")}
              </TabsTrigger>
            ) : null}
          </TabsList>
          <TabsContent value="write">
            <Field>
              <FieldLabel htmlFor={`${editing ? "edit" : "campaign"}-body`} className="sr-only">
                {wa ? t("campaigns.message") : t("campaigns.body")}
              </FieldLabel>
              <MarkdownEditor id={`${editing ? "edit" : "campaign"}-body`} value={body} onValueChange={setBody} dir={dir} toolbar={!wa} />
              <FieldDescription>{wa ? t("campaigns.whatsappBodyHint") : t("campaigns.bodyHint")}</FieldDescription>
            </Field>
          </TabsContent>
          <TabsContent value="preview">
            <CampaignPreview channel={channel} subject={subject} body={body} lang={lang} onLangChange={setLang} />
          </TabsContent>
          {showTest && campaign ? (
            <TabsContent value="test">
              <CampaignTestSend campaign={campaign} dirty={dirty} smtpReady={smtpReady} whatsappLinked={whatsappLinked} />
            </TabsContent>
          ) : null}
        </Tabs>

        <div className="flex flex-wrap items-center gap-3">
          <Button type="submit" disabled={busy || !subject.trim() || !body.trim() || !chosen || (editing && !dirty)}>
            {busy ? t("common.working") : editing ? t("campaigns.saveChanges") : t("campaigns.create")}
          </Button>
          {onCancel ? (
            <Button type="button" variant="ghost" onClick={onCancel}>
              {editing && !dirty ? t("common.done") : t("common.cancel")}
            </Button>
          ) : null}
          {editing && dirty ? (
            <span className="text-sm text-grid-muted" data-unsaved>
              {t("campaigns.unsaved")}
            </span>
          ) : null}
        </div>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}
