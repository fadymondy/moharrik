"use client"

import { useRouter } from "next/navigation"
import { useEffect } from "react"

import { useContactRecord } from "@/components/contacts/contact-record"
import { ContactEditForm } from "@/components/crm/contact-edit-form"
import { RecordBody, RecordCard } from "@/components/record/record"
import { useTranslations } from "@/lib/i18n"

/** MH-1157: the contact's editable details: name, email, phone, title, company. */
export default function ContactDetailsPage() {
  const { t } = useTranslations()
  const router = useRouter()
  const { contact, canWrite, rec, reload } = useContactRecord()
  // The tab is hidden without contacts:write; a typed URL goes back to the overview.
  useEffect(() => {
    if (!canWrite) router.replace(rec)
  }, [canWrite, rec, router])
  if (!canWrite) return null
  return (
    <RecordBody>
      <RecordCard title={t("record.details")} bodyClassName="px-4 pb-4">
        <ContactEditForm contact={contact} onSaved={reload} />
      </RecordCard>
    </RecordBody>
  )
}
