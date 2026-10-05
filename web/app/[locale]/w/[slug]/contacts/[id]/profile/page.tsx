"use client"

import { ContactFit } from "@/components/contacts/contact-fit"
import { useContactRecord } from "@/components/contacts/contact-record"
import { ContactWhatsAppPanel } from "@/components/crm/contact-whatsapp"
import { RecordBody, useFocusParam } from "@/components/record/record"

/** MH-1157: who the contact is: lifecycle and fit, and WhatsApp. */
export default function ContactProfilePage() {
  const { contact, canWrite, refresh } = useContactRecord()
  useFocusParam(true)
  return (
    <RecordBody>
      <ContactFit contact={contact} canWrite={canWrite} onSaved={refresh} />
      <ContactWhatsAppPanel key={contact.phone} contact={contact} />
    </RecordBody>
  )
}
