"use client"

import { useState } from "react"
import { CopyIcon, StarIcon, StarOffIcon, Trash2Icon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { ApiError, type InboxMessage } from "@/lib/api"
import { hideMessage, starMessage, unstarMessage } from "@/lib/queries"
import { useTranslations } from "@/lib/i18n"

/*
The select-messages action bar (MH-662): copy, star/unstar and "delete for
us" — WhatsApp's own three actions on a multi-select. Delete here NEVER
touches WhatsApp or a teammate's own view: it hides the message from the
signed-in member only, which the confirm dialog says outright so nobody reads
it as "unsend".
*/
export function ThreadSelectBar({
  messages,
  selectedIds,
  onExit,
  onChanged,
}: {
  messages: InboxMessage[]
  selectedIds: Set<string>
  onExit: () => void
  onChanged: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const selected = messages.filter((m) => selectedIds.has(m.id))
  const allStarred = selected.length > 0 && selected.every((m) => m.starred)

  async function copy() {
    const text = selected
      .slice()
      .sort((a, b) => a.created_at.localeCompare(b.created_at))
      .map((m) => m.body)
      .filter(Boolean)
      .join("\n\n")
    try {
      await navigator.clipboard.writeText(text)
      toast.success(t("inbox.select.copied"))
    } catch {
      toast.error(t("common.networkError"))
    }
    onExit()
  }

  async function toggleStar() {
    setBusy(true)
    try {
      await Promise.all(
        selected.map((m) => (allStarred ? unstarMessage(ws.id, m.id) : starMessage(ws.id, m.id))),
      )
      toast.success(allStarred ? t("inbox.select.unstarred") : t("inbox.select.starred"))
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
      onExit()
    }
  }

  async function deleteForUs() {
    setConfirmDelete(false)
    setBusy(true)
    try {
      await Promise.all(selected.map((m) => hideMessage(ws.id, m.id)))
      toast.success(t("inbox.select.deleted"))
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
      onExit()
    }
  }

  return (
    <>
      <div
        className="flex items-center gap-2 border-b border-line bg-grid-elevated px-6 py-2"
        data-thread-select-bar
      >
        <Button type="button" variant="ghost" size="icon-sm" aria-label={t("inbox.select.cancel")} onClick={onExit}>
          <XIcon />
        </Button>
        <span className="text-sm" data-select-count>
          {t("inbox.select.title", { n: selectedIds.size })}
        </span>
        <div className="ms-auto flex items-center gap-1">
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={t("inbox.select.copy")}
            disabled={busy || selected.length === 0}
            onClick={() => void copy()}
            data-select-copy
          >
            <CopyIcon />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={allStarred ? t("inbox.select.unstar") : t("inbox.select.star")}
            disabled={busy || selected.length === 0}
            onClick={() => void toggleStar()}
            data-select-star
          >
            {allStarred ? <StarOffIcon /> : <StarIcon />}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label={t("inbox.select.delete")}
            disabled={busy || selected.length === 0}
            onClick={() => setConfirmDelete(true)}
            data-select-delete
          >
            <Trash2Icon />
          </Button>
        </div>
      </div>

      <AlertDialog open={confirmDelete} onOpenChange={setConfirmDelete}>
        <AlertDialogContent data-confirm-dialog="select-delete">
          <AlertDialogHeader>
            <AlertDialogTitle>{t("inbox.select.deleteTitle", { n: selectedIds.size })}</AlertDialogTitle>
            <AlertDialogDescription>{t("inbox.select.deleteBody")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction variant="destructive" onClick={() => void deleteForUs()} data-confirm>
              {t("inbox.select.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
