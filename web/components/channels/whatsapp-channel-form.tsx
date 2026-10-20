"use client"

import Link from "next/link"
import { useEffect, useRef, useState, type FormEvent } from "react"
import {
  CheckCircle2Icon,
  KeyRoundIcon,
  QrCodeIcon,
  StarIcon,
  TriangleAlertIcon,
  WebhookIcon,
} from "lucide-react"
import {
  Badge,
  Button,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  toast,
} from "@fadymondy/nasaq/web"
import { FieldGroup, FormHint } from "@/components/form-layout"

import { Bdi } from "@/components/bidi"
import { ConfirmButton } from "@/components/confirm-button"
import { SetupGuide, type SetupGuideStep } from "@/components/channels/setup-guide"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type WhatsAppLink } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWhatsApp, useWhatsAppNumbers } from "@/lib/queries"

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
 * MH-571). A workspace can link several numbers (2026-10): the Channels page
 * lists each as its own row and opens this form in a sheet, either for one
 * number (numberId: re-pair, rename, daily limit, default, unlink) or to add
 * one (adding: the first number, or another next to the ones already linked,
 * which are never touched while it pairs).
 */
export function WhatsAppChannelForm({
  numberId,
  adding,
  onChanged,
  defaultGuideOpen,
}: {
  numberId?: string
  adding?: boolean
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
  const [label, setLabel] = useState("")
  const [busy, setBusy] = useState(false)
  // The number this form is about: the one it was opened for, or the one it
  // just added. Unset while adding, until the first QR comes back.
  const [targetId, setTargetId] = useState<string | undefined>(numberId)
  const waiting = !!qr || !!pairing
  const numbers = useWhatsAppNumbers(ws.id, waiting ? 4000 : 0)
  // Whether this deployment has a WhatsApp server at all (the default-number
  // route says so even when nothing is linked).
  const summary = useWhatsApp(ws.id)
  const all = (numbers.data ?? []).filter((n) => n.id)
  const current = targetId
    ? all.find((n) => n.id === targetId)
    : adding
      ? undefined
      : all.find((n) => n.is_default)
  const state = current?.state || "close"
  // Adding next to numbers already linked creates a new one; the very first
  // number goes through the default route, as it always has.
  const addsAnother = !!adding && !targetId && all.length > 0

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
      const json: Record<string, string> = byNumber ? { number: number.trim() } : {}
      let res: WhatsAppLink
      if (targetId) {
        res = await api<WhatsAppLink>(
          `/api/w/whatsapp/numbers/${encodeURIComponent(targetId)}/link`,
          { method: "POST", json, workspaceId: ws.id },
        )
      } else if (addsAnother) {
        res = await api<WhatsAppLink>("/api/w/whatsapp/numbers", {
          method: "POST",
          json: { ...json, label: label.trim() },
          workspaceId: ws.id,
        })
      } else {
        res = await api<WhatsAppLink>("/api/w/whatsapp/link", {
          method: "POST",
          json,
          workspaceId: ws.id,
        })
      }
      if (res.id) setTargetId(res.id)
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
      void numbers.mutate()
      onChanged?.()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function unlink() {
    if (!current?.id) return
    await api(`/api/w/whatsapp/numbers/${encodeURIComponent(current.id)}`, {
      method: "DELETE",
      workspaceId: ws.id,
    })
    setQr(null)
    setPairing(null)
    toast.success(t("whatsapp.unlinked"))
    void numbers.mutate()
    onChanged?.()
  }

  if (numbers.error) return <ErrorState error={numbers.error} />
  if (summary.error) return <ErrorState error={summary.error} />
  if (!numbers.data || !summary.data) return <LoadingRows rows={3} layout="form" />
  if (!summary.data.available)
    return <EmptyState title={t("whatsapp.unavailable")} body={t("whatsapp.unavailableBody")} />

  const hook = current?.webhook
  const hookOk = hook?.status === "ok"

  return (
    <div
      data-whatsapp-state={state}
      data-whatsapp-polling={waiting ? "on" : "off"}
      data-whatsapp-number={current?.id ?? ""}
    >
      <SetupGuide
        id="whatsapp"
        titleKey="channels.whatsapp.setupTitle"
        steps={WHATSAPP_GUIDE_STEPS}
        defaultOpen={defaultGuideOpen ?? !current?.linked}
      />

      {addsAnother ? (
        <p className="mt-4 text-sm text-muted-foreground" data-whatsapp-adding>
          {t("whatsapp.addNumberHint")}
        </p>
      ) : null}

      {current ? (
        <div className="mt-4 flex flex-wrap items-center gap-2 pb-4 text-sm">
          <Badge
            variant={state === "open" ? "brand" : state === "connecting" ? "neutral" : "outline"}
          >
            {t(`whatsapp.state.${state}`)}
          </Badge>
          {current.is_default ? (
            <Badge variant="info" data-whatsapp-default>
              {t("whatsapp.default")}
            </Badge>
          ) : null}
          {current.label ? (
            <span className="font-medium">
              <Bdi>{current.label}</Bdi>
            </span>
          ) : null}
          {current.phone ? (
            <span className="text-muted-foreground">
              <Ltr>+{current.phone}</Ltr>
            </span>
          ) : null}
          {current.linked_at ? (
            <span className="text-muted-foreground">
              · {t("whatsapp.linkedAt", { date: formatDate(current.linked_at) })}
            </span>
          ) : null}
          <span
            className="text-muted-foreground"
            data-whatsapp-sends={`${current.sent_today}/${current.failed_today}`}
          >
            ·{" "}
            {current.daily_cap > 0
              ? t("whatsapp.capToday", { sent: current.sent_today, cap: current.daily_cap })
              : t("whatsapp.sendsToday", {
                  sent: current.sent_today,
                  failed: current.failed_today,
                })}
          </span>
          {state === "open" ? (
            <Link
              className="text-nq-brand underline underline-offset-4"
              href={`/${locale}/w/${ws.slug}/contacts/import?source=whatsapp`}
              data-nav="import-chats"
            >
              {t("whatsapp.importChats")}
            </Link>
          ) : null}
        </div>
      ) : null}

      {current?.last_error ? (
        <p
          className="mb-4 flex items-start gap-2 border border-nq-danger-text/40 bg-nq-danger-text/5 px-4 py-3 text-sm"
          role="status"
          data-whatsapp-error
        >
          <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-nq-danger-text" />
          <span>{current.last_error}</span>
        </p>
      ) : null}

      {current?.linked && hook ? (
        <div
          className="mb-5 flex items-start gap-3 border border-nq-line bg-card px-4 py-3 text-sm"
          data-webhook-status={hook.status}
        >
          <WebhookIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          <div className="min-w-0 flex-1">
            <p className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{t("whatsapp.webhook.title")}</span>
              <Badge variant={hookOk ? "brand" : hook.status === "unknown" ? "neutral" : "danger"}>
                {t(`whatsapp.webhook.${hook.status}`)}
              </Badge>
            </p>
            <p className="text-muted-foreground">
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
            className="border border-nq-line bg-white p-2"
            data-whatsapp-qr
          />
          <p className="text-xs text-muted-foreground">
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
              className="inline-block border border-nq-line bg-card px-4 py-2 text-2xl tracking-[0.3em]"
            >
              {pairing.length === 8 ? `${pairing.slice(0, 4)}-${pairing.slice(4)}` : pairing}
            </Ltr>
          </div>
          <p className="text-xs text-muted-foreground">{t("whatsapp.watching")}</p>
        </div>
      ) : null}

      {state === "open" && !waiting ? (
        <p className="mb-4 flex items-center gap-2 text-sm" data-whatsapp-linked>
          <CheckCircle2Icon className="size-4 text-nq-brand" />
          {t("whatsapp.replyHint")}
        </p>
      ) : null}

      {canManage ? (
        <div className="flex flex-col gap-4">
          {addsAnother ? (
            <Field className="max-w-xl">
              <FieldLabel htmlFor="wa-number-label">{t("whatsapp.numberLabel")}</FieldLabel>
              <Input
                id="wa-number-label"
                value={label}
                maxLength={60}
                onChange={(e) => setLabel(e.target.value)}
                placeholder={t("whatsapp.numberLabelPlaceholder")}
                data-whatsapp-label
              />
              <FieldDescription>{t("whatsapp.numberLabelHint")}</FieldDescription>
            </Field>
          ) : null}
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
              <FormHint className="mt-2">{t("whatsapp.pairingNumberHint")}</FormHint>
            </form>
          ) : null}
          <div className="flex flex-wrap items-center gap-2">
            {!byNumber ? (
              <Button onClick={() => void startLink()} disabled={busy} data-whatsapp-link>
                <QrCodeIcon />
                {busy
                  ? t("common.working")
                  : current
                    ? t("whatsapp.repair")
                    : addsAnother
                      ? t("whatsapp.addNumber")
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
            {current?.linked ? (
              <ConfirmButton
                label={t("whatsapp.unlink")}
                title={t("whatsapp.unlinkTitle")}
                description={
                  all.length > 1 ? t("whatsapp.unlinkOneBody") : t("whatsapp.unlinkBody")
                }
                confirmLabel={t("whatsapp.unlink")}
                onConfirm={unlink}
              />
            ) : null}
          </div>
          {current?.id ? (
            <NumberSettings
              key={current.id}
              number={current}
              onSaved={() => {
                void numbers.mutate()
                onChanged?.()
              }}
            />
          ) : null}
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">{t("mailbox.managerOnly")}</p>
      )}

      {state !== "open" ? (
        <p className="pt-4 text-sm text-muted-foreground">{t("whatsapp.replyHint")}</p>
      ) : null}
    </div>
  )
}

/** One number's name, daily send limit and whether it is the default. */
function NumberSettings({ number, onSaved }: { number: WhatsAppLink; onSaved: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [label, setLabel] = useState(number.label)
  const [cap, setCap] = useState(String(number.daily_cap || 0))
  const [busy, setBusy] = useState(false)
  const capValue = Number.parseInt(cap, 10)
  const capOk = Number.isFinite(capValue) && capValue >= 0 && capValue <= 100000
  const dirty = label.trim() !== number.label || (capOk && capValue !== number.daily_cap)

  async function patch(json: Record<string, unknown>, done: string) {
    setBusy(true)
    try {
      await api(`/api/w/whatsapp/numbers/${encodeURIComponent(number.id as string)}`, {
        method: "PATCH",
        json,
        workspaceId: ws.id,
      })
      toast.success(done)
      onSaved()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form
      className="flex max-w-xl flex-col gap-3 border-t border-nq-line pt-4"
      data-form="whatsapp-number-settings"
      noValidate
      onSubmit={(e) => {
        e.preventDefault()
        if (!dirty || !capOk) return
        void patch({ label: label.trim(), daily_cap: capValue }, t("whatsapp.settingsSaved"))
      }}
    >
      <Field>
        <FieldLabel htmlFor={`wa-label-${number.id}`}>{t("whatsapp.numberLabel")}</FieldLabel>
        <Input
          id={`wa-label-${number.id}`}
          value={label}
          maxLength={60}
          onChange={(e) => setLabel(e.target.value)}
          placeholder={t("whatsapp.numberLabelPlaceholder")}
        />
        <FieldDescription>{t("whatsapp.numberLabelHint")}</FieldDescription>
      </Field>
      <Field>
        <FieldLabel htmlFor={`wa-cap-${number.id}`}>{t("whatsapp.dailyCap")}</FieldLabel>
        <Input
          id={`wa-cap-${number.id}`}
          dir="ltr"
          type="number"
          inputMode="numeric"
          min={0}
          max={100000}
          value={cap}
          onChange={(e) => setCap(e.target.value)}
          className="max-w-40"
        />
        <FieldDescription>{t("whatsapp.dailyCapHint")}</FieldDescription>
      </Field>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" disabled={busy || !dirty || !capOk}>
          {busy ? t("common.working") : t("common.save")}
        </Button>
        {!number.is_default ? (
          <Button
            type="button"
            variant="secondary"
            disabled={busy}
            onClick={() => void patch({ is_default: true }, t("whatsapp.madeDefault"))}
            data-whatsapp-make-default
          >
            <StarIcon />
            {t("whatsapp.makeDefault")}
          </Button>
        ) : null}
      </div>
    </form>
  )
}
