"use client"

import Link from "next/link"
import { useState } from "react"
import { BellIcon, BellRingIcon, CheckIcon, MailIcon, MessageCircleIcon, MessageSquareIcon, SettingsIcon } from "lucide-react"
import { toast } from "sonner"

import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useNotificationPermission } from "@/lib/alerts"
import { api, ApiError, type BellItem } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useBell } from "@/lib/queries"
import { cn } from "@/lib/utils"

/*
The header bell (MH-378): unread conversations plus unread notification items,
the latest of both, a way to mark them read, and a link to each.

Ported from the @shadcnspace/popover-03 registry item (notification list
popover: unread badge, All/Unread tabs, mark-all-read), wired to /api/w/alerts
and kept live by the workspace event stream — a customer's message or a webhook
item moves the badge in every open tab without a reload.

The API decides what is in it: a member who cannot read the inbox gets no
conversations, one who cannot read notifications gets no items, and a client
gets neither.
*/

const REFRESH_KINDS = new Set(["message", "conversation", "notification", "notifications"])

export function hrefFor(item: Pick<BellItem, "kind" | "id">, locale: string, slug: string) {
  return item.kind === "conversation"
    ? `/${locale}/w/${slug}/inbox?c=${encodeURIComponent(item.id)}`
    : `/${locale}/w/${slug}/notifications`
}

function initials(name: string) {
  const parts = name.trim().split(/\s+/).filter(Boolean)
  return (parts.length > 1 ? parts[0][0] + parts[1][0] : (parts[0] ?? "?").slice(0, 2)).toUpperCase()
}

function useRelative() {
  const { locale } = useTranslations()
  return (iso: string) => {
    const s = Math.round((new Date(iso).getTime() - Date.now()) / 1000)
    const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto", style: "short" })
    const abs = Math.abs(s)
    if (abs < 60) return rtf.format(s, "second")
    if (abs < 3600) return rtf.format(Math.round(s / 60), "minute")
    if (abs < 86400) return rtf.format(Math.round(s / 3600), "hour")
    return rtf.format(Math.round(s / 86400), "day")
  }
}

const severityTone: Record<string, string> = { info: "bg-grid-brand", warning: "bg-grid-warn", error: "bg-grid-danger" }

function ItemIcon({ item }: { item: BellItem }) {
  if (item.kind === "notification") return <BellIcon />
  if (item.channel === "email") return <MailIcon />
  if (item.channel === "whatsapp") return <MessageSquareIcon />
  return <MessageCircleIcon />
}

function Row({
  item,
  href,
  userId,
  canMark,
  onOpen,
  onMark,
}: {
  item: BellItem
  href: string
  userId?: string
  canMark: boolean
  onOpen: () => void
  onMark: () => void
}) {
  const { t } = useTranslations()
  const relative = useRelative()
  const name = item.title || (item.kind === "conversation" ? t("alerts.visitor") : t("alerts.notification"))
  return (
    <li
      data-bell-item={item.kind}
      data-bell-unread={item.unread ? "true" : "false"}
      className={cn("group relative flex gap-3 px-4 py-3 transition-colors hover:bg-grid-soft", item.unread && "bg-grid-card")}
    >
      <div className="relative h-fit shrink-0">
        <Avatar className="size-9">
          <AvatarFallback className="text-xs">{item.kind === "conversation" ? initials(name) : <BellIcon className="size-4" />}</AvatarFallback>
        </Avatar>
        <span
          className={cn(
            "absolute -bottom-0.5 -end-0.5 flex size-4 items-center justify-center rounded-full text-white [&_svg]:size-2.5",
            item.kind === "notification" ? (severityTone[item.severity ?? "info"] ?? "bg-grid-brand") : "bg-grid-action",
          )}
          aria-hidden
        >
          <ItemIcon item={item} />
        </span>
      </div>
      <Link href={href} onClick={onOpen} className="min-w-0 flex-1 after:absolute after:inset-0">
        <p className="bidi-block truncate text-sm leading-snug font-medium" dir="auto">
          {name}
        </p>
        {item.snippet ? (
          <p className="bidi-block mt-0.5 truncate text-xs text-grid-muted" dir="auto">
            {item.snippet}
          </p>
        ) : null}
        <p className="mt-1 text-xs text-grid-muted">
          {item.kind === "conversation" && item.channel ? `${t(`inbox.channel.${item.channel}`)} · ` : ""}
          {relative(item.at)}
          {item.assignee_user_id && item.assignee_user_id === userId ? ` · ${t("alerts.assignedToMe")}` : ""}
        </p>
      </Link>
      {item.unread ? (
        canMark ? (
          <Button
            variant="ghost"
            size="icon-xs"
            className="relative z-10 shrink-0 self-center"
            aria-label={t("alerts.markRead")}
            title={t("alerts.markRead")}
            onClick={onMark}
            data-bell-mark
          >
            <CheckIcon />
          </Button>
        ) : (
          <span className="mt-1.5 size-2 shrink-0 rounded-full bg-grid-action" aria-label={t("inbox.unread")} />
        )
      ) : null}
    </li>
  )
}

