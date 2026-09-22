"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { MailWarningIcon, MessageCircleWarningIcon, SendIcon } from "lucide-react"
import { toast } from "sonner"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Campaign, type CampaignChannel, type CampaignStatus, type MailList } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCampaigns, useMailLists, useWhatsAppAudience } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

const statusVariant: Record<CampaignStatus, "default" | "secondary" | "outline" | "destructive"> = {
  draft: "outline",
  sending: "secondary",
  sent: "default",
  failed: "destructive",
}

export default function CampaignsPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.campaigns")} · ${ws.summary.name}`)

  const canWrite = ws.can("campaigns:write")
  const canSend = ws.can("campaigns:send")
  const isAdmin = ws.can("mail:settings")
  const campaigns = useCampaigns(ws.id)
  const lists = useMailLists(ws.id)
  const smtpReady = !!campaigns.data?.smtp_configured
  const whatsappLinked = !!campaigns.data?.whatsapp_linked

  return (
    <>
      <SectionHeader micro={ws.summary.name} title={t("nav.campaigns")} description={t("campaigns.hint")} />

      {campaigns.data && !smtpReady ? (
        <p className="flex items-start gap-2 px-6 pb-4 text-sm text-grid-muted" role="status" data-smtp-missing>
          <MailWarningIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" />
          {isAdmin ? (
            <span>
              {t("campaigns.smtpMissing")}{" "}
              <Link className="underline underline-offset-4" href={`/${locale}/w/${ws.slug}/settings/email`}>
                {t("contacts.smtpLink")}
              </Link>
            </span>
          ) : (
            <span>{t("campaigns.smtpMissingMember")}</span>
          )}
        </p>
      ) : null}

      {campaigns.error ? (
        <ErrorState error={campaigns.error} />
      ) : campaigns.isLoading ? (
        <LoadingRows />
      ) : (campaigns.data?.campaigns ?? []).length === 0 ? (
        <EmptyState title={t("campaigns.empty")} body={canWrite ? t("campaigns.emptyBody") : undefined} />
      ) : (
        <CampaignTable
          items={campaigns.data?.campaigns ?? []}
          lists={lists.data?.lists ?? []}
          canWrite={canWrite}
          canSend={canSend}
          smtpReady={smtpReady}
          whatsappLinked={whatsappLinked}
          onChange={() => campaigns.mutate()}
        />
      )}

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />
      {canWrite ? (
        <NewCampaign lists={lists.data?.lists ?? []} whatsappLinked={whatsappLinked} onCreated={() => campaigns.mutate()} />
      ) : ws.current ? (
        <p className="px-6 py-6 text-sm text-grid-muted">{t("campaigns.readOnly")}</p>
      ) : null}
    </>
  )
}

/** Where to link a WhatsApp number, or who to ask — never a send that would 409. */
function WhatsAppMissing() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  return (
    <p className="flex items-start gap-2 text-sm text-grid-muted" role="status" data-whatsapp-missing>
      <MessageCircleWarningIcon className="mt-0.5 size-4 shrink-0 text-grid-warn" />
      {ws.can("inbox:manage") ? (
        <span>
          {t("campaigns.whatsappMissing")}{" "}
          <Link className="underline underline-offset-4" href={`/${locale}/w/${ws.slug}/inbox/whatsapp`}>
            {t("campaigns.whatsappLink")}
          </Link>
        </span>
      ) : (
        <span>{t("campaigns.whatsappMissingMember")}</span>
      )}
    </p>
  )
}

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

