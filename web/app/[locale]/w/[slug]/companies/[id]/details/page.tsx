"use client"

import { CompanyScore, EditCompany, useCompanyRecord } from "@/components/companies/company-record"
import { RecordBody, RecordCard } from "@/components/record/record"
import { useTranslations } from "@/lib/i18n"

/** MH-1157: the company's details, and the fit they produce. */
export default function CompanyDetailsPage() {
  const { t } = useTranslations()
  const { company, canWrite, base, reload } = useCompanyRecord()
  return (
    <RecordBody>
      {canWrite ? (
        <RecordCard title={t("record.details")} bodyClassName="px-4 pb-4">
          {/* keyed on the saved values: the form seeds its state once, on mount */}
          <EditCompany key={JSON.stringify(company)} company={company} onSaved={reload} />
        </RecordCard>
      ) : null}
      <CompanyScore company={company} base={base} />
    </RecordBody>
  )
}
