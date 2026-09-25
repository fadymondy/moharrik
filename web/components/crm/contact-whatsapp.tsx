"use client"

import Link from "next/link"
import { useState, type FormEvent } from "react"
import { CheckCircle2Icon, InfoIcon, MessageCircleIcon, XCircleIcon } from "lucide-react"
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
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldLabel } from "@/components/ui/field"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Textarea } from "@/components/ui/textarea"
import { Ltr } from "@/components/copy-field"
import { Bdi } from "@/components/bidi"
import { WhatsAppConsent } from "@/components/crm/whatsapp-consent"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type DirectSendResult } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useContactWhatsApp } from "@/lib/queries"

const MAX = 4096

/*
"Message on WhatsApp" on a contact's page (MH-410).

The API decides whether a message may go, and says why not; this only shows
it. A disabled button always has its reason written next to it: no consent,
a STOP, no number, no linked WhatsApp, or a limit reached.

A message that opens a new thread goes through a confirm step: the first call
returns a one-time code bound to this contact and this exact text, and the
dialog repeats the call with it. The same rule an agent meets over MCP.
*/
export function ContactWhatsAppPanel({
  contact,
}: {
  contact: { id: string; name: string; email: string; phone: string }
}) {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const canRead = ws.can("inbox:read")
  const canWrite = ws.can("inbox:write")
  const st = useContactWhatsApp(ws.id, contact.id, canRead)
  const [open, setOpen] = useState(false)

  if (!canRead) return null
  const data = st.data
  const who = contact.name || contact.email
  const threadHref = data?.conversation_id
    ? `/${locale}/w/${ws.slug}/inbox?c=${encodeURIComponent(data.conversation_id)}`
    : null

  return (
    <section
      className="px-6 pb-6"
      data-contact-whatsapp={data ? (data.can_send ? "ready" : data.reason) : "loading"}
    >
      <div className="flex flex-col gap-3 border border-line bg-grid-card px-4 py-3 sm:flex-row sm:items-center">
        <div className="flex min-w-0 flex-1 flex-col gap-1.5">
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <MessageCircleIcon className="size-4 shrink-0 text-grid-muted" />
            <span className="font-medium">{t("contacts.whatsapp")}</span>
            {data?.phone ? (
              <span className="text-grid-muted">
                <Ltr>+{data.phone}</Ltr>
              </span>
            ) : null}
            {data ? (
              <WhatsAppConsent
                contact={{
                  ...contact,
                  whatsapp_opt_in_at: data.whatsapp_opt_in_at,
                  whatsapp_opt_out_at: data.whatsapp_opt_out_at,
                }}
                onChange={() => st.mutate()}
              />
            ) : null}
          </div>
          {data && !data.can_send ? (
            <p
              id="wa-disabled-reason"
              className="flex items-start gap-1.5 text-xs text-grid-muted"
              data-whatsapp-disabled-reason={data.reason}
            >
              <InfoIcon className="mt-0.5 size-3.5 shrink-0" />
              <span>
                {t(`contactWa.reason.${data.reason ?? "unavailable"}`)}{" "}
                {data.reason === "not_linked" && ws.can("inbox:manage") ? (
                  <Link
                    className="underline underline-offset-4"
                    href={`/${locale}/w/${ws.slug}/inbox/whatsapp`}
                  >
                    {t("contactWa.linkNumber")}
                  </Link>
                ) : null}
              </span>
            </p>
          ) : null}
          {threadHref ? (
            <Link
              className="text-xs underline underline-offset-4"
              href={threadHref}
              data-whatsapp-thread
            >
              {t("contactWa.openThread")}
            </Link>
          ) : null}
        </div>
        {canWrite ? (
          <Button
            variant="outline"
            size="sm"
            disabled={!data?.can_send}
            onClick={() => setOpen(true)}
            data-message-whatsapp
            aria-describedby={data && !data.can_send ? "wa-disabled-reason" : undefined}
          >
            <MessageCircleIcon />
            {t("contactWa.message")}
          </Button>
        ) : null}
      </div>

      {data && data.recent.length > 0 ? (
        <details className="mt-2 text-xs text-grid-muted" data-whatsapp-log>
          <summary className="cursor-pointer select-none">
            {t("contactWa.log", { count: data.recent.length })}
          </summary>
          <ul className="mt-2 flex flex-col gap-1">
            {data.recent.map((r, i) => (
              <li key={i} className="flex flex-wrap items-center gap-2">
                {r.ok ? (
                  <CheckCircle2Icon className="size-3.5 text-grid-brand" />
                ) : (
                  <XCircleIcon className="size-3.5 text-grid-danger-text" />
                )}
                <Ltr>{formatDate(r.at)}</Ltr>
                <span>· {t(`contactWa.kind.${r.kind}`)}</span>
                {!r.ok && r.error ? (
                  <span className="text-grid-danger-text">· {r.error}</span>
                ) : null}
              </li>
            ))}
          </ul>
        </details>
      ) : null}

      <MessageSheet
        open={open}
        onOpenChange={setOpen}
        contactId={contact.id}
        who={who}
        newThread={!data?.conversation_id}
        onSent={() => st.mutate()}
      />
    </section>
  )
}

