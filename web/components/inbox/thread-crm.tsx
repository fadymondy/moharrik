"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useState, type FormEvent } from "react"
import { BriefcaseIcon, LinkIcon, SparklesIcon, UnlinkIcon, UserPlusIcon } from "lucide-react"
import {
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  FieldDescription,
  FieldLabel,
  Input,
  toast,
  TooltipRoot,
  TooltipContent,
  TooltipTrigger,
} from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError, FormHint } from "@/components/form-layout"

import { ContactPicker } from "@/components/crm/contact-picker"
import { PickSelect, toMinor } from "@/components/crm/shared"
import { Bdi, isolate } from "@/components/bidi"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type Conversation, type VisitorGuess } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { LOCALE_NAMES } from "@/lib/i18n-locale"
import { usePipelineStages } from "@/lib/queries"

type Done = () => void

/**
 * Where a conversation stands in the CRM (MH-379): the contact it is linked to
 * (and their company), the deal it became, and the three ways to get there —
 * link an existing contact, create one from what the visitor gave, open a deal
 * with the transcript attached.
 */
export function ThreadCRM({
  c,
  visitor,
  onChanged,
}: {
  c: Conversation
  visitor: VisitorGuess
  onChanged: Done
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  // Every action here writes to both the inbox and the CRM.
  const canWrite = ws.can("inbox:write") && ws.can("contacts:write")
  const base = `/${locale}/w/${ws.slug}`
  const [dialog, setDialog] = useState<"link" | "create" | "deal" | null>(null)

  async function unlink() {
    try {
      await api(`/api/w/conversations/${encodeURIComponent(c.id)}`, {
        method: "PATCH",
        json: { contact_id: "" },
        workspaceId: ws.id,
      })
      toast.success(t("inbox.crm.unlinked"))
      onChanged()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const who = c.contact ? c.contact.name || c.contact.email : ""

  return (
    <div
      className="@container/crm flex items-center gap-x-3 border-b border-nq-line bg-nq-surface-soft/40 px-4 py-1.5 text-sm"
      data-thread-crm
      data-thread-channel={c.channel}
    >
      <span className="eyebrow hidden shrink-0 @2xl/crm:inline">{t("inbox.crm.title")}</span>
      <span className="flex min-w-0 flex-1 items-center gap-x-3 overflow-hidden">
        {c.contact ? (
          <span className="flex min-w-0 items-center gap-x-2" data-crm-contact={c.contact.id}>
            <span className="hidden shrink-0 text-muted-foreground @lg/crm:inline">
              {t("inbox.crm.contact")}:
            </span>
            <Link
              href={`${base}/contacts/${c.contact.id}`}
              className="truncate font-medium underline-offset-4 hover:underline"
            >
              <Bdi>{who}</Bdi>
            </Link>
            {c.contact.company ? (
              <>
                <span className="text-muted-foreground max-@md/crm:hidden" aria-hidden>
                  ·
                </span>
                <Link
                  href={`${base}/companies/${c.contact.company.id}`}
                  className="truncate underline-offset-4 hover:underline max-@md/crm:hidden"
                  data-crm-company
                >
                  <Bdi>{c.contact.company.name}</Bdi>
                </Link>
              </>
            ) : null}
            {c.contact_link === "auto" ? (
              <TooltipRoot>
                <TooltipTrigger render={<Badge variant="neutral" data-crm-auto />}>
                  <SparklesIcon />
                  {t("inbox.crm.auto")}
                </TooltipTrigger>
                <TooltipContent>
                  {t(
                    c.channel === "whatsapp"
                      ? "inbox.crm.autoHint.phone"
                      : "inbox.crm.autoHint.email",
                  )}
                </TooltipContent>
              </TooltipRoot>
            ) : null}
          </span>
        ) : (
          <span className="truncate text-muted-foreground" data-crm-contact="">
            {t("inbox.crm.notLinked")}
          </span>
        )}

        {c.deal ? (
          <span className="flex min-w-0 items-center gap-2" data-crm-deal={c.deal.id}>
            <span className="hidden shrink-0 text-muted-foreground @lg/crm:inline">
              {t("inbox.crm.deal")}:
            </span>
            <BriefcaseIcon
              className="size-3.5 shrink-0 text-muted-foreground @lg/crm:hidden"
              aria-hidden
            />
            <Link
              href={`${base}/deals/${c.deal.id}`}
              className="truncate font-medium underline-offset-4 hover:underline"
            >
              <Bdi>{c.deal.title}</Bdi>
            </Link>
          </span>
        ) : null}
      </span>

      {/* Icon-only below a 28rem thread; the name stays as the tooltip and the label. */}
      {canWrite ? (
        <span className="flex shrink-0 items-center gap-1 [&_[data-crm-label]]:hidden @md/crm:[&_[data-crm-label]]:inline">
          {c.contact ? (
            <>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setDialog("link")}
                data-crm-action="change"
                title={t("inbox.crm.change")}
                aria-label={t("inbox.crm.change")}
              >
                <LinkIcon />
                <span data-crm-label>{t("inbox.crm.change")}</span>
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={unlink}
                data-crm-action="unlink"
                title={t("inbox.crm.unlink")}
                aria-label={t("inbox.crm.unlink")}
              >
                <UnlinkIcon />
                <span data-crm-label>{t("inbox.crm.unlink")}</span>
              </Button>
            </>
          ) : (
            <>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setDialog("link")}
                data-crm-action="link"
                title={t("inbox.crm.link")}
                aria-label={t("inbox.crm.link")}
              >
                <LinkIcon />
                <span data-crm-label>{t("inbox.crm.link")}</span>
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setDialog("create")}
                data-crm-action="create"
                title={t("inbox.crm.create")}
                aria-label={t("inbox.crm.create")}
              >
                <UserPlusIcon />
                <span data-crm-label>{t("inbox.crm.create")}</span>
              </Button>
            </>
          )}
          {!c.deal ? (
            <Button
              variant="secondary"
              size="sm"
              onClick={() => setDialog("deal")}
              data-crm-action="deal"
              title={t("inbox.crm.openDeal")}
              aria-label={t("inbox.crm.openDeal")}
              disabled={c.message_count === 0}
            >
              <BriefcaseIcon />
              <span data-crm-label>{t("inbox.crm.openDeal")}</span>
            </Button>
          ) : null}
        </span>
      ) : null}

      <LinkContactDialog
        open={dialog === "link"}
        onOpenChange={(v) => setDialog(v ? "link" : null)}
        c={c}
        onDone={onChanged}
      />
      <CreateContactDialog
        open={dialog === "create"}
        onOpenChange={(v) => setDialog(v ? "create" : null)}
        c={c}
        visitor={visitor}
        onDone={onChanged}
      />
      <OpenDealDialog
        open={dialog === "deal"}
        onOpenChange={(v) => setDialog(v ? "deal" : null)}
        c={c}
        onDone={onChanged}
      />
    </div>
  )
}

