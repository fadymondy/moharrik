"use client"

/*
The preset gallery (MH-478, phase 3a): every browser plugin fadymondy.com runs,
as a starting point this workspace can add as its own workflow.

Grouped by platform and searchable. Each preset says, before anything is added,
which sites it goes to, what it asks for, and whether it publishes, sends or
submits as the signed-in account — and names its rehearsal twin where there is
one. "Add" saves it as a workflow the workspace owns and, for an admin, can
allow its sites on a browser in the same step. The workspace profile at the
bottom is what the job-application presets take their defaults from.

Its own route, so the workflows list can link "New workflow → from a preset"
straight here.
*/

import Link from "next/link"
import { useMemo, useState, type FormEvent } from "react"
import { ArrowLeftIcon, CheckIcon, ExternalLinkIcon, PlusIcon, SearchIcon, ShieldCheckIcon, UserRoundIcon } from "lucide-react"
import { toast } from "sonner"

import { Bdi, isolate } from "@/components/bidi"
import { PresetIcon } from "@/components/automation/preset-icon"
import { PickSelect } from "@/components/crm/shared"
import { SectionHeader } from "@/components/public-frame"
import { EmptyState, ErrorState, LoadingRows } from "@/components/states"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationPreset, type BrowserAgent } from "@/lib/api"
import { useWorkspaceEvents } from "@/lib/events"
import { useTranslations } from "@/lib/i18n"
import { useAutomationProfile, useBrowsers, usePresets } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"

// The order platforms are listed in: reading the web first, then the networks
// a business posts to, then messaging and jobs.
const GROUP_ORDER = [
  "google-news",
  "google-trends",
  "search",
  "news",
  "linkedin",
  "x",
  "facebook",
  "instagram",
  "threads",
  "bluesky",
  "tiktok",
  "youtube",
  "gmail",
  "whatsapp",
  "telegram",
  "jobs",
]

