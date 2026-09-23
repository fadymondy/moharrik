"use client"

import Link from "next/link"
import { useEffect, useRef, useState, type FormEvent } from "react"
import { ArrowLeftIcon, CheckCircle2Icon, KeyRoundIcon, QrCodeIcon, TriangleAlertIcon, WebhookIcon } from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type WhatsAppLink } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWhatsApp } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

/*
Linking the workspace's WhatsApp number (MH-134), and MH-413's polish:

  - while a QR or a pairing code is showing, the status is polled every 4s,
    so the page turns "Linked" by itself the moment the phone links — nobody
    has to reload to find out it worked;
  - a pairing code, for linking without a camera: the number is typed, and the
    phone asks for the 8-character code instead of a scan;
  - the webhook's health (is Evolution delivering here?) and today's sends.
*/
export default function WhatsAppPage() {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("whatsapp.title")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")
  const [qr, setQr] = useState<string | null>(null)
  const [pairing, setPairing] = useState<string | null>(null)
  const [byNumber, setByNumber] = useState(false)
  const [number, setNumber] = useState("")
  const [busy, setBusy] = useState(false)
  // Poll only while something is waiting for the phone.
  const waiting = !!qr || !!pairing
  const link = useWhatsApp(ws.id, waiting ? 4000 : 0)

  // Empty is "close": this goes straight into a label key, and an empty one
  // rendered the raw key on the page.
  const state = link.data?.state || "close"

  // The moment the phone links: drop the code and say so.
  const announced = useRef(false)
  useEffect(() => {
    if (waiting && state === "open" && !announced.current) {
      announced.current = true
      setQr(null)
      setPairing(null)
      toast.success(t("whatsapp.nowLinked"))
    }
  }, [waiting, state, t])

  async function startLink(e?: FormEvent) {
    e?.preventDefault()
    setBusy(true)
    announced.current = false
    try {
      const res = await api<WhatsAppLink>("/api/w/whatsapp/link", {
        method: "POST",
        json: byNumber ? { number: number.trim() } : {},
        workspaceId: ws.id,
      })
      if (byNumber) {
        setPairing(res.pairing_code || null)
        setQr(null)
        if (!res.pairing_code && res.state !== "open") toast.error(t("whatsapp.noPairingCode"))
      } else {
        setQr(res.qr_base64 || null)
        setPairing(null)
      }
      if (!res.qr_base64 && !res.pairing_code && res.state === "open") toast.success(t("whatsapp.alreadyLinked"))
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
    setPairing(null)
    toast.success(t("whatsapp.unlinked"))
    link.mutate()
  }

  const hook = link.data?.webhook
  const hookOk = hook?.status === "ok"

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
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
        <div className="px-6" data-whatsapp-state={state} data-whatsapp-polling={waiting ? "on" : "off"}>
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
            <span className="text-grid-muted" data-whatsapp-sends={`${link.data.sent_today}/${link.data.failed_today}`}>
              · {t("whatsapp.sendsToday", { sent: link.data.sent_today, failed: link.data.failed_today })}
            </span>
          </div>

          {link.data.last_error ? (
            <p className="mb-4 flex items-start gap-2 border border-grid-danger-text/40 bg-grid-danger-text/5 px-4 py-3 text-sm" role="status" data-whatsapp-error>
              <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-danger-text" />
              <span>{link.data.last_error}</span>
            </p>
          ) : null}

          {link.data.linked && hook ? (
            <div className="mb-5 flex items-start gap-3 border border-line bg-grid-card px-4 py-3 text-sm" data-webhook-status={hook.status}>
              <WebhookIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" />
              <div className="min-w-0 flex-1">
                <p className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{t("whatsapp.webhook.title")}</span>
                  <Badge variant={hookOk ? "default" : hook.status === "unknown" ? "secondary" : "destructive"}>
                    {t(`whatsapp.webhook.${hook.status}`)}
                  </Badge>
                </p>
                <p className="text-grid-muted">
                  {hookOk ? t("whatsapp.webhook.okHint") : t(`whatsapp.webhook.${hook.status}Hint`)}{" "}
                  {hook.last_delivery_at ? t("whatsapp.webhook.lastDelivery", { date: formatDate(hook.last_delivery_at) }) : t("whatsapp.webhook.noDelivery")}
                </p>
              </div>
            </div>
          ) : null}

          {qr ? (
            <div className="mb-4 flex flex-col items-start gap-2">
              <p className="text-sm">{t("whatsapp.scanHint")}</p>
              {/* The QR comes back as a data URL from the WhatsApp server. */}
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={qr} alt={t("whatsapp.qrAlt")} width={256} height={256} className="border border-line bg-white p-2" data-whatsapp-qr />
              <p className="text-xs text-grid-muted">{t("whatsapp.scanExpires")} {t("whatsapp.watching")}</p>
            </div>
          ) : null}

          {pairing ? (
            <div className="mb-4 flex flex-col items-start gap-2" data-whatsapp-pairing>
              <p className="text-sm">{t("whatsapp.pairingHint")}</p>
              <div data-pairing-code={pairing}>
                <Ltr mono className="inline-block border border-line bg-grid-card px-4 py-2 text-2xl tracking-[0.3em]">
                  {pairing.length === 8 ? `${pairing.slice(0, 4)}-${pairing.slice(4)}` : pairing}
                </Ltr>
              </div>
              <p className="text-xs text-grid-muted">{t("whatsapp.watching")}</p>
            </div>
          ) : null}

          {state === "open" && !waiting ? (
            <p className="mb-4 flex items-center gap-2 text-sm" data-whatsapp-linked>
              <CheckCircle2Icon className="size-4 text-grid-brand" />
              {t("whatsapp.replyHint")}
            </p>
          ) : null}

          {canManage ? (
            <div className="flex flex-col gap-4">
              {byNumber ? (
                <form onSubmit={startLink} noValidate className="max-w-xl" data-form="whatsapp-pairing">
                  <FieldGroup className="sm:flex-row sm:items-end">
                    <Field className="sm:flex-1">
                      <FieldLabel htmlFor="wa-link-number">{t("whatsapp.pairingNumber")}</FieldLabel>
                      <Input id="wa-link-number" dir="ltr" inputMode="tel" value={number} onChange={(e) => setNumber(e.target.value)} placeholder="+20 100 000 0000" />
                    </Field>
                    <Button type="submit" disabled={busy || number.replace(/\D/g, "").length < 8}>
                      <KeyRoundIcon />
                      {busy ? t("common.working") : t("whatsapp.getPairingCode")}
                    </Button>
                  </FieldGroup>
                  <FieldDescription className="mt-2">{t("whatsapp.pairingNumberHint")}</FieldDescription>
                </form>
              ) : null}
              <div className="flex flex-wrap items-center gap-2">
                {!byNumber ? (
                  <Button onClick={() => void startLink()} disabled={busy}>
                    <QrCodeIcon />
                    {busy ? t("common.working") : state === "open" ? t("whatsapp.relink") : t("whatsapp.link")}
                  </Button>
                ) : null}
                <Button variant="ghost" size="sm" onClick={() => setByNumber((v) => !v)} data-toggle-pairing>
                  {byNumber ? t("whatsapp.useQr") : t("whatsapp.usePairing")}
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
            </div>
          ) : (
            <p className="text-sm text-grid-muted">{t("mailbox.managerOnly")}</p>
          )}

          {state !== "open" ? <p className="pt-4 text-sm text-grid-muted">{t("whatsapp.replyHint")}</p> : null}
        </div>
      )}
      <div className="pb-8" />
    </>
  )
}
