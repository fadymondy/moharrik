"use client"

/*
Channels (MH-571): every inbox channel of every type in one list, replacing
the seven per-type setup pages (email, whatsapp, chat widgets, telegram,
slack, discord, workflow-channels — each now redirects here with ?type=).
Filters (type, status, q) are URL-driven so a filtered view is linkable.

The list itself comes from GET /api/w/channels (useChannels) — a companion
card built that endpoint in parallel. Row actions (test, pause/resume, edit,
delete) still call each type's own existing endpoint, because that unified
row does not carry every type-specific field (a bot token, allowed chat ids,
...) needed to prefill an edit form or reconstruct a PUT body — this page
also loads each type's own list (useTelegramChannels etc., already used by
the old pages) purely to look an id up for those actions. The unified
endpoint owns listing, filtering and the error-first ordering; per-type
endpoints still own writes, same as before.
*/

import Link from "next/link"
import { useEffect, useState } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import {
  BotIcon,
  CableIcon,
  CheckIcon,
  HashIcon,
  MailIcon,
  MessageCircleIcon,
  MessageSquareIcon,
  PauseIcon,
  PlayIcon,
  PlusIcon,
  RefreshCwIcon,
  SearchIcon,
  SendIcon,
  SquarePenIcon,
  TriangleAlertIcon,
  WorkflowIcon,
} from "lucide-react"
import { Badge, Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Input, Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, toast } from "@fadymondy/nasaq/web"

import { Bdi, isolate } from "@/components/bidi"
import { ConfirmButton } from "@/components/confirm-button"
import { DiscordChannelForm } from "@/components/channels/discord-channel-form"
import { MailboxChannelForm } from "@/components/channels/mailbox-channel-form"
import { SlackChannelForm } from "@/components/channels/slack-channel-form"
import { TelegramChannelForm } from "@/components/channels/telegram-channel-form"
import { WhatsAppChannelForm } from "@/components/channels/whatsapp-channel-form"
import { WidgetChannelForm } from "@/components/channels/widget-channel-form"
import { WorkflowChannelForm } from "@/components/channels/workflow-channel-form"
import { WidgetSettingsSheet } from "@/components/inbox/widget-settings"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type ChannelRow, type ChannelStatus, type ChannelType } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import {
  useChannels,
  useChatWidgets,
  useDiscordChannels,
  useMailboxes,
  useSlackChannels,
  useTelegramChannels,
  useWhatsApp,
  useWorkflowChannels,
} from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

const CHANNEL_TYPES: ChannelType[] = [
  "email",
  "whatsapp",
  "chat",
  "telegram",
  "slack",
  "discord",
  "workflow",
]
const STATUSES: ChannelStatus[] = ["connected", "error", "paused"]

const TYPE_ICON: Record<ChannelType, typeof MailIcon> = {
  email: MailIcon,
  whatsapp: MessageCircleIcon,
  chat: MessageSquareIcon,
  telegram: SendIcon,
  slack: HashIcon,
  discord: BotIcon,
  workflow: WorkflowIcon,
}

/** The endpoint each type's row actions (test/pause/delete) call. WhatsApp has no id — one per workspace. */
const TYPE_PATH: Record<Exclude<ChannelType, "whatsapp">, string> = {
  email: "/api/w/mailboxes",
  chat: "/api/w/chat_widgets",
  telegram: "/api/w/telegram-channels",
  slack: "/api/w/slack-channels",
  discord: "/api/w/discord-channels",
  workflow: "/api/w/workflow-channels",
}

const TESTABLE: ChannelType[] = ["email", "telegram", "slack", "discord"]

