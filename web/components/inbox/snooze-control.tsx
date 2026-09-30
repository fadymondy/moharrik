"use client"

import { useMemo } from "react"
import { AlarmClockIcon, AlarmClockOffIcon, ChevronDownIcon } from "lucide-react"
import { toast } from "@fadymondy/nasaq/web"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Conversation } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag, isLocale } from "@/lib/i18n-locale"

type Preset = { key: "hour" | "later" | "tomorrow" | "nextWeek"; at: Date }

/** The snooze presets, in the member's own time zone (the browser's). */
export function snoozePresets(now = new Date()): Preset[] {
  const hour = new Date(now.getTime() + 60 * 60 * 1000)
  const later = new Date(now.getTime() + 3 * 60 * 60 * 1000)
  const tomorrow = new Date(now)
  tomorrow.setDate(now.getDate() + 1)
  tomorrow.setHours(9, 0, 0, 0)
  // Next Monday, 9:00 — a week ahead when today is Monday.
  const nextWeek = new Date(now)
  nextWeek.setDate(now.getDate() + ((8 - now.getDay()) % 7 || 7))
  nextWeek.setHours(9, 0, 0, 0)
  return [
    { key: "hour", at: hour },
    { key: "later", at: later },
    { key: "tomorrow", at: tomorrow },
    { key: "nextWeek", at: nextWeek },
  ]
}

export function useDateTime() {
  const { locale } = useTranslations()
  return useMemo(() => {
    const full = new Intl.DateTimeFormat(intlTag(isLocale(locale) ? locale : "en"), {
      dateStyle: "medium",
      timeStyle: "short",
    })
    const short = new Intl.DateTimeFormat(intlTag(isLocale(locale) ? locale : "en"), {
      weekday: "short",
      hour: "numeric",
      minute: "2-digit",
    })
    return {
      full: (d: Date | string) => full.format(new Date(d)),
      short: (d: Date) => short.format(d),
    }
  }, [locale])
}

/**
 * Snooze (MH-383): presets in a menu, and — once snoozed — when it wakes and
 * the button that wakes it now. The API reopens it, unread, at that time, or
 * sooner if the customer writes.
 */
export function SnoozeControl({ c, onChanged }: { c: Conversation; onChanged: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const fmt = useDateTime()

  async function patch(json: Record<string, unknown>, done: string) {
    try {
      await api(`/api/w/conversations/${encodeURIComponent(c.id)}`, {
        method: "PATCH",
        json,
        workspaceId: ws.id,
      })
      toast.success(done)
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  if (c.status === "snoozed") {
    // When it wakes is shown in the thread's title column (SnoozedNote); the
    // controls row keeps only the button, so it does not squeeze the subject.
    return (
      <Button
        variant="outline"
        size="sm"
        onClick={() => patch({ snoozed_until: "" }, t("inbox.unsnoozed"))}
        data-action="unsnooze"
      >
        <AlarmClockOffIcon />
        {t("inbox.unsnooze")}
      </Button>
    )
  }
  if (c.status === "closed") return null

  return (
    <DropdownMenu>
      <DropdownMenuTrigger render={<Button variant="outline" size="sm" data-action="snooze" />}>
        <AlarmClockIcon />
        {t("inbox.snooze")}
        <ChevronDownIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-64">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{t("inbox.snoozeFor")}</DropdownMenuLabel>
          {snoozePresets().map((p) => (
            <DropdownMenuItem
              key={p.key}
              data-snooze-preset={p.key}
              onClick={() =>
                patch(
                  { snoozed_until: p.at.toISOString().replace(/\.\d{3}Z$/, "Z") },
                  t("inbox.snoozed", { when: fmt.full(p.at) }),
                )
              }
            >
              <span className="flex-1">{t(`inbox.snooze.${p.key}`)}</span>
              <span className="text-xs text-muted-foreground">{fmt.short(p.at)}</span>
            </DropdownMenuItem>
          ))}
        </DropdownMenuGroup>
        <p className="max-w-64 px-2 py-1.5 text-xs text-muted-foreground">{t("inbox.snoozeHint")}</p>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/** "Snoozed until …" for a snoozed thread's title column; nothing otherwise. */
export function SnoozedNote({ c }: { c: Conversation }) {
  const { t } = useTranslations()
  const fmt = useDateTime()
  if (c.status !== "snoozed") return null
  return (
    <p
      className="flex items-center gap-1 text-xs text-muted-foreground [&_svg]:size-3.5"
      data-snoozed-until={c.snoozed_until ?? ""}
    >
      <AlarmClockIcon aria-hidden />
      {c.snoozed_until
        ? t("inbox.snoozedUntil", { when: fmt.full(c.snoozed_until) })
        : t("inbox.snoozedOpenEnded")}
    </p>
  )
}
