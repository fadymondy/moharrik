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

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
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
      <p className="px-4 py-6 text-sm text-grid-muted" data-node-output="none">
        {live ? t("canvas.output.waiting") : t("canvas.output.none")}
      </p>
    )
  }
  const tone =
    step.status === "ok" ? "default" : step.status === "pending" ? "secondary" : "outline"
  return (
    <div className="space-y-3 px-4 py-4" data-node-output={step.status}>
      <div className="flex flex-wrap items-center gap-2 text-sm">
        <Badge variant={tone}>{t(`automation.step.${step.status}`)}</Badge>
        {step.duration_ms > 0 ? (
          <span className="flex items-center gap-1 text-xs text-grid-muted">
            <ClockIcon className="size-3" />
            {t("canvas.output.ms", { n: step.duration_ms })}
          </span>
        ) : null}
        {rows ? (
          <span className="text-xs text-grid-muted">{t("canvas.items", { n: rows.length })}</span>
        ) : null}
      </div>
      {step.error ? (
        <p className="flex items-start gap-2 text-sm text-grid-danger-text" data-node-error>
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
                size="xs"
                variant={view === "table" ? "secondary" : "ghost"}
                onClick={() => setView("table")}
                aria-pressed={view === "table"}
              >
                {t("canvas.output.table")}
              </Button>
              <Button
                type="button"
                size="xs"
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
              className="max-h-96 overflow-auto rounded-md border border-line bg-grid-card px-3 py-2 font-mono text-xs whitespace-pre-wrap break-all"
              data-output-json
            >
              {step.result}
            </pre>
          )}
        </>
      ) : step.summary ? (
        <p className="text-sm text-grid-muted" dir="auto">
          {step.summary}
        </p>
      ) : null}
    </div>
  )
}

function OutputTable({ rows }: { rows: Record<string, unknown>[] }) {
  const cols = useMemo(() => [...new Set(rows.flatMap((r) => Object.keys(r)))], [rows])
  const cell = (v: unknown) =>
    v == null ? "" : typeof v === "object" ? JSON.stringify(v) : String(v)
  return (
    <div className="max-h-96 overflow-auto rounded-md border border-line" data-output-table>
      <table className="w-full text-xs">
        <thead className="sticky top-0 bg-grid-card">
          <tr>
            <th className="w-8 border-b border-line px-2 py-1.5 text-start font-medium text-grid-muted">
              #
            </th>
            {cols.map((c) => (
              <th
                key={c}
                className="border-b border-line px-2 py-1.5 text-start font-medium"
                dir="ltr"
              >
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i} className="border-b border-line last:border-0">
              <td className="px-2 py-1.5 text-grid-muted tabular-nums">{i + 1}</td>
              {cols.map((c) => (
                <td key={c} className="max-w-60 truncate px-2 py-1.5" dir="auto" title={cell(r[c])}>
                  {cell(r[c])}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
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
  if (failed) return <p className="text-xs text-grid-muted">{t("automation.shotGone")}</p>
  if (!src) return null
  return (
    <a
      href={src}
      target="_blank"
      rel="noreferrer"
      className="block overflow-hidden rounded-md border border-line"
      data-node-shot={position}
    >
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src={src} alt={t("automation.shotAlt", { n: position + 1 })} className="block w-full" />
    </a>
  )
}
