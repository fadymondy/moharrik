/*
Customer 360 (MH-1064): turns the timeline's kind codes and data into words, a tone and a link. The
API never sends prose, so the same payload reads in English and Arabic and a figure on screen is the
figure in the response. Tones are semantic: success = healthy/won, info = a channel or a record,
violet = Moharrik's own work (automation), warning = needs a look, danger = lost or failed.
*/

import { isolate } from "@/components/bidi"
import type { ContactEvent } from "@/lib/contact-timeline"
import { inboxHref } from "@/lib/inbox-link"

export type T = (key: string, vars?: Record<string, string | number>) => string
export type Tone = "success" | "info" | "violet" | "warning" | "danger" | "muted"
export type EventIcon =
  | "contact"
  | "note"
  | "call"
  | "meeting"
  | "task"
  | "conversation"
  | "deal"
  | "won"
  | "lost"
  | "lifecycle"
  | "lead"
  | "rule"

export type EventView = {
  tone: Tone
  icon: EventIcon
  title: string
  detail: string | null
  /** Workspace-relative link to the record, without a leading slash; null when there is no page. */
  href: string | null
}

type Ctx = {
  money: (minor: number, currency: string) => string
  who: (userId: string | null | undefined) => string
}

const str = (v: unknown): string => (typeof v === "string" ? v : "")
const num = (v: unknown): number => (typeof v === "number" ? v : 0)

/** The key's text only when the dictionary knows it; otherwise the fallback. */
function known(t: T, key: string, fallback: string): string {
  const s = t(key)
  return s === key ? fallback : s
}

export function channelName(t: T, channel: string): string {
  return known(t, `channel.${channel === "chat" ? "widget" : channel}`, channel)
}

export function lifecycleName(t: T, stage: string): string {
  return stage ? known(t, `lifecycle.${stage}`, stage) : t("lifecycle.none")
}

export function eventView(t: T, e: ContactEvent, ctx: Ctx): EventView {
  const d = e.data ?? {}
  const clip = (s: string) => (s ? isolate(s) : "")

  switch (e.kind) {
    case "contact.created":
      return {
        tone: "muted",
        icon: "contact",
        title: t("c360.ev.contact.created"),
        detail: null,
        href: null,
      }

    case "activity.note":
    case "activity.call":
    case "activity.meeting":
    case "activity.task": {
      const kind = e.kind.slice("activity.".length) as "note" | "call" | "meeting" | "task"
      const done = kind === "task" && !!d.done_at
      const onDeal = d.on_deal === true
      const title = done
        ? t("c360.ev.activity.task_done")
        : t(onDeal ? `c360.ev.activity.${kind}_deal` : `c360.ev.activity.${kind}`)
      return {
        tone: kind === "task" && !done ? "warning" : "info",
        icon: kind,
        title,
        detail: str(d.body) || null,
        href: onDeal && str(d.deal_id) ? `deals/${str(d.deal_id)}` : null,
      }
    }

    case "conversation": {
      const n = num(d.messages)
      return {
        tone: "info",
        icon: "conversation",
        title: t("c360.ev.conversation", {
          channel: channelName(t, str(d.channel)),
          subject: clip(str(d.subject)) || t("c360.ev.noSubject"),
        }),
        detail: t("c360.ev.conversation.detail", {
          n,
          inbound: num(d.inbound),
          outbound: Math.max(0, n - num(d.inbound)),
        }),
        // A query-only href opens the inbox dock over this page (MH-1147).
        href: inboxHref(e.id),
      }
    }

    case "deal.opened":
    case "deal.stage_changed":
    case "deal.won":
    case "deal.lost": {
      const value =
        num(d.value_minor) > 0 ? ctx.money(num(d.value_minor), str(d.currency) || "USD") : ""
      const vars = {
        title: clip(str(d.title)),
        stage: clip(str(d.stage)),
        from: clip(str(d.from_stage)),
        value,
      }
      const key = e.kind
      const tone: Tone = key === "deal.won" ? "success" : key === "deal.lost" ? "danger" : "info"
      const icon: EventIcon = key === "deal.won" ? "won" : key === "deal.lost" ? "lost" : "deal"
      const stepped = key === "deal.stage_changed" && vars.from
      return {
        tone,
        icon,
        title: t(`c360.ev.${key}`, vars),
        detail: stepped
          ? t("c360.ev.deal.detail_from", vars)
          : value
            ? t("c360.ev.deal.detail_value", vars)
            : null,
        href: `deals/${e.id}`,
      }
    }

    case "lifecycle.changed": {
      const source = str(d.source)
      const by =
        source === "manual"
          ? t("lifecycle.source.manualBy", { who: isolate(ctx.who(str(d.actor_user_id) || null)) })
          : known(t, `lifecycle.source.${source}`, source)
      return {
        tone: str(d.to) === "customer" ? "success" : "info",
        icon: "lifecycle",
        title: t(str(d.from) ? "c360.ev.lifecycle.changed" : "c360.ev.lifecycle.set", {
          from: lifecycleName(t, str(d.from)),
          to: lifecycleName(t, str(d.to)),
        }),
        detail: by || null,
        href: null,
      }
    }

    case "lead.submitted": {
      const utm = [str(d.utm_source), str(d.utm_medium), str(d.utm_campaign)]
        .filter(Boolean)
        .join(" / ")
      const parts = [
        utm ? t("c360.ev.lead.utm", { utm: isolate(utm) }) : "",
        str(d.message) ? isolate(str(d.message)) : "",
      ].filter(Boolean)
      return {
        tone: "info",
        icon: "lead",
        title: str(d.form_name)
          ? t("c360.ev.lead.form", { form: isolate(str(d.form_name)) })
          : t("c360.ev.lead.submitted"),
        detail: parts.join(" · ") || null,
        href: `leads/${e.id}`,
      }
    }

    case "rule.ran": {
      const status = str(d.status)
      return {
        tone: status === "failed" ? "danger" : "violet",
        icon: "rule",
        title: t("c360.ev.rule.ran", {
          rule: isolate(str(d.rule_name)),
          status: known(t, `automation.status.${status}`, status),
        }),
        detail: null,
        href: str(d.run_id) ? `automation/runs/${str(d.run_id)}` : `automation/rules/${e.id}`,
      }
    }

    default:
      return { tone: "muted", icon: "contact", title: e.kind, detail: null, href: null }
  }
}
