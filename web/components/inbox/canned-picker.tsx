"use client"

import Link from "next/link"
import { ChevronDownIcon, MessageSquareQuoteIcon } from "lucide-react"

import { Bdi } from "@/components/bidi"
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger } from "@fadymondy/nasaq/web"
import { useWorkspace } from "@/components/workspace/workspace-context"
import type { CannedReply } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCannedReplies } from "@/lib/queries"

/** The text a canned reply inserts for a conversation in `lang`, falling back to the other variant. */
export function cannedText(r: CannedReply, lang: "en" | "ar"): string {
  return lang === "ar" ? r.body_ar || r.body_en : r.body_en || r.body_ar
}

/**
 * Canned replies (MH-383) in the reply box. Picking one INSERTS it — nothing
 * is sent until Send — in the CONVERSATION's language (reply_language from the
 * API), not the member's UI language: reading the inbox in English, you still
 * answer an Arabic customer in Arabic.
 */
export function CannedPicker({
  lang,
  onInsert,
  open,
  onOpenChange,
}: {
  lang: "en" | "ar"
  onInsert: (text: string) => void
  /** Controlled open state — the composer opens this with "/" (MH-587). Omit for the default uncontrolled dropdown. */
  open?: boolean
  onOpenChange?: (open: boolean) => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const canned = useCannedReplies(ws.id)
  const replies = canned.data?.replies ?? []
  const language = t(`inbox.canned.language.${lang}`)
  const other = t(`inbox.canned.language.${lang === "ar" ? "en" : "ar"}`)

  return (
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger
        render={
          <Button
            type="button"
            variant="ghost"
            size="sm"
            data-action="canned"
            aria-label={t("inbox.canned.insert")}
            title={t("inbox.canned.insert")}
            data-quick-replies
            disabled={!canned.data}
          />
        }
      >
        <MessageSquareQuoteIcon />
        <span className="hidden @xl:inline">{t("inbox.canned.insert")}</span>
        <ChevronDownIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-80 max-w-[calc(100vw-2rem)]">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="whitespace-normal" data-canned-language={lang}>
            {t("inbox.canned.inLanguage", { language })}
          </DropdownMenuLabel>
          {replies.length === 0 ? (
            <p className="px-2 py-1.5 text-sm text-muted-foreground">{t("inbox.canned.none")}</p>
          ) : (
            replies.map((r) => {
              const text = cannedText(r, lang)
              const fallback = lang === "ar" ? !r.body_ar : !r.body_en
              return (
                <DropdownMenuItem
                  key={r.id}
                  data-canned={r.title}
                  onClick={() => onInsert(text)}
                  className="flex-col items-start gap-0.5"
                >
                  <Bdi className="font-medium">{r.title}</Bdi>
                  <span
                    dir="auto"
                    className="line-clamp-2 w-full text-start text-xs text-muted-foreground"
                  >
                    {text}
                  </span>
                  {fallback ? (
                    <span className="text-xs text-nq-warning-text">
                      {t("inbox.canned.fallback", { language, other })}
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
            <DropdownMenuItem render={<Link href={`/${locale}/w/${ws.slug}/inbox/canned`} />}>
              {t("inbox.canned.manage")}
            </DropdownMenuItem>
          </>
        ) : null}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
