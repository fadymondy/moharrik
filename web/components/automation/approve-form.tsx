"use client"

import { useState, type FormEvent } from "react"
import { PlusIcon } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

/** Approve a pairing by the code the extension shows. Shared by the automation page and the install page (MH-399). */
export function ApproveForm({ onApproved, embedded = false }: { onApproved: () => void; /** Inside a setup step, which already says what to do: no hint, no page padding. */ embedded?: boolean }) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [code, setCode] = useState("")
  const [name, setName] = useState("")
  const [hosts, setHosts] = useState("")
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState<string | null>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setErr(null)
    try {
      await api("/api/w/browsers/approve", {
        json: {
          code: code.trim(),
          name: name.trim(),
          allowed_hosts: hosts.split(",").map((h) => h.trim()).filter(Boolean),
        },
        workspaceId: ws.id,
      })
      toast.success(t("automation.paired"))
      setCode("")
      setName("")
      setHosts("")
      onApproved()
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={submit} noValidate className={embedded ? "" : "px-6 pt-6"} data-form="approve">
      {embedded ? null : <p className="pb-3 text-sm text-grid-muted">{t("automation.approveHint", { slug: ws.slug })}</p>}
      <FieldGroup className="sm:flex-row sm:items-end">
        <Field className="sm:w-40">
          <FieldLabel htmlFor="pair-code">{t("automation.code")}</FieldLabel>
          <Input id="pair-code" dir="ltr" inputMode="numeric" value={code} onChange={(e) => setCode(e.target.value)} maxLength={12} />
        </Field>
        <Field className="sm:w-56">
          <FieldLabel htmlFor="pair-name">
            {t("automation.browserName")} <span className="text-grid-muted">({t("common.optional")})</span>
          </FieldLabel>
          <Input id="pair-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
        </Field>
        <Field className="sm:flex-1">
          <FieldLabel htmlFor="pair-hosts">
            {t("automation.allowedHosts")} <span className="text-grid-muted">({t("common.optional")})</span>
          </FieldLabel>
          <Input id="pair-hosts" dir="ltr" value={hosts} onChange={(e) => setHosts(e.target.value)} placeholder="example.com" />
          <FieldDescription>{t("automation.allowedHostsHint")}</FieldDescription>
        </Field>
        <Button type="submit" disabled={busy || !code.trim()}>
          <PlusIcon />
          {busy ? t("common.working") : t("automation.approve")}
        </Button>
      </FieldGroup>
      {err ? <FieldError className="mt-3">{err}</FieldError> : null}
    </form>
  )
}

