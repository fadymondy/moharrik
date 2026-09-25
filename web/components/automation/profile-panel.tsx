"use client"

/*
The workspace profile (MH-478, moved to Settings for MH-478's autofill panel):
the handful of values presets and the extension's own-tab autofill fill forms
with — name, contact, links. Admin-only to change; anyone who can run
automation sees it, because it is what a preset's or a form's "who does this
default to" question answers.

Shared by the Settings page (its home) and the Presets gallery's "add" dialog,
which links here rather than duplicating the form.
*/

import { useState, type FormEvent } from "react"
import { CheckIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

export function ProfilePanel({
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
    <section className="scroll-mt-20" id="profile" data-profile>
      <form onSubmit={save} className="border-y border-line px-6 py-5" data-form="profile">
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
            <p className="text-xs text-grid-muted">{t("presets.profileAdminOnly")}</p>
          )}
        </FieldGroup>
      </form>
    </section>
  )
}
