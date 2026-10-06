"use client"

/*
A node's output (MH-480): what one step of a run brought back, n8n-style — a
table when the step produced records (a list extract's rows, a field extract's
record, an extract-all's values), the JSON exactly as the browser sent it
otherwise, and the picture when the step was a screenshot.

The JSON view is the stored text, not a re-serialisation: the run record's
whole point is that it is byte-for-byte what came back.
*/

import { useEffect, useMemo, useState } from "react"
import { CircleAlertIcon, ClockIcon } from "lucide-react"

import { Badge, Button } from "@fadymondy/nasaq/web"
import { RowsTable } from "@/components/nq-table"
import type { RunStep } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

import { parseResult, rowsOf } from "./model"

export function NodeOutput({
  step,
  runId,
  workspaceId,
  live,
}: {
  step?: RunStep
  runId?: string
  workspaceId: string
  live?: boolean
}) {
  const { t } = useTranslations()
  const data = useMemo(() => (step?.result ? parseResult(step.result) : null), [step?.result])
  const rows = useMemo(() => rowsOf(data), [data])
  const [view, setView] = useState<"table" | "json">("table")

  if (!step) {
    return (
      <p className="px-4 py-6 text-sm text-muted-foreground" data-node-output="none">
        {live ? t("canvas.output.waiting") : t("canvas.output.none")}
      </p>
    )
  }
  const tone = step.status === "ok" ? "brand" : step.status === "pending" ? "neutral" : "outline"
  return (
    <div className="space-y-3 px-4 py-4" data-node-output={step.status}>
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge variant={tone}>{t(`automation.step.${step.status}`)}</Badge>
        {step.duration_ms > 0 ? (
          <span className="flex items-center gap-1 text-xs text-muted-foreground">
            <ClockIcon className="size-3" />
            {t("canvas.output.ms", { n: step.duration_ms })}
          </span>
        ) : null}
        {rows ? (
          <span className="text-xs text-muted-foreground">
            {t("canvas.items", { n: rows.length })}
          </span>
        ) : null}
      </div>
      {step.error ? (
        <p className="flex items-start gap-2 text-sm text-nq-danger-text" data-node-error>
          <CircleAlertIcon className="mt-0.5 size-4 shrink-0" />
          <span dir="auto">{step.error}</span>
        </p>
      ) : null}
      {step.has_screenshot && runId ? (
        <Screenshot runId={runId} position={step.position} workspaceId={workspaceId} />
      ) : null}
      {step.result ? (
        <>
          {rows ? (
            <div className="flex gap-1" role="group" aria-label={t("canvas.output.view")}>
              <Button
                type="button"
                size="sm"
                variant={view === "table" ? "secondary" : "ghost"}
                onClick={() => setView("table")}
                aria-pressed={view === "table"}
              >
                {t("canvas.output.table")}
              </Button>
              <Button
                type="button"
                size="sm"
                variant={view === "json" ? "secondary" : "ghost"}
                onClick={() => setView("json")}
                aria-pressed={view === "json"}
              >
                {t("canvas.output.json")}
              </Button>
            </div>
          ) : null}
          {rows && view === "table" ? (
            <OutputTable rows={rows} />
          ) : (
            <pre
              dir="ltr"
              className="max-h-96 overflow-auto rounded-md border border-nq-line bg-card px-3 py-2 font-mono text-xs whitespace-pre-wrap break-all"
              data-output-json
            >
              {step.result}
            </pre>
          )}
        </>
      ) : step.summary ? (
        <p className="text-sm text-muted-foreground" dir="auto">
          {step.summary}
        </p>
      ) : null}
    </div>
  )
}

function OutputTable({ rows }: { rows: Record<string, unknown>[] }) {
  const { t } = useTranslations()
  const cols = useMemo(() => [...new Set(rows.flatMap((r) => Object.keys(r)))], [rows])
  const cell = (v: unknown) =>
    v == null ? "" : typeof v === "object" ? JSON.stringify(v) : String(v)
  return (
    <div className="max-h-96 overflow-auto" data-output-table>
      <RowsTable<{ i: number; r: Record<string, unknown> }>
        data={rows.map((r, i) => ({ i, r }))}
        getRowId={(x) => String(x.i)}
        label={t("canvas.output.table")}
        columns={[
          {
            id: "#",
            header: "#",
            hideable: false,
            sortValue: (x) => x.i,
            headerClassName: "w-8",
            className: "text-muted-foreground tabular-nums",
            cell: (x) => x.i + 1,
          },
          ...cols.map((c) => ({
            id: `c:${c}`,
            header: <span dir="ltr">{c}</span>,
            label: c,
            sortValue: (x: { r: Record<string, unknown> }) => cell(x.r[c]),
            className: "max-w-60 truncate",
            cell: (x: { r: Record<string, unknown> }) => (
              <span dir="auto" title={cell(x.r[c])}>
                {cell(x.r[c])}
              </span>
            ),
          })),
        ]}
      />
    </div>
  )
}

/**
 * A step's screenshot. Fetched with the workspace header and shown as a blob:
 * the route sits in the workspace tier, which a plain <img src> cannot reach.
 */
function Screenshot({
  runId,
  position,
  workspaceId,
}: {
  runId: string
  position: number
  workspaceId: string
}) {
  const { t } = useTranslations()
  const [src, setSrc] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    let url: string | null = null
    let alive = true
    fetch(`/api/w/runs/${encodeURIComponent(runId)}/steps/${position}/screenshot`, {
      headers: { "X-Workspace-ID": workspaceId },
      credentials: "same-origin",
      cache: "no-store",
    })
      .then((r) => (r.ok ? r.blob() : Promise.reject(new Error(String(r.status)))))
      .then((b) => {
        url = URL.createObjectURL(b)
        if (alive) setSrc(url)
      })
      .catch(() => alive && setFailed(true))
    return () => {
      alive = false
      if (url) URL.revokeObjectURL(url)
    }
  }, [runId, position, workspaceId])
  if (failed) return <p className="text-xs text-muted-foreground">{t("automation.shotGone")}</p>
  if (!src) return null
  return (
    <a
      href={src}
      target="_blank"
      rel="noreferrer"
      className="block overflow-hidden rounded-md border border-nq-line"
      data-node-shot={position}
    >
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src={src} alt={t("automation.shotAlt", { n: position + 1 })} className="block w-full" />
    </a>
  )
}
