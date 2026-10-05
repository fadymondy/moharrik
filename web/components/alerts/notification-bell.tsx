"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useState } from "react"
import {
  BellIcon,
  BellRingIcon,
  CheckIcon,
  MailIcon,
  MessageCircleIcon,
  MessageSquareIcon,
  SettingsIcon,
} from "lucide-react"
import {
  Button,
  NotificationCenter,
  toast,
  type ContextMenuAction,
  type NotificationCenterItem,
} from "@fadymondy/nasaq/web"

import { useNotificationPermission } from "@/lib/alerts"
import { api, ApiError, type BellItem } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useBell } from "@/lib/queries"
import { cn } from "@/lib/utils"

/*
The header bell (MH-378): unread conversations plus unread notification items,
the latest of both, a way to mark them read, and a link to each.

Nasaq's NotificationCenter as a side panel (MH-1112): All / Unread tabs, mark
all read, "Mark read" on a row's context menu. Wired to /api/w/alerts and kept
live by the workspace event stream, so a customer's message or a webhook item
moves the badge in every open tab without a reload.

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

const severityTone: Record<string, string> = {
  info: "bg-nq-brand",
  warning: "bg-nq-warning",
  error: "bg-nq-danger",
}

function ChannelIcon({ item }: { item: BellItem }) {
  if (item.kind === "notification") return <BellIcon />
  if (item.channel === "email") return <MailIcon />
  if (item.channel === "whatsapp") return <MessageSquareIcon />
  return <MessageCircleIcon />
}

/** A notification's icon: a bell in a circle, toned by severity. Conversations show the visitor's initials instead. */
function NoteIcon({ item }: { item: BellItem }) {
  return (
    <span
      className={cn(
        "grid size-9 place-items-center rounded-full text-white [&_svg]:size-4",
        severityTone[item.severity ?? "info"] ?? "bg-nq-brand",
      )}
    >
      <ChannelIcon item={item} />
    </span>
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
  const router = useRouter()
  const bell = useBell(workspaceId)
  const [open, setOpen] = useState(false)
  const [perm, requestPerm] = useNotificationPermission()

  useWorkspaceEvents(workspaceId, (e) => {
    if (REFRESH_KINDS.has(e.kind)) void bell.mutate()
  })

  const data = bell.data
  const raw = data?.items ?? []
  const count = (data?.unread_conversations ?? 0) + (data?.unread_notifications ?? 0)
  const canMarkConv = can("inbox:write")
  const canMarkNote = can("notifications:write")
  const nothing = data && !data.can_inbox && !data.can_notifications

  const byKey = new Map(raw.map((i) => [`${i.kind}-${i.id}`, i]))
  const items: NotificationCenterItem[] = raw.map((item) => {
    const name =
      item.title || (item.kind === "conversation" ? t("alerts.visitor") : t("alerts.notification"))
    const meta = [
      item.kind === "conversation" && item.channel ? t(`inbox.channel.${item.channel}`) : "",
      item.assignee_user_id && item.assignee_user_id === userId ? t("alerts.assignedToMe") : "",
    ]
      .filter(Boolean)
      .join(" · ")
    return {
      id: `${item.kind}-${item.id}`,
      title: (
        <span className="bidi-block" dir="auto">
          {name}
        </span>
      ),
      description:
        item.snippet || meta ? (
          <span className="bidi-block" dir="auto">
            {[item.snippet, meta].filter(Boolean).join(" — ")}
          </span>
        ) : undefined,
      ...(item.kind === "conversation"
        ? { actor: { name } }
        : { icon: <NoteIcon item={item} /> }),
      time: item.at,
      unread: item.unread,
    }
  })

  async function mark(item: BellItem) {
    try {
      if (item.kind === "conversation") {
        await api(`/api/w/conversations/${encodeURIComponent(item.id)}`, {
          method: "PATCH",
          json: { unread: false },
          workspaceId,
        })
      } else {
        await api(`/api/w/notifications/${encodeURIComponent(item.id)}`, {
          method: "PATCH",
          json: { read: true },
          workspaceId,
        })
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

  function itemActions(row: NotificationCenterItem): ContextMenuAction[] {
    const item = byKey.get(row.id)
    if (!item?.unread) return []
    const allowed = item.kind === "conversation" ? canMarkConv : canMarkNote
    return allowed
      ? [{ id: "read", label: t("alerts.markRead"), icon: CheckIcon, onSelect: () => void mark(item) }]
      : []
  }

  const close = () => setOpen(false)
  const header =
    nothing ? (
      <p className="text-sm text-muted-foreground">{t("alerts.nothingToRead")}</p>
    ) : (
      <div className="flex flex-col gap-2">
        {perm === "default" ? (
          // A click, never on load: see useNotificationPermission.
          <Button
            variant="secondary"
            size="sm"
            className="w-full"
            onClick={() => void requestPerm()}
            data-allow-desktop
          >
            <BellRingIcon />
            {t("alerts.enableDesktop")}
          </Button>
        ) : null}
        <div className="flex flex-wrap items-center gap-1">
          {data?.can_inbox ? (
            <Button
              variant="ghost"
              size="sm"
              nativeButton={false}
              render={<Link href={`/${locale}/w/${slug}/inbox`} onClick={close} />}
            >
              {t("alerts.openInbox")}
            </Button>
          ) : null}
          {data?.can_notifications ? (
            <Button
              variant="ghost"
              size="sm"
              nativeButton={false}
              render={<Link href={`/${locale}/w/${slug}/notifications`} onClick={close} />}
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
              render={<Link href={`/${locale}/w/${slug}/settings/delivery`} onClick={close} />}
            >
              <SettingsIcon />
            </Button>
          ) : null}
        </div>
      </div>
    )

  return (
    <NotificationCenter
      variant="sheet"
      items={items}
      unreadCount={count}
      open={open}
      onOpenChange={setOpen}
      header={header}
      itemActions={itemActions}
      onItemClick={(row) => {
        const item = byKey.get(row.id)
        if (!item) return
        close()
        router.push(hrefFor(item, locale, slug))
      }}
      onMarkAllRead={canMarkConv || canMarkNote ? () => void markAll() : undefined}
      labels={{
        title: t("alerts.bell"),
        all: t("alerts.tabAll"),
        unread: t("alerts.tabUnread"),
        markAllRead: t("alerts.markAllRead"),
        emptyAll: t("alerts.empty"),
        emptyAllDescription: t("alerts.emptyBody"),
        emptyUnread: t("alerts.caughtUp"),
        emptyUnreadDescription: t("alerts.caughtUpBody"),
        trigger: (n) => (n ? t("alerts.bellUnread", { n }) : t("alerts.bell")),
      }}
      data-bell
      data-bell-count={count}
    />
  )
}
