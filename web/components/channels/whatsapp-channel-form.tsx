"use client"

import Link from "next/link"
import { useEffect, useRef, useState, type FormEvent } from "react"
import {
  CheckCircle2Icon,
  KeyRoundIcon,
  QrCodeIcon,
  TriangleAlertIcon,
  WebhookIcon,
} from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { SetupGuide, type SetupGuideStep } from "@/components/channels/setup-guide"
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

const WHATSAPP_GUIDE_STEPS: SetupGuideStep[] = [
  {
    titleKey: "channels.whatsapp.guide.step1.title",
    bodyKey: "channels.whatsapp.guide.step1.body",
  },
  {
    titleKey: "channels.whatsapp.guide.step2.title",
    bodyKey: "channels.whatsapp.guide.step2.body",
  },
  {
    titleKey: "channels.whatsapp.guide.step3.title",
    warningKey: "channels.whatsapp.guide.step3.warning",
  },
  {
    titleKey: "channels.whatsapp.guide.step4.title",
    bodyKey: "channels.whatsapp.guide.step4.body",
  },
]

/**
 * WhatsApp's link/QR/pairing flow (extracted from inbox/whatsapp for
 * MH-571). There is at most one WhatsApp number per workspace, so this
 * component is both the "add" and the "edit"/manage view — the Channels
 * page opens it in a sheet either way.
 */
