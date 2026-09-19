"use client"

import Link from "next/link"

import { Button } from "@/components/ui/button"
import { PublicFrame, PublicPanel } from "@/components/public-frame"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

export default function NotFound() {
  const { t, locale } = useTranslations()
  useDocumentTitle(t("notFound.title"))
  return (
    <PublicFrame eyebrow="404" title={t("notFound.title")} description={t("notFound.body")}>
      <PublicPanel className="flex justify-center py-10">
        <Button variant="outline" nativeButton={false} render={<Link href={`/${locale}/workspaces`} />}>
          {t("notFound.back")}
        </Button>
      </PublicPanel>
    </PublicFrame>
  )
}
