"use client"

import { useEffect, useState, type FormEvent } from "react"
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
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, downloadFile, type MailList } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { isolate } from "@/components/bidi"

/*
Subscriber admin (MH-408): the pieces the contacts list and a contact's own page
share.
*/

export type UnsubscribeTarget = { contactId: string; who: string; listId: string; listName: string }

/**
 * Take one contact off one list, after saying exactly what that does and does
 * not do. The alternative used to be deleting the contact, which also deleted
 * their deals and history; the dialog says so, because that is the mistake it
 * exists to prevent.
 */
export function UnsubscribeDialog({
  target,
  onClose,
  onDone,
}: {
  target: UnsubscribeTarget | null
  onClose: () => void
  onDone: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [busy, setBusy] = useState(false)

  async function run() {
    if (!target) return
    setBusy(true)
    try {
      const res = await api<{ changed: boolean }>(
        `/api/w/contacts/${encodeURIComponent(target.contactId)}/lists/${encodeURIComponent(target.listId)}/unsubscribe`,
        { method: "POST", workspaceId: ws.id },
      )
      toast.success(
        res.changed
          ? t("contacts.unsubscribed", { list: isolate(target.listName) })
          : t("contacts.alreadyUnsubscribed"),
      )
      onDone()
      onClose()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AlertDialog open={target !== null} onOpenChange={(v) => (v ? null : onClose())}>
      <AlertDialogContent data-dialog="unsubscribe">
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t("contacts.unsubscribeTitle", { list: isolate(target?.listName ?? "") })}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t("contacts.unsubscribeBody", {
              who: isolate(target?.who ?? ""),
              list: isolate(target?.listName ?? ""),
            })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
          <AlertDialogAction variant="destructive" disabled={busy} onClick={run}>
            {busy ? t("common.working") : t("contacts.unsubscribeConfirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/** Rename a list and change its double opt-in after it was created. */
export function EditListDialog({
  list,
  onClose,
  onSaved,
}: {
  list: MailList | null
  onClose: () => void
  onSaved: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [doi, setDoi] = useState(true)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    if (!list) return
    setName(list.name)
    setDescription(list.description)
    setDoi(list.double_opt_in)
    setErr(null)
  }, [list])

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!list) return
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/mail/lists/${encodeURIComponent(list.id)}`, {
        method: "PATCH",
        json: { name: name.trim(), description: description.trim(), double_opt_in: doi },
        workspaceId: ws.id,
      })
      toast.success(t("lists.saved"))
      onSaved()
      onClose()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={list !== null} onOpenChange={(v) => (v ? null : onClose())}>
      <DialogContent className="sm:max-w-lg" data-dialog="edit-list">
        <form onSubmit={submit} noValidate className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>{t("lists.editTitle")}</DialogTitle>
            <DialogDescription>{t("lists.editHint")}</DialogDescription>
          </DialogHeader>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="edit-list-name">{t("lists.name")}</FieldLabel>
              <Input
                id="edit-list-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={80}
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="edit-list-description">
                {t("lists.description")}{" "}
                <span className="text-muted-foreground">({t("common.optional")})</span>
              </FieldLabel>
              <Input
                id="edit-list-description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                maxLength={500}
              />
            </Field>
            <Field orientation="horizontal">
              <Switch id="edit-list-doi" checked={doi} onCheckedChange={(v) => setDoi(!!v)} />
              <div>
                <FieldLabel htmlFor="edit-list-doi">{t("lists.doubleOptIn")}</FieldLabel>
                <FieldDescription>{t("lists.doubleOptInEditHint")}</FieldDescription>
              </div>
            </Field>
          </FieldGroup>
          {err ? <FieldError>{err}</FieldError> : null}
          <DialogFooter>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** Download a list (under the current status and search) as CSV. */
export async function exportList(
  workspaceId: string,
  list: { id: string; name: string },
  filter: { status?: string; q?: string },
  messages: { ok: string; fail: string },
) {
  const qs = new URLSearchParams()
  if (filter.status) qs.set("status", filter.status)
  if (filter.q?.trim()) qs.set("q", filter.q.trim())
  const path = `/api/w/mail/lists/${encodeURIComponent(list.id)}/export.csv${qs.size ? `?${qs}` : ""}`
  try {
    await downloadFile(path, { workspaceId, fallbackName: `${list.name}.csv` })
    toast.success(messages.ok)
  } catch (e) {
    toast.error(e instanceof ApiError ? e.message : messages.fail)
  }
}
