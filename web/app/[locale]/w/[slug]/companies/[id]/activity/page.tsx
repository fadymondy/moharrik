"use client"

import { useCompanyRecord } from "@/components/companies/company-record"
import { Timeline } from "@/components/crm/timeline"
import { RecordBody, RecordCard, useFocusParam } from "@/components/record/record"

/** MH-1157: log a call, note or meeting with the company, and its history. */
export default function CompanyActivityPage() {
  const { data, company, reload } = useCompanyRecord()
  useFocusParam(true)
  return (
    <RecordBody>
      <RecordCard bodyClassName="-mb-px pt-5">
        <Timeline
          items={data.activities}
          total={data.activities_total}
          nextCursor={data.activities_next_cursor}
          conversations={data.conversations ?? []}
          target={{ company_id: company.id }}
          onChange={reload}
        />
      </RecordCard>
    </RecordBody>
  )
}
