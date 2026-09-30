"use client"

/*
Adding a preset (MH-478/MH-517), unchanged by MH-527's gallery redesign —
only where it is opened from moved. Allows the preset's sites on the chosen
browser in the same step, asks for a WhatsApp number when the preset needs
one, and confirms before anything that publishes, sends or submits.
*/

import { useState, type FormEvent } from "react"
import { CheckIcon } from "lucide-react"
import { Button, Checkbox, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, Field, FieldDescription, FieldLabel, Input, toast } from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError } from "@/components/form-layout"

import { isolate } from "@/components/bidi"
import { PickSelect } from "@/components/crm/shared"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationPreset, type BrowserAgent } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { plainOf } from "@/components/automation/preset-card"

export function AddPresetDialog({
  preset: p,
  browsers,
  canManage,
  profile,
  onClose,
  onAdded,
}: {
  preset: AutomationPreset
  browsers: BrowserAgent[]
  canManage: boolean
  profile: Record<string, string>
  onClose: () => void
  onAdded: (workflowId: string) => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const plainName = plainOf(p, locale).name
  const [agent, setAgent] = useState(browsers[0]?.id ?? "")
  const chosen = browsers.find((b) => b.id === agent)
  const missingHosts = p.hosts.filter(
    (h) => !chosen?.allowed_hosts.some((a) => h === a || h.endsWith(`.${a}`)),
  )
  const [allow, setAllow] = useState(true)
  const [number, setNumber] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const missingProfile = p.needs_profile.filter((f) => !profile[f])
  const canAllow = canManage && browsers.length > 0 && p.hosts.length > 0

  async function submit(e: FormEvent) {
    e.preventDefault()
    setErr(null)
    setBusy(true)
    try {
      const out = await api<{ workflow: { id: string; name: string }; hosts_added: string[] }>(
        `/api/w/automation/presets/${encodeURIComponent(p.slug)}/add`,
        {
          json: {
            ...(canAllow && allow && agent && missingHosts.length > 0 ? { agent_id: agent } : {}),
            ...(p.needs_whatsapp && number.trim() ? { whatsapp_number: number.trim() } : {}),
            locale: locale === "ar" ? "ar" : "en",
          },
          workspaceId: ws.id,
        },
      )
      toast.success(
        out.hosts_added.length
          ? t("presets.addedWithHosts", {
              name: isolate(out.workflow.name),
              hosts: out.hosts_added.join(", "),
            })
          : t("presets.addedToast", { name: isolate(out.workflow.name) }),
      )
      onAdded(out.workflow.id)
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(v) => (v ? null : onClose())}>
      <DialogContent className="sm:max-w-lg">
        <form onSubmit={submit} noValidate data-form="add-preset" data-preset-dialog={p.slug}>
          <DialogHeader>
            <DialogTitle>{t("presets.addTitle", { name: isolate(plainName) })}</DialogTitle>
            <DialogDescription>{t("presets.addBody")}</DialogDescription>
          </DialogHeader>
          <FieldGroup className="my-4">
            {p.writes ? (
              <p className="border-s-2 border-nq-danger-text/60 ps-3 text-sm" data-add-writes>
                {t("presets.writesNote")}
              </p>
            ) : null}
            {p.needs_whatsapp ? (
              <Field>
                <FieldLabel htmlFor="preset-number">{t("presets.whatsappNumber")}</FieldLabel>
                <Input
                  id="preset-number"
                  dir="ltr"
                  inputMode="tel"
                  value={number}
                  onChange={(e) => setNumber(e.target.value)}
                  placeholder="201000000000"
                />
                <FieldDescription>{t("presets.whatsappHint")}</FieldDescription>
              </Field>
            ) : null}
            {p.needs_profile.length > 0 ? (
              <p className="text-sm text-muted-foreground" data-add-profile>
                {missingProfile.length === 0
                  ? t("presets.profileFilled")
                  : t("presets.profileMissing", {
                      fields: missingProfile.map((f) => t(`presets.profile.${f}`)).join(", "),
                    })}{" "}
                <a className="underline" href="#profile" onClick={onClose}>
                  {t("presets.profileEdit")}
                </a>
              </p>
            ) : null}
            {canAllow ? (
              <>
                <Field>
                  <FieldLabel htmlFor="preset-agent">{t("presets.browser")}</FieldLabel>
                  <PickSelect
                    id="preset-agent"
                    value={agent}
                    onChange={setAgent}
                    options={browsers.map((b) => ({ value: b.id, label: b.name }))}
                  />
                </Field>
                {missingHosts.length > 0 ? (
                  <label className="flex items-start gap-2 text-sm" htmlFor="preset-allow">
                    <Checkbox
                      id="preset-allow"
                      checked={allow}
                      onCheckedChange={(v) => setAllow(v === true)}
                      className="mt-0.5"
                      data-add-allow
                    />
                    <span>{t("presets.allowHosts", { hosts: missingHosts.join(", ") })}</span>
                  </label>
                ) : (
                  <p className="text-sm text-muted-foreground">
                    {t("presets.hostsAlready", { name: isolate(chosen?.name ?? "") })}
                  </p>
                )}
              </>
            ) : p.hosts.length > 0 ? (
              <p className="text-sm text-muted-foreground">
                {canManage
                  ? t("presets.noBrowser")
                  : t("presets.adminAllows", { hosts: p.hosts.join(", ") })}
              </p>
            ) : null}
          </FieldGroup>
          {err ? <FieldError className="mb-3">{err}</FieldError> : null}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy} data-confirm-add>
              <CheckIcon />
              {busy ? t("common.working") : t("presets.add")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
