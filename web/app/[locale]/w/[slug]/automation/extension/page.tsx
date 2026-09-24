"use client"

/*
Installing the browser extension (MH-399).

There used to be no way in: pairing worked, but only for someone who could
load the extension's source folder by hand. This page hands out the packaged
zip — its version, size and the sha256 of the exact file the download serves —
walks through loading it unpacked, and ends at the pairing step, with the
extension's own Connect button, which opens the approval page (MH-478).
*/

import Link from "next/link"
import { useMemo } from "react"
import { ArrowLeftIcon, DownloadIcon, PuzzleIcon, TriangleAlertIcon } from "lucide-react"

import { CopyField } from "@/components/copy-field"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import { intlTag } from "@/lib/i18n-locale"
import { useWorkspaceEvents } from "@/lib/events"
import { useBrowsers, useExtension } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function ExtensionInstallPage() {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("automation.ext.title")} · ${ws.summary.name}`)
  const canManage = ws.can("automation:manage")
  const ext = useExtension()
  const browsers = useBrowsers(ws.id)
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "browser") void browsers.mutate()
  })
  const info = ext.data
  const when = useMemo(() => new Intl.DateTimeFormat(intlTag(locale), { dateStyle: "medium", timeStyle: "short" }), [locale])
  const paired = (browsers.data?.browsers ?? []).filter((b) => !b.revoked_at)
  const fileName = info?.file ?? "moharrik-extension.zip"

  return (
    <>
      <SectionHeader
        micro={ws.summary.name}
        title={t("automation.ext.title")}
        description={t("automation.ext.hint")}
        action={
          <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation/browsers`} />}>
            <ArrowLeftIcon className="rtl:rotate-180" />
            {t("automation.backToAutomation")}
          </Button>
        }
      />

      {/* ---- the download ---- */}
      <section data-download>
        {ext.error ? (
          <ErrorState error={ext.error} />
        ) : !info ? (
          <LoadingRows rows={2} />
        ) : !info.available ? (
          <EmptyState title={t("automation.ext.unavailable")} body={info.reason} />
        ) : (
          <div className="space-y-4 border-y border-line px-6 py-6" data-version={info.version} data-sha256={info.sha256}>
            <div className="flex flex-wrap items-center gap-3">
              <PuzzleIcon className="size-5 text-grid-muted" />
              <p className="text-base font-medium">{t("automation.ext.name", { version: info.version ?? "" })}</p>
              {info.dev ? <Badge variant="outline">{t("automation.ext.devBadge")}</Badge> : null}
              <span className="text-sm text-grid-muted">
                {t("automation.ext.size", { kb: formatNumber(Math.max(1, Math.round((info.bytes ?? 0) / 1024))) })}
                {info.built_at ? ` · ${t("automation.ext.built", { date: when.format(new Date(info.built_at)) })}` : ""}
              </span>
            </div>
            {info.dev ? (
              <p className="flex items-start gap-2 text-sm text-grid-muted">
                <TriangleAlertIcon className="mt-0.5 size-4 shrink-0" />
                {t("automation.ext.devBody", { site: info.site ?? "" })}
              </p>
            ) : null}
            <div>
              <Button nativeButton={false} render={<a href={info.download_url} download={fileName} data-download-link />}>
                <DownloadIcon />
                {t("automation.ext.download", { file: fileName })}
              </Button>
            </div>
            <div className="space-y-1.5">
              <p className="text-sm font-medium">{t("automation.ext.checksum")}</p>
              <CopyField value={info.sha256 ?? ""} label="SHA-256" nowrap />
              <p className="text-xs text-grid-muted">{t("automation.ext.checksumHint")}</p>
              {[`certutil -hashfile ${fileName} SHA256`, `shasum -a 256 ${fileName}`].map((cmd) => (
                <code key={cmd} dir="ltr" className="block overflow-x-auto font-mono text-xs whitespace-nowrap text-grid-muted">
                  {cmd}
                </code>
              ))}
            </div>
          </div>
        )}
      </section>

      {/* ---- loading it unpacked ---- */}
      <section className="mt-8">
        <h2 className="px-6 pb-1 text-base font-medium">{t("automation.ext.installTitle")}</h2>
        <p className="px-6 pb-4 text-sm text-grid-muted">{t("automation.ext.installHint")}</p>
        <ol className="divide-y divide-line border-y border-line" data-install-steps>
          <Step n={1} title={t("automation.ext.s1Title")} body={t("automation.ext.s1Body")} />
          <Step n={2} title={t("automation.ext.s2Title")} body={t("automation.ext.s2Body")}>
            <div className="max-w-sm">
              <CopyField value="chrome://extensions" nowrap />
            </div>
            <p className="text-xs text-grid-muted">{t("automation.ext.s2Other")}</p>
          </Step>
          <Step n={3} title={t("automation.ext.s3Title")} body={t("automation.ext.s3Body")} />
          <Step n={4} title={t("automation.ext.s4Title")} body={t("automation.ext.s4Body")} />
          <Step n={5} title={t("automation.ext.s5Title")} body={t("automation.ext.s5Body")} />
        </ol>
      </section>

      {/* ---- pairing ---- */}
      <section className="mt-8" data-pairing-step>
        <h2 className="px-6 pb-1 text-base font-medium">{t("automation.ext.pairTitle")}</h2>
        <p className="px-6 pb-4 text-sm text-grid-muted">{t("automation.ext.pairHint")}</p>
        <ol className="divide-y divide-line border-y border-line">
          <Step n={6} title={t("automation.ext.s6Title")} body={t("automation.ext.s6Body")} />
          <Step n={7} title={t("automation.ext.s7Title")} body={canManage ? t("automation.ext.s7Body") : t("automation.ext.s7AskAdmin")}>
            {info?.site ? (
              <div className="max-w-md min-w-0 space-y-1">
                <p className="text-xs text-grid-muted">{t("automation.ext.siteField")}</p>
                <CopyField value={info.site} nowrap />
              </div>
            ) : null}
          </Step>
        </ol>
        {paired.length > 0 ? (
          <p className="px-6 pt-6 text-sm" data-paired-count={paired.length}>
            {t("automation.ext.pairedSoFar", { n: paired.length })}{" "}
            <Link className="underline" href={`/${locale}/w/${ws.slug}/automation/browsers`}>
              {t("automation.ext.seeBrowsers")}
            </Link>
          </p>
        ) : null}
      </section>

      {/* ---- updating ---- */}
      <section className="mt-8">
        <h2 className="px-6 pb-1 text-base font-medium">{t("automation.ext.updateTitle")}</h2>
        <p className="px-6 text-sm text-grid-muted">{t("automation.ext.updateBody")}</p>
      </section>

      {/* ---- self-updating, for managed browsers (MH-483) ---- */}
      {info?.available ? (
        <section className="mt-8" data-managed={info.signed ? "signed" : "unsigned"}>
          <h2 className="px-6 pb-1 text-base font-medium">{t("automation.ext.managedTitle")}</h2>
          {info.signed && info.extension_id && info.update_url ? (
            <div className="space-y-3 px-6">
              <p className="text-sm text-grid-muted">{t("automation.ext.managedBody")}</p>
              <div className="space-y-1">
                <p className="text-xs text-grid-muted">{t("automation.ext.managedPolicy")}</p>
                <CopyField value={`${info.extension_id};${info.update_url}`} nowrap />
              </div>
              <div className="space-y-1">
                <p className="text-xs text-grid-muted">{t("automation.ext.managedId")}</p>
                <CopyField value={info.extension_id} nowrap />
              </div>
            </div>
          ) : (
            <p className="px-6 text-sm text-grid-muted">{t("automation.ext.managedUnsigned")}</p>
          )}
        </section>
      ) : null}
      <div className="pb-8" />
    </>
  )
}

function Step({ n, title, body, children }: { n: number; title: string; body: string; children?: React.ReactNode }) {
  return (
    <li className="flex gap-4 px-6 py-4" data-install-step={n}>
      <span className="flex size-7 shrink-0 items-center justify-center rounded-full border border-line text-sm tabular-nums">{n}</span>
      <div className="min-w-0 flex-1 space-y-2">
        <p className="text-sm font-medium">{title}</p>
        <p className="text-sm text-grid-muted">{body}</p>
        {children}
      </div>
    </li>
  )
}
