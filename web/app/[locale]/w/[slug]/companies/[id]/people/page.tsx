"use client"

import { CompanyPeople, useCompanyRecord } from "@/components/companies/company-record"
import { RecordBody, RecordCard } from "@/components/record/record"
import { useTranslations } from "@/lib/i18n"

/** MH-1157: everyone at the company. */
export default function CompanyPeoplePage() {
  const { t } = useTranslations()
  const { data, base } = useCompanyRecord()
  return (
    <RecordBody>
      <RecordCard title={t("companies.people", { count: data.contacts.length })}>
        <CompanyPeople contacts={data.contacts} base={base} />
      </RecordCard>
    </RecordBody>
  )
}
