"use client"

import Link from "next/link"
import { useState } from "react"
import { FlaskConicalIcon, MailWarningIcon } from "lucide-react"
import { Button, Field, FieldDescription, FieldError, FieldLabel, Input, toast } from "@fadymondy/nasaq/web"

import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Campaign } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useMe } from "@/lib/queries"

type TestResult = { channel: "email" | "whatsapp"; to: string; subject?: string }

/*
Send one copy of the SAVED campaign to yourself (POST /api/w/campaigns/{id}/test).
It is not a send: the campaign's status and counters do not move. Email goes to
the address in the box (your own, prefilled) with "[TEST]" on the subject;
WhatsApp goes only to the workspace's own linked number.

Unsaved edits are not what the test would send, so the box says so and waits
for Save rather than mailing the previous version.

Not a <form>: it sits inside the campaign form, and a nested form submits the
outer one (a native GET that reloads the page).
*/
export function CampaignTestSend({
  campaign,
  dirty,
  smtpReady,
  whatsappLinked,
}: {
  campaign: Campaign
  dirty: boolean
  smtpReady: boolean
  whatsappLinked: boolean
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const me = useMe()
  const wa = campaign.channel === "whatsapp"
  // Prefilled with the member's own address; `null` until they type.
  const [typed, setTyped] = useState<string | null>(null)
  const to = typed ?? me.data?.email ?? ""
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const [done, setDone] = useState<TestResult | null>(null)

  const disabled = busy || dirty || (!wa && !to.trim())

  async function send() {
    if (disabled) return
    setBusy(true)
    setErr(null)
    setDone(null)
    try {
      const res = await api<TestResult>(
        `/api/w/campaigns/${encodeURIComponent(campaign.id)}/test`,
        {
          json: wa ? {} : { to: to.trim() },
          workspaceId: ws.id,
        },
      )
      setDone(res)
      toast.success(t("campaigns.testSent", { to: res.to }))
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  // The form above already says how to link one; this only says why there is no button.
  if (wa && !whatsappLinked) {
    return (
      <p className="text-sm text-muted-foreground" role="status" data-test-blocked="whatsapp">
        {t("campaigns.testNeedsWhatsApp")}
      </p>
    )
  }
  if (!wa && !smtpReady) {
    return (
      <p
        className="flex items-start gap-2 text-sm text-muted-foreground"
        role="status"
        data-test-blocked="smtp"
      >
        <MailWarningIcon className="mt-0.5 size-4 shrink-0 text-nq-warning" />
        {ws.can("mail:settings") ? (
          <span>
            {t("campaigns.testNeedsSmtp")}{" "}
            <Link
              className="underline underline-offset-4"
              href={`/${locale}/w/${ws.slug}/settings/email`}
            >
              {t("contacts.smtpLink")}
            </Link>
          </span>
        ) : (
          <span>{t("campaigns.smtpMissingMember")}</span>
        )}
      </p>
    )
  }

  return (
    <div className="flex flex-col gap-3" data-form="campaign-test">
      {wa ? (
        <p className="text-sm text-muted-foreground">{t("campaigns.testWhatsAppHint")}</p>
      ) : (
        <Field>
          <FieldLabel htmlFor="campaign-test-to">{t("campaigns.testTo")}</FieldLabel>
          <Input
            id="campaign-test-to"
            type="email"
            dir="ltr"
            value={to}
            onChange={(e) => setTyped(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault()
                void send()
              }
            }}
            maxLength={254}
          />
          <FieldDescription>{t("campaigns.testEmailHint")}</FieldDescription>
        </Field>
      )}
      {dirty ? (
        <p className="text-sm text-nq-warning-text" role="status" data-test-blocked="unsaved">
          {t("campaigns.testUnsaved")}
        </p>
      ) : null}
      <div className="flex flex-wrap items-center gap-3">
        <Button type="button" variant="secondary" disabled={disabled} onClick={send} data-send-test>
          <FlaskConicalIcon />
          {busy ? t("common.working") : t("campaigns.sendTest")}
        </Button>
        {done ? (
          <span className="text-sm text-nq-success-text" data-test-result={done.to}>
            {t("campaigns.testSentTo")} <Ltr>{done.to}</Ltr>
          </span>
        ) : null}
      </div>
      <p className="text-xs text-muted-foreground">{t("campaigns.testNotASend")}</p>
      {err ? <FieldError>{err}</FieldError> : null}
    </div>
  )
}
