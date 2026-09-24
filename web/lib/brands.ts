/*
The preset marketplace's brand catalog (MH-527): one entry per site a preset
can visit, built on the simple-icons package (CC0) — simpleicons.org — so a
brand tile carries its real logo and its real brand colour instead of the
placeholder `#e2661c` every preset in presets.json currently ships (that
field is a leftover default, not per-brand data; MH-527 replaces it here).

Not every site in presets.json has a simple-icons entry — a handful of
brands (LinkedIn, Bing, Yahoo, Hacker News and about a dozen news/job boards)
have been pulled from that package over the years, usually after a takedown
request. Those get a monogram tile — a neutral, undyed tint plus a Lucide
icon and the brand's first letter — rather than a guessed colour asserted as
official.

A preset's brand is read from the HOST it visits (preset.hosts[0]), not from
presets.json's coarse `group` field: "search", "news" and "jobs" each bundle
several unrelated companies (Google/Bing/DuckDuckGo/Brave/Yahoo; The Verge/
TechCrunch/Ars Technica/…; WeWorkRemotely/RemoteOK/…) that the gallery must
show as separate brand tiles, each with its own logo.
*/

import {
  siArstechnica,
  siBluesky,
  siBrave,
  siDevdotto,
  siDuckduckgo,
  siFacebook,
  siGithub,
  siGmail,
  siGoogle,
  siGooglenews,
  siGreenhouse,
  siInfoq,
  siInstagram,
  siMedium,
  siTechcrunch,
  siTelegram,
  siThreads,
  siTiktok,
  siWellfound,
  siWhatsapp,
  siX,
  siYcombinator,
  siYoutube,
  type SimpleIcon,
} from "simple-icons"
import {
  BriefcaseIcon,
  BuildingIcon,
  FileTextIcon,
  GlobeIcon,
  NewspaperIcon,
  type LucideIcon,
} from "lucide-react"

export type Brand = {
  key: string
  /** Proper noun — kept in Latin script in both languages, like every other brand name in this app. */
  name: string
  /** A simple-icons entry, when the package has this brand. */
  icon?: SimpleIcon
  /** Used when there is no `icon`: a neutral tile with this Lucide glyph instead of a guessed brand colour. */
  fallbackIcon: LucideIcon
}

