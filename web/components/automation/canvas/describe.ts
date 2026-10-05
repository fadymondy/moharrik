/*
A step in plain words (MH-1067): "Opens supplier.example/prices" instead of a
command and its arguments. Built only from what the step itself holds; nothing
here is guessed or generated.
*/

import type { WorkflowStep } from "@/lib/api"

import { isolate } from "@/components/bidi"
import { kindOf, subtitleOf } from "./model"

type T = (key: string, vars?: Record<string, string | number>) => string

export function describeStep(step: WorkflowStep, t: T): string {
  const kind = kindOf(step)
  const sub = isolate(subtitleOf(step))
  const a = step.args ?? {}
  switch (kind) {
    case "navigate":
      return t("wfo.say.navigate", { target: sub || t("wfo.target.page") })
    case "click":
      return t("wfo.say.click", { target: sub || t("wfo.target.element") })
    case "type":
      return t("wfo.say.type", { target: sub || t("wfo.target.element") })
    case "press":
      return t("wfo.say.press", { target: sub || t("wfo.target.key") })
    case "select":
      return t("wfo.say.select", { target: sub || t("wfo.target.element") })
    case "scroll":
      return t("wfo.say.scroll")
    case "upload":
      return t("wfo.say.upload")
    case "wait":
      return a.ms || !sub
        ? t("wfo.say.waitMs", { target: sub || "…" })
        : t("wfo.say.waitFor", { target: sub })
    case "wait_for":
      return t("wfo.say.waitFor", { target: sub || t("wfo.target.element") })
    case "extract":
      return t("wfo.say.extract", { target: sub || t("wfo.target.element") })
    case "extract_data":
      return t("wfo.say.extractData", { target: sub || t("wfo.target.data") })
    case "screenshot":
      return t("wfo.say.screenshot")
    case "eval_readonly":
      return t("wfo.say.eval")
    case "release":
      return t("wfo.say.release")
    case "close":
      return t("wfo.say.close")
    case "push_inbox":
      return t("wfo.say.push")
    default:
      return step.command
  }
}