export default function ChannelsPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("channels.title")} · ${ws.summary.name}`)
  const canManage = ws.can("inbox:manage")

  const router = useRouter()
  const pathname = usePathname()
  const params = useSearchParams()

  const typeParam = params.get("type")
  const selectedTypes = new Set<ChannelType>(
    (typeParam ? typeParam.split(",") : []).filter((t): t is ChannelType =>
      CHANNEL_TYPES.includes(t as ChannelType),
    ),
  )
  const status = (params.get("status") as ChannelStatus | null) ?? ""
  const [query, setQuery] = useState(() => params.get("q") ?? "")

  function setParam(key: string, value: string | null) {
    const next = new URLSearchParams(params.toString())
    if (value) next.set(key, value)
    else next.delete(key)
    const qs = next.toString()
    router.replace(qs ? `${pathname}?${qs}` : pathname, { scroll: false })
  }

  function toggleType(ty: ChannelType) {
    const next = new Set(selectedTypes)
    if (next.has(ty)) next.delete(ty)
    else next.add(ty)
    setParam("type", next.size ? Array.from(next).join(",") : null)
  }

  useEffect(() => {
    const id = window.setTimeout(() => setParam("q", query.trim() || null), 300)
    return () => window.clearTimeout(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query])

  const list = useChannels(ws.id, {
    type: Array.from(selectedTypes),
    status,
    q: params.get("q") ?? undefined,
  })
  // Header counts are the workspace's totals per type, not the filtered result —
  // a separate unfiltered request so they never look like "results" for the current filter.
  const totals = useChannels(ws.id, {})

  // Per-type lists: only used to look up the full typed entity for edit/actions (see file header).
  const mailboxes = useMailboxes(ws.id)
  const whatsapp = useWhatsApp(ws.id)
  const widgets = useChatWidgets(ws.id)
  const telegram = useTelegramChannels(ws.id)
  const slack = useSlackChannels(ws.id)
  const discord = useDiscordChannels(ws.id)
  const workflow = useWorkflowChannels(ws.id)

  function refreshAll() {
    void list.mutate()
    void totals.mutate()
    void mailboxes.mutate()
    void whatsapp.mutate()
    void widgets.mutate()
    void telegram.mutate()
    void slack.mutate()
    void discord.mutate()
    void workflow.mutate()
  }

  const [addType, setAddType] = useState<ChannelType | null>(null)
  const [editRow, setEditRow] = useState<ChannelRow | null>(null)
  const editingWidget =
    editRow?.type === "chat"
      ? (widgets.data?.widgets?.find((w) => w.id === editRow.id) ?? null)
      : null

  const items = list.data?.channels ?? []
  // The backend doesn't (yet) return a `counts` aggregate on /api/w/channels — derive per-type
  // totals from the unfiltered list itself rather than block the header on that field landing.
  const counts =
    totals.data?.counts ??
    (totals.data?.channels ?? []).reduce<Partial<Record<ChannelType, number>>>((acc, row) => {
      acc[row.type] = (acc[row.type] ?? 0) + 1
      return acc
    }, {})

  async function toggleEnabled(row: ChannelRow) {
    if (row.type === "whatsapp") return // WhatsApp has no enable switch — unlink instead.
    try {
      await api(`${TYPE_PATH[row.type]}/${encodeURIComponent(row.id)}`, {
        method: "PATCH",
        json: { enabled: !row.enabled },
        workspaceId: ws.id,
      })
      toast.success(row.enabled ? t("channels.turnedOff") : t("channels.turnedOn"))
      refreshAll()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function testRow(row: ChannelRow) {
    try {
      await api(
        `${TYPE_PATH[row.type as Exclude<ChannelType, "whatsapp" | "chat" | "workflow">]}/${encodeURIComponent(row.id)}/test`,
        {
          method: "POST",
          json: {},
          workspaceId: ws.id,
        },
      )
      toast.success(t("channels.testOk"))
      refreshAll()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
      refreshAll()
    }
  }

  async function removeRow(row: ChannelRow) {
    if (row.type === "whatsapp") {
      await api("/api/w/whatsapp", { method: "DELETE", workspaceId: ws.id })
    } else {
      await api(`${TYPE_PATH[row.type]}/${encodeURIComponent(row.id)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
    }
    toast.success(t("channels.removed"))
    refreshAll()
  }

  const base = `/${locale}/w/${ws.slug}`

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("channels.title")}
        description={t("channels.hint")}
        action={
          canManage ? (
            <DropdownMenu>
              <DropdownMenuTrigger render={<Button data-add-channel />}>
                <PlusIcon />
                {t("channels.addChannel")}
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                {CHANNEL_TYPES.map((ty) => {
                  const Icon = TYPE_ICON[ty]
                  return (
                    <DropdownMenuItem key={ty} onClick={() => setAddType(ty)} data-add-type={ty}>
                      <Icon />
                      {t(`inbox.channel.${ty}`)}
                    </DropdownMenuItem>
                  )
                })}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : undefined
        }
      />

      {/* Header counts: totals per type across the workspace — not the filtered count. */}
      <div
        className="flex flex-wrap items-center gap-2 px-6 pb-4"
        aria-label={t("channels.totalsLabel")}
      >
        <span className="text-xs text-muted-foreground">{t("channels.totalsLabel")}</span>
        {CHANNEL_TYPES.map((ty) => (
          <Badge key={ty} variant="outline" className="gap-1" data-total-type={ty}>
            {t(`inbox.channel.${ty}`)}
            <span className="text-muted-foreground">{counts?.[ty] ?? 0}</span>
          </Badge>
        ))}
      </div>

      <div className="flex flex-col gap-3 px-6 pb-4">
        <div
          className="flex flex-wrap items-center gap-2"
          role="group"
          aria-label={t("channels.filterType")}
        >
          {CHANNEL_TYPES.map((ty) => {
            const Icon = TYPE_ICON[ty]
            const active = selectedTypes.has(ty)
            return (
              <Button
                key={ty}
                type="button"
                size="sm"
                variant={active ? "primary" : "secondary"}
                aria-pressed={active}
                onClick={() => toggleType(ty)}
                data-type-filter={ty}
              >
                <Icon />
                {t(`inbox.channel.${ty}`)}
              </Button>
            )
          })}
        </div>
        <div className="flex flex-wrap items-center gap-3 lg:flex-row">
          <div
            className="flex flex-wrap items-center gap-2"
            role="group"
            aria-label={t("channels.filterStatus")}
          >
            <Button
              type="button"
              size="sm"
              variant={status === "" ? "primary" : "secondary"}
              aria-pressed={status === ""}
              onClick={() => setParam("status", null)}
              data-status-filter="all"
            >
              {t("channels.allStatuses")}
            </Button>
            {STATUSES.map((s) => (
              <Button
                key={s}
                type="button"
                size="sm"
                variant={status === s ? "primary" : "secondary"}
                aria-pressed={status === s}
                onClick={() => setParam("status", s)}
                data-status-filter={s}
              >
                {t(`channels.status.${s}`)}
              </Button>
            ))}
          </div>
          <div className="relative lg:ms-auto lg:w-72">
            <SearchIcon
              className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
              aria-hidden
            />
            <Input
              type="search"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("channels.search")}
              aria-label={t("channels.search")}
              className="ps-8"
              maxLength={200}
              data-channels-search
            />
          </div>
        </div>
      </div>

      {list.error ? (
        <ErrorState error={list.error} />
      ) : !list.data ? (
        <LoadingRows rows={5} />
      ) : items.length === 0 ? (
        <EmptyState
          title={
            selectedTypes.size || status || query ? t("channels.noMatch") : t("channels.empty")
          }
          body={selectedTypes.size || status || query ? undefined : t("channels.emptyBody")}
        />
      ) : (
        <ul className="divide-y divide-nq-line border-y border-nq-line" data-channel-list>
          {items.map((row) => (
            <ChannelListRow
              key={`${row.type}-${row.id}`}
              row={row}
              canManage={canManage}
              conversationsHref={`${base}/inbox?channel=${encodeURIComponent(row.type)}`}
              onTest={TESTABLE.includes(row.type) ? () => testRow(row) : undefined}
              onToggle={row.type !== "whatsapp" ? () => toggleEnabled(row) : undefined}
              onEdit={() => setEditRow(row)}
              onDelete={() => removeRow(row)}
            />
          ))}
        </ul>
      )}
      <div className="pb-8" />

      {/* Add channel: a type picker (the dropdown above), then that type's existing form in a sheet. */}
      <Sheet open={!!addType} onOpenChange={(v) => !v && setAddType(null)}>
        <SheetContent
          className="w-full overflow-y-auto sm:max-w-lg"
          data-add-channel-sheet={addType ?? undefined}
        >
          <SheetHeader>
            <SheetTitle>
              {addType ? t("channels.addTypeTitle", { type: t(`inbox.channel.${addType}`) }) : ""}
            </SheetTitle>
            <SheetDescription>{t("channels.addTypeHint")}</SheetDescription>
          </SheetHeader>
          <div className="px-4 pb-4">
            {/* The guide opens by default only for a type with no connections yet — once one exists, it starts collapsed but stays one click away. */}
            {addType === "email" ? (
              <MailboxChannelForm
                defaultGuideOpen={!counts?.email}
                onSaved={() => {
                  setAddType(null)
                  refreshAll()
                }}
                onCancel={() => setAddType(null)}
              />
            ) : null}
            {addType === "whatsapp" ? (
              <WhatsAppChannelForm
                defaultGuideOpen={!counts?.whatsapp}
                onChanged={() => {
                  refreshAll()
                }}
              />
            ) : null}
            {addType === "chat" ? (
              <WidgetChannelForm
                defaultGuideOpen={!counts?.chat}
                onSaved={() => {
                  setAddType(null)
                  refreshAll()
                }}
                onCancel={() => setAddType(null)}
              />
            ) : null}
            {addType === "telegram" ? (
              <TelegramChannelForm
                defaultGuideOpen={!counts?.telegram}
                onSaved={() => {
                  setAddType(null)
                  refreshAll()
                }}
                onCancel={() => setAddType(null)}
              />
            ) : null}
            {addType === "slack" ? (
              <SlackChannelForm
                defaultGuideOpen={!counts?.slack}
                onSaved={() => {
                  setAddType(null)
                  refreshAll()
                }}
                onCancel={() => setAddType(null)}
              />
            ) : null}
            {addType === "discord" ? (
              <DiscordChannelForm
                defaultGuideOpen={!counts?.discord}
                onSaved={() => {
                  setAddType(null)
                  refreshAll()
                }}
                onCancel={() => setAddType(null)}
              />
            ) : null}
            {addType === "workflow" ? (
              <WorkflowChannelForm
                defaultGuideOpen={!counts?.workflow}
                onSaved={() => {
                  setAddType(null)
                  refreshAll()
                }}
                onCancel={() => setAddType(null)}
              />
            ) : null}
          </div>
        </SheetContent>
      </Sheet>

      {/* Edit: the same forms, pre-filled from this type's own list (the unified row doesn't carry every field). */}
      <Sheet
        open={!!editRow && editRow.type !== "chat"}
        onOpenChange={(v) => !v && setEditRow(null)}
      >
        <SheetContent
          className="w-full overflow-y-auto sm:max-w-lg"
          data-edit-channel-sheet={editRow?.type ?? undefined}
        >
          <SheetHeader>
            <SheetTitle>{editRow ? isolate(editRow.name) : ""}</SheetTitle>
          </SheetHeader>
          <div className="px-4 pb-4">
            {editRow?.type === "email" ? (
              <MailboxChannelForm
                mailbox={mailboxes.data?.mailboxes?.find((m) => m.id === editRow.id)}
                onSaved={() => {
                  setEditRow(null)
                  refreshAll()
                }}
                onCancel={() => setEditRow(null)}
              />
            ) : null}
            {editRow?.type === "whatsapp" ? (
              <WhatsAppChannelForm onChanged={() => refreshAll()} />
            ) : null}
            {editRow?.type === "telegram" ? (
              <TelegramChannelForm
                channel={telegram.data?.channels?.find((c) => c.id === editRow.id)}
                onSaved={() => {
                  setEditRow(null)
                  refreshAll()
                }}
                onCancel={() => setEditRow(null)}
              />
            ) : null}
            {editRow?.type === "slack" ? (
              <SlackChannelForm
                channel={slack.data?.channels?.find((c) => c.id === editRow.id)}
                onSaved={() => {
                  setEditRow(null)
                  refreshAll()
                }}
                onCancel={() => setEditRow(null)}
              />
            ) : null}
            {editRow?.type === "discord" ? (
              <DiscordChannelForm
                channel={discord.data?.channels?.find((c) => c.id === editRow.id)}
                onSaved={() => {
                  setEditRow(null)
                  refreshAll()
                }}
                onCancel={() => setEditRow(null)}
              />
            ) : null}
            {editRow?.type === "workflow" ? (
              <WorkflowChannelForm
                channel={workflow.data?.channels?.find((c) => c.id === editRow.id)}
                onSaved={() => {
                  setEditRow(null)
                  refreshAll()
                }}
                onCancel={() => setEditRow(null)}
              />
            ) : null}
          </div>
        </SheetContent>
      </Sheet>

      {/* Chat widgets already have a full settings editor — reuse it instead of a new form. */}
      <WidgetSettingsSheet
        widget={editRow?.type === "chat" ? editingWidget : null}
        onOpenChange={(open) => (!open ? setEditRow(null) : undefined)}
        onSaved={() => {
          setEditRow(null)
          refreshAll()
        }}
      />
    </>
  )
}