export function WhatsAppChannelForm({
  onChanged,
  defaultGuideOpen,
}: {
  onChanged?: () => void
  defaultGuideOpen?: boolean
}) {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const canManage = ws.can("inbox:manage")
  const [qr, setQr] = useState<string | null>(null)
  const [pairing, setPairing] = useState<string | null>(null)
  const [byNumber, setByNumber] = useState(false)
  const [number, setNumber] = useState("")
  const [busy, setBusy] = useState(false)
  const waiting = !!qr || !!pairing
  const link = useWhatsApp(ws.id, waiting ? 4000 : 0)
  const state = link.data?.state || "close"

  const announced = useRef(false)
  useEffect(() => {
    if (waiting && state === "open" && !announced.current) {
      announced.current = true
      setQr(null)
      setPairing(null)
      toast.success(t("whatsapp.nowLinked"))
      onChanged?.()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
      if (!res.qr_base64 && !res.pairing_code && res.state === "open")
        toast.success(t("whatsapp.alreadyLinked"))
      link.mutate()
      onChanged?.()
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
    onChanged?.()
  }

  const hook = link.data?.webhook
  const hookOk = hook?.status === "ok"

  if (link.error) return <ErrorState error={link.error} />
  if (!link.data) return <LoadingRows rows={2} />
  if (!link.data.available)
    return <EmptyState title={t("whatsapp.unavailable")} body={t("whatsapp.unavailableBody")} />

  return (
    <div data-whatsapp-state={state} data-whatsapp-polling={waiting ? "on" : "off"}>
      <SetupGuide
        id="whatsapp"
        titleKey="channels.whatsapp.setupTitle"
        steps={WHATSAPP_GUIDE_STEPS}
        defaultOpen={defaultGuideOpen ?? !link.data.linked}
      />
      <div className="mt-4 flex flex-wrap items-center gap-2 pb-4 text-sm">
        <Badge
          variant={state === "open" ? "default" : state === "connecting" ? "secondary" : "outline"}
        >
          {t(`whatsapp.state.${state}`)}
        </Badge>
        {link.data.phone ? (
          <span className="text-grid-muted">
            <Ltr>+{link.data.phone}</Ltr>
          </span>
        ) : null}
        {link.data.linked_at ? (
          <span className="text-grid-muted">
            · {t("whatsapp.linkedAt", { date: formatDate(link.data.linked_at) })}
          </span>
        ) : null}
        <span
          className="text-grid-muted"
          data-whatsapp-sends={`${link.data.sent_today}/${link.data.failed_today}`}
        >
          ·{" "}
          {t("whatsapp.sendsToday", { sent: link.data.sent_today, failed: link.data.failed_today })}
        </span>
        {state === "open" ? (
          <Link
            className="text-grid-brand underline underline-offset-4"
            href={`/${locale}/w/${ws.slug}/contacts/import?source=whatsapp`}
            data-nav="import-chats"
          >
            {t("whatsapp.importChats")}
          </Link>
        ) : null}
      </div>

      {link.data.last_error ? (
        <p
          className="mb-4 flex items-start gap-2 border border-grid-danger-text/40 bg-grid-danger-text/5 px-4 py-3 text-sm"
          role="status"
          data-whatsapp-error
        >
          <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-danger-text" />
          <span>{link.data.last_error}</span>
        </p>
      ) : null}

      {link.data.linked && hook ? (
        <div
          className="mb-5 flex items-start gap-3 border border-line bg-grid-card px-4 py-3 text-sm"
          data-webhook-status={hook.status}
        >
          <WebhookIcon className="mt-0.5 size-4 shrink-0 text-grid-muted" />
          <div className="min-w-0 flex-1">
            <p className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{t("whatsapp.webhook.title")}</span>
              <Badge
                variant={
                  hookOk ? "default" : hook.status === "unknown" ? "secondary" : "destructive"
                }
              >
                {t(`whatsapp.webhook.${hook.status}`)}
              </Badge>
            </p>
            <p className="text-grid-muted">
              {hookOk ? t("whatsapp.webhook.okHint") : t(`whatsapp.webhook.${hook.status}Hint`)}{" "}
              {hook.last_delivery_at
                ? t("whatsapp.webhook.lastDelivery", { date: formatDate(hook.last_delivery_at) })
                : t("whatsapp.webhook.noDelivery")}
            </p>
          </div>
        </div>
      ) : null}

      {qr ? (
        <div className="mb-4 flex flex-col items-start gap-2">
          <p className="text-sm">{t("whatsapp.scanHint")}</p>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={qr}
            alt={t("whatsapp.qrAlt")}
            width={256}
            height={256}
            className="border border-line bg-white p-2"
            data-whatsapp-qr
          />
          <p className="text-xs text-grid-muted">
            {t("whatsapp.scanExpires")} {t("whatsapp.watching")}
          </p>
        </div>
      ) : null}

      {pairing ? (
        <div className="mb-4 flex flex-col items-start gap-2" data-whatsapp-pairing>
          <p className="text-sm">{t("whatsapp.pairingHint")}</p>
          <div data-pairing-code={pairing}>
            <Ltr
              mono
              className="inline-block border border-line bg-grid-card px-4 py-2 text-2xl tracking-[0.3em]"
            >
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
                  <Input
                    id="wa-link-number"
                    dir="ltr"
                    inputMode="tel"
                    value={number}
                    onChange={(e) => setNumber(e.target.value)}
                    placeholder="+20 100 000 0000"
                  />
                </Field>
                <Button type="submit" disabled={busy || number.replace(/\D/g, "").length < 8}>
                  <KeyRoundIcon />
                  {busy ? t("common.working") : t("whatsapp.getPairingCode")}
                </Button>
              </FieldGroup>
              <FieldDescription className="mt-2">
                {t("whatsapp.pairingNumberHint")}
              </FieldDescription>
            </form>
          ) : null}
          <div className="flex flex-wrap items-center gap-2">
            {!byNumber ? (
              <Button onClick={() => void startLink()} disabled={busy}>
                <QrCodeIcon />
                {busy
                  ? t("common.working")
                  : state === "open"
                    ? t("whatsapp.relink")
                    : t("whatsapp.link")}
              </Button>
            ) : null}
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setByNumber((v) => !v)}
              data-toggle-pairing
            >
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

      {state !== "open" ? (
        <p className="pt-4 text-sm text-grid-muted">{t("whatsapp.replyHint")}</p>
      ) : null}
    </div>
  )
}
