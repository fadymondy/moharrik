"use client"

import Link from "next/link"
import { useState } from "react"
import { MailWarningIcon, PencilIcon, SendIcon } from "lucide-react"
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
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { CampaignForm } from "@/components/campaigns/campaign-form"
import { Ltr } from "@/components/copy-field"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Campaign, type CampaignStatus, type MailList } from "@/lib/api"
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
  // The sheet follows the live row, so a campaign that starts sending while
  // it is open is seen as such.
  const [editingId, setEditingId] = useState<string | null>(null)
  const editing = (campaigns.data?.campaigns ?? []).find((c) => c.id === editingId) ?? null

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
          onEdit={(c) => setEditingId(c.id)}
        />
      )}
      {canWrite ? (
        <EditCampaignSheet
          campaign={editing}
          lists={lists.data?.lists ?? []}
          whatsappLinked={whatsappLinked}
          smtpReady={smtpReady}
          canSend={canSend}
          onClose={() => setEditingId(null)}
          onSaved={() => campaigns.mutate()}
        />
      ) : null}

      <div className="grid-separator-contained hatch-band mt-8" aria-hidden />
      {canWrite ? (
        <NewCampaign
          lists={lists.data?.lists ?? []}
          whatsappLinked={whatsappLinked}
          smtpReady={smtpReady}
          canSend={canSend}
          onCreated={() => campaigns.mutate()}
        />
      ) : ws.current ? (
        <p className="px-6 py-6 text-sm text-grid-muted">{t("campaigns.readOnly")}</p>
      ) : null}
    </>
  )
}

/** Drafting a new campaign: the shared form (components/campaigns/campaign-form.tsx). */
function NewCampaign({
  lists,
  whatsappLinked,
  smtpReady,
  canSend,
  onCreated,
}: {
  lists: MailList[]
  whatsappLinked: boolean
  smtpReady: boolean
  canSend: boolean
  onCreated: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
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
        <div className="mt-5">
          <CampaignForm lists={lists} whatsappLinked={whatsappLinked} smtpReady={smtpReady} canSend={canSend} onSaved={onCreated} />
        </div>
      )}
    </section>
  )
}

/*
Editing a campaign that has not gone out (MH-407), in a sheet over the list.
The campaign is read from the live list, so if it starts sending while the
sheet is open (another tab, a colleague, an agent) the form is replaced by
the reason instead of offering a save the API would refuse.
*/
function EditCampaignSheet({
  campaign,
  lists,
  whatsappLinked,
  smtpReady,
  canSend,
  onClose,
  onSaved,
}: {
  campaign: Campaign | null
  lists: MailList[]
  whatsappLinked: boolean
  smtpReady: boolean
  canSend: boolean
  onClose: () => void
  onSaved: () => void
}) {
  const { t, isRtl } = useTranslations()
  const editable = campaign && (campaign.status === "draft" || campaign.status === "failed")
  return (
    <Sheet open={!!campaign} onOpenChange={(open) => (!open ? onClose() : undefined)}>
      <SheetContent
        side={isRtl ? "left" : "right"}
        className="w-full overflow-y-auto data-[side=left]:w-full data-[side=right]:w-full data-[side=left]:sm:max-w-2xl data-[side=right]:sm:max-w-2xl"
        data-edit-sheet
      >
        <SheetHeader className="pe-12">
          <SheetTitle>{t("campaigns.editTitle")}</SheetTitle>
          <SheetDescription>{t("campaigns.editHint")}</SheetDescription>
        </SheetHeader>
        <div className="px-4 pb-6">
          {campaign && editable ? (
            <CampaignForm
              key={campaign.id}
              campaign={campaign}
              lists={lists}
              whatsappLinked={whatsappLinked}
              smtpReady={smtpReady}
              canSend={canSend}
              onSaved={onSaved}
              onCancel={onClose}
            />
          ) : campaign ? (
            <p className="text-sm text-grid-muted" role="status" data-edit-locked={campaign.status}>
              {t("campaigns.editLocked", { status: t(`campaigns.status.${campaign.status}`) })}
            </p>
          ) : null}
        </div>
      </SheetContent>
    </Sheet>
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
  onEdit,
}: {
  items: Campaign[]
  lists: MailList[]
  canWrite: boolean
  canSend: boolean
  smtpReady: boolean
  whatsappLinked: boolean
  onChange: () => void
  onEdit: (c: Campaign) => void
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
                  {canWrite && sendable ? (
                    <Button variant="ghost" size="sm" onClick={() => onEdit(c)} data-edit-campaign={c.subject}>
                      <PencilIcon />
                      {t("campaigns.edit")}
                    </Button>
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
