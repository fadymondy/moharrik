"use client"

/*
  YouTube Reporting (MH-443): the channel's daily report jobs and the reports copied so far, each
  downloadable as the CSV YouTube produced. Google keeps reports 60 days; Moharrik keeps them after.
  A job's first reports arrive about two days after it is created, and the panel says so rather than
  showing an empty list as if nothing would come. Checking now creates missing jobs on the channel's
  YouTube account, so only an admin sees that button.
*/

import { useState } from "react"
import { DownloadIcon, FileSpreadsheetIcon, RefreshCwIcon } from "lucide-react"
import { toast } from "sonner"

import { useFormat } from "@/components/overview/overview-cards"
import { ErrorState, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, downloadFile, type YtReports } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useYtReports } from "@/lib/queries"

const FAMILIES = ["channel_basic", "channel_traffic_source", "channel_device_os"]

function family(typeID: string) {
  const i = typeID.lastIndexOf("_a")
  return i > 0 ? typeID.slice(0, i) : typeID
}

export function YouTubeReports({ resourceId }: { resourceId: string }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const f = useFormat()
  const data = useYtReports(ws.id, resourceId)
  const [busy, setBusy] = useState(false)
  const manage = ws.can("integrations:manage")

  async function check() {
    setBusy(true)
    try {
      const out = await api<YtReports>(`/api/w/youtube/reports/refresh?resource=${encodeURIComponent(resourceId)}`, { method: "POST", workspaceId: ws.id })
      await data.mutate(out, { revalidate: false })
      toast.success(t("ytr.checked"))
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function download(id: string, name: string) {
    try {
      await downloadFile(`/api/w/youtube/reports/${encodeURIComponent(id)}/csv?resource=${encodeURIComponent(resourceId)}`, { workspaceId: ws.id, fallbackName: name })
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const label = (typeID: string) => {
    const fam = family(typeID)
    return FAMILIES.includes(fam) ? t(`ytr.family.${fam}`) : typeID
  }

  return (
    <section className="pt-8" data-yt-reports>
      <div className="flex flex-wrap items-start gap-3 px-6 pb-3">
        <div className="min-w-0 flex-1">
          <h2 className="flex items-center gap-2 text-sm font-medium [&_svg]:size-4 [&_svg]:text-grid-muted">
            <FileSpreadsheetIcon />
            {t("ytr.title")}
          </h2>
          <p className="mt-1 max-w-3xl text-sm text-grid-muted">{t("ytr.hint")}</p>
        </div>
        {manage ? (
          <Button variant="outline" size="sm" onClick={check} disabled={busy} data-reports-check>
            <RefreshCwIcon className={busy ? "animate-spin" : undefined} />
            {busy ? t("ytr.checking") : t("ytr.check")}
          </Button>
        ) : null}
      </div>
      {data.error ? (
        <ErrorState error={data.error} />
      ) : !data.data ? (
        <LoadingRows rows={2} />
      ) : (
        <>
          <ul className="divide-y divide-line border-y border-line text-sm">
            {(data.data.jobs ?? []).length === 0 ? (
              <li className="px-6 py-4 text-grid-muted" data-no-jobs>
                {data.data.checked_at ? t("ytr.noJobs") : t("ytr.notChecked")}
              </li>
            ) : (
              (data.data.jobs ?? []).map((j) => (
                <li key={j.job_id} className="flex flex-wrap items-center gap-x-4 gap-y-1 px-6 py-3" data-report-job={j.report_type_id}>
                  <span className="font-medium text-grid-fg">{label(j.report_type_id)}</span>
                  <span className="font-mono text-xs text-grid-muted" dir="ltr">
                    {j.report_type_id}
                  </span>
                  <span className="ms-auto text-xs text-grid-muted">
                    {j.reports > 0
                      ? t("ytr.jobReports", { n: f.n(j.reports), day: j.latest_day ? f.date(j.latest_day.slice(0, 10)) : "—" })
                      : t("ytr.jobWaiting", { when: j.created_at ? f.ago(j.created_at) : "—" })}
                  </span>
                </li>
              ))
            )}
          </ul>
          {(data.data.reports ?? []).length > 0 ? (
            <div className="border-b border-line">
              <Table className="table-fixed" data-reports-table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-36 ps-6">{t("ytr.day")}</TableHead>
                    <TableHead>{t("ytr.report")}</TableHead>
                    <TableHead className="hidden w-24 text-end sm:table-cell">{t("ytr.rows")}</TableHead>
                    <TableHead className="w-32 pe-6 text-end">
                      <span className="sr-only">{t("common.actions")}</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(data.data.reports ?? []).map((r) => {
                    const day = r.start_time ? r.start_time.slice(0, 10) : ""
                    return (
                      <TableRow key={r.report_id} data-report={r.report_id}>
                        <TableCell className="ps-6">{day ? f.date(day) : "—"}</TableCell>
                        <TableCell className="truncate">{label(r.report_type_id)}</TableCell>
                        <TableCell className="hidden text-end tabular-nums sm:table-cell">{f.n(r.row_count)}</TableCell>
                        <TableCell className="pe-6 text-end">
                          <Button variant="ghost" size="sm" onClick={() => download(r.report_id, `${r.report_type_id}-${day}.csv`)} data-report-download>
                            <DownloadIcon />
                            CSV
                          </Button>
                        </TableCell>
                      </TableRow>
                    )
                  })}
                </TableBody>
              </Table>
            </div>
          ) : null}
        </>
      )}
    </section>
  )
}