export const BRANDS: Record<string, Brand> = {
  google: { key: "google", name: "Google", icon: siGoogle, fallbackIcon: GlobeIcon },
  bing: { key: "bing", name: "Bing", fallbackIcon: GlobeIcon },
  duckduckgo: { key: "duckduckgo", name: "DuckDuckGo", icon: siDuckduckgo, fallbackIcon: GlobeIcon },
  brave: { key: "brave", name: "Brave", icon: siBrave, fallbackIcon: GlobeIcon },
  yahoo: { key: "yahoo", name: "Yahoo", fallbackIcon: GlobeIcon },
  "google-news": { key: "google-news", name: "Google News", icon: siGooglenews, fallbackIcon: NewspaperIcon },
  "google-trends": { key: "google-trends", name: "Google Trends", icon: siGoogle, fallbackIcon: GlobeIcon },
  gmail: { key: "gmail", name: "Gmail", icon: siGmail, fallbackIcon: GlobeIcon },
  theverge: { key: "theverge", name: "The Verge", fallbackIcon: NewspaperIcon },
  techcrunch: { key: "techcrunch", name: "TechCrunch", icon: siTechcrunch, fallbackIcon: NewspaperIcon },
  arstechnica: { key: "arstechnica", name: "Ars Technica", icon: siArstechnica, fallbackIcon: NewspaperIcon },
  wired: { key: "wired", name: "Wired", fallbackIcon: NewspaperIcon },
  technologyreview: { key: "technologyreview", name: "MIT Technology Review", fallbackIcon: NewspaperIcon },
  infoq: { key: "infoq", name: "InfoQ", icon: siInfoq, fallbackIcon: NewspaperIcon },
  github: { key: "github", name: "GitHub Blog", icon: siGithub, fallbackIcon: NewspaperIcon },
  devto: { key: "devto", name: "DEV Community", icon: siDevdotto, fallbackIcon: NewspaperIcon },
  hackernews: { key: "hackernews", name: "Hacker News", icon: siYcombinator, fallbackIcon: NewspaperIcon },
  medium: { key: "medium", name: "Medium", icon: siMedium, fallbackIcon: NewspaperIcon },
  techwd: { key: "techwd", name: "Tech-WD", fallbackIcon: NewspaperIcon },
  majarra: { key: "majarra", name: "Majarra", fallbackIcon: NewspaperIcon },
  hbrarabic: { key: "hbrarabic", name: "HBR Arabic", fallbackIcon: NewspaperIcon },
  mittrarabia: { key: "mittrarabia", name: "MIT Tech Review Arabia", fallbackIcon: NewspaperIcon },
  weworkremotely: { key: "weworkremotely", name: "We Work Remotely", fallbackIcon: BriefcaseIcon },
  remoteok: { key: "remoteok", name: "Remote OK", fallbackIcon: BriefcaseIcon },
  remotive: { key: "remotive", name: "Remotive", fallbackIcon: BriefcaseIcon },
  himalayas: { key: "himalayas", name: "Himalayas", fallbackIcon: BriefcaseIcon },
  wellfound: { key: "wellfound", name: "Wellfound", icon: siWellfound, fallbackIcon: BriefcaseIcon },
  greenhouse: { key: "greenhouse", name: "Greenhouse", icon: siGreenhouse, fallbackIcon: BuildingIcon },
  lever: { key: "lever", name: "Lever", fallbackIcon: BuildingIcon },
  ashby: { key: "ashby", name: "Ashby", fallbackIcon: BuildingIcon },
  larajobs: { key: "larajobs", name: "LaraJobs", fallbackIcon: BriefcaseIcon },
  youtube: { key: "youtube", name: "YouTube", icon: siYoutube, fallbackIcon: GlobeIcon },
  x: { key: "x", name: "X", icon: siX, fallbackIcon: GlobeIcon },
  facebook: { key: "facebook", name: "Facebook", icon: siFacebook, fallbackIcon: GlobeIcon },
  bluesky: { key: "bluesky", name: "Bluesky", icon: siBluesky, fallbackIcon: GlobeIcon },
  instagram: { key: "instagram", name: "Instagram", icon: siInstagram, fallbackIcon: GlobeIcon },
  linkedin: { key: "linkedin", name: "LinkedIn", fallbackIcon: GlobeIcon },
  threads: { key: "threads", name: "Threads", icon: siThreads, fallbackIcon: GlobeIcon },
  tiktok: { key: "tiktok", name: "TikTok", icon: siTiktok, fallbackIcon: GlobeIcon },
  whatsapp: { key: "whatsapp", name: "WhatsApp", icon: siWhatsapp, fallbackIcon: GlobeIcon },
  telegram: { key: "telegram", name: "Telegram", icon: siTelegram, fallbackIcon: GlobeIcon },
  web: { key: "web", name: "Any article", fallbackIcon: FileTextIcon },
}

/** Host (as it appears in a preset's `hosts` array) → brand key. */
const HOST_BRAND: Record<string, string> = {
  "google.com": "google",
  "bing.com": "bing",
  "duckduckgo.com": "duckduckgo",
  "search.brave.com": "brave",
  "search.yahoo.com": "yahoo",
  "news.google.com": "google-news",
  "trends.google.com": "google-trends",
  "mail.google.com": "gmail",
  "theverge.com": "theverge",
  "techcrunch.com": "techcrunch",
  "arstechnica.com": "arstechnica",
  "wired.com": "wired",
  "technologyreview.com": "technologyreview",
  "infoq.com": "infoq",
  "github.blog": "github",
  "dev.to": "devto",
  "news.ycombinator.com": "hackernews",
  "medium.com": "medium",
  "tech-wd.com": "techwd",
  "majarra.com": "majarra",
  "hbrarabic.com": "hbrarabic",
  "mittrarabia.com": "mittrarabia",
  "weworkremotely.com": "weworkremotely",
  "remoteok.com": "remoteok",
  "remotive.com": "remotive",
  "himalayas.app": "himalayas",
  "wellfound.com": "wellfound",
  "greenhouse.io": "greenhouse",
  "lever.co": "lever",
  "ashbyhq.com": "ashby",
  "larajobs.com": "larajobs",
  "youtube.com": "youtube",
  "x.com": "x",
  "facebook.com": "facebook",
  "bsky.app": "bluesky",
  "instagram.com": "instagram",
  "linkedin.com": "linkedin",
  "threads.com": "threads",
  "tiktok.com": "tiktok",
  "web.whatsapp.com": "whatsapp",
  "t.me": "telegram",
}

/** A preset's brand, from the first host it visits. A hostless preset (e.g. "read any article") is "web". */
export function brandForHosts(hosts: string[]): Brand {
  const host = hosts[0]
  const key = (host && HOST_BRAND[host]) || "web"
  return BRANDS[key] ?? BRANDS.web
}

export function brandTint(brand: Brand): string {
  return brand.icon ? `#${brand.icon.hex}` : "var(--grid-muted)"
}
