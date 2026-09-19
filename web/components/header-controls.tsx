"use client"

import { usePathname, useRouter } from "next/navigation"
import { useTheme } from "next-themes"
import { LanguagesIcon, MoonIcon, SunIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { LOCALES, LOCALE_COOKIE, LOCALE_NAMES, isLocale } from "@/lib/i18n-locale"
import { useTranslations } from "@/lib/i18n"

export function ThemeToggle() {
  const { resolvedTheme, setTheme } = useTheme()
  const { t } = useTranslations()
  const dark = resolvedTheme === "dark"
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label={t("header.toggleTheme")}
      title={t("header.toggleTheme")}
      onClick={() => setTheme(dark ? "light" : "dark")}
    >
      <SunIcon className="hidden dark:block" />
      <MoonIcon className="dark:hidden" />
    </Button>
  )
}

export function LanguageSwitcher() {
  const { t, locale } = useTranslations()
  const pathname = usePathname()

  function switchTo(next: string) {
    if (!isLocale(next) || next === locale) return
    document.cookie = `${LOCALE_COOKIE}=${next}; path=/; max-age=31536000; samesite=lax`
    const parts = pathname.split("/")
    if (isLocale(parts[1])) parts[1] = next
    else parts.splice(1, 0, next)
    // A full navigation: the root layout sets <html lang dir> on the server.
    window.location.assign(parts.join("/") + window.location.search)
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="icon-sm" aria-label={t("header.language")} title={t("header.language")} />}
      >
        <LanguagesIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-40">
        <DropdownMenuGroup>
          <DropdownMenuLabel>{t("header.language")}</DropdownMenuLabel>
          <DropdownMenuRadioGroup value={locale} onValueChange={(v) => switchTo(String(v))}>
            {LOCALES.map((l) => (
              <DropdownMenuRadioItem key={l} value={l} lang={l}>
                {LOCALE_NAMES[l]}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
