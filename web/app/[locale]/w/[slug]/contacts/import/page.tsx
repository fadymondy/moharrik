"use client"

import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { useEffect, useMemo, useState } from "react"
import { CheckIcon, MessageCircleIcon, UsersIcon } from "lucide-react"
import { toast } from "sonner"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { SectionHeader } from "@/components/public-frame"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type ImportItem } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useDebounced } from "@/components/crm/contact-picker"
import { useImportJob, useImportPreview, useImportSources } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"
import { Ltr } from "@/components/copy-field"

/*
Contacts → "Import from a connection" (MH-562): pick a source and connection,
preview what it has with filters, select some of it, import — in the
background, with progress. One page for every source on internal/imports'
Provider interface; today that is WhatsApp, with email and MH-563/564's
Telegram/Slack/Discord to follow on the same shape.
*/
export default function ImportPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const search = useSearchParams()
  useDocumentTitle(`${t("import.title")} · ${ws.summary.name}`)
  const base = `/${locale}/w/${ws.slug}`
  const canWrite = ws.can("inbox:write")

  const sources = useImportSources(ws.id)
  const [source, setSource] = useState<string>(search.get("source") ?? "")
  useEffect(() => {
    if (!source && sources.data?.sources?.length) setSource(sources.data.sources[0].source)
  }, [source, sources.data])

  const conn = sources.data?.sources.find((s) => s.source === source)?.connections[0]

  const [kind, setKind] = useState<"" | "direct" | "group">("")
  const [q, setQ] = useState("")
  const debouncedQ = useDebounced(q, 300)
  const preview = useImportPreview(ws.id, conn?.ready ? source : null, { connection_id: conn?.id, kind, q: debouncedQ })

  const [selected, setSelected] = useState<Set<string>>(new Set())
  useEffect(() => setSelected(new Set()), [source, kind, debouncedQ])
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

  const selectableIds = useMemo(() => items.filter((it) => !it.already_imported).map((it) => it.id), [items])
  const allSelected = selectableIds.length > 0 && selectableIds.every((id) => selected.has(id))

  async function startImport() {
    try {
      const res = await api<{ job: { id: string } }>(`/api/w/import/${encodeURIComponent(source)}`, {
        json: {
          connection_id: conn?.id,
          items: [...selected],
          history_depth: historyDepth ? Number(historyDepth) : undefined,
          import_participants: importParticipants,
        },
        workspaceId: ws.id,
      })
      setJobId(res.job.id)
      setSelected(new Set())
      toast.success(t("import.started"))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  return (
    <>
      <SectionHeader
        micro={t("nav.contacts")}
        title={t("import.title")}
        description={t("import.hint")}
        action={
          <Link className="text-sm underline underline-offset-4" href={`${base}/contacts`}>
            {t("contacts.backToList")}
          </Link>
        }
      />

      <div className="flex flex-wrap items-end gap-4 px-6 pb-4">
        <Field className="w-56">
          <FieldLabel htmlFor="import-source">{t("import.source")}</FieldLabel>
          <Select
            items={(sources.data?.sources ?? []).map((s) => ({ value: s.source, label: t(`import.source.${s.source}`) }))}
            value={source}
            onValueChange={(v) => setSource(String(v ?? ""))}
          >
            <SelectTrigger id="import-source" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {(sources.data?.sources ?? []).map((s) => (
                <SelectItem key={s.source} value={s.source}>
                  {t(`import.source.${s.source}`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>

        {conn && !conn.ready ? (
          <FieldDescription className="text-grid-warn-text" data-import-not-ready={source}>
            {conn.note || t("import.notReady")}{" "}
            {source === "whatsapp" ? (
              <Link className="underline underline-offset-4" href={`${base}/inbox/whatsapp`}>
                {t("import.linkWhatsApp")}
              </Link>
            ) : null}
          </FieldDescription>
        ) : null}
      </div>

      {conn?.ready ? (
        <>
          <div className="flex flex-wrap items-end gap-4 px-6 pb-4">
            <Field className="w-72">
              <FieldLabel htmlFor="import-q">{t("import.search")}</FieldLabel>
              <Input id="import-q" value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("import.searchPlaceholder")} />
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
            <div className="px-6 pb-4">
              <Field orientation="horizontal">
                <Checkbox id="import-participants" checked={importParticipants} onCheckedChange={(v) => setImportParticipants(!!v)} />
                <FieldLabel htmlFor="import-participants" className="font-normal">
                  {t("import.importParticipants")}
                </FieldLabel>
              </Field>
            </div>
          ) : null}

          {preview.error ? (
            <div className="px-6">
              <ErrorState error={preview.error} />
            </div>
          ) : !preview.data ? (
            <LoadingRows rows={4} />
          ) : items.length === 0 ? (
            <EmptyState title={t("import.empty")} body={t("import.emptyBody")} />
          ) : (
            <div className="border-y border-line">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="ps-6 w-10">
                      <Checkbox
                        aria-label={t("import.selectAll")}
                        checked={allSelected}
                        onCheckedChange={(v) => setSelected(v ? new Set(selectableIds) : new Set())}
                        disabled={selectableIds.length === 0}
                      />
                    </TableHead>
                    <TableHead>{t("import.name")}</TableHead>
                    <TableHead>{t("import.identity")}</TableHead>
                    <TableHead>{t("import.lastMessage")}</TableHead>
                    <TableHead className="pe-6">{t("import.status")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((it: ImportItem) => (
                    <TableRow key={it.id} data-import-item={it.identity}>
                      <TableCell className="ps-6">
                        <Checkbox
                          checked={selected.has(it.id)}
                          disabled={it.already_imported}
                          onCheckedChange={() => toggle(it.id)}
                        />
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-2">
                          {it.kind === "group" ? <UsersIcon className="size-4 text-grid-muted" /> : <MessageCircleIcon className="size-4 text-grid-muted" />}
                          <Bdi>{it.name || it.identity}</Bdi>
                        </div>
                        {it.kind === "group" && it.participants?.length ? (
                          <div className="mt-1 text-xs text-grid-muted">{t("import.participantsCount", { count: it.participants.length })}</div>
                        ) : null}
                      </TableCell>
                      <TableCell className="text-grid-muted">
                        <Ltr>{it.identity}</Ltr>
                      </TableCell>
                      <TableCell className="max-w-xs truncate text-grid-muted">
                        <Bdi>{it.last_message}</Bdi>
                      </TableCell>
                      <TableCell className="pe-6">
                        {it.already_imported ? (
                          <Badge variant="outline" data-already-imported={it.identity}>
                            <CheckIcon />
                            {t("import.alreadyImported")}
                          </Badge>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}

          <div className="flex flex-wrap items-center gap-4 px-6 py-6">
            <Button type="button" disabled={!canWrite || selected.size === 0 || running} onClick={startImport} data-action="start-import">
              {running ? t("import.importing") : t("import.importSelected", { count: selected.size })}
            </Button>
            {!canWrite ? <FieldDescription>{t("contacts.readOnly")}</FieldDescription> : null}
            {job.data?.job ? (
              <div className="text-sm text-grid-muted" data-import-job-status={job.data.job.status}>
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
          </div>
        </>
      ) : null}
    </>
  )
}
