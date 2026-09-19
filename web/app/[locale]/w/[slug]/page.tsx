"use client"

import Link from "next/link"
import { KeyRoundIcon, UsersIcon } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { SectionHeader } from "@/components/public-frame"
import { Ltr } from "@/components/copy-field"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

export default function OverviewPage() {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("nav.overview")} · ${ws.summary.name}`)
  const base = `/${locale}/w/${ws.slug}`
  const details = ws.current?.workspace

  return (
    <>
      <SectionHeader micro={t("nav.overview")} title={ws.summary.name} action={<Badge variant="outline">{t(`role.${ws.summary.role}`)}</Badge>} />


      <div className="grid-separator-contained hatch-band mt-8 border-t-0" aria-hidden />

      <dl className="grid grid-cols-1 divide-y divide-line border-b border-line text-sm sm:grid-cols-4 sm:divide-x sm:divide-y-0 rtl:sm:divide-x-reverse">
        {[
          [t("overview.slug"), <Ltr key="s" mono>/{ws.slug}</Ltr>],
          [t("overview.locale"), details?.locale ? <Ltr key="l" mono>{details.locale}</Ltr> : "—"],
          [t("overview.timezone"), details?.timezone ? <Ltr key="tz" mono>{details.timezone}</Ltr> : "—"],
          [t("overview.currency"), details?.currency ? <Ltr key="c" mono>{details.currency}</Ltr> : "—"],
        ].map(([label, value], i) => (
          <div key={i} className="px-6 py-4">
            <dt className="grid-micro mb-1">{label}</dt>
            <dd className="text-grid-fg">{value}</dd>
          </div>
        ))}
      </dl>

      <div className="flex flex-wrap gap-2 px-6 py-6">
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/members`} />}>
          <UsersIcon />
          {t("nav.members")}
        </Button>
        <Button variant="outline" nativeButton={false} render={<Link href={`${base}/settings/mcp`} />}>
          <KeyRoundIcon />
          {t("nav.mcpTokens")}
        </Button>
      </div>
    </>
  )
}
