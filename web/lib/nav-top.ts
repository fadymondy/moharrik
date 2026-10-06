/*
The sidebar by INTENT (MH-1060). nav-config.ts says which section owns which page;
this table says what the sidebar shows: a flat, grouped list a person scans by what
they want to do (find customers, automate, understand, connect) rather than by which
module a page came from. Every entry links to a page that already exists; nothing
here is a new route.

An item that owns a section lists its sub-pages (`subs`). They show nested under the
item while the URL is inside it, so Companies, Canned replies or Runs stay one click
away without crowding the top level. Which item is active is decided by the longest
matching prefix in `match`, so a nested route (/deals/<id>, /research/<id>) lights up
its parent.
*/

import {
  BotIcon,
  ChartColumnBigIcon,
  ClipboardListIcon,
  HandshakeIcon,
  KeyRoundIcon,
  LayoutDashboardIcon,
  LayoutGridIcon,
  PlugIcon,
  SearchCheckIcon,
  SendIcon,
  SettingsIcon,
  TrendingUpIcon,
  UserPlusIcon,
  UserRoundIcon,
  UsersIcon,
  type LucideIcon,
} from "lucide-react"

import { NAV, type NavItem } from "@/lib/nav-config"

export type TopItem = {
  key: string
  labelKey: string
  icon: LucideIcon
  /** Where the row links (workspace-relative; "" = the workspace home). */
  path: string
  /** Path prefixes this item owns. The longest match across the whole sidebar wins. */
  match: string[]
  can?: string
  /** Pages nested under the item while it is active. */
  subs?: NavItem[]
}

export type TopSection = {
  key: string
  /** null = the unlabelled group at the top. */
  labelKey: string | null
  items: TopItem[]
}

function navItem(key: string): NavItem {
  for (const e of NAV) {
    if (e.type === "link" && e.item.key === key) return e.item
    if (e.type === "group") {
      const it = e.group.items.find((i) => i.key === key)
      if (it) return it
    }
  }
  throw new Error(`nav-top: no NAV item "${key}"`)
}

/** A section's own dashboard as a sub-page ("Overview"), for the sections whose row links elsewhere. */
function overviewOf(groupKey: string): NavItem {
  const g = NAV.flatMap((e) => (e.type === "group" ? [e.group] : [])).find(
    (x) => x.key === groupKey,
  )
  if (!g) throw new Error(`nav-top: no group "${groupKey}"`)
  return {
    key: `${g.key}-overview`,
    labelKey: "nav.sectionOverview",
    icon: LayoutDashboardIcon,
    path: g.home,
    exact: true,
  }
}

