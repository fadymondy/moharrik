/*
MH-524: a rule run's action summaries/errors and a run's reason are English
sentences stored in automation_rule_runs, so the log read wrong on an Arabic
page. Every one written by internal/rules now also carries a stable code +
untranslated values (a name, a title, an id — never touched here); this
module looks the code up in the i18n dictionary and fills the values in, so
the viewer reads it in their own language.

A run or action logged before this shipped (or one wrapping an arbitrary
error from outside internal/rules) has no code: fall back to the plain
English text that is still stored alongside it.

A few codes carry a variable set of values (create_deal only has "stage" or
"value" when the rule set them, for instance) and are composed here from
smaller fragments, joined with rules.join, mirroring the order the Go code
builds its own English sentence in.
*/

import type { RuleActionResult, RuleRun } from "@/lib/rules"

type T = (key: string, vars?: Record<string, string | number>) => string

function composeCreateDeal(t: T, args: Record<string, string>): string {
  let s = t("rules.msg.create_deal.base", { title: args.title ?? "" })
  if (args.stage) s += t("rules.msg.create_deal.stage", { stage: args.stage })
  if (args.value) s += t("rules.msg.create_deal.value", { value: args.value })
  return s
}

function composeUpdateDeal(t: T, args: Record<string, string>): string {
  const parts: string[] = []
  if (args.stage) parts.push(t("rules.msg.update_deal.stage", { stage: args.stage }))
  if (args.value) parts.push(t("rules.msg.update_deal.value", { value: args.value }))
  if (args.owner) parts.push(t("rules.msg.update_deal.owner", { owner: args.owner }))
  return t("rules.msg.update_deal.base", { id: args.id ?? "" }) + parts.join(t("rules.join"))
}

function composeUpdateContact(t: T, args: Record<string, string>): string {
  const parts: string[] = []
  if (args.lifecycle) parts.push(t("rules.msg.update_contact.lifecycle", { lifecycle: args.lifecycle }))
  if (args.owner) parts.push(t("rules.msg.update_contact.owner", { owner: args.owner }))
  if (args.tags) parts.push(t("rules.msg.update_contact.tags", { tags: args.tags }))
  return t("rules.msg.update_contact.base", { id: args.id ?? "" }) + parts.join(t("rules.join"))
}

function composeActivity(t: T, kind: "create_task" | "add_note", args: Record<string, string>): string {
  let s = t(`rules.msg.${kind}.base`, { body: args.body ?? "" })
  const on: string[] = []
  if (args.contact_id) on.push(t("rules.msg.activity.onContact", { contact_id: args.contact_id }))
  if (args.deal_id) on.push(t("rules.msg.activity.onDeal", { deal_id: args.deal_id }))
  if (on.length > 0) s += " " + on.join(` ${t("rules.and")} `)
  if (args.due_days) s += t("rules.msg.activity.dueDays", { due_days: args.due_days })
  return s
}

/** Renders one code + its values into the viewer's language. */
export function msgText(t: T, code: string | undefined, args: Record<string, string> | undefined): string | null {
  if (!code) return null
  const a = args ?? {}
  switch (code) {
    case "create_deal":
      return composeCreateDeal(t, a)
    case "update_deal":
      return composeUpdateDeal(t, a)
    case "update_contact":
      return composeUpdateContact(t, a)
    case "create_task":
    case "add_note":
      return composeActivity(t, code, a)
    default:
      return t(`rules.msg.${code}`, a)
  }
}

/** An action's summary, translated when a code is present, English otherwise. */
export function actionSummaryText(t: T, a: RuleActionResult): string {
  return msgText(t, a.summary_code, a.summary_args) ?? a.summary
}

/** An action's error, translated when a code is present, English otherwise. */
export function actionErrorText(t: T, a: RuleActionResult): string | undefined {
  return msgText(t, a.error_code, a.error_args) ?? a.error
}

/** A run's reason (why it was skipped, limited or blocked, or its first
 * failure), translated when a code is present, English otherwise. */
export function runReasonText(t: T, run: Pick<RuleRun, "reason" | "reason_code" | "reason_args">): string {
  return msgText(t, run.reason_code, run.reason_args) ?? run.reason
}
