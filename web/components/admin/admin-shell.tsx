"use client"

// The superadmin panel's frame (MH-1184): Nasaq's AdminArea — an icon rail with sub-sidebars — guarded by
// /api/me/platform. A signed-out visitor goes to sign in (useRequireAuth); a signed-in account that is not
// a platform admin sees the ordinary 404, as the API answers 404 to it too, so the panel never advertises
// itself.
import { useEffect, useState, type MouseEvent, type ReactNode } from "react"
import { usePathname, useRouter } from "next/navigation"
import {
  ActivityIcon,
  ArrowLeftIcon,
  Building2Icon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  ScrollTextIcon,
  ShieldCheckIcon,
  UsersIcon,
} from "lucide-react"
import {
  AdminArea,
  Button,
  ProductMark,
  Skeleton,
  Tooltip,
  type RailSection,
} from "@fadymondy/nasaq/web"

import { NotFoundState } from "@/components/not-found-state"
import { ErrorState } from "@/components/states"
import { usePlatform } from "@/lib/admin"
import { useTranslations } from "@/lib/i18n"
import { useRequireAuth } from "@/lib/use-require-auth"

/** The page and its section from the path: /admin/users/x → users in people. */
function activeFor(path: string, base: string): { section: string; item: string } {
  const rest = path.startsWith(base) ? (path.slice(base.length).split("/")[1] ?? "") : ""
  switch (rest) {
    case "users":
      return { section: "people", item: "users" }
    case "permissions":
      return { section: "people", item: "permissions" }
    case "workspaces":
      return { section: "tenants", item: "workspaces" }
    case "audit":
      return { section: "security", item: "audit" }
    default:
      return { section: "overview", item: "dashboard" }
  }
}

export function AdminShell({ children }: { children: ReactNode }) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const pathname = usePathname()
  const me = useRequireAuth()
  const platform = usePlatform(!!me.data)
  const base = `/${locale}/admin`

  const active = activeFor(pathname, base)
  const [section, setSection] = useState(active.section)
  useEffect(() => setSection(active.section), [active.section])

  if (me.error || platform.error) return <ErrorState error={me.error ?? platform.error} />
  if (!me.data || !platform.data)
    return (
      <div className="flex flex-col gap-4 p-6">
        <Skeleton className="h-8 w-56" />
        <Skeleton className="h-32 w-full" />
      </div>
    )
  if (!platform.data.is_admin) return <NotFoundState />

  const sections: RailSection[] = [
    {
      id: "overview",
      label: t("admin.nav.overview"),
      icon: <LayoutDashboardIcon />,
      groups: [
        {
          id: "overview",
          items: [
            {
              id: "dashboard",
              label: t("admin.nav.dashboard"),
              icon: <ActivityIcon />,
              href: base,
            },
          ],
        },
      ],
    },
    {
      id: "people",
      label: t("admin.nav.people"),
      icon: <UsersIcon />,
      groups: [
        {
          id: "people",
          items: [
            {
              id: "users",
              label: t("admin.nav.users"),
              icon: <UsersIcon />,
              href: `${base}/users`,
            },
            {
              id: "permissions",
              label: t("admin.nav.permissions"),
              icon: <KeyRoundIcon />,
              href: `${base}/permissions`,
            },
          ],
        },
      ],
    },
    {
      id: "tenants",
      label: t("admin.nav.tenants"),
      icon: <Building2Icon />,
      groups: [
        {
          id: "tenants",
          items: [
            {
              id: "workspaces",
              label: t("admin.nav.workspaces"),
              icon: <Building2Icon />,
              href: `${base}/workspaces`,
            },
          ],
        },
      ],
    },
    {
      id: "security",
      label: t("admin.nav.security"),
      icon: <ShieldCheckIcon />,
      groups: [
        {
          id: "security",
          items: [
            {
              id: "audit",
              label: t("admin.nav.audit"),
              icon: <ScrollTextIcon />,
              href: `${base}/audit`,
            },
          ],
        },
      ],
    },
  ]

  // The rail renders native links: route plain clicks client-side, leave modified clicks to the browser.
  function onClickCapture(e: MouseEvent<HTMLDivElement>) {
    if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    const a = (e.target as HTMLElement).closest("a")
    const href = a?.getAttribute("href")
    if (!href || !href.startsWith(`/${locale}/`) || a?.target === "_blank") return
    e.preventDefault()
    router.push(href)
  }

  const back = `/${locale}/workspaces`
  return (
    <div className="h-dvh" onClickCapture={onClickCapture} data-admin-panel>
      <AdminArea
        sections={sections}
        value={section}
        onValueChange={setSection}
        activeItem={active.item}
        brand={<ProductMark brand="moharrik" size={32} />}
        user={{ name: me.data.email.split("@")[0] || me.data.email, email: me.data.email }}
        environment={t("admin.badge")}
        railFooter={
          <Tooltip content={t("admin.backToApp")} side="inline-end">
            <Button
              variant="ghost"
              size="icon"
              nativeButton={false}
              render={<a href={back} />}
              aria-label={t("admin.backToApp")}
            >
              <ArrowLeftIcon className="rtl:rotate-180" />
            </Button>
          </Tooltip>
        }
        labels={{ admin: t("admin.title"), account: t("admin.account") }}
      >
        {children}
      </AdminArea>
    </div>
  )
}
