"use client"

/*
Version history (MH-480, on MH-400's versions): every save of the workflow,
newest first. Choosing one draws it on the canvas, read-only, beside the list —
so "what did v3 look like" is answered by looking, not by reading JSON — and it
can be restored. A restore is itself a new version, so it can be undone too.
*/

import { useMemo, useState } from "react"
import { HistoryIcon, XIcon } from "lucide-react"
import { toast } from "@fadymondy/nasaq/web"

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
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationWorkflow, type WorkflowVersion } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { cn } from "@/lib/utils"

export function VersionsPanel({
  workflow,
  versions,
  previewing,
  dirty,
  canEdit,
  onPreview,
  onRestored,
  onClose,
}: {
  workflow: AutomationWorkflow
  versions: WorkflowVersion[]
  previewing: number | null
  dirty: boolean
  canEdit: boolean
  onPreview: (v: WorkflowVersion | null) => void
  onRestored: () => void
  onClose: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [confirm, setConfirm] = useState<WorkflowVersion | null>(null)
  const [busy, setBusy] = useState(false)
  const when = useMemo(
    () => new Intl.DateTimeFormat(intlTag(locale), { dateStyle: "medium", timeStyle: "short" }),
    [locale],
  )

  async function restore(v: WorkflowVersion) {
    setBusy(true)
    try {
      const { workflow: now } = await api<{ workflow: AutomationWorkflow }>(
        `/api/w/workflows/${encodeURIComponent(workflow.id)}/versions/${v.version}/restore`,
        {
          json: { base_version: workflow.version },
          workspaceId: ws.id,
        },
      )
      toast.success(t("automation.versions.restored", { from: v.version, n: now.version }))
      setConfirm(null)
      onPreview(null)
      onRestored()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex h-full flex-col" data-versions>
      <div className="flex items-start gap-3 border-b border-nq-line px-4 py-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-md border border-nq-line bg-card">
          <HistoryIcon className="size-4.5" />
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="text-base font-medium">{t("automation.versions.title")}</h2>
          <p className="text-xs text-muted-foreground">{t("canvas.versionsHint")}</p>
        </div>
        <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label={t("canvas.close")}>
          <XIcon />
        </Button>
      </div>
      <ol className="min-h-0 flex-1 divide-y divide-nq-line overflow-y-auto">
        {versions.map((v) => {
          const current = v.version === workflow.version
          const on = previewing === v.version || (previewing === null && current)
          return (
            <li
              key={v.version}
              className={cn("px-4 py-3", on && "bg-nq-surface-soft")}
              data-version-row={v.version}
            >
              <button
                type="button"
                className="block w-full text-start"
                onClick={() => onPreview(current ? null : v)}
                data-preview-version={v.version}
              >
                <span className="flex flex-wrap items-center gap-2 text-sm">
                  <span className="font-medium">
                    {t("automation.editor.versionBadge", { n: v.version })}
                  </span>
                  {current ? <Badge>{t("automation.versions.current")}</Badge> : null}
                  {previewing === v.version ? (
                    <Badge variant="secondary">{t("canvas.previewing")}</Badge>
                  ) : null}
                </span>
                <span className="block text-xs text-muted-foreground">
                  {v.note === "restored" && v.restored_from
                    ? t("automation.versions.note.restoredFrom", { n: v.restored_from })
                    : t(`automation.versions.note.${v.note}`)}
                  {" · "}
                  {when.format(new Date(v.created_at))}
                  {" · "}
                  {t("automation.stepCount", { n: v.steps.length })}
                </span>
              </button>
              {!current && canEdit && previewing === v.version ? (
                <Button
                  type="button"
                  size="xs"
                  className="mt-2"
                  onClick={() => setConfirm(v)}
                  data-restore={v.version}
                >
                  {t("automation.versions.restore")}
                </Button>
              ) : null}
            </li>
          )
        })}
      </ol>

      <AlertDialog open={!!confirm} onOpenChange={(o) => !o && setConfirm(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("automation.versions.confirmTitle", { n: confirm?.version ?? 0 })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("automation.versions.confirmBody", {
                n: confirm?.version ?? 0,
                next: workflow.version + 1,
              })}
              {dirty ? ` ${t("automation.versions.confirmDirty")}` : ""}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              disabled={busy}
              onClick={() => confirm && restore(confirm)}
              data-restore-confirm
            >
              {busy ? t("common.working") : t("automation.versions.restore")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
