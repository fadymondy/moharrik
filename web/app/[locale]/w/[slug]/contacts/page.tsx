"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useEffect, useState, type FormEvent } from "react"
import {
  DownloadIcon,
  FileUpIcon,
  ListPlusIcon,
  MailMinusIcon,
  MailWarningIcon,
  PencilIcon,
  PlusIcon,
  Trash2Icon,
  UsersIcon,
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
  CursorPager,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  toast,
} from "@fadymondy/nasaq/web"
import {
  CardMeta,
  EmptyState as NqEmptyState,
  EntityIdentity,
  EntityList,
  type DataTableColumn,
  type DataTableRowAction,
  type EntityFacet,
} from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError, FormHint } from "@/components/form-layout"

import { SectionHeader } from "@/components/public-frame"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { useDebounced } from "@/components/crm/contact-picker"
import { LifecycleBadge, ScoreButton } from "@/components/crm/icp"
import { PickSelect } from "@/components/crm/shared"
import { WhatsAppConsent } from "@/components/crm/whatsapp-consent"
import {
  EditListDialog,
  exportList,
  UnsubscribeDialog,
  type UnsubscribeTarget,
} from "@/components/mail/subscriber-admin"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  contactIdentity,
  LIFECYCLE_STAGES,
  type Contact,
  type MailList,
  type SubscriptionStatus,
} from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useContactsPage, useMailLists, useSmtpSettings } from "@/lib/queries"
import { useWorkspaceEvents } from "@/lib/events"
import { useDocumentTitle } from "@/lib/title"
import { Bdi, isolate } from "@/components/bidi"

const PAGE_SIZE = 50
const STATUSES: SubscriptionStatus[] = ["subscribed", "pending", "unsubscribed", "bounced"]
const MIN_SCORES = [25, 50, 75] as const

const statusVariant: Record<SubscriptionStatus, "brand" | "neutral" | "outline" | "danger"> = {
  subscribed: "brand",
  pending: "neutral",
  unsubscribed: "outline",
  bounced: "danger",
}

export default function ContactsPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.contacts")} · ${ws.summary.name}`)

  const canWrite = ws.can("contacts:write")
  const lists = useMailLists(ws.id)
  const listItems = lists.data?.lists ?? []
  // Bumped after a write, so the contacts browser refetches the page it is on.
  const [version, setVersion] = useState(0)
  const changed = () => {
    lists.mutate()
    setVersion((v) => v + 1)
  }
  // MH-413: subscriptions changed elsewhere (a form, a confirmation link, an
  // unsubscribe, another member) show up without a reload.
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "contacts") changed()
  })
  // MH-1144: the create forms are dialogs opened from the header.
  const [adding, setAdding] = useState(false)
  const [creatingList, setCreatingList] = useState(false)

  return (
    <>
      <SectionHeader
        title={t("nav.contacts")}
        description={t("contacts.hint")}
        action={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="secondary"
              size="sm"
              nativeButton={false}
              render={<Link href={`/${locale}/w/${ws.slug}/contacts/import`} data-nav="import" />}
            >
              <FileUpIcon />
              {t("import.hubTitle")}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              nativeButton={false}
              render={<Link href={`/${locale}/w/${ws.slug}/contacts/vcard`} data-nav="vcard" />}
            >
              {t("vcard.navLink")}
            </Button>
            {canWrite ? (
              <>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => setCreatingList(true)}
                  data-open="create-list"
                >
                  <ListPlusIcon />
                  {t("lists.new")}
                </Button>
                <Button size="sm" onClick={() => setAdding(true)} data-open="add-contact">
                  <PlusIcon />
                  {t("contacts.new")}
                </Button>
              </>
            ) : null}
          </div>
        }
      />
      <ListsSection
        lists={listItems}
        loading={lists.isLoading}
        error={lists.error}
        onChange={changed}
      />

      <div className="hatch h-8 border-y border-border mt-8" aria-hidden />

      <ContactsBrowser lists={listItems} version={version} onChange={changed} />
      <div className="pb-8" />

      {canWrite ? (
        <>
          <AddContactDialog
            open={adding}
            onOpenChange={setAdding}
            lists={listItems}
            onAdded={changed}
          />
          <CreateListDialog
            open={creatingList}
            onOpenChange={setCreatingList}
            onCreated={changed}
          />
        </>
      ) : null}
    </>
  )
}

