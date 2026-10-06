"use client"

// MH-1169: the record's AI context brief. A markdown page the team owns on who this contact,
// company or lead is and how we speak to them. Every AI answer, report and draft about the record
// reads it first. "Generate draft" builds one from what is on record; nothing is saved until the
// member reviews it and presses Save.
import { useEffect, useMemo, useState } from "react"
import useSWR from "swr"
import {
  BrainIcon,
  EyeIcon,
  InfoIcon,
  PencilLineIcon,
  RotateCcwIcon,
  SaveIcon,
  SparklesIcon,
} from "lucide-react"
import { Badge, Button, Skeleton, Toggle, ToggleGroup, toast } from "@fadymondy/nasaq/web"

import { MarkdownEditor } from "@/components/campaigns/markdown-editor"
import { ConfirmButton } from "@/components/confirm-button"
import { FieldError } from "@/components/form-layout"
import { useFormat } from "@/components/overview/overview-cards"
import { RecordBody, RecordCard } from "@/components/record/record"
import { ErrorState } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { renderMarkdownPreview } from "@/lib/markdown"
import { cn } from "@/lib/utils"

export type BriefCollection = "contacts" | "companies" | "leads"

type Brief = { markdown: string; updated_at: string | null; updated_by: string }
type BriefDraft = {
  markdown: string
  no_provider: boolean
  provider?: string
  model?: string
  facts: number
}

const MAX = 20000

