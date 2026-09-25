"use client"

/*
  Search Console's URL inspection and sitemaps (MH-446). Inspecting spends one of the site's 2,000
  daily inspections — Google's budget is per site, shared by every workspace reading it — so the
  budget is shown before the button, and the button is only there for a role that may spend it.
  Sitemaps are listed live from Google; a count Google does not give is a dash, never a zero.
*/

import { useState, type FormEvent, type ReactNode } from "react"
import {
  CheckCircle2Icon,
  CircleDashedIcon,
  ExternalLinkIcon,
  FileCode2Icon,
  ScanSearchIcon,
  TriangleAlertIcon,
  XCircleIcon,
} from "lucide-react"
import { toast } from "sonner"

import { Ltr } from "@/components/copy-field"
import { useFormat } from "@/components/overview/overview-cards"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type GscInspection, type GscSitemapsOut } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useGscInspections, useGscSitemaps } from "@/lib/queries"

function Verdict({ v }: { v: string }) {
  const { t } = useTranslations()
  const icon =
    v === "PASS" ? (
      <CheckCircle2Icon className="size-3.5 text-grid-ok" />
    ) : v === "FAIL" ? (
      <XCircleIcon className="size-3.5 text-grid-danger" />
    ) : v === "PARTIAL" ? (
      <TriangleAlertIcon className="size-3.5 text-grid-warn" />
    ) : (
      <CircleDashedIcon className="size-3.5 text-grid-muted" />
    )
  return (
    <span
      className="inline-flex items-center gap-1.5 text-xs font-medium whitespace-nowrap"
      data-verdict={v || "NONE"}
    >
      {icon}
      {t(`gsci.verdict.${v || "NONE"}`)}
    </span>
  )
}

