"use client"

/* Research → Evidence (MH-526), split out of the old tabbed /research page. */

import { Suspense } from "react"

import { SectionHeader } from "@/components/public-frame"
import { CapabilityNotes } from "@/components/research/shared"
import { EvidenceTab } from "@/components/research/tabs"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import { useResearchRuns } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function ResearchEvidencePage() {
  return (
    <Suspense>
      <ResearchEvidence />
    </Suspense>
  )
}

function ResearchEvidence() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("research.tabEvidence")} · ${t("nav.research")} · ${ws.summary.name}`)
  const list = useResearchRuns(ws.id)

  return (
    <>
      <SectionHeader
        title={t("research.tabEvidence")}
        description={t("research.hint")}
      />
      {list.data ? <CapabilityNotes caps={list.data} /> : null}
      <EvidenceTab />
      <div className="pb-8" />
    </>
  )
}