export function RecordBrief({
  collection,
  id,
  canWrite,
}: {
  collection: BriefCollection
  id: string
  canWrite: boolean
}) {
  const { t } = useTranslations()
  const f = useFormat()
  const ws = useWorkspace()
  const path = `/api/w/${collection}/${encodeURIComponent(id)}/ai-context`
  const brief = useSWR<Brief>(ws.id ? [path, ws.id] : null, ([p, w]: [string, string]) =>
    api<Brief>(p, { workspaceId: w }),
  )
  const saved = brief.data?.markdown ?? ""
  const [text, setText] = useState<string | null>(null)
  const [mode, setMode] = useState<"write" | "preview">("write")
  const [busy, setBusy] = useState<"save" | "draft" | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const [draft, setDraft] = useState<BriefDraft | null>(null)

  // The editor starts from the saved brief, and follows it until the member types.
  const value = text ?? saved
  const dirty = text !== null && text !== saved
  useEffect(() => {
    if (!canWrite) setMode("preview")
  }, [canWrite])

  const html = useMemo(() => renderMarkdownPreview(value), [value])

  async function save(markdown: string) {
    setBusy("save")
    setErr(null)
    try {
      const b = await api<Brief>(path, { method: "PUT", json: { markdown }, workspaceId: ws.id })
      await brief.mutate(b, { revalidate: false })
      setText(null)
      setDraft(null)
      toast.success(markdown.trim() ? t("brief.saved") : t("brief.cleared"))
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(null)
    }
  }

  async function generate() {
    setBusy("draft")
    setErr(null)
    try {
      const d = await api<BriefDraft>(`${path}/draft`, { method: "POST", workspaceId: ws.id })
      setDraft(d)
      setText(d.markdown)
      setMode("write")
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(null)
    }
  }

  if (brief.error) return <ErrorState error={brief.error} />

  return (
    <RecordBody>
      <div className="grid gap-4 lg:grid-cols-3">
        <RecordCard
          className="lg:col-span-2"
          title={
            <span className="inline-flex items-center gap-2">
              {t("brief.title")}
              {dirty ? <Badge variant="neutral">{t("brief.unsaved")}</Badge> : null}
            </span>
          }
          action={
            brief.data?.updated_at ? (
              <span className="text-muted-foreground" title={f.exact(brief.data.updated_at)}>
                {t("brief.updated", { when: f.ago(brief.data.updated_at) })}
              </span>
            ) : null
          }
          bodyClassName="flex flex-col gap-3 px-4 pb-4"
          data-record-brief={collection}
        >
          {!brief.data ? (
            <Skeleton className="h-72 w-full rounded-lg" />
          ) : (
            <>
              {canWrite ? (
                <div className="flex flex-wrap items-center gap-2">
                  <ToggleGroup
                    value={[mode]}
                    onValueChange={(v) => {
                      if (v[0] === "write" || v[0] === "preview") setMode(v[0])
                    }}
                    aria-label={t("brief.title")}
                  >
                    <Toggle value="write" data-brief-mode="write">
                      <PencilLineIcon />
                      {t("brief.write")}
                    </Toggle>
                    <Toggle value="preview" data-brief-mode="preview">
                      <EyeIcon />
                      {t("brief.preview")}
                    </Toggle>
                  </ToggleGroup>
                  <Button
                    variant="secondary"
                    size="sm"
                    className="ms-auto"
                    onClick={generate}
                    disabled={busy !== null}
                    data-action="brief-draft"
                  >
                    <SparklesIcon />
                    {busy === "draft" ? t("brief.drafting") : t("brief.generate")}
                  </Button>
                </div>
              ) : null}

              {draft ? (
                <p
                  className="flex items-start gap-2 rounded-lg bg-nq-surface-soft px-3 py-2 text-xs text-muted-foreground [&_svg]:mt-0.5 [&_svg]:size-3.5 [&_svg]:shrink-0"
                  data-brief-draft-note
                >
                  <InfoIcon />
                  {draft.no_provider
                    ? t("brief.draftNoProvider", { n: draft.facts })
                    : t("brief.draftReady", { n: draft.facts })}
                </p>
              ) : null}

              {mode === "write" && canWrite ? (
                <MarkdownEditor
                  id="brief-markdown"
                  value={value}
                  onValueChange={(v) => setText(v)}
                  rows={18}
                  className="font-mono text-sm"
                />
              ) : html ? (
                <div
                  className={cn(
                    "min-h-40 rounded-lg border border-border px-4 py-3 text-sm leading-relaxed break-words",
                    "[&_a]:text-primary [&_a]:underline [&_em]:text-muted-foreground [&_h1]:mt-4 [&_h1]:text-lg [&_h1]:font-semibold [&_h2]:mt-4 [&_h2]:text-base [&_h2]:font-semibold [&_h3]:mt-3 [&_h3]:font-medium [&_li]:my-0.5 [&_ol]:list-decimal [&_ol]:ps-5 [&_p]:my-2 [&_ul]:list-disc [&_ul]:ps-5 [&>*:first-child]:mt-0",
                  )}
                  dir="auto"
                  data-brief-preview
                  dangerouslySetInnerHTML={{ __html: html }}
                />
              ) : (
                <div className="flex min-h-40 flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border px-4 py-8 text-center text-sm text-muted-foreground">
                  <BrainIcon className="size-6" />
                  {canWrite ? t("brief.emptyWrite") : t("brief.emptyRead")}
                </div>
              )}

              {canWrite ? (
                <div className="flex flex-wrap items-center gap-2">
                  <Button
                    size="sm"
                    onClick={() => save(value)}
                    disabled={busy !== null || !dirty || value.length > MAX}
                    data-action="brief-save"
                  >
                    <SaveIcon />
                    {busy === "save" ? t("common.working") : t("common.save")}
                  </Button>
                  {dirty ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => {
                        setText(null)
                        setDraft(null)
                      }}
                      disabled={busy !== null}
                      data-action="brief-revert"
                    >
                      <RotateCcwIcon />
                      {t("brief.revert")}
                    </Button>
                  ) : null}
                  {saved && !dirty ? (
                    <ConfirmButton
                      label={t("brief.clear")}
                      title={t("brief.clearTitle")}
                      description={t("brief.clearBody")}
                      confirmLabel={t("brief.clear")}
                      onConfirm={() => save("")}
                    />
                  ) : null}
                  <span
                    className={cn(
                      "ms-auto text-xs tabular-nums",
                      value.length > MAX ? "text-destructive" : "text-muted-foreground",
                    )}
                  >
                    {f.n(value.length)} / {f.n(MAX)}
                  </span>
                  {err ? <FieldError>{err}</FieldError> : null}
                </div>
              ) : null}
            </>
          )}
        </RecordCard>

        <RecordCard title={t("brief.howTitle")} data-brief-how>
          <div className="flex flex-col gap-3 px-4 pb-4 text-sm text-muted-foreground">
            <p>{t("brief.howBody")}</p>
            <ul className="flex list-disc flex-col gap-1.5 ps-5">
              <li>{t("brief.howTone")}</li>
              <li>{t("brief.howFacts")}</li>
              <li>{t("brief.howDraft")}</li>
              <li>{t("brief.howMcp")}</li>
            </ul>
          </div>
        </RecordCard>
      </div>
    </RecordBody>
  )
}
