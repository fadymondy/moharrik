"use client"

import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { useEffect, useState } from "react"
import { InboxIcon, XIcon } from "lucide-react"
import { Badge, Button, CopilotDock } from "@fadymondy/nasaq/web"

import { BdiBlock } from "@/components/bidi"
import { InboxPanel } from "@/components/inbox/inbox-panel"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { INBOX_CHANNEL_PARAM, INBOX_PARAM, setOpenConversation } from "@/lib/inbox-link"
import { useBell } from "@/lib/queries"

/**
 * The inbox as a copilot-style dock (MH-1147): a slim bar fixed to the bottom of every
 * workspace page — what is unread and from whom — that opens into the whole inbox: the
 * conversation list beside the open thread. ⌘J / Ctrl+J toggles it from anywhere. It
 * replaces the old floating launcher and its per-conversation chat windows (ChatDock),
 * and the Inbox pages themselves.
 *
 * Nasaq's CopilotDock is the shell (bar, bottom/side/full-page positions, focus and
 * Escape handling); the panel body is InboxPanel. A link opens it with ?inbox=… — see
 * lib/inbox-link.ts.
 */
export function InboxDock() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const bell = useBell(ws.id)
  const router = useRouter()
  const pathname = usePathname()
  const params = useSearchParams()

  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState<string | null>(null)
  const [linkedUnread, setLinkedUnread] = useState(false)
  const [linkedChannel, setLinkedChannel] = useState("")
  // A link remounts the panel, so its filters start from what the link asked for.
  const [panelKey, setPanelKey] = useState(0)

  const target = params.get(INBOX_PARAM)
  useEffect(() => {
    if (!target) return
    const unreadOnly = target === "unread"
    const id = unreadOnly || target === "all" || target === "1" ? null : target
    setSelected(id)
    setLinkedUnread(unreadOnly)
    setLinkedChannel(params.get(INBOX_CHANNEL_PARAM) ?? "")
    setPanelKey((k) => k + 1)
    setOpen(true)
    const next = new URLSearchParams(params.toString())
    next.delete(INBOX_PARAM)
    next.delete(INBOX_CHANNEL_PARAM)
    // The old /inbox?unread=1 links carried this too; it means nothing to the page behind.
    next.delete("unread")
    const qs = next.toString()
    router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target])

  // Live alerts stay quiet about the thread the reader already has open.
  useEffect(() => {
    setOpenConversation(open ? selected : null)
    return () => setOpenConversation(null)
  }, [open, selected])

  // The bar's count and preview follow the stream; the open panel refreshes itself.
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "message" || e.kind === "conversation") void bell.mutate()
  })

  const data = bell.data
  if (!data?.can_inbox) return null

  const unread = data.unread_conversations
  const latest = (data.items ?? []).find((i) => i.kind === "conversation" && i.unread)

  return (
    <CopilotDock
      open={open}
      onOpenChange={(v) => {
        setOpen(v)
        if (!v) void bell.mutate()
      }}
      collapsedBar
      defaultSide="bottom"
      sides={["bottom", "end"]}
      persistKey="moharrik.inbox-dock.side"
      height="min(80vh, 48rem)"
      width="34rem"
      // Drag the inner edge: height at the bottom, width on the side (MH-1147 review).
      resizable
      // A floating sheet a little off the edges rather than a slab glued to them.
      className="data-[side=bottom]:inset-x-2 data-[side=bottom]:bottom-2 data-[side=bottom]:max-h-[calc(100%-1rem)] data-[side=end]:inset-y-2 data-[side=end]:end-2 data-[side=end]:max-w-[calc(100%-1rem)] rounded-card border"
      launcherIcon={<InboxIcon />}
      dockLabels={{
        open: t("inbox.dock.open"),
        close: t("inbox.dock.close"),
        panel: t("nav.inbox"),
      }}
      barContent={
        <>
          <span className="shrink-0 font-medium">{t("nav.inbox")}</span>
          {unread > 0 ? (
            <Badge
              variant="brand"
              className="h-5 min-w-5 shrink-0 rounded-full px-1 tabular-nums"
              data-inbox-bar-count={unread}
            >
              {unread > 99 ? "99+" : unread}
            </Badge>
          ) : null}
          <BdiBlock
            className="min-w-0 flex-1 truncate text-muted-foreground"
            data-inbox-bar-preview
          >
            {latest
              ? `${latest.title || t("alerts.visitor")}: ${latest.snippet}`
              : t("inbox.dock.caughtUp")}
          </BdiBlock>
        </>
      }
    >
      {({ controls, close }) => (
        <InboxPanel
          key={panelKey}
          selected={selected}
          onSelect={setSelected}
          linkedUnread={linkedUnread}
          linkedChannel={linkedChannel}
          controls={
            <>
              {controls}
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={t("inbox.dock.close")}
                title={t("inbox.dock.close")}
                onClick={close}
                data-inbox-dock-close
              >
                <XIcon />
              </Button>
            </>
          }
        />
      )}
    </CopilotDock>
  )
}
