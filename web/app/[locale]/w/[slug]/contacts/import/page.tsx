"use client"

import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { useEffect, useMemo, useRef, useState } from "react"
import {
  CheckIcon,
  ContactRoundIcon,
  MailIcon,
  MessageCircleIcon,
  MessagesSquareIcon,
  Share2Icon,
  SlackIcon,
  UsersIcon,
  XIcon,
} from "lucide-react"
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
  Checkbox,
  Field,
  FieldLabel,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Status,
  Stepper,
  StepperItem,
  toast,
  type StatusTone,
} from "@fadymondy/nasaq/web"
import { FormHint } from "@/components/form-layout"

import { BrandIcon } from "@/components/automation/brand-icon"
import { RowsTable } from "@/components/nq-table"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { SectionHeader } from "@/components/public-frame"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type ImportConnection, type ImportItem } from "@/lib/api"
import { BRANDS, type Brand } from "@/lib/brands"
import { useTranslations } from "@/lib/i18n"
import { useDebounced } from "@/components/crm/contact-picker"
import {
  useGoogleContactsConnections,
  useImportJob,
  useImportPreview,
  useImportSources,
} from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"
import { Ltr } from "@/components/copy-field"

/*
Contacts → Import (MH-1128): every way in, always on screen, as integration
cards with numbered steps — so somebody who has connected nothing yet still
sees how each source works and what to do first. The connection sources come
from internal/imports' Provider list (MH-562); a ready connection opens the
preview / select / import flow below the cards. Telegram and the browser
workflows have no history to import (their API says so), so their cards say
how chats arrive instead of offering a button that cannot work.
*/

type CardState = "ready" | "notConnected" | "reauth" | "live"

const STATE_TONE: Record<CardState, StatusTone> = {
  ready: "success",
  notConnected: "neutral",
  reauth: "warning",
  live: "info",
}

/** Sources whose history the API cannot list: chats arrive by themselves. */
const LIVE_ONLY = new Set(["telegram", "workflow"])

const SOURCE_BRAND: Record<string, Brand> = {
  vcard: { key: "vcard", name: "vCard", fallbackIcon: ContactRoundIcon },
  google: BRANDS.google,
  whatsapp: BRANDS.whatsapp,
  email: { key: "email", name: "Email", fallbackIcon: MailIcon },
  slack: { key: "slack", name: "Slack", fallbackIcon: SlackIcon },
  discord: { key: "discord", name: "Discord", fallbackIcon: MessagesSquareIcon },
  telegram: BRANDS.telegram,
  workflow: { key: "workflow", name: "Social", fallbackIcon: Share2Icon },
}

/** Where each connection source is set up. */
const CONNECT_PATH: Record<string, string> = {
  whatsapp: "settings/integrations?type=whatsapp",
  email: "settings/integrations?type=email",
  slack: "settings/integrations?type=slack",
  discord: "settings/integrations?type=discord",
  telegram: "settings/integrations?type=telegram",
  workflow: "settings/integrations?type=workflow",
}

type Picked = { source: string; connectionId: string }

