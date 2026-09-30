"use client"

/*
Parameter definitions (MH-400): the values a workflow asks for when it runs.

Every {{placeholder}} the steps use should be declared here — the editor lists
the ones that are not and declares them in one click. A SECRET parameter is
typed hidden, redacted from the run record, and sealed when a schedule keeps
it; it cannot have a default, because a default is stored in the clear in the
workflow and in every version of it.
*/

import { PlusIcon, Trash2Icon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import type { WorkflowParam, WorkflowStep } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

const PLACEHOLDER = /\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}/g

export function placeholdersIn(steps: WorkflowStep[]): string[] {
  const seen = new Set<string>()
  for (const m of JSON.stringify(steps).matchAll(PLACEHOLDER)) seen.add(m[1])
  return [...seen]
}

export const validParamName = (name: string) => /^[a-zA-Z_][a-zA-Z0-9_]*$/.test(name)

export function ParamsEditor({
  params,
  onChange,
  used,
}: {
  params: WorkflowParam[]
  onChange: (next: WorkflowParam[]) => void
  /** The placeholders the steps use. */
  used: string[]
}) {
  const { t } = useTranslations()
  const declared = new Set(params.map((p) => p.name))
  const missing = used.filter((n) => !declared.has(n))
  const set = (i: number, patch: Partial<WorkflowParam>) =>
    onChange(params.map((p, j) => (j === i ? { ...p, ...patch } : p)))

  return (
    <div data-params-editor>
      {missing.length > 0 ? (
        <div
          className="mx-6 mb-3 flex flex-wrap items-center gap-2 border border-nq-line bg-card px-3 py-2 text-sm"
          data-missing-params
        >
          <span className="text-muted-foreground">
            {t("automation.params.undeclared", { names: missing.join(", ") })}
          </span>
          <Button
            type="button"
            size="xs"
            variant="outline"
            onClick={() =>
              onChange([...params, ...missing.map((name) => ({ name, required: true }))])
            }
          >
            {t("automation.params.declare")}
          </Button>
        </div>
      ) : null}

      {params.length === 0 ? (
        <p className="px-6 pb-2 text-sm text-muted-foreground">{t("automation.params.none")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] text-sm">
            <thead className="text-start text-xs text-muted-foreground">
              <tr className="border-b border-nq-line">
                <th className="px-6 py-2 text-start font-normal">{t("automation.params.name")}</th>
                <th className="px-2 py-2 text-start font-normal">{t("automation.params.label")}</th>
                <th className="px-2 py-2 text-start font-normal">
                  {t("automation.params.default")}
                </th>
                <th className="px-2 py-2 text-center font-normal">
                  {t("automation.params.required")}
                </th>
                <th className="px-2 py-2 text-center font-normal">
                  {t("automation.params.secret")}
                </th>
                <th className="px-6 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-nq-line">
              {params.map((p, i) => (
                <tr key={i} data-param={p.name}>
                  <td className="px-6 py-2 align-top">
                    <Input
                      dir="ltr"
                      className="font-mono text-xs"
                      aria-label={t("automation.params.name")}
                      aria-invalid={!validParamName(p.name) || undefined}
                      value={p.name}
                      onChange={(e) => set(i, { name: e.target.value.trim() })}
                    />
                    {!used.includes(p.name) && validParamName(p.name) ? (
                      <p className="mt-1 text-xs text-muted-foreground">
                        {t("automation.params.unused")}
                      </p>
                    ) : null}
                  </td>
                  <td className="px-2 py-2 align-top">
                    <Input
                      aria-label={t("automation.params.label")}
                      value={p.label ?? ""}
                      onChange={(e) => set(i, { label: e.target.value })}
                    />
                  </td>
                  <td className="px-2 py-2 align-top">
                    {p.secret ? (
                      <p className="pt-1.5 text-xs text-muted-foreground">
                        {t("automation.params.secretNoDefault")}
                      </p>
                    ) : (
                      <Input
                        aria-label={t("automation.params.default")}
                        value={p.default ?? ""}
                        onChange={(e) => set(i, { default: e.target.value })}
                      />
                    )}
                  </td>
                  <td className="px-2 py-2 text-center align-top">
                    <Checkbox
                      className="mt-2"
                      aria-label={t("automation.params.required")}
                      checked={!!p.required}
                      onCheckedChange={(v) => set(i, { required: !!v })}
                    />
                  </td>
                  <td className="px-2 py-2 text-center align-top">
                    <Checkbox
                      className="mt-2"
                      aria-label={t("automation.params.secret")}
                      checked={!!p.secret}
                      onCheckedChange={(v) =>
                        set(i, { secret: !!v, ...(v ? { default: "" } : {}) })
                      }
                    />
                  </td>
                  <td className="px-6 py-2 text-end align-top">
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-sm"
                      className="text-nq-danger-text"
                      aria-label={t("automation.params.remove")}
                      onClick={() => onChange(params.filter((_, j) => j !== i))}
                    >
                      <Trash2Icon />
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="px-6 pt-3">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onChange([...params, { name: "", required: true }])}
        >
          <PlusIcon />
          {t("automation.params.add")}
        </Button>
      </div>
    </div>
  )
}
