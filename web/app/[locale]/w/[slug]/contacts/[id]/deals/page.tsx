"use client"

import { useContactRecord } from "@/components/contacts/contact-record"
import { DealsTable } from "@/components/crm/deals"
import { RecordBody, RecordCard } from "@/components/record/record"
import { useTranslations } from "@/lib/i18n"

/** MH-1157: every deal the contact is on. */
export default function ContactDealsPage() {
  const { t } = useTranslations()
  const { data } = useContactRecord()
  return (
    <RecordBody>
      <RecordCard
        title={<span data-c360-opportunities>{t("c360.opportunities")}</span>}
        bodyClassName="-mb-px"
      >
        <DealsTable deals={data.deals} total={data.deals_total} empty={t("contacts.noDeals")} />
      </RecordCard>
    </RecordBody>
  )
}
