"use client"

import { BootSplash, ProductMark } from "@fadymondy/nasaq/web"

import { useTranslations } from "@/lib/i18n"

/**
 * Nasaq's full-screen boot splash on the Moharrik mark, centred on the screen
 * (MH-1122, as in Mahaam). The shell shows it instead of a half-built sidebar
 * and header until it knows who you are and what you can do; a page that is
 * still fetching its first data shows it as an overlay, so the splash gives
 * way to a finished page rather than to a page that loads in pieces.
 */
export function AppSplash({ overlay = false }: { overlay?: boolean }) {
  const { t } = useTranslations()
  return (
    <BootSplash
      mark={<ProductMark brand="moharrik" size={56} title="" />}
      name={t("brand.name")}
      className={overlay ? "fixed inset-0 z-50" : undefined}
      data-app-splash={overlay ? "overlay" : "boot"}
    />
  )
}
