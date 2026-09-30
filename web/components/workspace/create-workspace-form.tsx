"use client"

import { useState, type FormEvent } from "react"
import { useRouter } from "next/navigation"
import { useSWRConfig } from "swr"

import { Button, Field, FieldDescription, FieldLabel, Input } from "@fadymondy/nasaq/web"
import { FieldGroup, FieldError } from "@/components/form-layout"
import { api, ApiError, type WorkspaceSummary } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

const SLUG_RE = /^[a-z0-9-]{3,40}$/

export function CreateWorkspaceForm({
  onCreated,
  autoFocus,
}: {
  onCreated?: () => void
  autoFocus?: boolean
}) {
  const { t, locale } = useTranslations()
  const router = useRouter()
  const { mutate } = useSWRConfig()
  const [name, setName] = useState("")
  const [slug, setSlug] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    const s = slug.trim().toLowerCase()
    if (s && !SLUG_RE.test(s)) {
      setError(t("workspaces.slugInvalid"))
      return
    }
    setBusy(true)
    try {
      const res = await api<{ workspace: WorkspaceSummary }>("/api/me/workspaces", {
        json: s ? { name: name.trim(), slug: s } : { name: name.trim() },
      })
      await mutate("/api/me/workspaces")
      onCreated?.()
      router.push(`/${locale}/w/${res.workspace.slug}`)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) setError(t("workspaces.slugTaken"))
      else setError(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate>
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="ws-name">{t("workspaces.name")}</FieldLabel>
          <Input
            id="ws-name"
            dir="auto"
            required
            autoFocus={autoFocus}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t("workspaces.namePlaceholder")}
          />
        </Field>
        <Field invalid={!!(!!error)}>
          <FieldLabel htmlFor="ws-slug">
            {t("workspaces.slug")}{" "}
            <span className="font-normal text-muted-foreground">({t("common.optional")})</span>
          </FieldLabel>
          <Input
            id="ws-slug"
            dir="ltr"
            className="font-mono"
            value={slug}
            onChange={(e) => setSlug(e.target.value.toLowerCase())}
            placeholder="acme-studio"
            pattern="[a-z0-9-]{3,40}"
          />
          <FieldDescription>{t("workspaces.slugHint")}</FieldDescription>
          {error ? <FieldError>{error}</FieldError> : null}
        </Field>
        <div>
          <Button type="submit" disabled={busy || !name.trim()}>
            {busy ? t("common.working") : t("workspaces.create")}
          </Button>
        </div>
      </FieldGroup>
    </form>
  )
}
