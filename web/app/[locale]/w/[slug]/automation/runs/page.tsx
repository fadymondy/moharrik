"use client"

/*
Runs (MH-526): the ad-hoc "run once" form and the run history, split out of
the old combined Automation page. A single run's own page is still at
/automation/runs/[id].
*/

import { useState } from "react"
import { PlayIcon, XIcon } from "lucide-react"

import { PausedBanner } from "@/components/automation/paused-banner"
import { RunRow, StartRun } from "@/components/automation/run-controls"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import { useWorkspaceEvents } from "@/lib/events"
import { useAutomationSettings, useBrowsers, useRuns } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

export default function AutomationRunsPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("automation.runs")} · ${t("nav.automation")} · ${ws.summary.name}`)
  const canRun = ws.can("automation:run")

  const browsers = useBrowsers(ws.id)
  const settings = useAutomationSettings(ws.id)
  const runs = useRuns(ws.id)

  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "browser") {
      void browsers.mutate()
      return
    }
    if (e.kind !== "schedule") return
    void runs.mutate()
  })

  const active = (browsers.data?.browsers ?? []).filter((b) => !b.revoked_at)
  const paused = settings.data?.paused ?? false
  const hasBrowser = active.length > 0
  const [runningOnce, setRunningOnce] = useState(false)

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("automation.runs")}
        description={t("automation.runsHint")}
        action={
          canRun && hasBrowser ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setRunningOnce((v) => !v)}
              data-run-once
            >
              {runningOnce ? <XIcon /> : <PlayIcon />}
              {runningOnce ? t("common.cancel") : t("automation.flow.runOnce")}
            </Button>
          ) : undefined
        }
      />
      {paused ? <PausedBanner reason={settings.data?.reason} /> : null}
      {canRun && hasBrowser && runningOnce ? (
        <StartRun
          browsers={active}
          paused={paused}
          onStarted={() => {
            setRunningOnce(false)
            runs.mutate()
          }}
        />
      ) : null}
      {runs.error ? (
        <ErrorState error={runs.error} />
      ) : runs.isLoading ? (
        <LoadingRows rows={3} />
      ) : (runs.data?.runs ?? []).length === 0 ? (
        <EmptyState
          title={t("automation.noRuns")}
          body={hasBrowser ? undefined : t("automation.flow.runsNeedBrowser")}
        />
      ) : (
        <ul className="divide-y divide-nq-line border-y border-nq-line">
          {(runs.data?.runs ?? []).map((r) => (
            <RunRow key={r.id} run={r} href={`/${locale}/w/${ws.slug}/automation/runs/${r.id}`} />
          ))}
        </ul>
      )}
      <div className="pb-8" />
    </>
  )
}
