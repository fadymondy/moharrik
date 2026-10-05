"use client"

import { useContactRecord } from "@/components/contacts/contact-record"
import { ContactContextPanel } from "@/components/crm/contact-context"
import { RecordBody } from "@/components/record/record"

/** MH-1157: what Moharrik knows about the contact: imported context and generated reports. */
export default function ContactIntelligencePage() {
  const { contact, reportsRev, reportsChanged } = useContactRecord()
  return (
    <RecordBody>
      <ContactContextPanel
        reportsKey={reportsRev}
        onReportsChanged={reportsChanged}
        contact={{ id: contact.id, name: contact.name, email: contact.email, phone: contact.phone }}
      />
    </RecordBody>
  )
}