export default function PresetsPage() {
  const { t, locale, formatNumber } = useTranslations()
  const ws = useWorkspace()
  useDocumentTitle(`${t("presets.title")} · ${ws.summary.name}`)
  const canRun = ws.can("automation:run")
  const canManage = ws.can("automation:manage")
  const presets = usePresets(ws.id)
  const browsers = useBrowsers(ws.id)
  const profile = useAutomationProfile(ws.id)
  useWorkspaceEvents(ws.id, (e) => {
    if (e.kind === "browser") void browsers.mutate()
  })

  const [q, setQ] = useState("")
  const [group, setGroup] = useState("")
  const [adding, setAdding] = useState<AutomationPreset | null>(null)
  const list = presets.data?.presets ?? []
  const bySlug = useMemo(() => new Map(list.map((p) => [p.slug, p])), [list])

  const groups = useMemo(() => {
    const needle = q.trim().toLowerCase()
    const hit = (p: AutomationPreset) =>
      (!group || p.group === group) &&
      (!needle || `${p.name} ${p.slug} ${p.description} ${p.hosts.join(" ")} ${t(`presets.group.${p.group}`)}`.toLowerCase().includes(needle))
    const m = new Map<string, AutomationPreset[]>()
    for (const p of list) if (hit(p)) m.set(p.group, [...(m.get(p.group) ?? []), p])
    return [...m.entries()].sort((a, b) => rank(a[0]) - rank(b[0]))
  }, [list, q, group, t])
  const shown = groups.reduce((n, [, ps]) => n + ps.length, 0)
  const active = (browsers.data?.browsers ?? []).filter((b) => !b.revoked_at)

  return (
    <>
      <SectionHeader
        micro={<Bdi>{ws.summary.name}</Bdi>}
        title={t("presets.title")}
        description={t("presets.hint")}
        action={
          <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation`} />}>
            <ArrowLeftIcon className="rtl:rotate-180" />
            {t("automation.backToAutomation")}
          </Button>
        }
      />

      <div className="flex flex-wrap items-center gap-3 border-b border-line px-6 py-4">
        <div className="relative min-w-0 flex-1 sm:max-w-sm">
          <SearchIcon className="pointer-events-none absolute start-2.5 top-1/2 size-4 -translate-y-1/2 text-grid-muted" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("presets.search")} aria-label={t("presets.search")} className="ps-8" data-preset-search />
        </div>
        <div className="w-52">
          <PickSelect
            id="preset-group"
            value={group}
            onChange={setGroup}
            noneLabel={t("presets.allPlatforms")}
            options={GROUP_ORDER.filter((g) => list.some((p) => p.group === g)).map((g) => ({ value: g, label: t(`presets.group.${g}`) }))}
          />
        </div>
        {presets.data ? (
          <p className="text-sm text-grid-muted" data-preset-count={shown}>
            {t("presets.count", { n: formatNumber(shown), total: formatNumber(list.length) })}
          </p>
        ) : null}
      </div>

      {presets.error ? (
        <ErrorState error={presets.error} />
      ) : presets.isLoading ? (
        <LoadingRows rows={6} />
      ) : shown === 0 ? (
        <EmptyState title={t("presets.noMatch", { q })} />
      ) : (
        groups.map(([g, ps]) => (
          <section key={g} className="mt-8" data-preset-group={g}>
            <h2 className="flex items-center gap-2 px-6 pb-2 text-base font-medium">
              {t(`presets.group.${g}`)}
              <span className="text-sm font-normal text-grid-muted tabular-nums">{formatNumber(ps.length)}</span>
            </h2>
            <ul className="divide-y divide-line border-y border-line">
              {ps.map((p) => (
                <PresetRow key={p.slug} preset={p} twin={bySlug.get(p.dry_run ?? p.dry_run_of ?? "")} canRun={canRun} onAdd={() => setAdding(p)} />
              ))}
            </ul>
          </section>
        ))
      )}

      <ProfilePanel canManage={canManage} data={profile.data} onSaved={() => profile.mutate()} />
      <div className="pb-8" />

      {adding ? (
        <AddDialog
          preset={adding}
          browsers={active}
          canManage={canManage}
          profile={profile.data?.profile ?? {}}
          onClose={() => setAdding(null)}
          onAdded={() => {
            setAdding(null)
            void presets.mutate()
            void browsers.mutate()
          }}
        />
      ) : null}
    </>
  )
}

function rank(g: string) {
  const i = GROUP_ORDER.indexOf(g)
  return i < 0 ? GROUP_ORDER.length : i
}

function PresetRow({ preset: p, twin, canRun, onAdd }: { preset: AutomationPreset; twin?: AutomationPreset; canRun: boolean; onAdd: () => void }) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const [more, setMore] = useState(false)
  return (
    <li className="flex flex-wrap items-start gap-3 px-6 py-4" id={`preset-${p.slug}`} data-preset={p.slug} data-writes={p.writes ? "true" : undefined}>
      <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md border border-line text-grid-muted">
        <PresetIcon name={p.icon} className="size-4" />
      </span>
      <div className="min-w-0 flex-1 space-y-1">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          <Bdi>{p.name}</Bdi>
          {p.writes ? (
            <Badge variant="secondary" data-preset-publishes>
              {t("presets.publishes")}
            </Badge>
          ) : null}
          {p.dry_run_of ? <Badge variant="outline">{t("presets.rehearsal")}</Badge> : null}
          {p.workflow_id ? (
            <Badge variant="outline" data-preset-added>
              <CheckIcon />
              {t("presets.added")}
            </Badge>
          ) : null}
        </p>
        <p className={more ? "text-sm text-grid-muted" : "line-clamp-2 text-sm text-grid-muted"} dir="ltr">
          {p.description}
        </p>
        {p.description.length > 160 ? (
          <button type="button" className="text-xs text-grid-muted underline" onClick={() => setMore((v) => !v)}>
            {more ? t("presets.less") : t("presets.more")}
          </button>
        ) : null}
        <p className="flex items-start gap-1 text-xs text-grid-muted" data-preset-hosts={p.hosts.join(",")}>
          <ShieldCheckIcon className="mt-0.5 size-3 shrink-0" />
          <span className="min-w-0 break-words">{p.hosts.length ? t("presets.goesTo", { hosts: p.hosts.join(", ") }) : t("presets.anySite")}</span>
        </p>
        {p.inputs.length > 0 ? (
          <p className="text-xs text-grid-muted" data-preset-inputs>
            {t("presets.asksFor", { names: isolate(p.inputs.map((i) => (i.required ? `${i.name}*` : i.name)).join(", ")) })}
          </p>
        ) : null}
        {twin && p.dry_run ? (
          <p className="text-xs text-grid-muted">
            {t("presets.rehearseFirst")}{" "}
            <a className="underline" href={`#preset-${twin.slug}`}>
              <Bdi>{twin.name}</Bdi>
            </a>
          </p>
        ) : null}
      </div>
      <div className="flex items-center gap-2">
        {p.workflow_id ? (
          <Button variant="ghost" size="sm" nativeButton={false} render={<Link href={`/${locale}/w/${ws.slug}/automation/workflows/${p.workflow_id}`} />} data-open-added>
            <ExternalLinkIcon />
            {t("presets.open")}
          </Button>
        ) : canRun ? (
          <Button variant="outline" size="sm" onClick={onAdd} data-add-preset={p.slug}>
            <PlusIcon />
            {t("presets.add")}
          </Button>
        ) : null}
      </div>
    </li>
  )
}