// Each dialog's form is its own component: Base UI unmounts a closed dialog's
// content, so the form's state starts fresh (and prefilled) on every opening.

type DialogProps = {
  open: boolean
  onOpenChange: (v: boolean) => void
  c: Conversation
  onDone: Done
}

export function LinkContactDialog({ open, onOpenChange, c, onDone }: DialogProps) {
  const { t } = useTranslations()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("inbox.crm.linkTitle")}</DialogTitle>
          <DialogDescription>{t("inbox.crm.linkHint")}</DialogDescription>
        </DialogHeader>
        <LinkContactForm c={c} onDone={() => (onDone(), onOpenChange(false))} />
      </DialogContent>
    </Dialog>
  )
}

function LinkContactForm({ c, onDone }: { c: Conversation; onDone: Done }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [contactId, setContactId] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!contactId) return
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{ conversation: Conversation }>(
        `/api/w/conversations/${encodeURIComponent(c.id)}`,
        {
          method: "PATCH",
          json: { contact_id: contactId },
          workspaceId: ws.id,
        },
      )
      const linked = res.conversation.contact
      toast.success(
        t("inbox.crm.linked", { who: isolate(linked ? linked.name || linked.email : "") }),
      )
      onDone()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4" data-form="link-contact">
      <Field>
        <FieldLabel htmlFor="link-contact">{t("inbox.crm.contact")}</FieldLabel>
        <ContactPicker id="link-contact" value={contactId} onChange={setContactId} />
      </Field>
      {err ? <FieldError>{err}</FieldError> : null}
      <DialogFooter>
        <Button type="submit" disabled={busy || !contactId}>
          {busy ? t("common.working") : t("inbox.crm.linkAction")}
        </Button>
      </DialogFooter>
    </form>
  )
}

