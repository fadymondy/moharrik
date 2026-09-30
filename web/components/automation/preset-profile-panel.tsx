"use client"

/* The workspace's automation profile (MH-478), unchanged by MH-527 — the
   job-application presets' defaults, edited at the bottom of the gallery. */

import { useState, type FormEvent } from "react"
import { CheckIcon, UserRoundIcon } from "lucide-react"
import { Button, Field, FieldLabel, Input, toast } from "@fadymondy/nasaq/web"
import { FieldGroup } from "@/components/form-layout"

import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

export function PresetProfilePanel({
  canManage,
  data,
  onSaved,
}: {
  canManage: boolean
  data?: { profile: Record<string, string>; fields: string[] }
  onSaved: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [draft, setDraft] = useState<Record<string, string> | null>(null)
  const [busy, setBusy] = useState(false)
  if (!data) return null
  const values = draft ?? data.profile

  async function save(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api("/api/w/automation/profile", {
        method: "PUT",
        json: { profile: values },
        workspaceId: ws.id,
      })
      toast.success(t("presets.profileSaved"))
      setDraft(null)
      onSaved()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="mt-10 scroll-mt-20" id="profile" data-profile>
      <div className="px-6 pb-4">
        <h2 className="flex items-center gap-2 text-base font-medium">
          <UserRoundIcon className="size-4" />
          {t("presets.profileTitle")}
        </h2>
        <p className="max-w-2xl text-sm text-muted-foreground">{t("presets.profileHint")}</p>
      </div>
      <form onSubmit={save} className="border-y border-nq-line px-6 py-5" data-form="profile">
        <FieldGroup>
          <div className="grid gap-4 sm:grid-cols-2">
            {data.fields.map((f) => (
              <Field key={f}>
                <FieldLabel htmlFor={`profile-${f}`}>{t(`presets.profile.${f}`)}</FieldLabel>
                <Input
                  id={`profile-${f}`}
                  dir={/email|phone|linkedin|github|website/.test(f) ? "ltr" : undefined}
                  value={values[f] ?? ""}
                  disabled={!canManage}
                  maxLength={200}
                  onChange={(e) => setDraft({ ...values, [f]: e.target.value })}
                />
              </Field>
            ))}
          </div>
          {canManage ? (
            <div>
              <Button type="submit" disabled={busy || !draft} data-save-profile>
                <CheckIcon />
                {busy ? t("common.working") : t("common.save")}
              </Button>
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">{t("presets.profileAdminOnly")}</p>
          )}
        </FieldGroup>
      </form>
    </section>
  )
}
