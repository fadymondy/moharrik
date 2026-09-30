"use client"

import { useRouter } from "next/navigation"
import { useSWRConfig } from "swr"
import { LogOutIcon, MessageSquarePlusIcon, UserIcon } from "lucide-react"
import { toast } from "@fadymondy/nasaq/web"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { api, resetCsrf } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

export function UserMenu({ email, onReport }: { email: string; onReport?: () => void }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const { mutate } = useSWRConfig()

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
