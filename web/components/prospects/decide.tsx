"use client"

import { useState, type FormEvent } from "react"
import { CheckIcon, XIcon } from "lucide-react"
import { toast } from "sonner"

import { isolate } from "@/components/bidi"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Prospect } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

/** Accept and Reject, each behind a short dialog (MH-397). */
export function DecideButtons({ p, onDone }: { p: Prospect; onDone: () => void }) {
  const { t } = useTranslations()
  const [open, setOpen] = useState<"accept" | "reject" | null>(null)
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button size="sm" onClick={() => setOpen("accept")} data-action="accept-prospect">
        <CheckIcon />
        {t("prospects.accept")}
      </Button>
      <Button size="sm" variant="outline" onClick={() => setOpen("reject")} data-action="reject-prospect">
        <XIcon />
        {t("prospects.reject")}
      </Button>
      {open === "accept" ? <AcceptDialog p={p} onClose={() => setOpen(null)} onDone={onDone} /> : null}
      {open === "reject" ? <RejectDialog p={p} onClose={() => setOpen(null)} onDone={onDone} /> : null}
    </div>
  )
}

function AcceptDialog({ p, onClose, onDone }: { p: Prospect; onClose: () => void; onDone: () => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState(p.contact.name)
  const [title, setTitle] = useState(p.contact.title)
  const [email, setEmail] = useState(p.contact.email)
  const [deal, setDeal] = useState(`${p.name} — ${locale === "ar" ? "عميل محتمل" : "prospect"}`)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{ contact_id: string }>(`/api/w/prospects/${encodeURIComponent(p.id)}/accept`, {
        json: { contact_name: name.trim(), contact_title: title.trim(), contact_email: email.trim(), deal_title: deal.trim() },
        workspaceId: ws.id,
      })
      toast.success(res.contact_id ? t("prospects.accepted", { name: isolate(p.name) }) : t("prospects.acceptedNoContact", { name: isolate(p.name) }))
      onClose()
      onDone()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent data-dialog="accept-prospect">
        <DialogHeader>
          <DialogTitle>{t("prospects.acceptTitle", { name: isolate(p.name) })}</DialogTitle>
          <DialogDescription>{t("prospects.acceptBody")}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} noValidate>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="pa-name">{t("prospects.contactName")}</FieldLabel>
              <Input id="pa-name" dir="auto" maxLength={120} value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="pa-title">{t("prospects.contactTitle")}</FieldLabel>
              <Input id="pa-title" dir="auto" maxLength={120} value={title} onChange={(e) => setTitle(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="pa-email">{t("prospects.contactEmail")}</FieldLabel>
              <Input id="pa-email" type="email" dir="ltr" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="name@company.com" />
              <FieldDescription>{t("prospects.contactEmailHint")}</FieldDescription>
            </Field>
            <Field>
              <FieldLabel htmlFor="pa-deal">{t("prospects.dealTitle")}</FieldLabel>
              <Input id="pa-deal" dir="auto" maxLength={160} value={deal} onChange={(e) => setDeal(e.target.value)} />
            </Field>
          </FieldGroup>
          {err ? <FieldError className="mt-3">{err}</FieldError> : null}
          <DialogFooter className="mt-6">
            <Button type="button" variant="ghost" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy} data-action="confirm-accept-prospect">
              <CheckIcon />
              {busy ? t("common.working") : t("prospects.accept")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RejectDialog({ p, onClose, onDone }: { p: Prospect; onClose: () => void; onDone: () => void }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [reason, setReason] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api(`/api/w/prospects/${encodeURIComponent(p.id)}/reject`, { json: { reason: reason.trim() }, workspaceId: ws.id })
      toast.success(t("prospects.rejected"))
      onClose()
      onDone()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(v) => !v && onClose()}>
      <DialogContent data-dialog="reject-prospect">
        <DialogHeader>
          <DialogTitle>{t("prospects.rejectTitle", { name: isolate(p.name) })}</DialogTitle>
          <DialogDescription>{t("prospects.rejectBody")}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} noValidate>
          <Field>
            <FieldLabel htmlFor="pr-reason">{t("prospects.rejectReason")}</FieldLabel>
            <Textarea id="pr-reason" dir="auto" rows={2} maxLength={500} value={reason} onChange={(e) => setReason(e.target.value)} />
          </Field>
          {err ? <FieldError className="mt-3">{err}</FieldError> : null}
          <DialogFooter className="mt-6">
            <Button type="button" variant="ghost" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" variant="destructive" disabled={busy} data-action="confirm-reject-prospect">
              <XIcon />
              {busy ? t("common.working") : t("prospects.reject")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
