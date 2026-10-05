"use client"

import { useState } from "react"
import Link from "next/link"
import { ChevronDownIcon, MailIcon } from "lucide-react"
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger, toast } from "@fadymondy/nasaq/web"

import { Bdi } from "@/components/bidi"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useEmailTemplates } from "@/lib/queries"

/**
 * Email templates (MH-661) in the reply box — canned replies' bilingual,
 * subject-carrying sibling, shown only on an email conversation. Picking one
 * calls the render endpoint for THIS conversation's contact (falling back to
 * sample data when there is none linked) and fills both the subject and the
 * body — nothing is sent until Send. Unknown {{...}} placeholders come back
 * exactly as written, never blanked, so a typo in a template stays visible.
 */
export function EmailTemplatePicker({
  conversationId,
  lang,
  onInsert,
}: {
  conversationId: string
  lang: "en" | "ar"
  onInsert: (result: { subject: string; body: string }) => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const templates = useEmailTemplates(ws.id)
  const list = templates.data?.templates ?? []
  const [renderingId, setRenderingId] = useState<string | null>(null)
  const language = t(`inbox.canned.language.${lang}`)

  async function pick(id: string) {
    setRenderingId(id)
    try {
      const res = await api<{ subject: string; body: string }>(
        `/api/w/email_templates/${encodeURIComponent(id)}/render`,
        { json: { conversation_id: conversationId }, workspaceId: ws.id },
      )
      onInsert(res)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setRenderingId(null)
    }
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            type="button"
            variant="ghost"
            size="sm"
            data-action="email-templates"
            aria-label={t("inbox.emailTemplates.insert")}
            title={t("inbox.emailTemplates.insert")}
            disabled={!templates.data}
          />
        }
      >
        <MailIcon />
        <span className="hidden @xl:inline">{t("inbox.emailTemplates.insert")}</span>
        <ChevronDownIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-80 max-w-[calc(100vw-2rem)]">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="whitespace-normal" data-email-template-language={lang}>
            {t("inbox.emailTemplates.inLanguage", { language })}
          </DropdownMenuLabel>
          {list.length === 0 ? (
            <p className="px-2 py-1.5 text-sm text-muted-foreground">{t("inbox.emailTemplates.none")}</p>
          ) : (
            list.map((tpl) => {
              const subject = lang === "ar" ? tpl.subject_ar || tpl.subject_en : tpl.subject_en || tpl.subject_ar
              const fallback = lang === "ar" ? !tpl.body_ar : !tpl.body_en
              return (
                <DropdownMenuItem
                  key={tpl.id}
                  data-email-template={tpl.name}
                  onClick={() => pick(tpl.id)}
                  disabled={renderingId !== null}
                  className="flex-col items-start gap-0.5"
                >
                  <Bdi className="font-medium">{tpl.name}</Bdi>
                  {subject ? (
                    <span dir="auto" className="line-clamp-1 w-full text-start text-xs text-muted-foreground">
                      {subject}
                    </span>
                  ) : null}
                  {fallback ? (
                    <span className="text-xs text-nq-warning-text">
                      {t("inbox.emailTemplates.fallback", {
                        language,
                        other: t(`inbox.canned.language.${lang === "ar" ? "en" : "ar"}`),
                      })}
                    </span>
                  ) : null}
                </DropdownMenuItem>
              )
            })
          )}
        </DropdownMenuGroup>
        {ws.can("inbox:manage") ? (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem render={<Link href={`/${locale}/w/${ws.slug}/settings/email-templates`} />}>
              {t("inbox.emailTemplates.manage")}
            </DropdownMenuItem>
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