export function CreateContactDialog({
  open,
  onOpenChange,
  c,
  visitor,
  onDone,
}: DialogProps & { visitor: VisitorGuess }) {
  const { t } = useTranslations()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("inbox.crm.createTitle")}</DialogTitle>
          <DialogDescription>{t("inbox.crm.createHint")}</DialogDescription>
        </DialogHeader>
        <CreateContactForm c={c} visitor={visitor} onDone={() => (onDone(), onOpenChange(false))} />
      </DialogContent>
    </Dialog>
  )
}

/** Exported for the inbox row's contact dialog (MH-595): the SAME create/link
 * form and POST /api/w/conversations/{id}/contact call, seeded from whatever
 * visitor guess the caller has to hand — a list row has only its own
 * customer_name/email/phone/locale, not the full VisitorGuess a thread loads. */
export function CreateContactForm({
  c,
  visitor,
  onDone,
}: {
  c: Conversation
  visitor: VisitorGuess
  onDone: Done
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  // Prefilled from what the visitor gave (the API reads it from the
  // conversation and from their messages).
  const [name, setName] = useState(visitor.name)
  const [email, setEmail] = useState(visitor.email)
  const [phone, setPhone] = useState(visitor.phone)
  const [lang, setLang] = useState<"en" | "ar">(visitor.locale === "ar" ? "ar" : "en")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    if (!email.trim() && !phone.trim()) {
      setErr(t("inbox.crm.identityRequired"))
      return
    }
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{
        contact_id: string
        contact_created: boolean
        conversation: Conversation
      }>(`/api/w/conversations/${encodeURIComponent(c.id)}/contact`, {
        json: { email: email.trim(), name: name.trim(), phone: phone.trim(), locale: lang },
        workspaceId: ws.id,
      })
      const who = res.conversation.contact
        ? res.conversation.contact.name || res.conversation.contact.email || phone
        : email || phone
      toast.success(
        res.contact_created ? t("inbox.crm.created") : t("inbox.crm.reused", { who: isolate(who) }),
      )
      onDone()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-4" data-form="create-contact">
      <FieldGroup className="grid gap-4 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="new-contact-name">{t("contacts.name")}</FieldLabel>
          <Input
            id="new-contact-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            maxLength={120}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="new-contact-email">
            {t("contacts.email")}{" "}
            <span className="text-muted-foreground">({t("common.optional")})</span>
          </FieldLabel>
          <Input
            id="new-contact-email"
            type="email"
            dir="ltr"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            maxLength={254}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="new-contact-phone">
            {t("contacts.phone")}{" "}
            <span className="text-muted-foreground">({t("common.optional")})</span>
          </FieldLabel>
          <Input
            id="new-contact-phone"
            dir="ltr"
            value={phone}
            onChange={(e) => setPhone(e.target.value)}
            maxLength={40}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="new-contact-lang">{t("inbox.crm.language")}</FieldLabel>
          <PickSelect
            id="new-contact-lang"
            value={lang}
            onChange={(v) => setLang(v === "ar" ? "ar" : "en")}
            options={[
              { value: "en", label: LOCALE_NAMES.en },
              { value: "ar", label: LOCALE_NAMES.ar },
            ]}
          />
        </Field>
      </FieldGroup>
      {!email.trim() && !phone.trim() ? <FormHint>{t("contacts.needsIdentity")}</FormHint> : null}
      {err ? <FieldError>{err}</FieldError> : null}
      <DialogFooter>
        <Button type="submit" disabled={busy || (!email.trim() && !phone.trim())}>
          {busy ? t("common.working") : t("inbox.crm.createAction")}
        </Button>
      </DialogFooter>
    </form>
  )
}

