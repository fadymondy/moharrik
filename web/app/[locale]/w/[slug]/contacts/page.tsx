"use client"

import Link from "next/link"
import { useEffect, useState, type FormEvent } from "react"
import {
  ChevronLeftIcon,
  ChevronRightIcon,
  DownloadIcon,
  EllipsisIcon,
  MailWarningIcon,
  PencilIcon,
  SearchIcon,
} from "lucide-react"
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, Badge, Button, DropdownMenu, DropdownMenuContent, DropdownMenuGroup, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger, Field, FieldDescription, FieldLabel, Input, InputGroup, InputGroupAddon, InputGroupInput, CursorPager, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Switch, Table, TableBody, TableCell, TableHead, TableHeader, TableRow, toast, Toggle, ToggleGroup } from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError, FormHint } from "@/components/form-layout"

import { SectionHeader } from "@/components/public-frame"
import { PersonAvatar } from "@/components/avatar-with-fallback"
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
const ALL_LISTS = "__all__"
const ANY = "__any__"
const MIN_SCORES = [25, 50, 75] as const

const statusVariant: Record<
  SubscriptionStatus,
  "brand" | "neutral" | "outline" | "danger"
> = {
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

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("nav.contacts")}
        description={t("contacts.hint")}
        action={
          <div className="flex items-center gap-4">
            <Link
              className="text-sm underline underline-offset-4"
              href={`/${locale}/w/${ws.slug}/contacts/import`}
              data-nav="import"
            >
              {t("import.title")}
            </Link>
            <Link
              className="text-sm underline underline-offset-4"
              href={`/${locale}/w/${ws.slug}/contacts/vcard`}
              data-nav="vcard"
            >
              {t("vcard.navLink")}
            </Link>
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

      {canWrite ? (
        <AddContact lists={listItems} onAdded={changed} />
      ) : ws.current ? (
        <p className="px-6 py-6 text-sm text-muted-foreground">{t("contacts.readOnly")}</p>
      ) : null}

      <ContactsBrowser lists={listItems} version={version} onChange={changed} />
      <div className="pb-8" />
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
      onChange()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

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

      {canWrite ? (
        <form onSubmit={create} noValidate className="px-6 pt-6" data-form="create-list">
          <FieldGroup className="sm:flex-row sm:items-end">
            <Field className="sm:flex-1">
              <FieldLabel htmlFor="list-name">{t("lists.name")}</FieldLabel>
              <Input
                id="list-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                maxLength={80}
              />
            </Field>
            <Field className="sm:flex-1">
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
            <Button type="submit" disabled={busy || !name.trim()}>
              {busy ? t("common.working") : t("lists.create")}
            </Button>
          </FieldGroup>
          <Field className="flex-row items-center gap-3 mt-4">
            <Switch id="list-doi" checked={doi} onCheckedChange={(v) => setDoi(!!v)} />
            <div>
              <FieldLabel htmlFor="list-doi">{t("lists.doubleOptIn")}</FieldLabel>
              <FieldDescription>{t("lists.doubleOptInHint")}</FieldDescription>
            </div>
          </Field>
          {err ? <FieldError className="mt-3">{err}</FieldError> : null}
        </form>
      ) : null}
      <EditListDialog list={editing} onClose={() => setEditing(null)} onSaved={onChange} />
    </section>
  )
}

