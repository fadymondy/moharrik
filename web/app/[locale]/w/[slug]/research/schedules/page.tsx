"use client"

/* Research → Schedules (MH-526), split out of the old tabbed /research page. */

import { Suspense } from "react"

import { SectionHeader } from "@/components/public-frame"
import { CapabilityNotes } from "@/components/research/shared"
import { SchedulesTab } from "@/components/research/tabs"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import { useResearchRuns } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function ResearchSchedulesPage() {
  return (
    <Suspense>
      <ResearchSchedules />
    </Suspense>
  )
}

function ResearchSchedules() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("research.tabSchedules")} · ${t("nav.research")} · ${ws.summary.name}`)
  const list = useResearchRuns(ws.id)

  return (
    <>
      <SectionHeader
        title={t("research.tabSchedules")}
        description={t("research.hint")}
      />
      {list.data ? <CapabilityNotes caps={list.data} /> : null}
      <SchedulesTab caps={list.data} />
      <div className="pb-8" />
    </>
  )
}