export default function ImportPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const search = useSearchParams()
  useDocumentTitle(`${t("import.hubTitle")} · ${ws.summary.name}`)
  const base = `/${locale}/w/${ws.slug}`

  const sources = useImportSources(ws.id)
  const google = useGoogleContactsConnections(ws.id)
  const [picked, setPicked] = useState<Picked | null>(null)
  const flowRef = useRef<HTMLDivElement>(null)

  // ?source=whatsapp (the WhatsApp channel form links here) opens that source's
  // first ready connection.
  const wanted = search.get("source")
  const [usedWanted, setUsedWanted] = useState(false)
  useEffect(() => {
    if (usedWanted || !wanted || !sources.data) return
    const conn = sources.data.sources
      .find((s) => s.source === wanted)
      ?.connections.find((c) => c.ready)
    if (conn) setPicked({ source: wanted, connectionId: conn.id })
    setUsedWanted(true)
  }, [usedWanted, wanted, sources.data])

  function choose(p: Picked) {
    setPicked(p)
    requestAnimationFrame(() =>
      flowRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }),
    )
  }

  const pickedConn = picked
    ? sources.data?.sources
        .find((s) => s.source === picked.source)
        ?.connections.find((c) => c.id === picked.connectionId && c.ready)
    : undefined

  const googleList = google.data?.connections ?? []
  const googleState: CardState = googleList.some((c) => c.status === "")
    ? "ready"
    : googleList.length
      ? "reauth"
      : "notConnected"

  return (
    <>
      <SectionHeader
        micro={t("nav.contacts")}
        title={t("import.hubTitle")}
        description={t("import.hubHint")}
        action={
          <Link className="text-sm underline underline-offset-4" href={`${base}/contacts`}>
            {t("contacts.backToList")}
          </Link>
        }
      />

      <section
        className="grid gap-4 px-4 pb-6 sm:grid-cols-2 sm:px-6 xl:grid-cols-3"
        data-import-sources
      >
        <SourceCard
          source="vcard"
          state="ready"
          action={
            <Button
              size="sm"
              nativeButton={false}
              render={<Link href={`${base}/contacts/vcard`} />}
              data-action="open-vcard"
            >
              {t("import.openVcard")}
            </Button>
          }
        />
        <SourceCard
          source="google"
          state={google.data ? googleState : "notConnected"}
          detail={googleList.map((c) => c.email).join(", ") || undefined}
          action={
            <Button
              size="sm"
              variant={googleState === "ready" ? "primary" : "secondary"}
              nativeButton={false}
              render={<Link href={`${base}/contacts/vcard#google-contacts`} />}
              data-action="open-google"
            >
              {googleState === "ready" ? t("import.openGoogle") : t("import.connectGoogle")}
            </Button>
          }
        />

        {!sources.data ? (
          sources.error ? (
            <div className="sm:col-span-2 xl:col-span-3">
              <ErrorState error={sources.error} />
            </div>
          ) : (
            <div className="sm:col-span-2 xl:col-span-3">
              <LoadingRows rows={2} />
            </div>
          )
        ) : (
          sources.data.sources.map((s) => {
            const ready = s.connections.filter((c) => c.ready)
            const live = LIVE_ONLY.has(s.source)
            const state: CardState = live ? "live" : ready.length ? "ready" : "notConnected"
            const connectHref = CONNECT_PATH[s.source]
              ? `${base}/${CONNECT_PATH[s.source]}`
              : undefined
            return (
              <SourceCard
                key={s.source}
                source={s.source}
                state={state}
                detail={
                  s.connections
                    .map((c) => c.label)
                    .filter(Boolean)
                    .join(", ") || undefined
                }
                active={picked?.source === s.source}
                action={
                  state === "ready" ? (
                    <div className="flex flex-wrap gap-2">
                      {ready.map((c) => (
                        <Button
                          key={c.id}
                          size="sm"
                          onClick={() => choose({ source: s.source, connectionId: c.id })}
                          aria-pressed={picked?.connectionId === c.id}
                          data-action="choose-import"
                          data-source={s.source}
                        >
                          {ready.length > 1 && c.label ? <Ltr>{c.label}</Ltr> : t("import.choose")}
                        </Button>
                      ))}
                    </div>
                  ) : connectHref ? (
                    <Button
                      size="sm"
                      variant="secondary"
                      nativeButton={false}
                      render={<Link href={connectHref} />}
                      data-action="connect-source"
                      data-source={s.source}
                    >
                      {live
                        ? t("import.setUp", { source: t(`import.source.${s.source}`) })
                        : t("import.connect", { source: t(`import.source.${s.source}`) })}
                    </Button>
                  ) : null
                }
              />
            )
          })
        )}
      </section>

      <div ref={flowRef} className="scroll-mt-4">
        {picked && pickedConn ? (
          <ImportFlow
            key={`${picked.source}|${picked.connectionId}`}
            source={picked.source}
            conn={pickedConn}
            onClose={() => setPicked(null)}
          />
        ) : null}
      </div>
    </>
  )
}

/** One way in: its mark, where it stands, the steps, and the one thing to do next. */
function SourceCard({
  source,
  state,
  detail,
  active,
  action,
}: {
  source: string
  state: CardState
  detail?: string
  active?: boolean
  action?: React.ReactNode
}) {
  const { t } = useTranslations()
  const brand = SOURCE_BRAND[source] ?? {
    key: source,
    name: source,
    fallbackIcon: MessageCircleIcon,
  }
  // Connected sources are past step one; a file is always at step one.
  const current = state === "ready" && source !== "vcard" ? 1 : 0
  return (
    <Card
      className={active ? "ring-2 ring-primary" : undefined}
      data-import-source={source}
      data-import-state={state}
    >
      <CardHeader className="flex items-center gap-3">
        <BrandIcon brand={brand} size={40} />
        <div className="flex min-w-0 flex-col gap-0.5">
          <CardTitle className="truncate">{t(`import.source.${source}`)}</CardTitle>
          <Status tone={STATE_TONE[state]} className="text-xs">
            {t(`import.state.${state}`)}
          </Status>
        </div>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-4">
        <CardDescription>{t(`import.desc.${source}`)}</CardDescription>
        {detail ? (
          <p className="truncate text-xs text-muted-foreground" data-import-detail>
            <Ltr>{detail}</Ltr>
          </p>
        ) : null}
        <Stepper
          current={current}
          orientation="vertical"
          aria-label={t("import.stepsLabel", { source: t(`import.source.${source}`) })}
        >
          {[1, 2, 3].map((n) => (
            <StepperItem key={n} title={t(`import.steps.${source}.${n}`)} />
          ))}
        </Stepper>
      </CardContent>
      {action ? <CardFooter>{action}</CardFooter> : null}
    </Card>
  )
}