export function InspectTab({ resourceId, siteLabel }: { resourceId: string; siteLabel: string }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const f = useFormat()
  const data = useGscInspections(ws.id, resourceId)
  const [url, setUrl] = useState("")
  const [busy, setBusy] = useState(false)
  const [open, setOpen] = useState<string | null>(null)

  async function inspect(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      const res = await api<GscInspection>(
        `/api/w/search-console/inspect?resource=${encodeURIComponent(resourceId)}`,
        {
          method: "POST",
          json: { url: url.trim(), locale },
          workspaceId: ws.id,
        },
      )
      setOpen(res.url)
      setUrl("")
      await data.mutate()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  if (data.error) return <ErrorState error={data.error} />
  if (!data.data) return <LoadingRows rows={3} />
  const q = data.data.quota
  const list = data.data.inspections ?? []
  const left = Math.max(0, q.limit - q.used)
  return (
    <div className="pb-10" data-gsc-inspect>
      <div className="space-y-3 px-6 pb-5">
        <p className="max-w-3xl text-sm text-grid-muted">{t("gsci.hint", { site: siteLabel })}</p>
        {data.data.can_write ? (
          <form onSubmit={inspect} className="flex max-w-3xl flex-col gap-2 sm:flex-row">
            <Input
              type="url"
              required
              dir="ltr"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="https://"
              aria-label={t("gsci.url")}
              data-inspect-url
            />
            <Button type="submit" disabled={busy || !url.trim() || left === 0} data-inspect>
              <ScanSearchIcon />
              {busy ? t("gsci.inspecting") : t("gsci.inspect")}
            </Button>
          </form>
        ) : (
          <p className="text-sm text-grid-muted">{t("gsci.managerOnly")}</p>
        )}
        <p className="text-xs text-grid-muted" data-inspect-quota>
          {t("gsci.quota", { used: f.n(q.used), limit: f.n(q.limit) })}
        </p>
      </div>
      {list.length === 0 ? (
        <EmptyState
          title={t("gsci.none")}
          body={data.data.can_write ? t("gsci.noneBody") : undefined}
        />
      ) : (
        <ul className="divide-y divide-line border-y border-line">
          {list.map((i) => (
            <li key={i.url} data-inspection={i.url}>
              <button
                type="button"
                onClick={() => setOpen(open === i.url ? null : i.url)}
                className="flex w-full flex-col gap-1 px-6 py-3 text-start hover:bg-grid-soft sm:flex-row sm:items-center sm:gap-4"
                aria-expanded={open === i.url}
              >
                <span
                  dir="ltr"
                  className="min-w-0 flex-1 truncate font-mono text-sm text-grid-fg rtl:text-right"
                  title={i.url}
                >
                  {i.url}
                </span>
                <span className="flex shrink-0 items-center gap-3">
                  <Verdict v={i.verdict} />
                  <span className="text-xs text-grid-muted">{i.coverage_state || "—"}</span>
                  <span className="text-xs text-grid-muted" title={f.exact(i.inspected_at)}>
                    {f.ago(i.inspected_at)}
                  </span>
                </span>
              </button>
              {open === i.url ? <InspectionDetail i={i} /> : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function InspectionDetail({ i }: { i: GscInspection }) {
  const { t } = useTranslations()
  const f = useFormat()
  const rows: [string, ReactNode][] = [
    [t("gsci.coverage"), i.coverage_state || "—"],
    [t("gsci.lastCrawl"), i.last_crawl_time ? f.exact(i.last_crawl_time) : t("gsci.neverCrawled")],
    [t("gsci.fetch"), i.page_fetch_state || "—"],
    [t("gsci.robots"), i.robots_txt_state || "—"],
    [t("gsci.indexing"), i.indexing_state || "—"],
    [t("gsci.googleCanonical"), i.google_canonical ? <Ltr mono>{i.google_canonical}</Ltr> : "—"],
    [t("gsci.userCanonical"), i.user_canonical ? <Ltr mono>{i.user_canonical}</Ltr> : "—"],
    [t("gsci.mobile"), i.mobile_verdict ? <Verdict v={i.mobile_verdict} /> : "—"],
    [t("gsci.rich"), i.rich_results_verdict ? <Verdict v={i.rich_results_verdict} /> : "—"],
  ]
  return (
    <div className="border-t border-line bg-grid-card px-6 py-4" data-inspection-detail>
      <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[12rem_1fr]">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-grid-muted">{k}</dt>
            <dd className="min-w-0 break-words text-grid-fg">{v}</dd>
          </div>
        ))}
      </dl>
      {i.result_link ? (
        <Button
          variant="outline"
          size="sm"
          className="mt-3"
          nativeButton={false}
          render={<a href={i.result_link} target="_blank" rel="noreferrer" />}
        >
          <ExternalLinkIcon />
          {t("gsci.openInGsc")}
        </Button>
      ) : null}
    </div>
  )
}

export function SitemapsTab({ resourceId, site }: { resourceId: string; site: string }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const f = useFormat()
  const data = useGscSitemaps(ws.id, resourceId)
  const origin = site.startsWith("sc-domain:")
    ? `https://${site.slice("sc-domain:".length)}`
    : site.replace(/\/$/, "")
  const [url, setUrl] = useState(`${origin}/sitemap.xml`)
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      const out = await api<GscSitemapsOut>(
        `/api/w/search-console/sitemaps?resource=${encodeURIComponent(resourceId)}`,
        { method: "POST", json: { url: url.trim() }, workspaceId: ws.id },
      )
      await data.mutate(out, { revalidate: false })
      toast.success(t("gscs.submitted"))
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  if (data.error) return <ErrorState error={data.error} />
  if (!data.data) return <LoadingRows rows={2} />
  const list = data.data.sitemaps ?? []
  const n = (v: number | null) => (v == null ? "—" : f.n(v))
  return (
    <div className="pb-10" data-gsc-sitemaps>
      <div className="space-y-3 px-6 pb-5">
        <p className="max-w-3xl text-sm text-grid-muted">{t("gscs.hint")}</p>
        {data.data.can_write ? (
          <form onSubmit={submit} className="flex max-w-3xl flex-col gap-2 sm:flex-row">
            <Input
              type="url"
              required
              dir="ltr"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              aria-label={t("gscs.url")}
              data-sitemap-url
            />
            <Button type="submit" disabled={busy || !url.trim()} data-sitemap-submit>
              <FileCode2Icon />
              {busy ? t("gscs.submitting") : t("gscs.submit")}
            </Button>
          </form>
        ) : (
          <p className="text-sm text-grid-muted">{t("gsci.managerOnly")}</p>
        )}
      </div>
      {data.data.error ? (
        <div
          role="alert"
          className="mx-6 mb-4 flex items-start gap-3 border border-line bg-grid-card px-4 py-3 text-sm"
          data-data-error
        >
          <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-grid-danger" />
          <p className="break-words text-grid-danger-text">
            <Ltr>{data.data.error}</Ltr>
          </p>
        </div>
      ) : list.length === 0 ? (
        <EmptyState title={t("gscs.none")} body={t("gscs.noneBody")} />
      ) : (
        <div className="border-y border-line">
          <Table className="table-fixed">
            <TableHeader>
              <TableRow>
                <TableHead className="ps-6">{t("gscs.sitemap")}</TableHead>
                <TableHead className="hidden w-40 md:table-cell">
                  {t("gscs.lastSubmitted")}
                </TableHead>
                <TableHead className="w-32">{t("gscs.status")}</TableHead>
                <TableHead className="w-24 text-end">{t("gscs.urls")}</TableHead>
                <TableHead className="hidden w-24 pe-6 text-end sm:table-cell">
                  {t("gscs.issues")}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.map((m) => (
                <TableRow key={m.path} data-sitemap={m.path}>
                  <TableCell className="ps-6">
                    <span
                      dir="ltr"
                      className="block truncate font-mono text-sm rtl:text-right"
                      title={m.path}
                    >
                      {m.path}
                    </span>
                  </TableCell>
                  <TableCell className="hidden text-xs text-grid-muted md:table-cell">
                    {m.last_submitted ? f.ago(m.last_submitted) : "—"}
                  </TableCell>
                  <TableCell>
                    {m.is_pending ? (
                      <Badge variant="outline">{t("gscs.pending")}</Badge>
                    ) : m.last_downloaded ? (
                      <span className="text-xs text-grid-muted" title={f.exact(m.last_downloaded)}>
                        {t("gscs.read", { when: f.ago(m.last_downloaded) })}
                      </span>
                    ) : (
                      "—"
                    )}
                  </TableCell>
                  <TableCell className="text-end tabular-nums">{n(m.submitted)}</TableCell>
                  <TableCell className="hidden pe-6 text-end text-xs tabular-nums sm:table-cell">
                    {m.errors || m.warnings
                      ? t("gscs.issuesN", { e: n(m.errors), w: n(m.warnings) })
                      : m.errors === 0 && m.warnings === 0
                        ? t("gscs.noIssues")
                        : "—"}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  )
}
