/*
The Rules pages' shared vocabulary (MH-427): an icon per trigger and action,
the starting templates, and what a run looks like on the canvas.
*/

import {
  ArrowRightLeftIcon,
  BellIcon,
  BellRingIcon,
  BotOffIcon,
  CalendarClockIcon,
  CircleXIcon,
  ClipboardListIcon,
  FlameIcon,
  HandshakeIcon,
  ListPlusIcon,
  ListTodoIcon,
  MailCheckIcon,
  MailIcon,
  MessageCircleIcon,
  MessageSquareIcon,
  MessageSquarePlusIcon,
  MessageSquareReplyIcon,
  PencilLineIcon,
  RouteIcon,
  SendIcon,
  StickyNoteIcon,
  TrophyIcon,
  UserCheckIcon,
  UserPenIcon,
  UserPlusIcon,
  WebhookIcon,
  WorkflowIcon,
  ZapIcon,
  type LucideIcon,
} from "lucide-react"

import type { RuleAction, RuleActionResult, RuleCondition, RuleInput, RuleRun } from "@/lib/rules"

export const TRIGGER_ICON: Record<string, LucideIcon> = {
  "lead.created": UserPlusIcon,
  "form.submitted": ClipboardListIcon,
  "conversation.created": MessageSquarePlusIcon,
  "message.received": MessageSquareIcon,
  "deal.created": HandshakeIcon,
  "deal.stage_changed": ArrowRightLeftIcon,
  "deal.won": TrophyIcon,
  "deal.lost": CircleXIcon,
  "contact.lifecycle_changed": RouteIcon,
  "contact.opted_in": MailCheckIcon,
  "campaign.sent": SendIcon,
  "automation.run_failed": BotOffIcon,
  "notification.received": BellIcon,
  "trend.topic": FlameIcon,
  schedule: CalendarClockIcon,
}

export const ACTION_ICON: Record<string, LucideIcon> = {
  notify_members: BellRingIcon,
  create_deal: HandshakeIcon,
  update_deal: PencilLineIcon,
  create_contact: UserPlusIcon,
  update_contact: UserPenIcon,
  create_task: ListTodoIcon,
  add_note: StickyNoteIcon,
  assign_member: UserCheckIcon,
  canned_reply: MessageSquareReplyIcon,
  send_email: MailIcon,
  whatsapp_contact: MessageCircleIcon,
  add_to_list: ListPlusIcon,
  run_workflow: WorkflowIcon,
  webhook: WebhookIcon,
}

export const triggerIcon = (t: string): LucideIcon => TRIGGER_ICON[t] ?? ZapIcon
export const actionIcon = (t: string): LucideIcon => ACTION_ICON[t] ?? ZapIcon

/** The pickers group triggers by where they come from. */
export const TRIGGER_GROUPS: { id: string; types: string[] }[] = [
  {
    id: "crm",
    types: [
      "lead.created",
      "deal.created",
      "deal.stage_changed",
      "deal.won",
      "deal.lost",
      "contact.lifecycle_changed",
    ],
  },
  { id: "inbox", types: ["conversation.created", "message.received"] },
  { id: "marketing", types: ["form.submitted", "contact.opted_in", "campaign.sent"] },
  { id: "signals", types: ["notification.received", "trend.topic", "automation.run_failed"] },
  { id: "time", types: ["schedule"] },
]

/** Operators that compare with nothing. */
export const UNARY = new Set(["is_empty", "is_not_empty"])

export type Draft = {
  name: string
  description: string
  trigger: string
  match: "all" | "any"
  conditions: RuleCondition[]
  actions: RuleAction[]
  cron: string
  timezone: string
  once_per_subject: boolean
  max_runs_per_hour: number
}

export const emptyDraft = (): Draft => ({
  name: "",
  description: "",
  trigger: "lead.created",
  match: "all",
  conditions: [],
  actions: [],
  cron: "0 9 * * 1-5",
  timezone: "UTC",
  once_per_subject: false,
  max_runs_per_hour: 30,
})

export function toInput(d: Draft): RuleInput {
  const actions = d.actions.map((a) => {
    const params: Record<string, string> = {}
    for (const [k, v] of Object.entries(a.params ?? {})) if (v !== "") params[k] = v
    return { type: a.type, params }
  })
  return {
    name: d.name.trim(),
    description: d.description.trim(),
    trigger: d.trigger,
    match: d.match,
    conditions: d.conditions,
    actions,
    cron: d.trigger === "schedule" ? d.cron.trim() : undefined,
    timezone: d.trigger === "schedule" ? d.timezone.trim() || "UTC" : undefined,
    once_per_subject: d.once_per_subject,
    max_runs_per_hour: d.max_runs_per_hour,
  }
}

const act = (type: string, params: Record<string, string>): RuleAction => ({ type, params })

/** The templates the card names, ready to adjust. */
export type Template = {
  id: string
  draft: (ctx: { userId: string; listId: string; t: (k: string) => string }) => Draft
}

export const TEMPLATES: Template[] = [
  {
    id: "lead",
    draft: ({ t }) => ({
      ...emptyDraft(),
      name: t("rules.tpl.lead.name"),
      trigger: "lead.created",
      conditions: [{ field: "email", op: "is_not_empty", value: "" }],
      actions: [
        act("notify_members", {
          members: "all",
          title: t("rules.tpl.lead.title"),
          body: "{{message}}",
          whatsapp: "false",
        }),
        act("create_deal", { title: "{{company}} — {{name}}", contact_id: "" }),
        act("create_task", {
          body: t("rules.tpl.lead.task"),
          due_in_days: "1",
          deal_id: "{{deal_id}}",
        }),
      ],
    }),
  },
  {
    id: "sentry",
    draft: ({ t, userId }) => ({
      ...emptyDraft(),
      name: t("rules.tpl.sentry.name"),
      trigger: "notification.received",
      conditions: [
        { field: "source_kind", op: "equals", value: "sentry" },
        { field: "severity", op: "in", value: "error, fatal" },
      ],
      actions: [
        act("notify_members", {
          members: userId,
          whatsapp: "true",
          title: "Sentry: {{title}}",
          body: "{{body}}",
        }),
      ],
    }),
  },
  {
    id: "optin",
    draft: ({ t, listId }) => ({
      ...emptyDraft(),
      name: t("rules.tpl.optin.name"),
      trigger: "contact.opted_in",
      conditions: [{ field: "channel", op: "equals", value: "email" }],
      actions: [act("add_to_list", { list_id: listId, contact_id: "{{contact_id}}" })],
      once_per_subject: true,
    }),
  },
]

// ── a run on the canvas ──────────────────────────────────────────────────

export type NodeState = "ok" | "failed" | "skipped" | "would" | undefined

export type CanvasRun = {
  trigger: NodeState
  conditions: NodeState
  actions: NodeState[]
}

/** How a run (a test or a real one) paints the nodes. */
export function paint(run: RuleRun | null | undefined, actionCount: number): CanvasRun | undefined {
  if (!run) return undefined
  const blocked = run.status === "loop_blocked" || run.status === "rate_limited"
  const condFailed = run.status === "skipped" && run.conditions.some((c) => !c.passed)
  const acts: NodeState[] = Array.from({ length: actionCount }, (_, i) => {
    const a: RuleActionResult | undefined = run.actions[i]
    if (!a) return blocked || condFailed || run.status === "skipped" ? "skipped" : undefined
    return a.status
  })
  return {
    trigger: blocked ? "failed" : "ok",
    conditions: blocked ? "skipped" : condFailed ? "failed" : "ok",
    actions: acts,
  }
}