function AddDialog({
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
  onAdded: () => void
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [agent, setAgent] = useState(browsers[0]?.id ?? "")
  const chosen = browsers.find((b) => b.id === agent)
  const missingHosts = p.hosts.filter((h) => !chosen?.allowed_hosts.some((a) => h === a || h.endsWith(`.${a}`)))
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
      const out = await api<{ workflow: { id: string; name: string }; hosts_added: string[] }>(`/api/w/automation/presets/${encodeURIComponent(p.slug)}/add`, {
        json: {
          ...(canAllow && allow && agent && missingHosts.length > 0 ? { agent_id: agent } : {}),
          ...(p.needs_whatsapp && number.trim() ? { whatsapp_number: number.trim() } : {}),
        },
        workspaceId: ws.id,
      })
      toast.success(
        out.hosts_added.length
          ? t("presets.addedWithHosts", { name: isolate(out.workflow.name), hosts: out.hosts_added.join(", ") })
          : t("presets.addedToast", { name: isolate(out.workflow.name) }),
      )
      onAdded()
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
            <DialogTitle>{t("presets.addTitle", { name: isolate(p.name) })}</DialogTitle>
            <DialogDescription>{t("presets.addBody")}</DialogDescription>
          </DialogHeader>
          <FieldGroup className="my-4">
            {p.writes ? (
              <p className="border-s-2 border-grid-danger-text/60 ps-3 text-sm" data-add-writes>
                {t("presets.writesNote")}
              </p>
            ) : null}
            {p.needs_whatsapp ? (
              <Field>
                <FieldLabel htmlFor="preset-number">{t("presets.whatsappNumber")}</FieldLabel>
                <Input id="preset-number" dir="ltr" inputMode="tel" value={number} onChange={(e) => setNumber(e.target.value)} placeholder="201000000000" />
                <FieldDescription>{t("presets.whatsappHint")}</FieldDescription>
              </Field>
            ) : null}
            {p.needs_profile.length > 0 ? (
              <p className="text-sm text-grid-muted" data-add-profile>
                {missingProfile.length === 0
                  ? t("presets.profileFilled")
                  : t("presets.profileMissing", { fields: missingProfile.map((f) => t(`presets.profile.${f}`)).join(", ") })}{" "}
                <a className="underline" href="#profile" onClick={onClose}>
                  {t("presets.profileEdit")}
                </a>
              </p>
            ) : null}
            {canAllow ? (
              <>
                <Field>
                  <FieldLabel htmlFor="preset-agent">{t("presets.browser")}</FieldLabel>
                  <PickSelect id="preset-agent" value={agent} onChange={setAgent} options={browsers.map((b) => ({ value: b.id, label: b.name }))} />
                </Field>
                {missingHosts.length > 0 ? (
                  <label className="flex items-start gap-2 text-sm" htmlFor="preset-allow">
                    <Checkbox id="preset-allow" checked={allow} onCheckedChange={(v) => setAllow(v === true)} className="mt-0.5" data-add-allow />
                    <span>{t("presets.allowHosts", { hosts: missingHosts.join(", ") })}</span>
                  </label>
                ) : (
                  <p className="text-sm text-grid-muted">{t("presets.hostsAlready", { name: isolate(chosen?.name ?? "") })}</p>
                )}
              </>
            ) : p.hosts.length > 0 ? (
              <p className="text-sm text-grid-muted">{canManage ? t("presets.noBrowser") : t("presets.adminAllows", { hosts: p.hosts.join(", ") })}</p>
            ) : null}
          </FieldGroup>
          {err ? <FieldError className="mb-3">{err}</FieldError> : null}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={busy} data-confirm-add>
              <PlusIcon />
              {busy ? t("common.working") : t("presets.add")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function ProfilePanel({ canManage, data, onSaved }: { canManage: boolean; data?: { profile: Record<string, string>; fields: string[] }; onSaved: () => void }) {
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
      await api("/api/w/automation/profile", { method: "PUT", json: { profile: values }, workspaceId: ws.id })
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
        <p className="max-w-2xl text-sm text-grid-muted">{t("presets.profileHint")}</p>
      </div>
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
