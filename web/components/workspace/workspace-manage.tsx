"use client"

import { useRef, useState, type ChangeEvent, type FormEvent } from "react"
import { useRouter } from "next/navigation"
import { LogOutIcon, Trash2Icon } from "lucide-react"
import { toast } from "sonner"
import { mutate } from "swr"

import { Button } from "@/components/ui/button"
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Ltr } from "@/components/copy-field"
import { BdiBlock, isolate } from "@/components/bidi"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  WORKSPACE_AVATAR_TYPES,
  WorkspaceMark,
  prepareWorkspaceAvatar,
} from "@/components/workspace/workspace-mark"
import { api, ApiError, type WorkspaceProfile as Profile } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useCurrentWorkspace, useMe } from "@/lib/queries"

/*
Managing the workspace itself (MH-679), on Settings → General: its name and
picture, leaving it, and — for owners — deleting it. Every change refreshes
/api/me/workspaces too, since the switcher and the workspaces list draw the
name and picture from there.
*/

const WORKSPACES_KEY = "/api/me/workspaces"

function errorText(err: unknown, fallback: string) {
  return err instanceof ApiError ? err.message : fallback
}

export function WorkspaceProfileSection() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const current = useCurrentWorkspace(ws.id)
  const canWrite = ws.can("workspace:update")
  const fileRef = useRef<HTMLInputElement>(null)
  const [name, setName] = useState<string | null>(null)
  const [savingName, setSavingName] = useState(false)
  const [nameErr, setNameErr] = useState<string | null>(null)
  const [picBusy, setPicBusy] = useState(false)
  const [picErr, setPicErr] = useState<string | null>(null)

  const storedName = current.data?.workspace.name ?? ws.summary.name
  const avatarUrl = current.data?.workspace.avatar_url ?? ws.summary.avatar_url ?? ""
  const value = name ?? storedName

  async function refresh() {
    await Promise.all([current.mutate(), mutate(WORKSPACES_KEY)])
  }

  async function saveName(e: FormEvent) {
    e.preventDefault()
    const next = value.trim()
    if (!next) {
      setNameErr(t("manage.nameRequired"))
      return
    }
    setSavingName(true)
    setNameErr(null)
    try {
      await api<Profile>("/api/w/workspace", {
        method: "PUT",
        json: { name: next },
        workspaceId: ws.id,
      })
      await refresh()
      // Keep anything typed while the save was in flight.
      setName((typed) => (typed === null || typed.trim() === next ? null : typed))
      toast.success(t("manage.renamed"))
    } catch (err) {
      setNameErr(errorText(err, t("common.networkError")))
    } finally {
      setSavingName(false)
    }
  }

  async function onPick(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    e.target.value = ""
    if (!file) return
    setPicErr(null)
    if (!WORKSPACE_AVATAR_TYPES.includes(file.type)) {
      setPicErr(t("manage.pictureBadType"))
      return
    }
    setPicBusy(true)
    try {
      const body = await prepareWorkspaceAvatar(file)
      await api<Profile>("/api/w/workspace/avatar", {
        method: "PUT",
        json: body,
        workspaceId: ws.id,
      })
      await refresh()
      toast.success(t("manage.pictureSaved"))
    } catch (err) {
      setPicErr(errorText(err, t("manage.pictureUnreadable")))
    } finally {
      setPicBusy(false)
    }
  }

  async function removePicture() {
    setPicBusy(true)
    setPicErr(null)
    try {
      await api<Profile>("/api/w/workspace/avatar", { method: "DELETE", workspaceId: ws.id })
      await refresh()
      toast.success(t("manage.pictureRemoved"))
    } catch (err) {
      setPicErr(errorText(err, t("common.networkError")))
    } finally {
      setPicBusy(false)
    }
  }

  return (
    <section className="px-6 py-6" data-workspace-profile>
      <h2 className="text-base font-medium">{t("manage.profileTitle")}</h2>
      <p className="mt-1 mb-5 text-sm text-grid-muted">
        {canWrite ? t("manage.profileHint") : t("generalSettings.adminOnly")}
      </p>
      <div className="flex items-center gap-4">
        <WorkspaceMark name={storedName} avatarUrl={avatarUrl} size="lg" />
        {canWrite ? (
          <div className="flex flex-col gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <input
                ref={fileRef}
                type="file"
                accept={WORKSPACE_AVATAR_TYPES.join(",")}
                className="hidden"
                onChange={onPick}
                data-workspace-avatar-input
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={picBusy}
                onClick={() => fileRef.current?.click()}
                data-workspace-avatar-upload
              >
                {picBusy
                  ? t("common.working")
                  : avatarUrl
                    ? t("manage.pictureReplace")
                    : t("manage.pictureUpload")}
              </Button>
              {avatarUrl ? (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  disabled={picBusy}
                  onClick={removePicture}
                  data-workspace-avatar-remove
                >
                  {t("manage.pictureRemove")}
                </Button>
              ) : null}
            </div>
            {picErr ? (
              <FieldError>{picErr}</FieldError>
            ) : (
              <p className="text-xs text-grid-muted">{t("manage.pictureHint")}</p>
            )}
          </div>
        ) : (
          <BdiBlock className="text-lg font-medium">{storedName}</BdiBlock>
        )}
      </div>

      {canWrite ? (
        <form onSubmit={saveName} noValidate className="mt-6">
          <FieldGroup className="max-w-md">
            <Field>
              <FieldLabel htmlFor="workspace-name">{t("workspaces.name")}</FieldLabel>
              <div className="flex gap-2">
                <Input
                  id="workspace-name"
                  value={value}
                  maxLength={120}
                  onChange={(e) => setName(e.target.value)}
                  aria-invalid={!!nameErr || undefined}
                  data-workspace-name
                />
                <Button
                  type="submit"
                  disabled={savingName || value.trim() === storedName}
                  data-workspace-name-save
                >
                  {savingName ? t("common.working") : t("common.save")}
                </Button>
              </div>
              {nameErr ? (
                <FieldError>{nameErr}</FieldError>
              ) : (
                <FieldDescription>
                  {t("manage.addressFixed")} <Ltr mono>/{ws.slug}</Ltr>
                </FieldDescription>
              )}
            </Field>
          </FieldGroup>
        </form>
      ) : null}
    </section>
  )
}