export function OpenDealDialog({ open, onOpenChange, c, onDone }: DialogProps) {
  const { t } = useTranslations()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("inbox.crm.dealTitle")}</DialogTitle>
          <DialogDescription>{t("inbox.crm.dealHint")}</DialogDescription>
        </DialogHeader>
        <OpenDealForm c={c} onDone={() => (onDone(), onOpenChange(false))} />
      </DialogContent>
    </Dialog>
  )
}

function OpenDealForm({ c, onDone }: { c: Conversation; onDone: Done }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const stages = usePipelineStages(ws.id)
  const currency = ws.current?.workspace.currency || "USD"
  const [title, setTitle] = useState("")
  const [stageId, setStageId] = useState("")
  const [value, setValue] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const openStages = (stages.data?.stages ?? []).filter((s) => s.kind === "open")
  const stageOpts = (stages.data?.stages ?? []).map((s) => ({ value: s.id, label: s.name }))
  const chosenStage = stageId || openStages[0]?.id || ""
  const who = c.contact
    ? c.contact.name || c.contact.email
    : c.customer_name || c.customer_email || c.customer_phone
  const placeholder = `${who || t("inbox.anonymous")} — ${t(`inbox.channel.${c.channel}`)}`

  async function submit(e: FormEvent) {
    e.preventDefault()
    const minor = toMinor(value, currency)
    if (minor === null) {
      setErr(t("deals.valueInvalid"))
      return
    }
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{ deal_id: string }>(
        `/api/w/conversations/${encodeURIComponent(c.id)}/deal`,
        {
          json: {
            title: title.trim() || undefined,
            stage_id: chosenStage || undefined,
            value_minor: minor || undefined,
          },
          workspaceId: ws.id,
        },
      )
      toast.success(t("inbox.crm.dealOpened"), {
        action: {
          label: t("inbox.crm.viewDeal"),
          onClick: () => router.push(`/${locale}/w/${ws.slug}/deals/${res.deal_id}`),
        },
      })
      onDone()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className="flex flex-col gap-4" data-form="open-deal">
      {!c.contact ? (
        <p
          className="rounded-md border border-nq-line bg-nq-surface-soft px-3 py-2 text-sm text-muted-foreground"
          data-deal-no-contact
        >
          {t("inbox.crm.dealNoContact")}
        </p>
      ) : (
        <p className="text-sm text-muted-foreground">
          {t("inbox.crm.contact")}: <Bdi>{c.contact.name || c.contact.email}</Bdi>
          {c.contact.company ? (
            <>
              {" · "}
              <Bdi>{c.contact.company.name}</Bdi>
            </>
          ) : null}
        </p>
      )}
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="deal-title">{t("inbox.crm.dealName")}</FieldLabel>
          <Input
            id="deal-title"
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            maxLength={160}
            placeholder={placeholder}
          />
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field>
            <FieldLabel htmlFor="deal-stage">{t("inbox.crm.stage")}</FieldLabel>
            <PickSelect
              id="deal-stage"
              value={chosenStage}
              onChange={setStageId}
              options={stageOpts}
              disabled={!stages.data}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor="deal-value">{t("inbox.crm.value", { currency })}</FieldLabel>
            <Input
              id="deal-value"
              dir="ltr"
              inputMode="decimal"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder="0"
            />
            <FieldDescription>{t("common.optional")}</FieldDescription>
          </Field>
        </div>
      </FieldGroup>
      {stages.error ? (
        <FieldError>
          {stages.error instanceof ApiError ? stages.error.message : t("common.networkError")}
        </FieldError>
      ) : null}
      {err ? <FieldError>{err}</FieldError> : null}
      <DialogFooter>
        <Button type="submit" disabled={busy || !stages.data}>
          {busy ? t("common.working") : t("inbox.crm.dealAction")}
        </Button>
      </DialogFooter>
    </form>
  )
}
