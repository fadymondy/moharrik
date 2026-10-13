// A contact timeline event (internal/crm/contact_timeline.go contactEventSources) as one line.
import { channelName, type Key, type T } from "./i18n"
import type { ContactEvent } from "./types"

const KNOWN = new Set<string>([
  "contact.created",
  "activity",
  "deal.opened",
  "deal.won",
  "deal.lost",
  "deal.stage_changed",
  "lifecycle.changed",
  "conversation",
  "lead.submitted",
  "rule.ran",
])

const str = (v: unknown) => (typeof v === "string" ? v : typeof v === "number" ? String(v) : "")

export function eventText(t: T, e: Pick<ContactEvent, "kind" | "data">): { title: string; body: string } {
  const d = (e.data ?? {}) as Record<string, unknown>
  const kind = e.kind.startsWith("activity.") ? "activity" : e.kind
  const stage = (v: unknown) => (str(v) ? t(`lifecycle.${str(v)}` as Key) : "—")
  const title = KNOWN.has(kind)
    ? t(`event.${kind}` as Key, {
        stage: str(d.stage),
        from: stage(d.from),
        to: stage(d.to),
        channel: channelName(t, str(d.channel)),
        form: str(d.form_name),
        rule: str(d.rule_name),
        kind: e.kind.replace(/^activity\./, ""),
      })
    : e.kind
  const body = str(d.title) || str(d.subject) || str(d.body) || str(d.message)
  return { title, body }
}
