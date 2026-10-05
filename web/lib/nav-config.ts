/*
The workspace sidebar's structure (MH-526), as one table instead of one long flat
list: a handful of top-level links plus a handful of GROUPS. Each group is a
section with its own page at `home` (MH-673): opening it shows that section's
dashboard, and the sidebar — derived from the URL alone — shows only that
section's pages, with a back link to the workspace overview. A crowded area
(Automation, Research, Notifications) reads as one screen at a time
instead of forty rows.

Every entry here owns exactly one route. A page that used to live at a since-
moved URL gets a redirect (see the old route's page.tsx), not a duplicate nav
entry, so "every existing page has exactly one home" and no old link 404s.
*/

import {
  BellIcon,
  BotIcon,
  Building2Icon,
  ChartColumnBigIcon,
  ChartLineIcon,
  ClipboardListIcon,
  GlobeIcon,
  HandshakeIcon,
  KeyRoundIcon,
  LaptopIcon,
  LayoutGridIcon,
  MailIcon,
  MessageSquareQuoteIcon,
  PlayIcon,
  PlugIcon,
  RadarIcon,
  RssIcon,
  ScrollTextIcon,
  SearchCheckIcon,
  SearchIcon,
  SendIcon,
  ServerIcon,
  SettingsIcon,
  SparklesIcon,
  TargetIcon,
  TrendingUpIcon,
  UserPlusIcon,
  UserRoundIcon,
  UsersIcon,
  WebhookIcon,
  WorkflowIcon,
  YoutubeIcon,
  ZapIcon,
  type LucideIcon,
} from "lucide-react"

export type NavItem = {
  /** Stable id: used for the e2e sweep and as the mini-rail title. */
  key: string
  labelKey: string
  icon: LucideIcon
  /** Path segment(s) under the group/workspace base, e.g. "automation/browsers". "" = the base itself. */
  path: string
  /** Only this exact path counts as active (default: path and anything under it). */
  exact?: boolean
  /** Sub-paths that light up their OWN entry instead of this one. */
  except?: string[]
  /** Capability required to show this item at all (hidden, not disabled). */
  can?: string
}

export type NavGroup = {
  key: string
  labelKey: string
  icon: LucideIcon
  /** The section's own page (MH-673): its dashboard, and where the sidebar row links. */
  home: string
  /** Capability required for the group to appear at all. */
  can?: string
  items: NavItem[]
}

export type NavEntry = { type: "link"; item: NavItem } | { type: "group"; group: NavGroup }

