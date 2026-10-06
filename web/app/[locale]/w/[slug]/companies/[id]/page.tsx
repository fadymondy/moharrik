"use client"

import Link from "next/link"
import { ActivityIcon, GaugeIcon, HandshakeIcon, UsersIcon } from "lucide-react"

import {
  CompanyPeople,
  CompanyScore,
  useCompanyRecord,
} from "@/components/companies/company-record"
import { DealsTable } from "@/components/crm/deals"
import { RecordBody, RecordCard, RecordStat } from "@/components/record/record"
import { useTranslations } from "@/lib/i18n"

const PREVIEW = 5

/** MH-1157: the company at a glance: the numbers, its fit, its people and its deals. */
export default function CompanyOverviewPage() {
  const { t, formatNumber } = useTranslations()
  const { data, company, base, rec } = useCompanyRecord()
  const viewAll = (href: string) => (
    <Link href={href} className="text-muted-foreground hover:text-foreground hover:underline">
      {t("record.viewAll")}
    </Link>
  )

  return (
    <RecordBody>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4" data-record-stats>
        <RecordStat
          icon={<GaugeIcon />}
          label={t("icp.fit")}
          value={company.score === null ? "—" : formatNumber(company.score)}
          hint={company.score === null ? t("icp.unscored") : undefined}
        />
        <RecordStat
          icon={<UsersIcon />}
          label={t("record.tab.people")}
          value={formatNumber(data.contacts.length)}
          href={`${rec}/people`}
        />
        <RecordStat
          icon={<HandshakeIcon />}
          label={t("record.openDeals")}
          value={formatNumber(company.open_deals)}
          hint={t("record.dealsTotal", { n: formatNumber(data.deals_total) })}
          href={`${rec}/deals`}
        />
        <RecordStat
          icon={<ActivityIcon />}
          label={t("record.tab.activity")}
          value={formatNumber(data.activities_total)}
          href={`${rec}/activity`}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <div className="min-w-0 lg:col-span-2">
          <CompanyScore company={company} base={base} />
        </div>
        <RecordCard
          title={t("companies.people", { count: data.contacts.length })}
          action={data.contacts.length > PREVIEW ? viewAll(`${rec}/people`) : null}
        >
          <CompanyPeople contacts={data.contacts} base={base} limit={PREVIEW} />
        </RecordCard>
      </div>

      <RecordCard
        title={t("nav.deals")}
        action={data.deals.length ? viewAll(`${rec}/deals`) : null}
        bodyClassName="-mb-px"
      >
        <DealsTable deals={data.deals.slice(0, PREVIEW)} empty={t("companies.noDeals")} />
      </RecordCard>
    </RecordBody>
  )
}
