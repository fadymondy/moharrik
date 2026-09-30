"use client"

/* Schedules (MH-526): the schedule list and its "new schedule" form, split
   out of the old combined Automation page. */

import Link from "next/link"
import { useState } from "react"
import { DownloadIcon, PlusIcon, XIcon } from "lucide-react"

import { PausedBanner } from "@/components/automation/paused-banner"
import { ScheduleForm } from "@/components/automation/schedule-form"
import { ScheduleRow } from "@/components/automation/schedule-row"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Button } from "@/components/ui/button"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import { useWorkspaceEvents } from "@/lib/events"
import {
  useAutomationSettings,
  useBrowsers,
  useJobs,
  useRuns,
  useSchedules,
  useWorkflows,
} from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

export default function AutomationSchedulesPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("automation.schedules")} · ${t("nav.automation")} · ${ws.summary.name}`)
  const canRun = ws.can("automation:run")

  const browsers = useBrowsers(ws.id)
  const settings = useAutomationSettings(ws.id)
  const schedules = useSchedules(ws.id)
  const runs = useRuns(ws.id)
  const workflows = useWorkflows(ws.id)
  const jobs = useJobs(ws.id)

  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "browser") {
      void browsers.mutate()
      return
    }
    if (e.kind !== "schedule") return
    void schedules.mutate()
    void runs.mutate()
  })

  const active = (browsers.data?.browsers ?? []).filter((b) => !b.revoked_at)
  const paused = settings.data?.paused ?? false
  const hasBrowser = active.length > 0
  const canJob = (jobs.data?.jobs ?? []).some((j) => j.allowed)
  const workflowList = workflows.data?.workflows ?? []
  const [scheduling, setScheduling] = useState(false)
  const installHref = `/${locale}/w/${ws.slug}/automation/extension`

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("automation.schedules")}
        description={t("automation.schedulesHint")}
        action={
          canRun && (hasBrowser || canJob) && (schedules.data?.schedules ?? []).length > 0 ? (
            <Button
              variant="outline"
              size="sm"
              onClick={() => setScheduling((v) => !v)}
              data-new-schedule
            >
              {scheduling ? <XIcon /> : <PlusIcon />}
              {scheduling ? t("common.cancel") : t("automation.sched.new")}
            </Button>
          ) : undefined
        }
      />
      {paused ? <PausedBanner reason={settings.data?.reason} /> : null}
      {schedules.error ? (
        <ErrorState error={schedules.error} />
      ) : schedules.isLoading ? (
        <LoadingRows rows={2} />
      ) : (schedules.data?.schedules ?? []).length === 0 && !scheduling ? (
        // A schedule runs in a paired browser. With none paired, "add a
        // schedule" was a dead end: say what is missing and where to get it.
        !hasBrowser && !canJob ? (
          <EmptyState
            title={t("automation.sched.needBrowserTitle")}
            body={t("automation.sched.needBrowserBody")}
            action={
              <Button
                size="sm"
                nativeButton={false}
                render={<Link href={installHref} />}
                data-schedule-install
              >
                <DownloadIcon />
                {t("automation.ext.link")}
              </Button>
            }
          />
        ) : (
          <EmptyState
            title={t("automation.noSchedules")}
            body={canRun ? t("automation.noSchedulesBody") : undefined}
            action={
              canRun ? (
                <Button size="sm" onClick={() => setScheduling(true)} data-new-schedule>
                  <PlusIcon />
                  {t("automation.sched.new")}
                </Button>
              ) : undefined
            }
          />
        )
      ) : (schedules.data?.schedules ?? []).length > 0 ? (
        <ul className="divide-y divide-nq-line border-y border-nq-line">
          {(schedules.data?.schedules ?? []).map((sc) => (
            <ScheduleRow
              key={sc.id}
              schedule={sc}
              canRun={canRun}
              browsers={active}
              workflows={workflowList}
              jobs={jobs.data?.jobs ?? []}
              onChange={() => {
                schedules.mutate()
                runs.mutate()
              }}
            />
          ))}
        </ul>
      ) : null}
      {canRun && (hasBrowser || canJob) && scheduling ? (
        <ScheduleForm
          browsers={active}
          workflows={hasBrowser ? workflowList : []}
          jobs={jobs.data?.jobs ?? []}
          onAdded={() => {
            setScheduling(false)
            schedules.mutate()
          }}
        />
      ) : null}
      <div className="pb-8" />
    </>
  )
}
