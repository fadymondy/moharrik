"use client"

/* Limits & settings (MH-526): the workspace's own automation limits, split
   out of the old combined Automation page so it is its own sidebar item. */

import { LimitsPanel } from "@/components/automation/limits-panel"
import { SectionHeader } from "@/components/public-frame"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import { useAutomationSettings } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { Bdi } from "@/components/bidi"

export default function AutomationSettingsPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("automation.limitsAndSettings")} · ${t("nav.automation")} · ${ws.summary.name}`)
  const canManage = ws.can("automation:manage")
  const settings = useAutomationSettings(ws.id)

  return (
    <>
      <SectionHeader micro={<Bdi>{ws.summary.name}</Bdi>} title={t("automation.limitsAndSettings")} description={t("automation.limits.hint")} />
      <LimitsPanel settings={settings.data} canManage={canManage} onSaved={() => settings.mutate()} />
      <div className="pb-8" />
    </>
  )
}