function ListsSection({
  lists,
  loading,
  error,
  onChange,
}: {
  lists: MailList[]
  loading: boolean
  error: unknown
  onChange: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  const canDelete = ws.can("campaigns:write")
  const canExport = ws.can("contacts:export")
  const [editing, setEditing] = useState<MailList | null>(null)

  async function remove(id: string) {
    try {
      await api(`/api/w/mail/lists/${encodeURIComponent(id)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      toast.success(t("lists.deleted"))
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <section>
      <h2 className="px-6 pb-1 text-base font-medium">{t("lists.title")}</h2>
      <p className="px-6 pb-4 text-sm text-muted-foreground">{t("lists.hint")}</p>
      {error ? (
        <ErrorState error={error} />
      ) : loading ? (
        <LoadingRows rows={2} />
      ) : lists.length === 0 ? (
        <EmptyState title={t("lists.empty")} body={canWrite ? t("lists.emptyBody") : undefined} />
      ) : (
        <div className="border-y border-nq-line">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("lists.name")}</TableHead>
                <TableHead>{t("lists.doubleOptIn")}</TableHead>
                <TableHead>{t("lists.subscribed")}</TableHead>
                <TableHead>{t("lists.pending")}</TableHead>
                <TableHead className="pe-6 text-end">
                  <span className="sr-only">{t("common.actions")}</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {lists.map((l) => (
                <TableRow key={l.id} data-list-row={l.name}>
                  <TableCell className="ps-6">
                    <div className="font-medium">
                      <Bdi>{l.name}</Bdi>
                    </div>
                    {l.description ? (
                      <div className="text-xs text-muted-foreground">
                        <Bdi>{l.description}</Bdi>
                      </div>
                    ) : null}
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {l.double_opt_in ? t("lists.on") : t("lists.off")}
                  </TableCell>
                  <TableCell>{l.subscribed}</TableCell>
                  <TableCell className="text-muted-foreground">{l.pending}</TableCell>
                  <TableCell className="pe-6 text-end">
                    <div className="inline-flex items-center gap-1">
                      {canExport ? (
                        <Button
                          variant="ghost"
                          size="sm"
                          data-export-list={l.name}
                          onClick={() =>
                            exportList(
                              ws.id,
                              l,
                              {},
                              { ok: t("contacts.exported"), fail: t("common.networkError") },
                            )
                          }
                        >
                          <DownloadIcon />
                          {t("contacts.export")}
                        </Button>
                      ) : null}
                      {canWrite ? (
                        <Button
                          variant="ghost"
                          size="sm"
                          data-edit-list={l.name}
                          onClick={() => setEditing(l)}
                        >
                          <PencilIcon />
                          {t("common.edit")}
                        </Button>
                      ) : null}
                      {canDelete ? (
                        <ConfirmButton
                          label={t("common.delete")}
                          title={t("lists.deleteTitle")}
                          description={t("lists.deleteBody", { name: isolate(l.name) })}
                          confirmLabel={t("common.delete")}
                          onConfirm={() => remove(l.id)}
                        />
                      ) : null}
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      <EditListDialog list={editing} onClose={() => setEditing(null)} onSaved={onChange} />
    </section>
  )
}

/** New list (MH-1144): a dialog opened from the page header. */
function CreateListDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [doi, setDoi] = useState(true)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function create(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/mail/lists", {
        json: { name: name.trim(), description: description.trim(), double_opt_in: doi },
        workspaceId: ws.id,
      })
      toast.success(t("lists.created"))
      setName("")
      setDescription("")
      onCreated()
      onOpenChange(false)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) setErr(null)
        onOpenChange(v)
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("lists.new")}</DialogTitle>
          <DialogDescription>{t("lists.hint")}</DialogDescription>
        </DialogHeader>
        <form onSubmit={create} noValidate data-form="create-list">
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="list-name">{t("lists.name")}</FieldLabel>
              <Input
                id="list-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={80}
                autoFocus
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="list-description">
                {t("lists.description")}{" "}
                <span className="text-muted-foreground">({t("common.optional")})</span>
              </FieldLabel>
              <Input
                id="list-description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                maxLength={500}
              />
            </Field>
            <Field className="flex-row items-center gap-3">
              <Switch id="list-doi" checked={doi} onCheckedChange={(v) => setDoi(!!v)} />
              <div>
                <FieldLabel htmlFor="list-doi">{t("lists.doubleOptIn")}</FieldLabel>
                <FieldDescription>{t("lists.doubleOptInHint")}</FieldDescription>
              </div>
            </Field>
          </FieldGroup>
          {err ? <FieldError className="mt-3">{err}</FieldError> : null}
          <DialogFooter className="mt-6">
            <Button type="button" variant="secondary" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("lists.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/** New contact (MH-1144): a dialog opened from the page header, not a form between the lists and the contacts. */
function AddContactDialog({
  open,
  onOpenChange,
  lists,
  onAdded,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  lists: MailList[]
  onAdded: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const isAdmin = ws.can("mail:settings")
  const smtp = useSmtpSettings(ws.id, isAdmin)
  const [email, setEmail] = useState("")
  const [phone, setPhone] = useState("")
  const [handle, setHandle] = useState("")
  const [name, setName] = useState("")
  const [lang, setLang] = useState<string>(locale === "ar" ? "ar" : "en")
  const [listId, setListId] = useState<string>("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  const hasIdentity = !!(email.trim() || phone.trim() || handle.trim())
  // A list is an email audience (MH-561): choosing one needs an email too.
  const listBlocked = !!listId && !email.trim()
  const listItems = lists.map((l) => ({ value: l.id, label: l.name }))
  const langItems = [
    { value: "en", label: t("lang.en") },
    { value: "ar", label: t("lang.ar") },
  ]
  const needsSmtp =
    isAdmin && smtp.data && !smtp.data.configured && lists.some((l) => l.double_opt_in)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      const res = await api<{
        status: string
        confirmation: string
        error?: string
        contact: Contact
      }>("/api/w/contacts", {
        json: {
          email: email.trim(),
          phone: phone.trim(),
          handle: handle.trim(),
          name: name.trim(),
          locale: lang,
          list_id: listId,
        },
        workspaceId: ws.id,
      })
      if (res.confirmation === "sent") toast.success(t("contacts.addedConfirmSent"))
      else if (res.confirmation === "not_sent")
        toast.warning(t("contacts.addedConfirmNotSent", { error: res.error ?? "" }))
      else if (res.status === "subscribed") toast.success(t("contacts.addedSubscribed"))
      else toast.success(t("contacts.addedCrm"))
      setEmail("")
      setPhone("")
      setHandle("")
      setName("")
      onAdded()
      onOpenChange(false)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) setErr(null)
        onOpenChange(v)
      }}
    >
      <DialogContent className="sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("contacts.addTitle")}</DialogTitle>
        </DialogHeader>
        {needsSmtp ? (
          <p className="flex items-start gap-2 text-sm text-muted-foreground">
            <MailWarningIcon className="mt-0.5 size-4 shrink-0 text-nq-warning" />
            <span>
              {t("contacts.smtpMissing")}{" "}
              <Link
                className="underline underline-offset-4"
                href={`/${locale}/w/${ws.slug}/settings/email`}
              >
                {t("contacts.smtpLink")}
              </Link>
            </span>
          </p>
        ) : null}
        {lists.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t("contacts.noListsHint")}</p>
        ) : null}
        <form onSubmit={onSubmit} noValidate data-form="add-contact">
          <FieldGroup className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel htmlFor="contact-email">
                {t("contacts.email")}{" "}
                <span className="text-muted-foreground">({t("common.optional")})</span>
              </FieldLabel>
              <Input
                id="contact-email"
                type="email"
                autoFocus
                dir="ltr"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="name@company.com"
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="contact-phone">
                {t("contacts.phone")}{" "}
                <span className="text-muted-foreground">({t("common.optional")})</span>
              </FieldLabel>
              <Input
                id="contact-phone"
                type="tel"
                dir="ltr"
                value={phone}
                onChange={(e) => setPhone(e.target.value)}
                placeholder="+20 101 234 5678"
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="contact-handle">
                {t("contacts.handle")}{" "}
                <span className="text-muted-foreground">({t("common.optional")})</span>
              </FieldLabel>
              <Input
                id="contact-handle"
                dir="ltr"
                value={handle}
                onChange={(e) => setHandle(e.target.value)}
                placeholder="@handle"
              />
            </Field>
            <Field>
              <FieldLabel htmlFor="contact-name">
                {t("contacts.name")}{" "}
                <span className="text-muted-foreground">({t("common.optional")})</span>
              </FieldLabel>
              <Input id="contact-name" value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="contact-lang">{t("contacts.language")}</FieldLabel>
              <Select
                items={langItems}
                value={lang}
                onValueChange={(v) => setLang(String(v ?? "en"))}
              >
                <SelectTrigger id="contact-lang" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {langItems.map((i) => (
                    <SelectItem key={i.value} value={i.value}>
                      {i.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            {lists.length > 0 ? (
              <Field>
                <FieldLabel htmlFor="contact-list">
                  {t("contacts.list")}{" "}
                  <span className="text-muted-foreground">({t("common.optional")})</span>
                </FieldLabel>
                <PickSelect
                  id="contact-list"
                  value={listId}
                  onChange={setListId}
                  options={listItems}
                  noneLabel={t("contacts.noList")}
                />
              </Field>
            ) : null}
          </FieldGroup>
          {listBlocked ? (
            <FormHint className="mt-2">{t("contacts.listNeedsEmail")}</FormHint>
          ) : null}
          {!hasIdentity ? (
            <FormHint className="mt-2">{t("contacts.needsIdentity")}</FormHint>
          ) : null}
          {err ? <FieldError className="mt-3">{err}</FieldError> : null}
          <DialogFooter className="mt-6">
            <Button type="button" variant="secondary" onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy || !hasIdentity || listBlocked}>
              {busy ? t("common.working") : t("contacts.add")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/*
The contacts list (MH-408). It used to be every contact at once — until 1,000,
where it stopped without a word. Now it is a page at a time with the total said
out loud, a search that reaches every row, and per-list status filters that say
how many each holds.

Keyset paging, so "previous" is a stack of the cursors already visited rather
than an offset: a contact added while somebody is paging does not shift every
page by one.
*/
function ContactsBrowser({
  lists,
  version,
  onChange,
}: {
  lists: MailList[]
  version: number
  onChange: () => void
}) {
  const { t, locale, formatNumber, formatDate } = useTranslations()
  const ws = useWorkspace()
  const router = useRouter()
  const base = `/${locale}/w/${ws.slug}`
  const canWrite = ws.can("contacts:write")
  const canExport = ws.can("contacts:export")

  const [listId, setListId] = useState("")
  const [status, setStatus] = useState<SubscriptionStatus | "">("")
  const [search, setSearch] = useState("")
  const q = useDebounced(search.trim())
  // MH-395: lifecycle stage, minimum score and the order.
  const [lifecycle, setLifecycle] = useState("")
  const [minScore, setMinScore] = useState(0)
  const [sort, setSort] = useState<"" | "score">("")
  // cursors[i] opens page i+1; cursors[0] is the first page.
  const [cursors, setCursors] = useState<string[]>([""])

  // A list that was deleted stops being the filter.
  const list = lists.find((l) => l.id === listId) ?? null
  const activeList = list ? listId : ""
  const activeStatus = activeList ? status : ""

  // Any change of filter starts again from the first page.
  useEffect(() => setCursors([""]), [activeList, activeStatus, q, lifecycle, minScore, sort])

  const page = useContactsPage(ws.id, {
    listId: activeList,
    status: activeStatus,
    q,
    lifecycle,
    minScore,
    sort,
    cursor: cursors[cursors.length - 1],
    limit: PAGE_SIZE,
  })
  const { mutate } = page
  useEffect(() => {
    if (version > 0) mutate()
  }, [version, mutate])

  const rows = page.data?.contacts ?? []
  const total = page.data?.total ?? 0
  // The index of the page on screen — which, while the next one loads, is still
  // the previous one.
  const shownCursor =
    new URLSearchParams((page.data?.path ?? "").split("?")[1] ?? "").get("cursor") ?? ""
  const pageIndex = page.current
    ? cursors.length - 1
    : Math.max(0, cursors.lastIndexOf(shownCursor))
  const from = rows.length ? pageIndex * PAGE_SIZE + 1 : 0
  const to = pageIndex * PAGE_SIZE + rows.length
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const counts = page.data?.counts ?? null

  const [leaving, setLeaving] = useState<UnsubscribeTarget | null>(null)
  const [deleting, setDeleting] = useState<Contact | null>(null)
  const [busy, setBusy] = useState(false)

  async function resend(subId: string) {
    try {
      await api(`/api/w/subscriptions/${encodeURIComponent(subId)}/resend`, {
        method: "POST",
        workspaceId: ws.id,
      })
      toast.success(t("contacts.resent"))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  async function remove(c: Contact) {
    setBusy(true)
    try {
      await api(`/api/w/contacts/${encodeURIComponent(c.id)}`, {
        method: "DELETE",
        workspaceId: ws.id,
      })
      toast.success(t("contacts.deleted"))
      setDeleting(null)
      onChange()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  /*
  The server takes one value per filter, so each facet behaves as a single
  choice: picking a second value replaces the first.
  */
  const facets: EntityFacet<Contact>[] = [
    {
      id: "list",
      title: t("contacts.list"),
      options: lists.map((l) => ({ value: l.id, label: l.name })),
      getValues: () => [],
    },
    ...(list
      ? [
          {
            id: "status",
            title: t("contacts.statusFilter"),
            options: STATUSES.map((st) => ({
              value: st,
              label: counts ? `${t(`sub.${st}`)} · ${formatNumber(counts[st])}` : t(`sub.${st}`),
            })),
            getValues: () => [],
          },
        ]
      : []),
    {
      id: "stage",
      title: t("lifecycle.label"),
      options: [
        ...LIFECYCLE_STAGES.map((st) => ({ value: st, label: t(`lifecycle.${st}`) })),
        { value: "none", label: t("lifecycle.none") },
      ],
      getValues: () => [],
    },
    {
      id: "score",
      title: t("icp.score"),
      options: MIN_SCORES.map((n) => ({
        value: String(n),
        label: t("icp.atLeast", { n: formatNumber(n) }),
      })),
      getValues: () => [],
    },
  ]
  const facetValues: Record<string, string[]> = {
    list: activeList ? [activeList] : [],
    status: activeStatus ? [activeStatus] : [],
    stage: lifecycle ? [lifecycle] : [],
    score: minScore ? [String(minScore)] : [],
  }
  const single = (next: string[] | undefined, prev: string[]) =>
    (next ?? []).find((v) => !prev.includes(v)) ?? (next ?? [])[0] ?? ""
  const onFacetValuesChange = (next: Record<string, string[]>) => {
    const listNext = single(next.list, facetValues.list)
    if (listNext !== activeList) {
      setListId(listNext)
      setStatus("")
    } else {
      setStatus(single(next.status, facetValues.status) as SubscriptionStatus | "")
    }
    setLifecycle(single(next.stage, facetValues.stage))
    setMinScore(Number(single(next.score, facetValues.score)) || 0)
  }

  const columns: DataTableColumn<Contact>[] = [
    {
      id: "name",
      header: t("contacts.name"),
      hideable: false,
      className: "min-w-56",
      cell: (c) => (
        <span data-contact-row={contactIdentity(c)} className="block min-w-0">
          <EntityIdentity
            name={c.name || contactIdentity(c)}
            avatarName={c.name || contactIdentity(c)}
            avatar={c.avatar_url || undefined}
            subtitle={c.name ? <Ltr>{contactIdentity(c)}</Ltr> : undefined}
          />
        </span>
      ),
    },
    {
      id: "company",
      header: t("contacts.company"),
      cell: (c) =>
        c.company_name || c.title ? (
          <span className="flex min-w-0 flex-col">
            <Bdi className="truncate">{c.company_name || "—"}</Bdi>
            {c.title ? (
              <Bdi className="truncate text-sm text-muted-foreground">{c.title}</Bdi>
            ) : null}
          </span>
        ) : (
          <span className="text-muted-foreground">—</span>
        ),
    },
    {
      id: "stage",
      header: t("lifecycle.label"),
      cell: (c) => <LifecycleBadge stage={c.lifecycle ?? ""} />,
    },
    {
      id: "score",
      header: t("icp.score"),
      // The server orders by score; the newest-first order is the default.
      sortValue: (c) => c.score,
      cell: (c) => (
        <ScoreButton
          score={c.score}
          reasons={c.score_reasons}
          who={isolate(c.name || contactIdentity(c))}
        />
      ),
    },
    {
      id: "lists",
      header: t("contacts.lists"),
      cell: (c) => <Subscriptions contact={c} canWrite={canWrite} onResend={resend} />,
    },
    {
      id: "whatsapp",
      header: t("contacts.whatsapp"),
      cell: (c) => <WhatsAppConsent contact={c} onChange={onChange} />,
    },
    {
      id: "added",
      header: t("contacts.added"),
      align: "end",
      cell: (c) => (
        <span className="text-muted-foreground">
          <Ltr>{formatDate(c.created_at)}</Ltr>
        </span>
      ),
    },
  ]

  const rowActions = (c: Contact): DataTableRowAction[] =>
    canWrite
      ? [
          ...(c.subscriptions ?? [])
            .filter((s) => s.status !== "unsubscribed")
            .map((s) => ({
              id: `unsubscribe-${s.id}`,
              label: t("contacts.unsubscribeFromList", { list: isolate(s.list_name) }),
              icon: MailMinusIcon,
              group: "lists",
              onSelect: () =>
                setLeaving({
                  contactId: c.id,
                  who: c.name || contactIdentity(c),
                  listId: s.list_id,
                  listName: s.list_name,
                }),
            })),
          {
            id: "delete",
            label: t("contacts.deleteContact"),
            icon: Trash2Icon,
            danger: true,
            group: "danger",
            onSelect: () => setDeleting(c),
          },
        ]
      : []

  return (
    <section data-contacts-browser className="px-4 pt-6 sm:px-6">
      <EntityList<Contact>
        data={rows}
        columns={columns}
        getRowId={(c) => c.id}
        rowLabel={(c) => c.name || contactIdentity(c)}
        label={t("nav.contacts")}
        manual
        rowCount={total}
        selectable={false}
        query={{ value: search, onChange: setSearch }}
        searchPlaceholder={t("contacts.searchPlaceholder")}
        facets={facets}
        facetValues={facetValues}
        onFacetValuesChange={onFacetValuesChange}
        sort={{
          value: sort === "score" ? { id: "score", direction: "desc" } : null,
          onChange: (s) => setSort(s ? "score" : ""),
        }}
        rowActions={canWrite ? rowActions : undefined}
        onRowClick={(c) => router.push(`${base}/contacts/${c.id}`)}
        loading={!page.data && !page.error}
        error={
          page.error
            ? page.error instanceof Error
              ? page.error.message
              : t("common.networkError")
            : undefined
        }
        onRetry={() => void mutate()}
        empty={
          <NqEmptyState
            icon={UsersIcon}
            title={t("contacts.empty")}
            description={canWrite ? t("contacts.emptyBody") : undefined}
            className="border-0"
          />
        }
        toolbar={
          list && canExport ? (
            <Button
              size="sm"
              data-export-view
              onClick={() =>
                exportList(
                  ws.id,
                  list,
                  { status: activeStatus, q },
                  { ok: t("contacts.exported"), fail: t("common.networkError") },
                )
              }
            >
              <DownloadIcon />
              {t("contacts.exportView")}
            </Button>
          ) : null
        }
        renderCard={(c) => (
          <div className="flex min-w-0 flex-col gap-3" data-contact-card={contactIdentity(c)}>
            <EntityIdentity
              className="pe-(--entity-card-controls)"
              name={c.name || contactIdentity(c)}
              avatarName={c.name || contactIdentity(c)}
              avatar={c.avatar_url || undefined}
              size="lg"
              subtitle={c.title || c.company_name || undefined}
            />
            {c.name ? (
              <span className="truncate text-sm text-muted-foreground">
                <Ltr>{contactIdentity(c)}</Ltr>
              </span>
            ) : null}
            <div className="flex flex-col gap-1.5">
              <CardMeta label={t("lifecycle.label")}>
                <LifecycleBadge stage={c.lifecycle ?? ""} />
              </CardMeta>
              <CardMeta label={t("icp.score")}>
                <ScoreButton
                  score={c.score}
                  reasons={c.score_reasons}
                  who={isolate(c.name || contactIdentity(c))}
                />
              </CardMeta>
              <CardMeta label={t("contacts.added")}>
                <Ltr>{formatDate(c.created_at)}</Ltr>
              </CardMeta>
            </div>
            <Subscriptions contact={c} canWrite={canWrite} onResend={resend} />
          </div>
        )}
        pagination={
          page.data && total > 0 ? (
            <div
              className="flex flex-wrap items-center justify-between gap-3"
              data-contacts-current={page.current ? "yes" : "no"}
            >
              <p
                className="text-sm text-muted-foreground"
                aria-live="polite"
                data-contacts-summary={`${from}-${to}/${total}`}
              >
                {t("contacts.showing", {
                  from: formatNumber(from),
                  to: formatNumber(to),
                  total: formatNumber(total),
                })}
              </p>
              {pages > 1 ? (
                <CursorPager
                  from={0}
                  to={0}
                  summary={
                    <span data-page-of={`${pageIndex + 1}/${pages}`}>
                      {t("contacts.pageOf", {
                        page: formatNumber(pageIndex + 1),
                        pages: formatNumber(pages),
                      })}
                    </span>
                  }
                  previousLabel={t("contacts.prevPage")}
                  nextLabel={t("contacts.nextPage")}
                  hasPrevious={cursors.length > 1}
                  hasNext={!!page.data?.next_cursor}
                  loading={!page.current}
                  onPrevious={() =>
                    setCursors((c) =>
                      c.length > 1 && c[c.length - 1] === shownCursor ? c.slice(0, -1) : c,
                    )
                  }
                  onNext={() => {
                    const next = page.data?.next_cursor
                    // Only from the page it belongs to: a second click before the
                    // re-render must not push the same cursor twice.
                    if (next && page.current)
                      setCursors((c) => (c[c.length - 1] === shownCursor ? [...c, next] : c))
                  }}
                />
              ) : null}
            </div>
          ) : (
            false
          )
        }
        className={page.current ? undefined : "[&_[data-slot=table]]:opacity-60"}
      />

      <UnsubscribeDialog target={leaving} onClose={() => setLeaving(null)} onDone={onChange} />
      <AlertDialog open={deleting !== null} onOpenChange={(v) => (v ? null : setDeleting(null))}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("contacts.deleteTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("contacts.deleteBody", { email: isolate(deleting?.email ?? "") })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              variant="danger"
              disabled={busy}
              onClick={() => deleting && remove(deleting)}
            >
              {busy ? t("common.working") : t("common.delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

/** A contact's list subscriptions as status badges, with "Resend" on a pending one. */
function Subscriptions({
  contact,
  canWrite,
  onResend,
}: {
  contact: Contact
  canWrite: boolean
  onResend: (subId: string) => void
}) {
  const { t, formatDate } = useTranslations()
  const subs = contact.subscriptions ?? []
  if (!subs.length) return <span className="text-muted-foreground">—</span>
  return (
    <div className="flex flex-wrap gap-1.5">
      {subs.map((s) => (
        <span key={s.id} className="inline-flex items-center gap-1">
          <Badge
            variant={statusVariant[s.status]}
            data-sub={`${s.list_name}:${s.status}`}
            title={
              s.status === "unsubscribed" && s.unsubscribed_by_workspace
                ? t("contacts.unsubscribedByTeam", { date: formatDate(s.unsubscribed_at) })
                : t(`sub.${s.status}`)
            }
          >
            <Bdi>{s.list_name}</Bdi> · {t(`sub.${s.status}`)}
          </Badge>
          {canWrite && s.status === "pending" ? (
            <Button
              variant="link"
              size="sm"
              className="h-auto px-1 text-xs"
              onClick={() => onResend(s.id)}
            >
              {t("contacts.resend")}
            </Button>
          ) : null}
        </span>
      ))}
    </div>
  )
}