export const NAV: NavEntry[] = [
  {
    type: "link",
    item: {
      key: "overview",
      labelKey: "nav.overview",
      icon: LayoutGridIcon,
      path: "",
      exact: true,
    },
  },
  {
    type: "group",
    group: {
      key: "crm",
      home: "crm",
      labelKey: "nav.group.crm",
      icon: HandshakeIcon,
      items: [
        { key: "leads", labelKey: "nav.leads", icon: UserPlusIcon, path: "leads" },
        { key: "companies", labelKey: "nav.companies", icon: Building2Icon, path: "companies" },
        { key: "contacts", labelKey: "nav.contacts", icon: MailIcon, path: "contacts" },
        { key: "deals", labelKey: "nav.deals", icon: HandshakeIcon, path: "deals" },
        {
          key: "reports",
          labelKey: "nav.reports",
          icon: ChartColumnBigIcon,
          path: "reports",
          can: "contacts:read",
        },
      ],
    },
  },
  {
    type: "group",
    group: {
      key: "marketing",
      home: "marketing",
      labelKey: "nav.group.marketing",
      icon: SendIcon,
      items: [
        { key: "campaigns", labelKey: "nav.campaigns", icon: SendIcon, path: "campaigns" },
        {
          key: "subscribe-forms",
          labelKey: "nav.subscribeForms",
          icon: ClipboardListIcon,
          path: "subscribe-forms",
        },
      ],
    },
  },
  {
    type: "group",
    group: {
      key: "automation",
      home: "automation",
      labelKey: "nav.automation",
      icon: BotIcon,
      items: [
        {
          key: "automation-browsers",
          labelKey: "automation.browsers",
          icon: LaptopIcon,
          path: "automation/browsers",
        },
        {
          key: "automation-workflows",
          labelKey: "nav.workflows",
          icon: WorkflowIcon,
          path: "automation/workflows",
        },
        {
          key: "automation-presets",
          labelKey: "presets.title",
          icon: SparklesIcon,
          path: "automation/presets",
        },
        { key: "automation-rules", labelKey: "nav.rules", icon: ZapIcon, path: "automation/rules" },
        {
          key: "automation-runs",
          labelKey: "automation.runs",
          icon: PlayIcon,
          path: "automation/runs",
        },
        {
          key: "automation-schedules",
          labelKey: "automation.schedules",
          icon: ClipboardListIcon,
          path: "automation/schedules",
        },
        {
          key: "automation-settings",
          labelKey: "automation.limitsAndSettings",
          icon: SettingsIcon,
          path: "automation/settings",
        },
      ],
    },
  },
  {
    type: "group",
    group: {
      key: "research",
      home: "research",
      labelKey: "nav.research",
      icon: SearchCheckIcon,
      can: "research:read",
      items: [
        {
          key: "research-runs",
          labelKey: "research.tabRuns",
          icon: SearchCheckIcon,
          path: "research/runs",
        },
        {
          key: "research-evidence",
          labelKey: "research.tabEvidence",
          icon: ScrollTextIcon,
          path: "research/evidence",
        },
        {
          key: "research-schedules",
          labelKey: "research.tabSchedules",
          icon: ClipboardListIcon,
          path: "research/schedules",
        },
        { key: "prospects", labelKey: "nav.prospects", icon: RadarIcon, path: "prospects" },
      ],
    },
  },
  {
    type: "group",
    group: {
      key: "insights",
      home: "insights",
      labelKey: "nav.group.insights",
      icon: TrendingUpIcon,
      items: [
        {
          key: "trends-topics",
          labelKey: "nav.trendsTopics",
          icon: TrendingUpIcon,
          path: "trends",
          except: ["trends/sources"],
        },
        {
          key: "trends-sources",
          labelKey: "nav.trendsSources",
          icon: RssIcon,
          path: "trends/sources",
        },
        {
          key: "analytics",
          labelKey: "nav.analytics",
          icon: ChartLineIcon,
          path: "analytics",
          can: "analytics:read",
        },
        {
          key: "search-console",
          labelKey: "nav.searchConsole",
          icon: SearchIcon,
          path: "search-console",
          can: "analytics:read",
        },
        {
          key: "youtube",
          labelKey: "nav.youtube",
          icon: YoutubeIcon,
          path: "youtube",
          can: "analytics:read",
        },
      ],
    },
  },
  {
    type: "group",
    group: {
      key: "notifications",
      home: "notifications/overview",
      labelKey: "nav.notifications",
      icon: BellIcon,
      items: [
        {
          key: "notifications-center",
          labelKey: "nav.notifications",
          icon: BellIcon,
          path: "notifications",
          exact: true,
        },
        {
          key: "notifications-sources",
          labelKey: "notifications.sources",
          icon: WebhookIcon,
          path: "notifications/sources",
        },
      ],
    },
  },
  {
    type: "link",
    item: { key: "members", labelKey: "nav.members", icon: UsersIcon, path: "members" },
  },
  {
    type: "group",
    group: {
      key: "settings",
      home: "settings",
      labelKey: "nav.settings",
      icon: SettingsIcon,
      items: [
        {
          key: "settings-general",
          labelKey: "nav.general",
          icon: GlobeIcon,
          // Everyone: members come here to leave the workspace (MH-679).
          path: "settings/general",
        },
        { key: "settings-email", labelKey: "nav.email", icon: ServerIcon, path: "settings/email" },
        // MH-1148: the inbox is a dock now (MH-1147), so its setup pages live here.
        {
          key: "settings-canned",
          labelKey: "inbox.canned",
          icon: MessageSquareQuoteIcon,
          path: "settings/canned",
          can: "inbox:manage",
        },
        {
          key: "settings-email-templates",
          labelKey: "inbox.emailTemplates.title",
          icon: MailIcon,
          path: "settings/email-templates",
          can: "inbox:manage",
        },
        {
          key: "settings-icp",
          labelKey: "nav.icp",
          icon: TargetIcon,
          path: "settings/icp",
          can: "contacts:read",
        },
        {
          key: "settings-integrations",
          labelKey: "nav.integrations",
          icon: PlugIcon,
          path: "settings/integrations",
        },
        {
          key: "settings-mcp",
          labelKey: "nav.mcpTokens",
          icon: KeyRoundIcon,
          path: "settings/mcp",
        },
        // The workspace profile (MH-478, landed on main by batch H): what a preset's
        // job-application inputs and the extension's Application autofill panel fill from.
        {
          key: "settings-automation-profile",
          labelKey: "nav.automationProfile",
          icon: UserRoundIcon,
          path: "settings/automation-profile",
          can: "automation:read",
        },
        {
          key: "settings-audit",
          labelKey: "nav.audit",
          icon: ScrollTextIcon,
          path: "settings/audit",
          can: "audit:read",
        },
        {
          key: "settings-delivery",
          labelKey: "nav.delivery",
          icon: BellIcon,
          path: "settings/delivery",
          can: "push:manage",
        },
      ],
    },
  },
]

function canSee(can: string[] | undefined, need?: string) {
  return !need || !!can?.includes(need)
}

export function visibleItems(items: NavItem[], can: string[] | undefined) {
  return items.filter((i) => canSee(can, i.can))
}

export function visibleEntries(can: string[] | undefined) {
  return NAV.filter((e) => (e.type === "link" ? canSee(can, e.item.can) : canSee(can, e.group.can)))
}

/** Whether `pathname` (already stripped of the workspace base) is inside this item's path. */
export function itemActive(item: NavItem, rest: string): boolean {
  // A section's own dashboard belongs to no page inside it (MH-673): /notifications/overview
  // must not light up the Notifications page row that owns /notifications.
  if (groupForHome(rest) && item.path !== rest) return false
  const p = item.path
  // A research run's own page (/research/<id>) has no "/runs" segment, but it is
  // that run's evidence, so it belongs under "Runs" like any other item there.
  const researchRunDetail =
    item.key === "research-runs" &&
    /^research\/[^/]+$/.test(rest) &&
    rest !== "research/evidence" &&
    rest !== "research/schedules"
  const inside =
    researchRunDetail || (p === "" ? rest === "" : rest === p || rest.startsWith(p + "/"))
  if (!inside) return false
  if (item.except?.some((x) => rest === x || rest.startsWith(x + "/"))) return false
  if (item.exact) return rest === p
  return true
}

/** The group whose section dashboard is exactly this path, if any. */
export function groupForHome(rest: string): NavGroup | null {
  for (const e of NAV) if (e.type === "group" && e.group.home === rest) return e.group
  return null
}

/** Which group (if any) owns the current path — its dashboard or any page in it — so the sidebar shows that section. */
export function groupForPath(rest: string): NavGroup | null {
  const home = groupForHome(rest)
  if (home) return home
  for (const e of NAV) {
    if (e.type !== "group") continue
    if (e.group.items.some((i) => itemActive(i, rest))) return e.group
  }
  // A research run detail (/research/<uuid>) matches research-runs above via the
  // special case; nothing else needs one.
  return null
}