/** Preview a connection's chats or senders, pick some, import them in the background (MH-562). */
function ImportFlow({
  source,
  conn,
  onClose,
}: {
  source: string
  conn: ImportConnection
  onClose: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("inbox:write")

  const [kind, setKind] = useState<"" | "direct" | "group">("")
  const [q, setQ] = useState("")
  const debouncedQ = useDebounced(q, 300)
  const preview = useImportPreview(ws.id, source, {
    connection_id: conn.id,
    kind,
    q: debouncedQ,
  })

  const [selected, setSelected] = useState<Set<string>>(new Set())
  useEffect(() => setSelected(new Set()), [kind, debouncedQ])
  const items = preview.data?.items ?? []
  const anyGroupSelected = items.some((it) => selected.has(it.id) && it.kind === "group")
  const [importParticipants, setImportParticipants] = useState(false)
  const [historyDepth, setHistoryDepth] = useState("")

  const [jobId, setJobId] = useState<string | null>(null)
  const job = useImportJob(ws.id, jobId, jobId ? 1000 : 0)
  const running = job.data?.job.status === "running"
  useEffect(() => {
    if (job.data?.job.status === "done") {
      toast.success(t("import.done", { count: job.data.job.done }))
      preview.mutate()
    } else if (job.data?.job.status === "failed") {
      toast.error(t("import.failed", { error: job.data.job.last_error ?? "" }))
    }
  }, [job.data?.job.status]) // eslint-disable-line react-hooks/exhaustive-deps

  function toggle(id: string) {
    setSelected((cur) => {
      const next = new Set(cur)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const selectableIds = useMemo(
    () => items.filter((it) => !it.already_imported).map((it) => it.id),
    [items],
  )
  const allSelected = selectableIds.length > 0 && selectableIds.every((id) => selected.has(id))

  async function startImport() {
    try {
      const res = await api<{ job: { id: string } }>(
        `/api/w/import/${encodeURIComponent(source)}`,
        {
          json: {
            connection_id: conn.id,
            items: [...selected],
            history_depth: historyDepth ? Number(historyDepth) : undefined,
            import_participants: importParticipants,
          },
          workspaceId: ws.id,
        },
      )
      setJobId(res.job.id)
      setSelected(new Set())
      toast.success(t("import.started"))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const step = job.data?.job.status === "done" ? 3 : selected.size > 0 || running ? 2 : 1

  return (
    <section className="px-4 pb-8 sm:px-6" data-import-flow={source}>
      <Card>
        <CardHeader className="flex flex-wrap items-center gap-3">
          <BrandIcon brand={SOURCE_BRAND[source] ?? SOURCE_BRAND.email} size={32} />
          <div className="flex min-w-0 flex-1 flex-col">
            <CardTitle>{t("import.flowTitle", { source: t(`import.source.${source}`) })}</CardTitle>
            {conn.label ? (
              <CardDescription>
                <Ltr>{conn.label}</Ltr>
              </CardDescription>
            ) : null}
          </div>
          <Button
            size="icon-sm"
            variant="ghost"
            onClick={onClose}
            aria-label={t("common.close")}
            data-action="close-import"
          >
            <XIcon />
          </Button>
        </CardHeader>
        <CardContent className="flex flex-col gap-5">
          <Stepper
            current={step}
            aria-label={t("import.flowTitle", { source: t(`import.source.${source}`) })}
          >
            <StepperItem title={t("import.flowStep.connect")} />
            <StepperItem title={t("import.flowStep.pick")} />
            <StepperItem title={t("import.flowStep.import")} />
          </Stepper>

          <div className="flex flex-wrap items-end gap-4">
            <Field className="w-72 max-w-full">
              <FieldLabel htmlFor="import-q">{t("import.search")}</FieldLabel>
              <Input
                id="import-q"
                value={q}
                onChange={(e) => setQ(e.target.value)}
                placeholder={t("import.searchPlaceholder")}
              />
            </Field>
            <Field className="w-44">
              <FieldLabel htmlFor="import-kind">{t("import.kind")}</FieldLabel>
              <Select
                items={[
                  { value: "", label: t("import.kind.all") },
                  { value: "direct", label: t("import.kind.direct") },
                  { value: "group", label: t("import.kind.group") },
                ]}
                value={kind}
                onValueChange={(v) => setKind((v as "" | "direct" | "group") ?? "")}
              >
                <SelectTrigger id="import-kind" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="">{t("import.kind.all")}</SelectItem>
                  <SelectItem value="direct">{t("import.kind.direct")}</SelectItem>
                  <SelectItem value="group">{t("import.kind.group")}</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field className="w-36">
              <FieldLabel htmlFor="import-depth">{t("import.historyDepth")}</FieldLabel>
              <Input
                id="import-depth"
                type="number"
                min={1}
                max={200}
                value={historyDepth}
                onChange={(e) => setHistoryDepth(e.target.value)}
                placeholder={t("import.historyDepthDefault")}
              />
            </Field>
          </div>

          {anyGroupSelected ? (
            <Field className="flex-row items-center gap-3">
              <Checkbox
                id="import-participants"
                checked={importParticipants}
                onCheckedChange={(v) => setImportParticipants(!!v)}
              />
              <FieldLabel htmlFor="import-participants" className="font-normal">
                {t("import.importParticipants")}
              </FieldLabel>
            </Field>
          ) : null}

          {preview.error ? (
            <ErrorState error={preview.error} />
          ) : !preview.data ? (
            <LoadingRows rows={4} />
          ) : items.length === 0 ? (
            <EmptyState title={t("import.empty")} body={t("import.emptyBody")} />
          ) : (
            <RowsTable<ImportItem>
              data={items}
              getRowId={(it) => it.id}
              rowLabel={(it) => it.name || it.identity}
              label={t("import.name")}
              data-import-items={items.length}
              columns={[
                {
                  id: "select",
                  header: (
                    <Checkbox
                      aria-label={t("import.selectAll")}
                      checked={allSelected}
                      onCheckedChange={(v) => setSelected(v ? new Set(selectableIds) : new Set())}
                      disabled={selectableIds.length === 0}
                    />
                  ),
                  label: t("import.selectAll"),
                  hideable: false,
                  className: "w-10",
                  headerClassName: "w-10",
                  cell: (it) => (
                    <Checkbox
                      aria-label={it.name || it.identity}
                      checked={selected.has(it.id)}
                      disabled={it.already_imported}
                      onCheckedChange={() => toggle(it.id)}
                    />
                  ),
                },
                {
                  id: "name",
                  header: t("import.name"),
                  hideable: false,
                  sortValue: (it) => it.name || it.identity,
                  cell: (it) => (
                    <div data-import-item={it.identity}>
                      <div className="flex items-center gap-2">
                        {it.kind === "group" ? (
                          <UsersIcon className="size-4 text-muted-foreground" />
                        ) : (
                          <MessageCircleIcon className="size-4 text-muted-foreground" />
                        )}
                        <Bdi>{it.name || it.identity}</Bdi>
                      </div>
                      {it.kind === "group" && it.participants?.length ? (
                        <div className="mt-1 text-xs text-muted-foreground">
                          {t("import.participantsCount", { count: it.participants.length })}
                        </div>
                      ) : null}
                    </div>
                  ),
                },
                {
                  id: "identity",
                  header: t("import.identity"),
                  cell: (it) => <Ltr className="text-muted-foreground">{it.identity}</Ltr>,
                },
                {
                  id: "lastMessage",
                  header: t("import.lastMessage"),
                  className: "max-w-xs truncate",
                  cell: (it) => (
                    <span className="text-muted-foreground">
                      <Bdi>{it.last_message}</Bdi>
                    </span>
                  ),
                },
                {
                  id: "status",
                  header: t("import.status"),
                  sortValue: (it) => (it.already_imported ? 1 : 0),
                  cell: (it) =>
                    it.already_imported ? (
                      <Badge variant="outline" data-already-imported={it.identity}>
                        <CheckIcon />
                        {t("import.alreadyImported")}
                      </Badge>
                    ) : null,
                },
              ]}
            />
          )}
        </CardContent>
        <CardFooter className="flex flex-wrap items-center gap-4">
          <Button
            type="button"
            disabled={!canWrite || selected.size === 0 || running}
            onClick={startImport}
            data-action="start-import"
          >
            {running ? t("import.importing") : t("import.importSelected", { count: selected.size })}
          </Button>
          {!canWrite ? <FormHint>{t("contacts.readOnly")}</FormHint> : null}
          {job.data?.job ? (
            <div
              className="text-sm text-muted-foreground"
              data-import-job-status={job.data.job.status}
            >
              {job.data.job.status === "running"
                ? t("import.progress", { done: job.data.job.done, total: job.data.job.total })
                : job.data.job.status === "done"
                  ? t("import.summary", {
                      contacts: job.data.job.contacts_created,
                      conversations: job.data.job.conversations_created,
                      messages: job.data.job.messages_imported,
                      skipped: job.data.job.skipped,
                    })
                  : t("import.failed", { error: job.data.job.last_error ?? "" })}
            </div>
          ) : null}
        </CardFooter>
      </Card>
    </section>
  )
}
