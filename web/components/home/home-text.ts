/*
Turns the structured parts of GET /api/w/home (reason codes, event kinds, entity refs) into words and
links. The API never sends prose: it sends a code and the data behind it, so the same payload reads in
English and Arabic and a number on screen is always the number from the response.
*/

import type { HomeEntityRef, HomeEvent, HomeLaneItem, HomeReason } from "@/lib/home-types"
import { inboxHref } from "@/lib/inbox-link"

type T = (key: string, vars?: Record<string, string | number>) => string

/** "have" returns the key's text only when the dictionary knows it; otherwise the fallback. */
function known(t: T, key: string): string | null {
  const s = t(key)
  return s === key ? null : s
}

/** The page a referenced thing lives on (workspace-relative, no leading slash). */
export function entityRoute(ref: HomeEntityRef): string | null {
  switch (ref.type) {
    case "contact":
      return `contacts/${ref.id}`
    case "company":
      return `companies/${ref.id}`
    case "deal":
      return `deals/${ref.id}`
    case "lead":
      return `leads/${ref.id}`
    case "conversation":
      // Query-only: opens the inbox dock over the home page (MH-1147).
      return inboxHref(ref.id)
    case "prospect":
      return "prospects"
    case "rule":
      return `automation/rules/${ref.id}`
    case "run":
      return `automation/runs/${ref.id}`
    case "research":
      return `research/${ref.id}`
    case "campaign":
      return "campaigns"
    default:
      return null
  }
}

export function channelLabel(t: T, channel: string | undefined): string {
  if (!channel) return ""
  return known(t, `channel.${channel}`) ?? channel
}

export function greetingKey(timezone: string, now = new Date()): string {
  let hour = now.getHours()
  try {
    hour = Number(
      new Intl.DateTimeFormat("en-US", {
        hour: "numeric",
        hourCycle: "h23",
        timeZone: timezone,
      }).format(now),
    )
  } catch {
    // An unknown zone falls back to the browser's clock.
  }
  if (hour >= 5 && hour < 12) return "home.greeting.morning"
  if (hour >= 12 && hour < 18) return "home.greeting.afternoon"
  return "home.greeting.evening"
}

export function reasonText(t: T, r: HomeReason, when: (iso: string) => string): string {
  const vars: Record<string, string | number> = {
    n: r.n ?? 0,
    text: r.text ?? "",
    channel: channelLabel(t, r.channel),
    when: r.at ? when(r.at) : "",
  }
  if (r.code === "replied" && !r.channel) return t("home.why.replied_nochannel", vars)
  return known(t, `home.why.${r.code}`) ? t(`home.why.${r.code}`, vars) : r.code
}

export function eventText(
  t: T,
  e: HomeEvent,
  n: (v: number) => string,
): { text: string; by: string | null } {
  const subject = e.subject?.trim() || t("home.ev.unnamed")
  const anon = !e.subject?.trim()
  const vars = { subject, channel: channelLabel(t, e.channel), n: n(e.n ?? 0) }
  const candidates = [
    e.kind === "message.received" && anon ? "home.ev.message.received_anon" : "",
    e.kind === "message.received" && e.channel ? "home.ev.message.received_via" : "",
    e.status ? `home.ev.${e.kind}.${e.status}` : "",
    e.kind === "research.finished" && e.n ? "home.ev.research.finished.n" : "",
    `home.ev.${e.kind}`,
  ].filter(Boolean)
  const key = candidates.find((k) => known(t, k)) ?? ""
  const text = key ? t(key, vars) : `${e.kind} ${subject}`
  let by: string | null = null
  if (e.actor) {
    by = e.actor.kind === "user" && e.actor.name ? t("home.ev.by", { name: e.actor.name }) : null
    if (e.actor.kind !== "user" && e.actor.kind !== "system") by = t("home.ev.byAi")
  }
  return { text, by }
}

export function laneItemText(
  t: T,
  it: HomeLaneItem,
  n: (v: number) => string,
  when: (iso: string) => string,
): string | null {
  if (it.key === "prospect_profile" && it.at) {
    return t("home.lane.prospect_profile_next", { when: when(it.at) })
  }
  if (!known(t, `home.lane.${it.key}`)) return null
  const text = it.key === "channel_kind" ? channelLabel(t, it.text) : (it.text ?? "")
  return t(`home.lane.${it.key}`, { n: n(it.n ?? 0), text })
}