function MessageSheet({
  open,
  onOpenChange,
  contactId,
  who,
  newThread,
  onSent,
}: {
  open: boolean
  onOpenChange: (v: boolean) => void
  contactId: string
  who: string
  newThread: boolean
  onSent: () => void
}) {
  const { t, isRtl, locale } = useTranslations()
  const ws = useWorkspace()
  const [text, setText] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  // The confirm step for a new thread: the code the first call returned.
  const [confirm, setConfirm] = useState<{ code: string; summary: string } | null>(null)
  const [sentTo, setSentTo] = useState<string | null>(null)

  async function send(code?: string) {
    setBusy(true)
    setErr(null)
    try {
      const res = await api<DirectSendResult>(
        `/api/w/contacts/${encodeURIComponent(contactId)}/whatsapp/send`,
        {
          json: { text: text.trim(), ...(code ? { code } : {}) },
          workspaceId: ws.id,
        },
      )
      if (res.confirmation_required && res.code) {
        setConfirm({ code: res.code, summary: res.summary })
        return
      }
      setConfirm(null)
      setText("")
      setSentTo(res.conversation_id ?? null)
      toast.success(t("contactWa.sent", { who }))
      onSent()
    } catch (e) {
      setConfirm(null)
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      onSent()
    } finally {
      setBusy(false)
    }
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    void send()
  }

  const len = [...text].length
  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v)
        if (!v) {
          setSentTo(null)
          setErr(null)
        }
      }}
    >
      <SheetContent
        side={isRtl ? "left" : "right"}
        className="w-full overflow-y-auto data-[side=left]:w-full data-[side=right]:w-full data-[side=left]:sm:max-w-lg data-[side=right]:sm:max-w-lg"
        data-whatsapp-sheet
      >
        <SheetHeader className="pe-12">
          <SheetTitle>{t("contactWa.title", { who })}</SheetTitle>
          <SheetDescription>
            {newThread ? t("contactWa.hintNew") : t("contactWa.hintThread")}
          </SheetDescription>
        </SheetHeader>
        <div className="px-4 pb-6">
          {sentTo ? (
            <div className="flex flex-col items-start gap-3 text-sm" data-whatsapp-sent>
              <p className="flex items-center gap-2">
                <CheckCircle2Icon className="size-4 text-grid-brand" />
                {t("contactWa.sent", { who })}
              </p>
              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  nativeButton={false}
                  render={
                    <Link
                      href={`/${locale}/w/${ws.slug}/inbox?c=${encodeURIComponent(sentTo)}`}
                      data-open-thread
                    />
                  }
                >
                  {t("contactWa.openThread")}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setSentTo(null)}>
                  {t("contactWa.another")}
                </Button>
              </div>
            </div>
          ) : (
            <form onSubmit={onSubmit} noValidate data-form="whatsapp-message">
              <Field>
                <FieldLabel htmlFor="wa-text">{t("contactWa.messageLabel")}</FieldLabel>
                <Textarea
                  id="wa-text"
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  rows={6}
                  maxLength={MAX}
                  dir="auto"
                  placeholder={t("contactWa.placeholder")}
                />
                <FieldDescription>
                  <Ltr>
                    {len} / {MAX}
                  </Ltr>{" "}
                  · {t("contactWa.pacing")}
                </FieldDescription>
              </Field>
              <div className="mt-4 flex items-center gap-2">
                <Button type="submit" disabled={busy || !text.trim() || len > MAX}>
                  <MessageCircleIcon />
                  {busy ? t("common.working") : t("contactWa.send")}
                </Button>
                <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
                  {t("common.cancel")}
                </Button>
              </div>
              {err ? <FieldError className="mt-3">{err}</FieldError> : null}
            </form>
          )}
        </div>
      </SheetContent>

      <AlertDialog open={!!confirm} onOpenChange={(v) => (!v ? setConfirm(null) : undefined)}>
        <AlertDialogContent data-whatsapp-confirm>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("contactWa.confirmTitle", { who })}</AlertDialogTitle>
            <AlertDialogDescription>{t("contactWa.confirmBody")}</AlertDialogDescription>
          </AlertDialogHeader>
          <blockquote
            className="max-h-48 overflow-y-auto border-s-2 border-line ps-3 text-sm whitespace-pre-wrap"
            dir="auto"
          >
            <Bdi>{text.trim()}</Bdi>
          </blockquote>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction disabled={busy} onClick={() => confirm && send(confirm.code)}>
              {busy ? t("common.working") : t("contactWa.confirmSend")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </Sheet>
  )
}
