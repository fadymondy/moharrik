"use client"

import { useContactRecord } from "@/components/contacts/contact-record"
import { RecordBrief } from "@/components/record/record-brief"

/** MH-1169: the contact's AI context brief: who they are and how we speak to them. */
export default function ContactContextPage() {
  const { contact, canWrite } = useContactRecord()
  return <RecordBrief collection="contacts" id={contact.id} canWrite={canWrite} />
}
