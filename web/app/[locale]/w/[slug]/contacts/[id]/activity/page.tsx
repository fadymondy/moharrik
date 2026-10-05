"use client"

import {
  AutomationsPanel,
  ConversationsPanel,
  UnifiedTimeline,
} from "@/components/contacts/c360-sections"
import { useContactRecord } from "@/components/contacts/contact-record"
import { Timeline } from "@/components/crm/timeline"
import { RecordBody, RecordCard, useFocusParam } from "@/components/record/record"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useContactTimeline } from "@/lib/contact-timeline"

/** MH-1157: log a call, note or meeting, and everything that has happened with the contact. */
export default function ContactActivityPage() {
  const ws = useWorkspace()
  const { data, contact, base, refresh } = useContactRecord()
  const timeline = useContactTimeline(ws.id, contact.id)
  useFocusParam(true)

  return (
    <RecordBody className="[&_[data-card]]:rounded-xl">
      <RecordCard bodyClassName="-mb-px pt-5" data-c360-activities>
        <Timeline
          items={data.activities}
          total={data.activities_total}
          nextCursor={data.activities_next_cursor}
          conversations={data.conversations ?? []}
          target={{ contact_id: contact.id }}
          onChange={refresh}
        />
      </RecordCard>

      {timeline.error ? (
        <ErrorState error={timeline.error} />
      ) : !timeline.data ? (
        <LoadingRows rows={4} />
      ) : (
        <div className="grid gap-4 lg:grid-cols-3">
          <div className="min-w-0 lg:col-span-2">
            <UnifiedTimeline contactId={contact.id} page={timeline.data} base={base} />
          </div>
          <div className="flex min-w-0 flex-col gap-4">
            {timeline.data.overview?.conversations ? (
              <ConversationsPanel rows={timeline.data.overview.conversations} base={base} />
            ) : null}
            {timeline.data.overview?.automations ? (
              <AutomationsPanel rows={timeline.data.overview.automations} base={base} />
            ) : null}
          </div>
        </div>
      )}
    </RecordBody>
  )
}
