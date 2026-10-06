"use client"

/* Research → Runs (MH-526), split out of the old tabbed /research page. */

import { Suspense } from "react"

import { SectionHeader } from "@/components/public-frame"
import { CapabilityNotes } from "@/components/research/shared"
import { RunsTab } from "@/components/research/tabs"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import { useResearchRuns } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function ResearchRunsPage() {
  return (
    <Suspense>
      <ResearchRuns />
    </Suspense>
  )
}

function ResearchRuns() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("research.tabRuns")} · ${t("nav.research")} · ${ws.summary.name}`)
  const list = useResearchRuns(ws.id)

  return (
    <>
      <SectionHeader title={t("nav.research")} description={t("research.hint")} />
      {list.data ? <CapabilityNotes caps={list.data} /> : null}
      <RunsTab list={list} />
      <div className="pb-8" />
    </>
  )
}
