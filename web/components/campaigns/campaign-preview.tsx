"use client"

import { useEffect, useState } from "react"

import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type CampaignChannel } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

export type CampaignPreviewData = {
  channel: CampaignChannel
  locale: "en" | "ar"
  dir: "ltr" | "rtl"
  subject: string
  html?: string
  text: string
}

/*
What recipients get, rendered by the API through the send path itself
(POST /api/w/campaigns/preview → marketing.RenderPreview), so this pane never
drifts from the real email. Works on the unsaved form: it previews what you are
typing, not what was last saved.

The language switch IS the campaign's language — the same state as the form's
Language field — because a campaign goes out in one language, and a preview of
a version nobody receives would be a preview of nothing.
*/
export function CampaignPreview({
  channel,
  subject,
  body,
  lang,
  onLangChange,
}: {
  channel: CampaignChannel
  subject: string
  body: string
  lang: "en" | "ar"
  onLangChange: (l: "en" | "ar") => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [data, setData] = useState<CampaignPreviewData | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    const ctl = new AbortController()
    // A short pause so typing in the other tab does not fire a request per key.
    const timer = setTimeout(() => {
      api<CampaignPreviewData>("/api/w/campaigns/preview", {
        json: { channel, subject, body, locale: lang },
        workspaceId: ws.id,
        signal: ctl.signal,
      })
        .then((d) => {
          setData(d)
          setErr(null)
        })
        .catch((e) => {
          if (ctl.signal.aborted) return
          setErr(e instanceof ApiError ? e.message : t("common.networkError"))
        })
    }, 250)
    return () => {
      clearTimeout(timer)
      ctl.abort()
    }
  }, [channel, subject, body, lang, ws.id, t])

  const wa = channel === "whatsapp"

  return (
    <div
      className="flex flex-col gap-3"
      data-campaign-preview={data ? `${data.channel}:${data.locale}` : ""}
    >
      <div className="flex flex-wrap items-center gap-3">
        <span className="text-sm text-muted-foreground">{t("campaigns.previewLanguage")}</span>
        <ToggleGroup
          variant="outline"
          size="sm"
          spacing={0}
          value={[lang]}
          onValueChange={(v) => {
            const next = v[0]
            if (next === "en" || next === "ar") onLangChange(next)
          }}
          aria-label={t("campaigns.previewLanguage")}
        >
          <ToggleGroupItem value="en" data-preview-lang="en">
            {t("lang.en")}
          </ToggleGroupItem>
          <ToggleGroupItem value="ar" data-preview-lang="ar">
            {t("lang.ar")}
          </ToggleGroupItem>
        </ToggleGroup>
        <span className="text-xs text-muted-foreground">
          {wa ? t("campaigns.previewWhatsAppHint") : t("campaigns.previewEmailHint")}
        </span>
      </div>

      {err ? (
        <p className="text-sm text-nq-danger-text" role="alert">
          {err}
        </p>
      ) : !body.trim() ? (
        <p className="border border-dashed border-nq-line px-4 py-8 text-center text-sm text-muted-foreground">
          {t("campaigns.previewEmpty")}
        </p>
      ) : !data ? (
        <p className="px-4 py-8 text-center text-sm text-muted-foreground">{t("common.working")}</p>
      ) : data.channel === "whatsapp" ? (
        <div className="border border-nq-line bg-nq-surface-soft px-4 py-6" data-whatsapp-preview>
          <div
            dir={data.dir}
            className="max-w-sm whitespace-pre-wrap rounded-lg rounded-ss-none border border-nq-line bg-card px-3 py-2 text-sm text-foreground shadow-sm [overflow-wrap:anywhere]"
            data-whatsapp-bubble
          >
            {data.text}
          </div>
        </div>
      ) : (
        <div className="flex flex-col border border-nq-line">
          <div className="border-b border-nq-line px-3 py-2 text-sm" dir={data.dir}>
            <span className="text-muted-foreground">{t("campaigns.subject")}: </span>
            <span className="font-medium" data-preview-subject>
              {data.subject}
            </span>
          </div>
          {/* srcDoc in a sandbox with no permissions: the email is a whole document with
              its own styles, and this keeps them (and any link in it) out of the app. */}
          <iframe
            title={t("campaigns.preview")}
            srcDoc={data.html}
            sandbox=""
            className="h-[28rem] w-full"
            data-email-preview
          />
        </div>
      )}
    </div>
  )
}
