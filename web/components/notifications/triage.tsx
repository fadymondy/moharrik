"use client"

/*
Resolve and snooze (MH-1069). Read is "I have seen it"; resolved is "it is
handled"; snoozed is "ask me again later". The feed's default view and every
count show only what is still actionable: open and not snoozed. A snooze needs
no job to end, the API compares its time at read time.
*/

import { AlarmClockIcon, AlarmClockOffIcon, CircleCheckIcon, UndoIcon } from "lucide-react"
import {
  Badge,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
  toast,
} from "@fadymondy/nasaq/web"

import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type NotificationItem } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

const HOUR = 60 * 60 * 1000

/** The snooze choices, each resolved to a concrete time when it is picked. */
const PRESETS = [
  { key: "hour", until: (now: Date) => new Date(now.getTime() + HOUR) },
  { key: "threeHours", until: (now: Date) => new Date(now.getTime() + 3 * HOUR) },
  {
    key: "tomorrow",
    until: (now: Date) => {
      const d = new Date(now)
      d.setDate(d.getDate() + 1)
      d.setHours(9, 0, 0, 0)
      return d
    },
  },
  { key: "week", until: (now: Date) => new Date(now.getTime() + 7 * 24 * HOUR) },
] as const

/** The state badge a row or the detail shows for a snoozed or resolved item. */
export function StateBadge({ item }: { item: NotificationItem }) {
  const { t, formatDate } = useTranslations()
  if (item.state === "resolved")
    return (
      <Badge variant="outline" className="text-nq-success-text" data-state="resolved">
        <CircleCheckIcon />
        {t("ntf.state.resolved")}
      </Badge>
    )
  if (item.state === "snoozed" && item.snoozed_until)
    return (
      <Badge variant="warning" data-state="snoozed">
        <AlarmClockIcon />
        {t("ntf.state.snoozedUntil", { date: formatDate(item.snoozed_until) })}
      </Badge>
    )
  return null
}

export function TriageActions({
  item,
  onChange,
  size = "icon-sm",
}: {
  item: NotificationItem
  onChange: () => void
  /** "icon-sm" in a row; "sm" in the detail sheet, where the labels are shown. */
  size?: "icon-sm" | "sm"
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const labelled = size === "sm"
  const base = `/api/w/notifications/${encodeURIComponent(item.id)}`

  async function post(path: string, body: unknown, done: string) {
    try {
      await api(`${base}/${path}`, { method: "POST", json: body ?? {}, workspaceId: ws.id })
      toast.success(done)
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const btn = (label: string, icon: React.ReactNode, onClick: () => void, attr: string) => (
    <Button
      variant={labelled ? "secondary" : "ghost"}
      size={size}
      aria-label={label}
      title={label}
      onClick={onClick}
      {...{ [attr]: "" }}
    >
      {icon}
      {labelled ? label : null}
    </Button>
  )

  if (item.state === "resolved")
    return btn(
      t("ntf.reopen"),
      <UndoIcon />,
      () => post("unresolve", {}, t("ntf.reopened")),
      "data-triage-reopen",
    )

  return (
    <>
      {item.state === "snoozed"
        ? btn(
            t("ntf.unsnooze"),
            <AlarmClockOffIcon />,
            () => post("unsnooze", {}, t("ntf.unsnoozed")),
            "data-triage-unsnooze",
          )
        : null}
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant={labelled ? "secondary" : "ghost"}
              size={size}
              aria-label={t("ntf.snooze")}
              title={t("ntf.snooze")}
              data-triage-snooze
            />
          }
        >
          <AlarmClockIcon />
          {labelled ? t("ntf.snooze") : null}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-48">
          <DropdownMenuGroup>
            <DropdownMenuLabel>{t("ntf.snoozeUntil")}</DropdownMenuLabel>
            {PRESETS.map((p) => (
              <DropdownMenuItem
                key={p.key}
                data-snooze-preset={p.key}
                onClick={() =>
                  post(
                    "snooze",
                    { until: p.until(new Date()).toISOString() },
                    t("ntf.snoozed", { when: t(`ntf.preset.${p.key}`) }),
                  )
                }
              >
                {t(`ntf.preset.${p.key}`)}
              </DropdownMenuItem>
            ))}
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {btn(
        t("ntf.resolve"),
        <CircleCheckIcon />,
        () => post("resolve", {}, t("ntf.resolved")),
        "data-triage-resolve",
      )}
    </>
  )
}
