"use client"

import { Timeline } from "@/components/crm/timeline"
import { useDealRecord } from "@/components/deals/deal-record"
import { RecordBody, RecordCard, useFocusParam } from "@/components/record/record"

/** MH-1161: log a call, note, meeting or task on the deal, and its history. */
export default function DealActivityPage() {
  const { data, deal, reload } = useDealRecord()
  useFocusParam(true)
  return (
    <RecordBody>
      <RecordCard bodyClassName="-mb-px pt-5" data-deal-activity>
        <Timeline
          items={data.activities}
          total={data.activities_total}
          nextCursor={data.activities_next_cursor}
          conversations={data.conversations ?? []}
          target={{ deal_id: deal.id }}
          onChange={reload}
        />
      </RecordCard>
    </RecordBody>
  )
}
