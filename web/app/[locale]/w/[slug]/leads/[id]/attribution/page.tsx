"use client"

import { Attribution, useLeadRecord } from "@/components/leads/lead-record"
import { RecordBody, RecordCard } from "@/components/record/record"
import { useTranslations } from "@/lib/i18n"

/** MH-1157: where the lead came from: source, UTM tags, click id, referrer, landing page. */
export default function LeadAttributionPage() {
  const { t } = useTranslations()
  const { lead } = useLeadRecord()
  return (
    <RecordBody>
      <RecordCard title={t("leads.attribution")} data-lead-attribution>
        <Attribution lead={lead} />
      </RecordCard>
    </RecordBody>
  )
}
