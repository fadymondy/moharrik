"use client"

import { Bdi } from "@/components/bidi"
import { Ltr } from "@/components/copy-field"
import { Converted, Facts, useLeadRecord } from "@/components/leads/lead-record"
import { RecordBody, RecordCard } from "@/components/record/record"
import { useTranslations } from "@/lib/i18n"
import { dateFormat } from "@/lib/i18n-locale"

/** MH-1157: what the lead wrote, the details they left, and what they became. */
export default function LeadOverviewPage() {
  const { t, locale } = useTranslations()
  const { lead, base } = useLeadRecord()

  return (
    <RecordBody>
      {lead.status === "converted" ? <Converted lead={lead} base={base} /> : null}

      <div className="grid gap-4 lg:grid-cols-3">
        <RecordCard title={t("leads.message")} className="lg:col-span-2" bodyClassName="px-4 pb-4">
          <p
            className="text-sm leading-6 break-words whitespace-pre-wrap text-foreground"
            data-lead-message
            dir="auto"
          >
            {lead.message}
          </p>
        </RecordCard>
        <RecordCard title={t("leads.details")}>
          <Facts
            rows={[
              [t("leads.field.email"), <Ltr key="e">{lead.email}</Ltr>],
              [t("leads.field.phone"), lead.phone ? <Ltr key="p">{lead.phone}</Ltr> : null],
              [t("leads.field.company"), lead.company ? <Bdi key="c">{lead.company}</Bdi> : null],
              [t("leads.field.budget"), lead.budget ? <Bdi key="b">{lead.budget}</Bdi> : null],
              [t("leads.field.form"), lead.form_name ? <Bdi key="f">{lead.form_name}</Bdi> : null],
              [t("leads.field.site"), lead.origin ? <Ltr key="o">{lead.origin}</Ltr> : null],
              [
                t("leads.field.received"),
                <Ltr key="r">
                  {dateFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(
                    new Date(lead.created_at),
                  )}
                </Ltr>,
              ],
            ]}
          />
        </RecordCard>
      </div>
    </RecordBody>
  )
}