export function LeaveWorkspaceSection() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const me = useMe()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const myId = me.data?.id

  async function leave() {
    if (myId == null) return
    setBusy(true)
    try {
      await api(`/api/w/members/${encodeURIComponent(String(myId))}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      setOpen(false)
      router.replace(`/${locale}/workspaces`)
      await mutate(WORKSPACES_KEY)
      toast.success(t("manage.left", { name: isolate(ws.summary.name) }))
    } catch (err) {
      toast.error(errorText(err, t("common.networkError")))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section
      className="flex flex-wrap items-center justify-between gap-4 px-6 py-6"
      data-leave-workspace
    >
      <div className="max-w-xl">
        <h2 className="text-base font-medium">{t("manage.leaveTitle")}</h2>
        <p className="mt-1 text-sm text-grid-muted">
          {ws.summary.role === "owner" ? t("manage.leaveHintOwner") : t("manage.leaveHint")}
        </p>
      </div>
      <Button
        variant="outline"
        onClick={() => setOpen(true)}
        disabled={myId == null}
        data-leave-open
      >
        <LogOutIcon className="rtl:rotate-180" />
        {t("manage.leave")}
      </Button>
      <AlertDialog open={open} onOpenChange={setOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("manage.leaveConfirmTitle", { name: isolate(ws.summary.name) })}
            </AlertDialogTitle>
            <AlertDialogDescription>{t("manage.leaveConfirmBody")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <Button variant="destructive" disabled={busy} onClick={leave} data-leave-confirm>
              {busy ? t("common.working") : t("manage.leave")}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

export function DeleteWorkspaceSection() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const [open, setOpen] = useState(false)
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  if (!ws.can("workspace:delete")) return null

  async function remove(e: FormEvent) {
    e.preventDefault()
    if (typed.trim() !== ws.slug) return
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/workspace?confirm=${encodeURIComponent(ws.slug)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      setOpen(false)
      router.replace(`/${locale}/workspaces`)
      await mutate(WORKSPACES_KEY)
      toast.success(t("manage.deleted", { name: isolate(ws.summary.name) }))
    } catch (error) {
      setErr(errorText(error, t("common.networkError")))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="px-6 py-6" data-delete-workspace>
      <div className="flex flex-wrap items-center justify-between gap-4 border border-grid-danger-text/40 p-4">
        <div className="max-w-xl">
          <h2 className="text-base font-medium text-grid-danger-text">{t("manage.deleteTitle")}</h2>
          <p className="mt-1 text-sm text-grid-muted">{t("manage.deleteHint")}</p>
        </div>
        <Button
          variant="destructive"
          onClick={() => {
            setTyped("")
            setErr(null)
            setOpen(true)
          }}
          data-delete-open
        >
          <Trash2Icon />
          {t("manage.delete")}
        </Button>
      </div>
      <AlertDialog open={open} onOpenChange={setOpen}>
        <AlertDialogContent>
          <form onSubmit={remove} noValidate className="grid gap-4">
            <AlertDialogHeader>
              <AlertDialogTitle>
                {t("manage.deleteConfirmTitle", { name: isolate(ws.summary.name) })}
              </AlertDialogTitle>
              <AlertDialogDescription>{t("manage.deleteConfirmBody")}</AlertDialogDescription>
            </AlertDialogHeader>
            <Field>
              <FieldLabel htmlFor="delete-confirm">
                {t("manage.deleteType")} <Ltr mono>{ws.slug}</Ltr>
              </FieldLabel>
              <Input
                id="delete-confirm"
                dir="ltr"
                autoComplete="off"
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                data-delete-confirm-input
              />
              {err ? <FieldError>{err}</FieldError> : null}
            </Field>
            <AlertDialogFooter>
              <AlertDialogCancel type="button">{t("common.cancel")}</AlertDialogCancel>
              <Button
                type="submit"
                variant="destructive"
                disabled={busy || typed.trim() !== ws.slug}
                data-delete-confirm
              >
                {busy ? t("common.working") : t("manage.deleteForever")}
              </Button>
            </AlertDialogFooter>
          </form>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}
