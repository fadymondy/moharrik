"use client"

import { useContactRecord } from "@/components/contacts/contact-record"
import { MetaPanel } from "@/components/crm/contact-identities"
import { RecordBody } from "@/components/record/record"

/** MH-1157: the contact's custom fields. */
export default function ContactFieldsPage() {
  const { contact, canWrite, reload } = useContactRecord()
  return (
    <RecordBody>
      {/* keyed on the map's contents: MetaPanel seeds its row state from `meta` only on
          mount, so a re-fetch after save needs a remount to show the new server truth. */}
      <MetaPanel
        key={JSON.stringify(contact.meta ?? {})}
        contactId={contact.id}
        meta={contact.meta ?? {}}
        canWrite={canWrite}
        onChanged={reload}
      />
    </RecordBody>
  )
}
