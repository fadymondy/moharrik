"use client"

import { useState } from "react"
import { toast } from "sonner"

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
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

/** What the consent control needs of a contact: the contacts list and the contact page both have it. */
export type ConsentContact = {
  id: string
  name: string
  email: string
  phone: string
  whatsapp_opt_in_at: string | null
  whatsapp_opt_out_at: string | null
}

/*
WhatsApp consent (MH-320), its own thing and never implied by a list.

Three states, each shown as what it is. Recording an opt-in is a member's
note that the contact agreed. For a contact who said STOP the control is NOT
a re-tick: it is a separately worded, confirmed reversal, and a manager's call.
*/
export function WhatsAppConsent({ contact, onChange }: { contact: ConsentContact; onChange: () => void }) {
  const { t, formatDate } = useTranslations()
  const ws = useWorkspace()
  const [open, setOpen] = useState<null | "optin" | "reverse">(null)
  const [busy, setBusy] = useState(false)
  const optedOut = !!contact.whatsapp_opt_out_at
  const optedIn = !optedOut && !!contact.whatsapp_opt_in_at
  const noPhone = !contact.phone?.trim()
  const who = contact.name || contact.email

  async function run(kind: "optin" | "reverse") {
    setBusy(true)
    try {
      const path = kind === "optin" ? "opt-in" : "reverse-opt-out"
      await api(`/api/w/contacts/${encodeURIComponent(contact.id)}/whatsapp/${path}`, {
        method: "POST",
        json: kind === "reverse" ? { confirm: true } : undefined,
        workspaceId: ws.id,
      })
      toast.success(kind === "optin" ? t("contacts.waOptInRecorded") : t("contacts.waReversed"))
      setOpen(null)
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-1.5" data-wa-consent={optedOut ? "opted_out" : optedIn ? "opted_in" : "none"}>
      {optedOut ? (
        <Badge variant="destructive" title={formatDate(contact.whatsapp_opt_out_at!)}>
          {t("contacts.waOptedOut")}
        </Badge>
      ) : optedIn ? (
        <Badge variant="default" title={formatDate(contact.whatsapp_opt_in_at!)}>
          {t("contacts.waOptedIn")}
        </Badge>
      ) : (
        <span className="text-xs text-grid-muted">{t("contacts.waNone")}</span>
      )}
      {optedIn && noPhone ? <span className="text-xs text-grid-warn">{t("contacts.waNoPhone")}</span> : null}

      {!optedOut && !optedIn && ws.can("contacts:write") ? (
        <Button variant="link" size="sm" className="h-auto px-1 text-xs" onClick={() => setOpen("optin")}>
          {t("contacts.waRecordOptIn")}
        </Button>
      ) : null}
      {optedOut && ws.can("campaigns:write") ? (
        <Button variant="link" size="sm" className="h-auto px-1 text-xs text-grid-danger-text" onClick={() => setOpen("reverse")}>
          {t("contacts.waReverse")}
        </Button>
      ) : null}

      <AlertDialog open={open !== null} onOpenChange={(v) => (v ? null : setOpen(null))}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{open === "reverse" ? t("contacts.waReverseTitle") : t("contacts.waOptInTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {open === "reverse" ? t("contacts.waReverseBody", { who }) : t("contacts.waOptInBody", { who })}
              {noPhone ? ` ${t("contacts.waOptInNoPhone")}` : ""}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              variant={open === "reverse" ? "destructive" : "default"}
              disabled={busy}
              onClick={() => open && run(open)}
            >
              {busy ? t("common.working") : open === "reverse" ? t("contacts.waReverseConfirm") : t("contacts.waOptInConfirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
