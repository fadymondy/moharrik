"use client"

import Link from "next/link"
import { useState } from "react"
import { ArrowLeftIcon, QrCodeIcon, TriangleAlertIcon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type WhatsAppLink } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWhatsApp } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function WhatsAppPage() {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("whatsapp.title")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")
  const link = useWhatsApp(ws.id)
  const [qr, setQr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function startLink() {
    setBusy(true)
    try {
      const res = await api<WhatsAppLink>("/api/w/whatsapp/link", { method: "POST", workspaceId: ws.id })
      setQr(res.qr_base64 || null)
      if (!res.qr_base64 && res.state === "open") toast.success(t("whatsapp.alreadyLinked"))
      link.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function unlink() {
    await api("/api/w/whatsapp", { method: "DELETE", workspaceId: ws.id })
    setQr(null)
    toast.success(t("whatsapp.unlinked"))
    link.mutate()
  }

  // Empty is "close": this goes straight into a label key, and an empty one
  // rendered the raw key on the page.
  const state = link.data?.state || "close"

  return (
    <>
      <SectionHeader
        micro={ws.summary.name}
        title={t("whatsapp.title")}
        description={t("whatsapp.hint")}
        action={
          <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/inbox`} />}>
            <ArrowLeftIcon className="rtl:rotate-180" />
            {t("mailbox.backToInbox")}
          </Button>
        }
      />

      {link.error ? (
        <ErrorState error={link.error} />
      ) : !link.data ? (
        <LoadingRows rows={2} />
      ) : !link.data.available ? (
        // Not a dead button: this deployment has no WhatsApp server at all, and
        // saying so is more useful than a control that always fails.
        <EmptyState title={t("whatsapp.unavailable")} body={t("whatsapp.unavailableBody")} />
      ) : (
        <div className="px-6" data-whatsapp-state={state}>
          <div className="flex flex-wrap items-center gap-2 pb-4 text-sm">
            <Badge variant={state === "open" ? "default" : state === "connecting" ? "secondary" : "outline"}>
              {t(`whatsapp.state.${state}`)}
            </Badge>
            {link.data.phone ? (
              <span className="text-grid-muted">
                <Ltr>+{link.data.phone}</Ltr>
              </span>
            ) : null}
            {link.data.linked_at ? <span className="text-grid-muted">· {t("whatsapp.linkedAt", { date: formatDate(link.data.linked_at) })}</span> : null}
          </div>

          {link.data.last_error ? (
            <p className="mb-4 flex items-start gap-2 border border-grid-danger-text/40 bg-grid-danger-text/5 px-4 py-3 text-sm" role="status" data-whatsapp-error>
              <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-danger-text" />
              <span>{link.data.last_error}</span>
            </p>
          ) : null}

          {qr ? (
            <div className="mb-4 flex flex-col items-start gap-2">
              <p className="text-sm">{t("whatsapp.scanHint")}</p>
              {/* The QR comes back as a data URL from the WhatsApp server. */}
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={qr} alt={t("whatsapp.qrAlt")} width={256} height={256} className="border border-line bg-white p-2" data-whatsapp-qr />
              <p className="text-xs text-grid-muted">{t("whatsapp.scanExpires")}</p>
            </div>
          ) : null}

          {canManage ? (
            <div className="flex flex-wrap items-center gap-2">
              <Button onClick={startLink} disabled={busy}>
                <QrCodeIcon />
                {busy ? t("common.working") : state === "open" ? t("whatsapp.relink") : t("whatsapp.link")}
              </Button>
              {link.data.linked ? (
                <ConfirmButton
                  label={t("whatsapp.unlink")}
                  title={t("whatsapp.unlinkTitle")}
                  description={t("whatsapp.unlinkBody")}
                  confirmLabel={t("whatsapp.unlink")}
                  onConfirm={unlink}
                />
              ) : null}
            </div>
          ) : (
            <p className="text-sm text-grid-muted">{t("mailbox.managerOnly")}</p>
          )}

          <p className="pt-4 text-sm text-grid-muted">{t("whatsapp.replyHint")}</p>
        </div>
      )}
      <div className="pb-8" />
    </>
  )
}