function AddContact({ lists, onAdded }: { lists: MailList[]; onAdded: () => void }) {
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
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="px-6 py-6">
      <h2 className="text-base font-medium">{t("contacts.addTitle")}</h2>
      {needsSmtp ? (
        <p className="mt-1 flex items-start gap-2 text-sm text-muted-foreground">
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
        <p className="mt-2 text-sm text-muted-foreground">{t("contacts.noListsHint")}</p>
      ) : null}
      <form onSubmit={onSubmit} noValidate className="mt-5" data-form="add-contact">
        <FieldGroup className="sm:flex-row sm:items-end sm:flex-wrap">
          <Field className="sm:flex-1 sm:min-w-48">
            <FieldLabel htmlFor="contact-email">
              {t("contacts.email")}{" "}
              <span className="text-muted-foreground">({t("common.optional")})</span>
            </FieldLabel>
            <Input
              id="contact-email"
              type="email"
              dir="ltr"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="name@company.com"
            />
          </Field>
          <Field className="sm:flex-1 sm:min-w-40">
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
          <Field className="sm:flex-1 sm:min-w-40">
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
          <Field className="sm:flex-1 sm:min-w-40">
            <FieldLabel htmlFor="contact-name">
              {t("contacts.name")} <span className="text-muted-foreground">({t("common.optional")})</span>
            </FieldLabel>
            <Input id="contact-name" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field className="sm:w-36">
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
            <Field className="sm:w-44">
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
          <Button type="submit" disabled={busy || !hasIdentity || listBlocked}>
            {busy ? t("common.working") : t("contacts.add")}
          </Button>
        </FieldGroup>
        {listBlocked ? (
          <FormHint className="mt-2">{t("contacts.listNeedsEmail")}</FormHint>
        ) : null}
        {!hasIdentity ? (
          <FormHint className="mt-2">{t("contacts.needsIdentity")}</FormHint>
        ) : null}
        {err ? <FieldError className="mt-3">{err}</FieldError> : null}
      </form>
    </section>
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
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
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
  const filtered = !!(activeList || q || lifecycle || minScore)
  const counts = page.data?.counts ?? null

  const listItems = [
    { value: ALL_LISTS, label: t("contacts.allContacts") },
    ...lists.map((l) => ({ value: l.id, label: l.name })),
  ]
  const stageItems = [
    { value: ANY, label: t("lifecycle.any") },
    ...LIFECYCLE_STAGES.map((st) => ({ value: st, label: t(`lifecycle.${st}`) })),
    { value: "none", label: t("lifecycle.none") },
  ]
  const scoreItems = [
    { value: ANY, label: t("icp.anyScore") },
    ...MIN_SCORES.map((n) => ({
      value: String(n),
      label: t("icp.atLeast", { n: formatNumber(n) }),
    })),
  ]

  return (
    <section data-contacts-browser>
      <div className="flex flex-col gap-3 px-6 pt-6 pb-4 sm:flex-row sm:items-end">
        <Field className="sm:flex-1">
          <FieldLabel htmlFor="contacts-search">{t("contacts.search")}</FieldLabel>
          <InputGroup>
            <InputGroupInput
              id="contacts-search"
              type="search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder={t("contacts.searchPlaceholder")}
              maxLength={200}
            />
            <InputGroupAddon>
              <SearchIcon />
            </InputGroupAddon>
          </InputGroup>
        </Field>
        <Field className="sm:w-56">
          <FieldLabel htmlFor="contacts-list-filter">{t("contacts.list")}</FieldLabel>
          <Select
            items={listItems}
            value={activeList || ALL_LISTS}
            onValueChange={(v) => setListId(v == null || v === ALL_LISTS ? "" : String(v))}
          >
            <SelectTrigger id="contacts-list-filter" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {listItems.map((i) => (
                <SelectItem key={i.value} value={i.value}>
                  <Bdi>{i.label}</Bdi>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field className="sm:w-44">
          <FieldLabel htmlFor="contacts-stage-filter">{t("lifecycle.label")}</FieldLabel>
          <Select
            items={stageItems}
            value={lifecycle || ANY}
            onValueChange={(v) => setLifecycle(v == null || v === ANY ? "" : String(v))}
          >
            <SelectTrigger id="contacts-stage-filter" className="w-full" data-stage-filter>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {stageItems.map((i) => (
                <SelectItem key={i.value} value={i.value}>
                  {i.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field className="sm:w-40">
          <FieldLabel htmlFor="contacts-score-filter">{t("icp.score")}</FieldLabel>
          <Select
            items={scoreItems}
            value={minScore ? String(minScore) : ANY}
            onValueChange={(v) => setMinScore(v == null || v === ANY ? 0 : Number(v))}
          >
            <SelectTrigger id="contacts-score-filter" className="w-full" data-score-filter>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {scoreItems.map((i) => (
                <SelectItem key={i.value} value={i.value}>
                  {i.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        {list && canExport ? (
          <Button
            variant="secondary"
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
        ) : null}
      </div>

      <div className="flex flex-wrap items-center gap-2 px-6 pb-4">
        <span className="text-sm text-muted-foreground">{t("icp.sortBy")}</span>
        <ToggleGroup
          value={[sort || "newest"]}
          onValueChange={(v) => v[0] && setSort(v[0] === "score" ? "score" : "")}
          aria-label={t("icp.sortBy")}
        >
          <Toggle value="newest" data-sort="newest">
            {t("icp.sortNewest")}
          </Toggle>
          <Toggle value="score" data-sort="score">
            {t("icp.sortScore")}
          </Toggle>
        </ToggleGroup>
      </div>

      {list ? (
        <div className="px-6 pb-4">
          <ToggleGroup
            className="flex-wrap"
            value={[activeStatus || "all"]}
            onValueChange={(v) => {
              const next = v[0]
              if (next === undefined) return
              setStatus(next === "all" ? "" : (next as SubscriptionStatus))
            }}
            aria-label={t("contacts.statusFilter")}
          >
            <Toggle value="all" data-status-filter="all">
              {t("contacts.statusAll")}{" "}
              <span className="text-muted-foreground tabular-nums">
                {counts ? formatNumber(counts.all) : ""}
              </span>
            </Toggle>
            {STATUSES.map((st) => (
              <Toggle key={st} value={st} data-status-filter={st}>
                {t(`sub.${st}`)}{" "}
                <span className="text-muted-foreground tabular-nums">
                  {counts ? formatNumber(counts[st]) : ""}
                </span>
              </Toggle>
            ))}
          </ToggleGroup>
        </div>
      ) : null}

      {page.error ? (
        <ErrorState error={page.error} />
      ) : !page.data ? (
        <LoadingRows />
      ) : total === 0 ? (
        filtered ? (
          <EmptyState
            title={t("contacts.noMatch")}
            body={t("contacts.noMatchBody")}
            action={
              <Button
                variant="secondary"
                size="sm"
                onClick={() => {
                  setSearch("")
                  setListId("")
                  setStatus("")
                  setLifecycle("")
                  setMinScore(0)
                }}
              >
                {t("contacts.clearFilters")}
              </Button>
            }
          />
        ) : (
          <EmptyState
            title={t("contacts.empty")}
            body={canWrite ? t("contacts.emptyBody") : undefined}
          />
        )
      ) : (
        <>
          <p
            className="px-6 pb-3 text-sm text-muted-foreground"
            aria-live="polite"
            data-contacts-summary={`${from}-${to}/${total}`}
          >
            {t("contacts.showing", {
              from: formatNumber(from),
              to: formatNumber(to),
              total: formatNumber(total),
            })}
          </p>
          <div
            className={page.current ? undefined : "opacity-60 transition-opacity"}
            aria-busy={!page.current}
            data-contacts-current={page.current ? "yes" : "no"}
          >
            <ContactsTable items={rows} canWrite={canWrite} onChange={onChange} />
          </div>
          {pages > 1 ? (
            <CursorPager
              className="px-6 pt-4"
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
        </>
      )}
    </section>
  )
}

function ContactsTable({
  items,
  canWrite,
  onChange,
}: {
  items: Contact[]
  canWrite: boolean
  onChange: () => void
}) {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const base = `/${locale}/w/${ws.slug}`
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

  return (
    <div className="border-y border-nq-line">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="ps-6">{t("contacts.identity")}</TableHead>
            <TableHead>{t("contacts.name")}</TableHead>
            <TableHead>{t("lifecycle.label")}</TableHead>
            <TableHead>{t("icp.score")}</TableHead>
            <TableHead>{t("contacts.lists")}</TableHead>
            <TableHead>{t("contacts.whatsapp")}</TableHead>
            <TableHead>{t("contacts.added")}</TableHead>
            <TableHead className="pe-6 text-end">
              <span className="sr-only">{t("common.actions")}</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((c) => {
            const leavable = (c.subscriptions ?? []).filter((s) => s.status !== "unsubscribed")
            return (
              <TableRow key={c.id} data-contact-row={contactIdentity(c)}>
                <TableCell className="ps-6">
                  <Link
                    className="flex items-center gap-2 hover:underline"
                    href={`${base}/contacts/${c.id}`}
                  >
                    <PersonAvatar
                      id={c.id}
                      name={c.name || contactIdentity(c)}
                      photoUrl={c.avatar_url}
                      size="sm"
                      className="shrink-0"
                    />
                    <Ltr>{contactIdentity(c)}</Ltr>
                  </Link>
                </TableCell>
                <TableCell>
                  {c.name ? (
                    <Link className="hover:underline" href={`${base}/contacts/${c.id}`}>
                      <Bdi>{c.name}</Bdi>
                    </Link>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell>
                  <LifecycleBadge stage={c.lifecycle ?? ""} />
                </TableCell>
                <TableCell>
                  <ScoreButton
                    score={c.score}
                    reasons={c.score_reasons}
                    who={isolate(c.name || contactIdentity(c))}
                  />
                </TableCell>
                <TableCell>
                  <div className="flex flex-wrap gap-1.5">
                    {(c.subscriptions ?? []).map((s) => (
                      <span key={s.id} className="inline-flex items-center gap-1">
                        <Badge
                          variant={statusVariant[s.status]}
                          data-sub={`${s.list_name}:${s.status}`}
                          title={
                            s.status === "unsubscribed" && s.unsubscribed_by_workspace
                              ? t("contacts.unsubscribedByTeam", {
                                  date: formatDate(s.unsubscribed_at),
                                })
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
                            onClick={() => resend(s.id)}
                          >
                            {t("contacts.resend")}
                          </Button>
                        ) : null}
                      </span>
                    ))}
                  </div>
                </TableCell>
                <TableCell>
                  <WhatsAppConsent contact={c} onChange={onChange} />
                </TableCell>
                <TableCell className="text-muted-foreground">
                  <Ltr>{formatDate(c.created_at)}</Ltr>
                </TableCell>
                <TableCell className="pe-6 text-end">
                  {canWrite ? (
                    <DropdownMenu>
                      <DropdownMenuTrigger
                        render={
                          <Button
                            variant="ghost"
                            size="icon-sm"
                            aria-label={t("contacts.actionsFor", {
                              who: isolate(contactIdentity(c)),
                            })}
                            data-row-actions={contactIdentity(c)}
                          />
                        }
                      >
                        <EllipsisIcon />
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end" className="min-w-56">
                        {leavable.length > 0 ? (
                          <>
                            <DropdownMenuGroup>
                              <DropdownMenuLabel>{t("contacts.unsubscribeFrom")}</DropdownMenuLabel>
                              {leavable.map((s) => (
                                <DropdownMenuItem
                                  key={s.id}
                                  data-unsubscribe-from={s.list_name}
                                  onClick={() =>
                                    setLeaving({
                                      contactId: c.id,
                                      who: c.name || contactIdentity(c),
                                      listId: s.list_id,
                                      listName: s.list_name,
                                    })
                                  }
                                >
                                  <Bdi>{s.list_name}</Bdi>
                                </DropdownMenuItem>
                              ))}
                            </DropdownMenuGroup>
                            <DropdownMenuSeparator />
                          </>
                        ) : null}
                        <DropdownMenuItem
                          variant="danger"
                          data-delete-contact
                          onClick={() => setDeleting(c)}
                        >
                          {t("contacts.deleteContact")}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  ) : null}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>

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
    </div>
  )
}
