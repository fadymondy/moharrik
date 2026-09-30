"use client"

/*
Browsers (MH-526): the paired-browser setup flow and list, split out of the
old combined Automation page so it is its own item in the sidebar's Automation
group. This is where a freshly-connected browser lands (MH-483's "Open
automation" and the canvas's "manage schedules" both point elsewhere now, but
a bare old /automation link still redirects here).
*/

import Link from "next/link"
import { useState } from "react"
import { BookOpenIcon, ChevronUpIcon, DownloadIcon, OctagonXIcon, PlayIcon } from "lucide-react"
import { toast } from "@fadymondy/nasaq/web"

import { ConfirmButton } from "@/components/confirm-button"
import { BrowserRow, BrowsersStatus, FlowStep } from "@/components/automation/browser-setup"
import { PausedBanner } from "@/components/automation/paused-banner"
import { SectionHeader } from "@/components/public-frame"
import { ErrorState, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { useWorkspaceEvents } from "@/lib/events"
import { useAutomationSettings, useBrowsers, useExtension } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

export default function AutomationBrowsersPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("automation.browsers")} · ${t("nav.automation")} · ${ws.summary.name}`)
  const canManage = ws.can("automation:manage")

  const browsers = useBrowsers(ws.id)
  const settings = useAutomationSettings(ws.id)
  const ext = useExtension()

  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind !== "browser") return
    void browsers.mutate()
  })

  const list = browsers.data?.browsers ?? []
  const active = list.filter((b) => !b.revoked_at)
  const paused = settings.data?.paused ?? false
  const hasBrowser = active.length > 0
  const hasSites = active.some((b) => b.allowed_hosts.length > 0)
  const setupDone = hasBrowser && hasSites
  const [managing, setManaging] = useState(false)

  async function setPaused(next: boolean) {
    try {
      await api("/api/w/automation/settings", { json: { paused: next }, workspaceId: ws.id })
      toast.success(next ? t("automation.stopped") : t("automation.resumed"))
      settings.mutate()
      browsers.mutate()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const installHref = `/${locale}/w/${ws.slug}/automation/extension`

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("automation.browsers")}
        description={t("automation.browsersHint")}
        action={
          canManage ? (
            paused ? (
              <Button size="sm" onClick={() => setPaused(false)}>
                <PlayIcon />
                {t("automation.resume")}
              </Button>
            ) : (
              <ConfirmButton
                label={
                  <span className="flex items-center gap-2">
                    <OctagonXIcon className="size-4" />
                    {t("automation.stopAll")}
                  </span>
                }
                title={t("automation.stopTitle")}
                description={t("automation.stopBody")}
                confirmLabel={t("automation.stopAll")}
                onConfirm={() => setPaused(true)}
              />
            )
          ) : undefined
        }
      />

      {paused ? <PausedBanner reason={settings.data?.reason} /> : null}

      {browsers.error ? (
        <ErrorState error={browsers.error} />
      ) : browsers.isLoading ? (
        <LoadingRows rows={3} />
      ) : setupDone && !managing ? (
        <BrowsersStatus
          browsers={active}
          onManage={() => setManaging(true)}
          installHref={installHref}
        />
      ) : (
        <section data-setup={setupDone ? "managing" : "open"}>
          <div className="flex flex-wrap items-end justify-between gap-3 px-6 pb-4">
            <div>
              <h2 className="text-base font-medium">
                {setupDone ? t("automation.browsers") : t("automation.flow.title")}
              </h2>
              <p className="text-sm text-muted-foreground">
                {setupDone ? t("automation.browsersHint") : t("automation.flow.hint")}
              </p>
            </div>
            {setupDone ? (
              <Button variant="ghost" size="sm" onClick={() => setManaging(false)} data-manage-done>
                <ChevronUpIcon />
                {t("automation.flow.collapse")}
              </Button>
            ) : null}
          </div>
          <ol className="divide-y divide-nq-line border-y border-nq-line">
            <FlowStep
              n={1}
              state={hasBrowser ? "done" : "current"}
              title={t("automation.flow.install.title")}
              data="install"
            >
              {hasBrowser ? (
                <p className="text-sm text-muted-foreground">{t("automation.flow.install.done")}</p>
              ) : (
                <p className="text-sm text-muted-foreground">{t("automation.flow.install.body")}</p>
              )}
              <div className="flex flex-wrap items-center gap-2">
                {ext.data?.available && ext.data.download_url ? (
                  <Button
                    size="sm"
                    variant={hasBrowser ? "outline" : "default"}
                    nativeButton={false}
                    render={
                      <a href={ext.data.download_url} download={ext.data.file} data-flow-download />
                    }
                  >
                    <DownloadIcon />
                    {t("automation.flow.install.download", { version: ext.data.version ?? "" })}
                  </Button>
                ) : null}
                <Button
                  size="sm"
                  variant="ghost"
                  nativeButton={false}
                  render={<Link href={installHref} />}
                  data-install-link
                >
                  <BookOpenIcon />
                  {t("automation.flow.install.steps")}
                </Button>
              </div>
              {ext.data && !ext.data.available ? (
                <p className="text-xs text-muted-foreground">{ext.data.reason}</p>
              ) : null}
            </FlowStep>

            <FlowStep
              n={2}
              state={hasBrowser ? "done" : "current"}
              title={t("automation.flow.pair.title")}
              data="pair"
            >
              <p className="text-sm text-muted-foreground">
                {hasBrowser
                  ? t("automation.flow.pair.done", { n: active.length })
                  : t("automation.flow.pair.body")}
              </p>
              {/* Connecting is done from the extension (MH-478): its button opens an
                  approval page on this site where the workspace is chosen. Nothing
                  is typed here, so this step only says where to press. */}
              {!canManage ? (
                <p className="text-sm text-muted-foreground" data-connect-how>
                  {t("automation.flow.pair.askAdmin")}
                </p>
              ) : null}
            </FlowStep>

            <FlowStep
              n={3}
              state={hasSites ? "done" : hasBrowser ? "current" : "waiting"}
              title={t("automation.flow.sites.title")}
              data="sites"
            >
              <p className="text-sm text-muted-foreground">
                {!hasBrowser
                  ? t("automation.flow.sites.waiting")
                  : hasSites
                    ? t("automation.flow.sites.done")
                    : t("automation.flow.sites.body")}
              </p>
            </FlowStep>
            {hasBrowser ? (
              <li className="list-none">
                <ul className="divide-y divide-nq-line border-t border-nq-line" data-browser-list>
                  {list.map((b) => (
                    <BrowserRow
                      key={b.id}
                      browser={b}
                      canManage={canManage}
                      onChange={() => browsers.mutate()}
                    />
                  ))}
                </ul>
              </li>
            ) : null}
          </ol>
        </section>
      )}
      <div className="pb-8" />
    </>
  )
}
