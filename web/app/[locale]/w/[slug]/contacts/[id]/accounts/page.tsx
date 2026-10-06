"use client"

import { useContactRecord } from "@/components/contacts/contact-record"
import { IdentitiesPanel } from "@/components/crm/contact-identities"
import { RecordBody, useFocusParam } from "@/components/record/record"

/** MH-1157: the contact's linked accounts: WhatsApp, Telegram, Instagram and the rest. */
export default function ContactAccountsPage() {
  const { contact, canWrite, reload } = useContactRecord()
  useFocusParam(true)
  return (
    <RecordBody>
      <IdentitiesPanel
        contactId={contact.id}
        identities={contact.identities ?? []}
        canWrite={canWrite}
        onChanged={reload}
      />
    </RecordBody>
  )
}
