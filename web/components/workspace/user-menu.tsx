"use client"

import { useRouter } from "next/navigation"
import { useSWRConfig } from "swr"
import { LogOutIcon, MessageSquarePlusIcon, ShieldCheckIcon, UserIcon } from "lucide-react"
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  toast,
} from "@fadymondy/nasaq/web"

import { usePlatform } from "@/lib/admin"
import { api, resetCsrf } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

export function UserMenu({ email, onReport }: { email: string; onReport?: () => void }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const { mutate } = useSWRConfig()
  const platform = usePlatform()

  async function signOut() {
    try {
      await api("/api/auth/logout", { method: "POST" })
    } catch {
      toast.error(t("common.networkError"))
      return
    }
    resetCsrf()
    // Drop every cached response: the next account must not see this one's data.
    await mutate(() => true, undefined, { revalidate: false })
    router.replace(`/${locale}/login`)
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="icon-sm" aria-label={t("header.account")} />}
      >
        <UserIcon />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-56">
        <DropdownMenuGroup>
          <DropdownMenuLabel>
            <span className="eyebrow block">{t("header.signedInAs")}</span>
            <span dir="ltr" className="ltr-isolate mt-1 block truncate text-sm text-foreground">
              {email}
            </span>
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        {platform.data?.is_admin ? (
          <DropdownMenuItem onClick={() => router.push(`/${locale}/admin`)} data-admin-link>
            <ShieldCheckIcon />
            {t("admin.panel")}
          </DropdownMenuItem>
        ) : null}
        {onReport ? (
          <DropdownMenuItem onClick={onReport}>
            <MessageSquarePlusIcon />
            {t("feedback.report")}
          </DropdownMenuItem>
        ) : null}
        <DropdownMenuItem onClick={signOut}>
          <LogOutIcon />
          {t("header.signOut")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