function NewCampaign({ lists, whatsappLinked, onCreated }: { lists: MailList[]; whatsappLinked: boolean; onCreated: () => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [listId, setListId] = useState("")
  const [subject, setSubject] = useState("")
  const [body, setBody] = useState("")
  const [lang, setLang] = useState<string>(locale === "ar" ? "ar" : "en")
  const [channel, setChannel] = useState<CampaignChannel>("email")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const chosen = listId || lists[0]?.id || ""
  const wa = channel === "whatsapp"
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
    try {
      await api("/api/w/campaigns", { json: { list_id: chosen, channel, subject: subject.trim(), body, locale: lang }, workspaceId: ws.id })
      toast.success(t("campaigns.created"))
      setSubject("")
      setBody("")
      onCreated()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="px-6 py-6 pb-10">
      <h2 className="text-base font-medium">{t("campaigns.newTitle")}</h2>
      {lists.length === 0 ? (
        <p className="mt-2 text-sm text-grid-muted">
          <Link className="underline underline-offset-4" href={`/${locale}/w/${ws.slug}/contacts`}>
            {t("campaigns.needList")}
          </Link>
        </p>
      ) : (
        <form onSubmit={onSubmit} noValidate className="mt-5 max-w-3xl" data-form="new-campaign">
          <FieldGroup>
            <div className="flex flex-col gap-4 sm:flex-row">
              <Field className="sm:w-40">
                <FieldLabel htmlFor="campaign-channel">{t("campaigns.channel")}</FieldLabel>
                <Select items={channelItems} value={channel} onValueChange={(v) => setChannel(v === "whatsapp" ? "whatsapp" : "email")}>
                  <SelectTrigger id="campaign-channel" className="w-full">
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
                <FieldLabel htmlFor="campaign-list">{t("campaigns.list")}</FieldLabel>
                <Select items={listItems} value={chosen} onValueChange={(v) => setListId(String(v ?? ""))}>
                  <SelectTrigger id="campaign-list" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {listItems.map((i) => (
                      <SelectItem key={i.value} value={i.value}>
                        {i.label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field className="sm:w-40">
                <FieldLabel htmlFor="campaign-lang">{t("campaigns.language")}</FieldLabel>
                <Select items={langItems} value={lang} onValueChange={(v) => setLang(String(v ?? "en"))}>
                  <SelectTrigger id="campaign-lang" className="w-full">
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
              <FieldLabel htmlFor="campaign-subject">{wa ? t("campaigns.internalName") : t("campaigns.subject")}</FieldLabel>
              <Input id="campaign-subject" value={subject} onChange={(e) => setSubject(e.target.value)} maxLength={200} dir={lang === "ar" ? "rtl" : "ltr"} />
              {wa ? <FieldDescription>{t("campaigns.internalNameHint")}</FieldDescription> : null}
            </Field>
            <Field>
              <FieldLabel htmlFor="campaign-body">{wa ? t("campaigns.message") : t("campaigns.body")}</FieldLabel>
              <Textarea id="campaign-body" rows={10} value={body} onChange={(e) => setBody(e.target.value)} dir={lang === "ar" ? "rtl" : "ltr"} />
              <FieldDescription>{wa ? t("campaigns.whatsappBodyHint") : t("campaigns.bodyHint")}</FieldDescription>
            </Field>
            <div>
              <Button type="submit" disabled={busy || !subject.trim() || !body.trim() || !chosen}>
                {busy ? t("common.working") : t("campaigns.create")}
              </Button>
            </div>
          </FieldGroup>
          {err ? <FieldError className="mt-3">{err}</FieldError> : null}
        </form>
      )}
    </section>
  )
}

function CampaignTable({
  items,
  lists,
  canWrite,
  canSend,
  smtpReady,
  whatsappLinked,
  onChange,
}: {
  items: Campaign[]
  lists: MailList[]
  canWrite: boolean
  canSend: boolean
  smtpReady: boolean
  whatsappLinked: boolean
  onChange: () => void
}) {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()

  async function remove(id: string) {
    try {
      await api(`/api/w/campaigns/${encodeURIComponent(id)}`, { method: "DELETE", workspaceId: ws.id })
      toast.success(t("campaigns.deleted"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <div className="border-y border-line">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="ps-6">{t("campaigns.subject")}</TableHead>
            <TableHead>{t("campaigns.channel")}</TableHead>
            <TableHead>{t("campaigns.list")}</TableHead>
            <TableHead>{t("campaigns.status")}</TableHead>
            <TableHead>{t("campaigns.progress")}</TableHead>
            <TableHead>{t("campaigns.createdAt")}</TableHead>
            <TableHead className="pe-6 text-end">
              <span className="sr-only">{t("common.actions")}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((c) => {
            const subscribed = lists.find((l) => l.id === c.list_id)?.subscribed ?? 0
            const wa = c.channel === "whatsapp"
            const sendable = c.status === "draft" || c.status === "failed"
            return (
              <TableRow key={c.id} data-campaign-row={c.subject}>
                <TableCell className="ps-6">
                  <div className="font-medium" dir="auto">
                    {c.subject}
                  </div>
                  {c.last_error ? (
                    <div className="max-w-md truncate text-xs text-grid-danger-text" title={c.last_error}>
                      {t("campaigns.lastError", { error: c.last_error })}
                    </div>
                  ) : null}
                </TableCell>
                <TableCell>
                  <Badge variant="outline" data-channel={wa ? "whatsapp" : "email"}>
                    {t(wa ? "campaigns.channel.whatsapp" : "campaigns.channel.email")}
                  </Badge>
                </TableCell>
                <TableCell>{c.list_name}</TableCell>
                <TableCell>
                  <Badge variant={statusVariant[c.status]}>{t(`campaigns.status.${c.status}`)}</Badge>
                </TableCell>
                <TableCell className="text-grid-muted">
                  <Ltr>
                    {c.status === "draft" ? "—" : `${c.sent_count} / ${c.recipient_count}${c.failed_count ? ` (−${c.failed_count})` : ""}`}
                  </Ltr>
                </TableCell>
                <TableCell className="text-grid-muted">
                  <Ltr>{formatDate(c.sent_at ?? c.created_at)}</Ltr>
                </TableCell>
                <TableCell className="pe-6 text-end whitespace-nowrap">
                  {canSend && sendable && wa && !whatsappLinked ? (
                    ws.can("inbox:manage") ? (
                      <Link className="me-2 text-xs underline underline-offset-4" href={`/${locale}/w/${ws.slug}/inbox/whatsapp`} data-send-blocked="whatsapp">
                        {t("campaigns.whatsappLinkToSend")}
                      </Link>
                    ) : (
                      <span className="me-2 text-xs text-grid-muted" data-send-blocked="whatsapp">
                        {t("campaigns.whatsappNotLinkedShort")}
                      </span>
                    )
                  ) : canSend && sendable && (wa || smtpReady) ? (
                    <SendButton campaign={c} count={subscribed} onSent={onChange} />
                  ) : null}
                  {canWrite && c.status !== "sending" ? (
                    <ConfirmButton
                      label={t("common.delete")}
                      title={t("campaigns.deleteTitle")}
                      description={t("campaigns.deleteBody", { subject: c.subject })}
                      confirmLabel={t("common.delete")}
                      onConfirm={() => remove(c.id)}
                    />
                  ) : null}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}

function SendButton({ campaign, count, onSent }: { campaign: Campaign; count: number; onSent: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const wa = campaign.channel === "whatsapp"
  const aud = useWhatsAppAudience(ws.id, wa && open ? campaign.list_id : null)
  const reachable = aud.data?.reachable

  async function send() {
    setBusy(true)
    try {
      await api(`/api/w/campaigns/${encodeURIComponent(campaign.id)}/send`, { method: "POST", workspaceId: ws.id })
      toast.success(t("campaigns.sending"))
      setOpen(false)
      onSent()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        <SendIcon />
        {campaign.status === "failed" ? t("campaigns.retry") : t("campaigns.send")}
      </Button>
      <AlertDialog open={open} onOpenChange={setOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("campaigns.sendTitle")}</AlertDialogTitle>
            <AlertDialogDescription data-send-reachable={wa ? (reachable ?? "") : undefined}>
              {wa
                ? reachable === undefined
                  ? t("common.working")
                  : reachable === 0
                    ? t("campaigns.sendNobodyWhatsApp", { list: campaign.list_name })
                    : t("campaigns.sendBodyWhatsApp", { subject: campaign.subject, list: campaign.list_name, count: reachable })
                : t("campaigns.sendBody", { subject: campaign.subject, list: campaign.list_name, count })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction disabled={busy || (wa && !reachable)} onClick={send}>
              {busy ? t("common.working") : t("campaigns.send")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