export const TOP_NAV: TopSection[] = [
  {
    key: "main",
    labelKey: null,
    items: [
      { key: "home", labelKey: "nav.home", icon: LayoutGridIcon, path: "", match: [""] },
      {
        key: "customers",
        labelKey: "nav.customers",
        icon: UserRoundIcon,
        path: "contacts",
        match: ["contacts", "companies", "leads", "crm"],
        subs: [
          overviewOf("crm"),
          { ...navItem("contacts"), except: ["contacts/import"] },
          navItem("companies"),
          navItem("leads"),
          {
            key: "contacts-import",
            labelKey: "nav.importCustomers",
            icon: UserPlusIcon,
            path: "contacts/import",
          },
        ],
      },
      {
        key: "opportunities",
        labelKey: "nav.opportunities",
        icon: HandshakeIcon,
        path: "deals",
        match: ["deals"],
      },
      {
        key: "research",
        labelKey: "nav.top.research",
        icon: SearchCheckIcon,
        path: "research",
        match: ["research", "prospects"],
        can: "research:read",
        subs: [
          navItem("research-runs"),
          navItem("research-evidence"),
          navItem("research-schedules"),
          navItem("prospects"),
        ],
      },
    ],
  },
  {
    key: "automate",
    labelKey: "nav.group.automate",
    items: [
      {
        key: "automations",
        labelKey: "nav.automations",
        icon: BotIcon,
        path: "automation",
        match: ["automation"],
        subs: [
          navItem("automation-workflows"),
          navItem("automation-rules"),
          navItem("automation-presets"),
          navItem("automation-runs"),
          navItem("automation-browsers"),
          {
            key: "automation-extension",
            labelKey: "nav.extension",
            icon: PlugIcon,
            path: "automation/extension",
          },
          navItem("automation-settings"),
        ],
      },
      {
        key: "campaigns",
        labelKey: "nav.campaigns",
        icon: SendIcon,
        path: "campaigns",
        match: ["campaigns", "marketing", "subscribe-forms"],
        subs: [overviewOf("marketing"), navItem("campaigns"), navItem("subscribe-forms")],
      },
      {
        key: "schedules",
        labelKey: "nav.top.schedules",
        icon: ClipboardListIcon,
        path: "automation/schedules",
        match: ["automation/schedules"],
      },
    ],
  },
  {
    key: "understand",
    labelKey: "nav.group.understand",
    items: [
      {
        key: "insights",
        labelKey: "nav.group.insights",
        icon: TrendingUpIcon,
        path: "insights",
        match: ["insights", "trends", "analytics", "search-console", "youtube"],
        subs: [
          navItem("analytics"),
          navItem("search-console"),
          navItem("youtube"),
          navItem("trends-topics"),
          navItem("trends-sources"),
        ],
      },
      {
        key: "reports",
        labelKey: "nav.reports",
        icon: ChartColumnBigIcon,
        path: "reports",
        match: ["reports"],
        can: "contacts:read",
      },
    ],
  },
  {
    key: "connect",
    labelKey: "nav.group.connect",
    items: [
      {
        key: "integrations",
        labelKey: "nav.integrations",
        icon: PlugIcon,
        path: "settings/integrations",
        // MH-1148: messaging channels are set up here too, beside Google and the products.
        match: ["settings/integrations"],
      },
      {
        key: "mcp",
        labelKey: "nav.mcp",
        icon: KeyRoundIcon,
        path: "settings/mcp",
        match: ["settings/mcp"],
      },
    ],
  },
]

/** Pinned to the bottom of the sidebar, above the user menu. */
export const FOOTER_NAV: TopItem[] = [
  { key: "team", labelKey: "nav.team", icon: UsersIcon, path: "members", match: ["members"] },
  {
    key: "settings",
    labelKey: "nav.settings",
    icon: SettingsIcon,
    path: "settings",
    match: ["settings"],
    subs: [
      overviewOf("settings"),
      navItem("settings-general"),
      navItem("settings-email"),
      navItem("settings-canned"),
      navItem("settings-email-templates"),
      navItem("settings-icp"),
      navItem("settings-automation-profile"),
      navItem("settings-audit"),
      navItem("settings-delivery"),
    ],
  },
]

function prefixLen(rest: string, p: string): number {
  if (p === "") return rest === "" ? 1 : -1
  return rest === p || rest.startsWith(p + "/") ? p.length + 1 : -1
}

/** The sidebar item that owns this path: the longest matching prefix wins, so /settings/mcp is MCP, not Settings. */
export function topForPath(rest: string): TopItem | null {
  let best: TopItem | null = null
  let bestLen = -1
  for (const it of [...TOP_NAV.flatMap((s) => s.items), ...FOOTER_NAV]) {
    for (const p of it.match) {
      const n = prefixLen(rest, p)
      if (n > bestLen) {
        best = it
        bestLen = n
      }
    }
  }
  return best
}

export function visibleTop(items: TopItem[], can: string[] | undefined) {
  return items.filter((i) => !i.can || !!can?.includes(i.can))
}

/** Every sub-page the sidebar can reach, flattened: the command palette registers these too. */
export function allTopPages(): { item: NavItem; parent: TopItem }[] {
  const out: { item: NavItem; parent: TopItem }[] = []
  for (const parent of [...TOP_NAV.flatMap((s) => s.items), ...FOOTER_NAV]) {
    for (const item of parent.subs ?? []) out.push({ item, parent })
  }
  return out
}
