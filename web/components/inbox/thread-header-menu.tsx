"use client"

import { useState } from "react"
import {
  BanIcon,
  BellIcon,
  BellOffIcon,
  CheckIcon,
  DownloadIcon,
  EraserIcon,
  FileArchiveIcon,
  FileTextIcon,
  ListChecksIcon,
  MoreVerticalIcon,
  SearchIcon,
  StarIcon,
  TagIcon,
  Trash2Icon,
  UserIcon,
} from "lucide-react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
  toast,
} from "@fadymondy/nasaq/web"

import { isMuted, LabelsDialog } from "@/components/inbox/chat-row-menu"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, downloadFile, type Conversation } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

/*
The thread header's ⋮ menu (MH-662) — WhatsApp's own set of chat actions, from
the open thread rather than the inbox row. Pin/archive live on the row menu
(ChatRowMenu, MH-657) only; everything the row menu already owns here reuses
its logic (isMuted, LabelsDialog) rather than a second implementation, since a
DropdownMenu and a ContextMenu are different Base UI primitive families and
cannot share JSX directly.
*/

type Confirm = "block" | "clear" | "delete" | null

export function ThreadHeaderMenu({
  c,
  onChanged,
  onContactInfo,
  onSearch,
  onSelectMessages,
  onToggleStatus,
  onDeleted,
}: {
  c: Conversation
  onChanged?: () => void
  onContactInfo: () => void
  onSearch: () => void
  /** Omitted where there is no select bar (the docked chat): the item is hidden. */
  onSelectMessages?: () => void
  onToggleStatus: () => void
  onDeleted?: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("inbox:write")
  const canManage = ws.can("inbox:manage")
  const [labelsOpen, setLabelsOpen] = useState(false)
  const [confirm, setConfirm] = useState<Confirm>(null)
  const [exporting, setExporting] = useState<"txt" | "zip" | null>(null)
  const muted = isMuted(c)
  const path = `/api/w/conversations/${encodeURIComponent(c.id)}`

  async function organise(patch: Record<string, unknown>, message: string) {
    try {
      await api(`${path}/organise`, { method: "PATCH", json: patch, workspaceId: ws.id })
      toast.success(message)
      onChanged?.()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function runConfirm() {
    const what = confirm
    setConfirm(null)
    try {
      if (what === "block") {
        await api(`${path}/organise`, {
          method: "PATCH",
          json: { blocked: !c.blocked },
          workspaceId: ws.id,
        })
        toast.success(c.blocked ? t("inbox.org.unblocked") : t("inbox.org.blocked"))
        onChanged?.()
      } else if (what === "clear") {
        await api(`${path}/clear`, { method: "POST", workspaceId: ws.id })
        toast.success(t("inbox.org.cleared"))
        onChanged?.()
      } else if (what === "delete") {
        await api(path, { method: "DELETE", workspaceId: ws.id })
        toast.success(t("inbox.org.deleted"))
        onDeleted?.()
        onChanged?.()
      }
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function exportChat(kind: "txt" | "zip") {
    setExporting(kind)
    try {
      await downloadFile(`${path}/export.${kind}`, {
        workspaceId: ws.id,
        fallbackName: `chat-${c.id}.${kind}`,
      })
    } catch (e) {
      toast.error(
        e instanceof ApiError && e.status === 404
          ? t("inbox.menu.exportNoAttachments")
          : e instanceof ApiError
            ? e.message
            : t("common.networkError"),
      )
    } finally {
      setExporting(null)
    }
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon"
              aria-label={t("inbox.menu.title")}
              data-thread-menu-trigger
            />
          }
        >
          <MoreVerticalIcon />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" data-thread-menu>
          <DropdownMenuItem data-action="contact-info" onClick={onContactInfo}>
            <UserIcon />
            {t("inbox.menu.contactInfo")}
          </DropdownMenuItem>
          <DropdownMenuItem data-action="search" onClick={onSearch}>
            <SearchIcon />
            {t("inbox.menu.search")}
          </DropdownMenuItem>
          {onSelectMessages ? (
            <DropdownMenuItem data-action="select-messages" onClick={onSelectMessages}>
              <ListChecksIcon />
              {t("inbox.menu.selectMessages")}
            </DropdownMenuItem>
          ) : null}

          <DropdownMenuSeparator />

          {muted ? (
            <DropdownMenuItem
              data-action="unmute"
              onClick={() => organise({ mute: "" }, t("inbox.org.unmuted"))}
            >
              <BellIcon />
              {t("inbox.org.unmute")}
            </DropdownMenuItem>
          ) : (
            <DropdownMenuSub>
              <DropdownMenuSubTrigger data-action="mute">
                <BellOffIcon />
                {t("inbox.org.mute")}
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent data-mute-menu>
                {(["8h", "1w", "always"] as const).map((d) => (
                  <DropdownMenuItem
                    key={d}
                    data-mute={d}
                    onClick={() => organise({ mute: d }, t("inbox.org.muted"))}
                  >
                    {t(`inbox.org.mute.${d}`)}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuSubContent>
            </DropdownMenuSub>
          )}
          <DropdownMenuItem
            data-action="favourite"
            onClick={() =>
              organise(
                { favourite: !c.favourite },
                c.favourite ? t("inbox.org.unfavourited") : t("inbox.org.favourited"),
              )
            }
          >
            <StarIcon />
            {c.favourite ? t("inbox.org.unfavourite") : t("inbox.org.favourite")}
          </DropdownMenuItem>
          {canWrite ? (
            <DropdownMenuItem data-action="labels" onClick={() => setLabelsOpen(true)}>
              <TagIcon />
              {t("inbox.org.labels")}
            </DropdownMenuItem>
          ) : null}

          <DropdownMenuSeparator />

          <DropdownMenuSub>
            <DropdownMenuSubTrigger data-action="export">
              <DownloadIcon />
              {t("inbox.menu.export")}
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent data-export-menu>
              <DropdownMenuItem
                data-export="txt"
                disabled={exporting != null}
                onClick={() => void exportChat("txt")}
              >
                <FileTextIcon />
                {exporting === "txt" ? t("common.working") : t("inbox.menu.exportTxt")}
              </DropdownMenuItem>
              <DropdownMenuItem
                data-export="zip"
                disabled={exporting != null}
                onClick={() => void exportChat("zip")}
              >
                <FileArchiveIcon />
                {exporting === "zip" ? t("common.working") : t("inbox.menu.exportZip")}
              </DropdownMenuItem>
            </DropdownMenuSubContent>
          </DropdownMenuSub>

          <DropdownMenuItem data-action="toggle-status" onClick={onToggleStatus}>
            <CheckIcon />
            {c.status === "closed" ? t("inbox.reopen") : t("inbox.close")}
          </DropdownMenuItem>

          {canManage ? (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                data-action="block"
                variant="danger"
                onClick={() => setConfirm("block")}
              >
                <BanIcon />
                {c.blocked ? t("inbox.org.unblock") : t("inbox.org.block")}
              </DropdownMenuItem>
              <DropdownMenuItem
                data-action="clear"
                variant="danger"
                onClick={() => setConfirm("clear")}
              >
                <EraserIcon />
                {t("inbox.org.clear")}
              </DropdownMenuItem>
              <DropdownMenuItem
                data-action="delete"
                variant="danger"
                onClick={() => setConfirm("delete")}
              >
                <Trash2Icon />
                {t("inbox.org.delete")}
              </DropdownMenuItem>
            </>
          ) : null}
        </DropdownMenuContent>
      </DropdownMenu>

      <LabelsDialog
        c={c}
        open={labelsOpen}
        onOpenChange={setLabelsOpen}
        onDone={() => onChanged?.()}
      />

      <AlertDialog open={confirm != null} onOpenChange={(open) => !open && setConfirm(null)}>
        <AlertDialogContent data-confirm-dialog={confirm ?? undefined}>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {confirm === "block"
                ? c.blocked
                  ? t("inbox.org.unblockTitle")
                  : t("inbox.org.blockTitle")
                : confirm === "clear"
                  ? t("inbox.org.clearTitle")
                  : t("inbox.org.deleteTitle")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirm === "block"
                ? c.blocked
                  ? t("inbox.org.unblockBody")
                  : c.channel === "whatsapp"
                    ? t("inbox.org.blockBodyWhatsApp")
                    : t("inbox.org.blockBody")
                : confirm === "clear"
                  ? t("inbox.org.clearBody")
                  : t("inbox.org.deleteBody")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              data-confirm
              variant={confirm === "block" && c.blocked ? "primary" : "danger"}
              onClick={runConfirm}
            >
              {confirm === "block"
                ? c.blocked
                  ? t("inbox.org.unblock")
                  : t("inbox.org.block")
                : confirm === "clear"
                  ? t("inbox.org.clear")
                  : t("inbox.org.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
