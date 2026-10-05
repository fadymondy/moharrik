"use client"

import { useCompanyRecord } from "@/components/companies/company-record"
import { DealsTable } from "@/components/crm/deals"
import { RecordBody, RecordCard } from "@/components/record/record"
import { useTranslations } from "@/lib/i18n"

/** MH-1157: every deal with the company. */
export default function CompanyDealsPage() {
  const { t } = useTranslations()
  const { data } = useCompanyRecord()
  return (
    <RecordBody>
      <RecordCard title={t("nav.deals")} bodyClassName="-mb-px">
        <DealsTable deals={data.deals} total={data.deals_total} empty={t("companies.noDeals")} />
      </RecordCard>
    </RecordBody>
  )
}
