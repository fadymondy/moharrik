"use client"

import { useRouter } from "next/navigation"
import { useEffect } from "react"

import { DealForm } from "@/components/crm/deals"
import { useDealRecord } from "@/components/deals/deal-record"
import { RecordBody, RecordCard } from "@/components/record/record"
import { useTranslations } from "@/lib/i18n"

/** MH-1161: the deal's editable details: title, value, stage, contact, company, close date. */
export default function DealDetailsPage() {
  const { t } = useTranslations()
  const router = useRouter()
  const { deal, stages, currency, canWrite, rec, reload } = useDealRecord()
  // The tab is hidden without contacts:write; a typed URL goes back to the overview.
  useEffect(() => {
    if (!canWrite) router.replace(rec)
  }, [canWrite, rec, router])
  if (!canWrite) return null
  return (
    <RecordBody>
      <RecordCard title={t("record.details")} bodyClassName="px-4 pb-4">
        {/* keyed on the saved deal: the form seeds its state once, on mount */}
        <DealForm
          key={JSON.stringify(deal)}
          formId="edit-deal"
          stages={stages}
          currency={currency}
          deal={deal}
          onSaved={reload}
        />
      </RecordCard>
    </RecordBody>
  )
}
