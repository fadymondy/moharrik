"use client"

/*
The workspace profile settings page (MH-478): where an admin keeps the name,
contact and links a preset's job-application inputs default to, and — since
the extension's own-tab Application autofill panel (popup.html) — what that
panel fills a form with too. One profile, one place to edit it; both readers
just read it.
*/

import { SectionHeader } from "@/components/public-frame"
import { ProfilePanel } from "@/components/automation/profile-panel"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import { useAutomationProfile } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

export default function AutomationProfileSettingsPage() {
  const { t } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("presets.profileTitle")} · ${ws.summary.name}`)
  const canManage = ws.can("automation:manage")
  const profile = useAutomationProfile(ws.id)

  return (
    <>
      <SectionHeader
        micro={t("nav.settings")}
        title={t("presets.profileTitle")}
        description={t("presets.profileHint")}
      />
      <ProfilePanel canManage={canManage} data={profile.data} onSaved={() => profile.mutate()} />
      <div className="pb-8" />
    </>
  )
}