export function NotificationBell({
  workspaceId,
  slug,
  userId,
  can,
}: {
  workspaceId: string
  slug: string
  userId?: string
  can: (p: string) => boolean
}) {
  const { t, locale } = useTranslations()
  const bell = useBell(workspaceId)
  const [open, setOpen] = useState(false)
  const [perm, requestPerm] = useNotificationPermission()

  useWorkspaceEvents(workspaceId, (e) => {
    if (REFRESH_KINDS.has(e.kind)) void bell.mutate()
  })

  const data = bell.data
  const items = data?.items ?? []
  const unreadItems = items.filter((i) => i.unread)
  const count = (data?.unread_conversations ?? 0) + (data?.unread_notifications ?? 0)
  const canMarkConv = can("inbox:write")
  const canMarkNote = can("notifications:write")
  const nothing = data && !data.can_inbox && !data.can_notifications

  async function mark(item: BellItem) {
    try {
      if (item.kind === "conversation") {
        await api(`/api/w/conversations/${encodeURIComponent(item.id)}`, { method: "PATCH", json: { unread: false }, workspaceId })
      } else {
        await api(`/api/w/notifications/${encodeURIComponent(item.id)}`, { method: "PATCH", json: { read: true }, workspaceId })
      }
      void bell.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function markAll() {
    try {
      await api("/api/w/alerts/mark_all_read", { method: "POST", workspaceId })
      toast.success(t("alerts.markedAllRead"))
      void bell.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const list = (rows: BellItem[], empty: string) =>
    rows.length ? (
      <ul className="divide-y divide-line">
        {rows.map((item) => (
          <Row
            key={`${item.kind}-${item.id}`}
            item={item}
            href={hrefFor(item, locale, slug)}
            userId={userId}
            canMark={item.kind === "conversation" ? canMarkConv : canMarkNote}
            onOpen={() => setOpen(false)}
            onMark={() => void mark(item)}
          />
        ))}
      </ul>
    ) : (
      <div className="flex h-64 flex-col items-center justify-center gap-2 px-6 text-center">
        <BellIcon className="size-8 text-grid-muted/50" />
        <p className="text-sm text-grid-muted">{empty}</p>
      </div>
    )

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            className="relative"
            aria-label={count ? t("alerts.bellUnread", { n: count }) : t("alerts.bell")}
            title={t("alerts.bell")}
            data-bell
          />
        }
      >
        <BellIcon />
        {count > 0 ? (
          <Badge
            className="pointer-events-none absolute -top-1 -end-1 h-4.5 min-w-4.5 rounded-full px-1 text-xs leading-none tabular-nums"
            data-bell-count={count}
          >
            {count > 99 ? "99+" : count}
          </Badge>
        ) : null}
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[22rem] max-w-[calc(100vw-1rem)] gap-0 p-0" data-bell-panel>
        <div className="flex items-center justify-between gap-2 border-b border-line px-4 py-3">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium">{t("alerts.bell")}</span>
            {count > 0 ? (
              <Badge variant="secondary" className="px-1.5 py-0 text-xs">
                {t("alerts.new", { n: count })}
              </Badge>
            ) : null}
          </div>
          {count > 0 && (canMarkConv || canMarkNote) ? (
            <Button variant="ghost" size="xs" onClick={markAll} className="text-grid-muted hover:text-grid-fg" data-bell-mark-all>
              {t("alerts.markAllRead")}
            </Button>
          ) : null}
        </div>

        {nothing ? (
          <p className="px-4 py-8 text-center text-sm text-grid-muted">{t("alerts.nothingToRead")}</p>
        ) : (
          <Tabs defaultValue="all" className="gap-0">
            <div className="border-b border-line px-4 py-2">
              <TabsList className="h-8 w-full">
                <TabsTrigger value="all" className="flex-1 text-xs">
                  {t("alerts.tabAll")}
                </TabsTrigger>
                <TabsTrigger value="unread" className="flex-1 text-xs">
                  {t("alerts.tabUnread")}
                  {unreadItems.length ? <span className="text-xs">({unreadItems.length})</span> : null}
                </TabsTrigger>
              </TabsList>
            </div>
            <TabsContent value="all" className="mt-0">
              <ScrollArea className="h-72">{list(items, t("alerts.empty"))}</ScrollArea>
            </TabsContent>
            <TabsContent value="unread" className="mt-0">
              <ScrollArea className="h-72">{list(unreadItems, t("alerts.caughtUp"))}</ScrollArea>
            </TabsContent>
          </Tabs>
        )}

        {perm === "default" ? (
          <div className="border-t border-line px-4 py-2">
            {/* A click, never on load: see useNotificationPermission. */}
            <Button variant="outline" size="sm" className="w-full" onClick={() => void requestPerm()} data-allow-desktop>
              <BellRingIcon />
              {t("alerts.enableDesktop")}
            </Button>
          </div>
        ) : null}

        <div className="flex flex-wrap items-center gap-1 border-t border-line px-2 py-2">
          {data?.can_inbox ? (
            <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${slug}/inbox`} onClick={() => setOpen(false)} />}>
              {t("alerts.openInbox")}
            </Button>
          ) : null}
          {data?.can_notifications ? (
            <Button
              variant="ghost"
              size="sm"
              nativeButton={false}
              render={<Link href={`/${locale}/w/${slug}/notifications`} onClick={() => setOpen(false)} />}
            >
              {t("alerts.allNotifications")}
            </Button>
          ) : null}
          {can("push:manage") ? (
            <Button
              variant="ghost"
              size="icon-sm"
              className="ms-auto"
              aria-label={t("alerts.delivery")}
              title={t("alerts.delivery")}
              nativeButton={false}
              render={<Link href={`/${locale}/w/${slug}/settings/delivery`} onClick={() => setOpen(false)} />}
            >
              <SettingsIcon />
            </Button>
          ) : null}
        </div>
      </PopoverContent>
    </Popover>
  )
}
