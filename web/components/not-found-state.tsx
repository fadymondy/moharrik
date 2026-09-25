"use client"

import Link from "next/link"

import { Button } from "@/components/ui/button"
import { CubeMark } from "@/components/brand/cube-mark"
import { MOHARRIK_MARK } from "@/lib/brand/mark"
import { useTranslations } from "@/lib/i18n"
import { useDocumentTitle } from "@/lib/title"

/** A proper not-found state: used by the 404 pages and by an unknown workspace slug. */
export function NotFoundState({
  title,
  body,
  backHref,
  backLabel,
}: {
  title?: string
  body?: string
  backHref?: string
  backLabel?: string
}) {
  const { t, locale } = useTranslations()
  useDocumentTitle(title ?? t("notFound.title"))

  return (
    <div className="flex flex-col items-center gap-4 px-6 py-20 text-center">
      <CubeMark mark={MOHARRIK_MARK} size={40} />
      <p className="grid-micro">404</p>
      <h1 className="grid-title text-2xl">{title ?? t("notFound.title")}</h1>
      <p className="grid-body max-w-md text-sm">{body ?? t("notFound.body")}</p>
      <Button
        variant="outline"
        nativeButton={false}
        render={<Link href={backHref ?? `/${locale}/workspaces`} />}
      >
        {backLabel ?? t("notFound.back")}
      </Button>
    </div>
  )
}