function ChannelListRow({
  row,
  canManage,
  conversationsHref,
  onTest,
  onToggle,
  onEdit,
  onDelete,
}: {
  row: ChannelRow
  canManage: boolean
  conversationsHref: string
  onTest?: () => void
  onToggle?: () => void
  onEdit: () => void
  onDelete: () => Promise<void>
}) {
  const { t, formatDate } = useTranslations()
  const Icon = TYPE_ICON[row.type]
  const [testing, setTesting] = useState(false)

  const statusVariant: Record<ChannelStatus, "brand" | "neutral" | "outline"> = {
    connected: "brand",
    error: "destructive" as "outline",
    paused: "outline",
  }

  return (
    <li
      className="px-6 py-4"
      data-channel-row={row.id}
      data-channel-type={row.type}
      data-channel-status={row.status}
    >
      <div className="flex flex-wrap items-center gap-3">
        <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
        <p className="text-sm font-medium">
          <Bdi>{row.name}</Bdi>
        </p>
        {row.type === "workflow" && row.network ? (
          <Badge variant="neutral">{t(`inbox.channel.${row.network}`)}</Badge>
        ) : null}
        <Badge
          variant={
            row.status === "error"
              ? "danger"
              : row.status === "connected"
                ? "brand"
                : "outline"
          }
          data-channel-status-badge
        >
          {row.status === "error" ? <TriangleAlertIcon className="size-3" /> : null}
          {t(`channels.status.${row.status}`)}
        </Badge>
        <div className="ms-auto flex flex-wrap items-center gap-2">
          <Button
            variant="ghost"
            size="sm"
            nativeButton={false}
            render={<Link href={conversationsHref} />}
            data-open-conversations
          >
            {t("channels.openConversations")}
          </Button>
          {onTest ? (
            <Button
              variant="secondary"
              size="sm"
              disabled={testing}
              onClick={async () => {
                setTesting(true)
                try {
                  await onTest()
                } finally {
                  setTesting(false)
                }
              }}
              data-test-connection
            >
              <RefreshCwIcon className={testing ? "animate-spin" : undefined} />
              {t("channels.test")}
            </Button>
          ) : null}
          {canManage && onToggle ? (
            <Button variant="secondary" size="sm" onClick={onToggle} data-toggle-enabled>
              {row.enabled ? <PauseIcon /> : <PlayIcon />}
              {row.enabled ? t("channels.pause") : t("channels.resume")}
            </Button>
          ) : null}
          {canManage ? (
            <Button variant="secondary" size="sm" onClick={onEdit} data-edit-channel>
              <SquarePenIcon />
              {t("common.edit")}
            </Button>
          ) : null}
          {canManage ? (
            <ConfirmButton
              label={t("common.delete")}
              title={t("channels.deleteTitle")}
              description={t("channels.deleteBody", { name: isolate(row.name) })}
              confirmLabel={t("common.delete")}
              onConfirm={onDelete}
            />
          ) : null}
        </div>
      </div>
      <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>
          {row.last_message_at
            ? t("channels.lastMessage", { date: formatDate(row.last_message_at) })
            : t("channels.neverReceived")}
        </span>
        <span>{t("channels.conversations30d", { n: row.conversations_30d })}</span>
        {row.unread > 0 ? (
          <span className="font-medium text-foreground" data-unread={row.unread}>
            {t("channels.unread", { n: row.unread })}
          </span>
        ) : null}
      </div>
      {row.status === "error" && row.last_error ? (
        <p
          className="mt-2 flex items-start gap-2 border border-nq-danger-text/40 bg-nq-danger-text/5 px-3 py-2 text-sm"
          role="status"
          data-channel-error
        >
          <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-nq-danger-text" />
          <span>{row.last_error}</span>
        </p>
      ) : null}
    </li>
  )
}
