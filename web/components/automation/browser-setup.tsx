"use client"

/*
The browser pairing flow and the paired-browser list, split out of the old
combined Automation page (MH-526) so /automation/browsers can render it on
its own. Unchanged from what it replaced: install → pair → allow sites, then
one status row once all three are done, opened again on demand.
*/

import Link from "next/link"
import { useState, type FormEvent } from "react"
import {
  BookOpenIcon,
  CheckIcon,
  ChevronUpIcon,
  DownloadIcon,
  PencilIcon,
  SettingsIcon,
  ShieldCheckIcon,
} from "lucide-react"
import { toast } from "sonner"

import { ConfirmButton } from "@/components/confirm-button"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type BrowserAgent } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"
import { Bdi, isolate } from "@/components/bidi"

/** One step of the setup flow: a numbered marker, its state, and what to do. */
export function FlowStep({
  n,
  state,
  title,
  data,
  children,
}: {
  n: number
  state: "done" | "current" | "waiting"
  title: string
  data: string
  children: React.ReactNode
}) {
  const { t } = useTranslations()
  return (
    <li className="flex gap-4 px-6 py-5" data-flow-step={data} data-flow-state={state}>
      <span
        className={`flex size-8 shrink-0 items-center justify-center rounded-full border text-sm tabular-nums ${
          state === "done"
            ? "border-primary bg-primary text-primary-foreground"
            : state === "current"
              ? "border-primary text-primary"
              : "border-line text-grid-muted"
        }`}
        aria-hidden
      >
        {state === "done" ? <CheckIcon className="size-4" /> : n}
      </span>
      <div className="min-w-0 flex-1 space-y-3">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          {title}
          <Badge
            variant={state === "done" ? "secondary" : state === "current" ? "default" : "outline"}
          >
            {t(`automation.flow.state.${state}`)}
          </Badge>
        </p>
        {children}
      </div>
    </li>
  )
}

/** Setup is done: one row that says so, with the way back in. */
export function BrowsersStatus({
  browsers,
  onManage,
  installHref,
}: {
  browsers: BrowserAgent[]
  onManage: () => void
  installHref: string
}) {
  const { t } = useTranslations()
  const online = browsers.filter((b) => b.status === "online").length
  const outdated = browsers.filter((b) => b.update_available).length
  const hosts = [...new Set(browsers.flatMap((b) => b.allowed_hosts))]
  const waiting = [
    ...new Set(browsers.filter((b) => b.status === "online").flatMap((b) => b.pending_hosts ?? [])),
  ]
  return (
    <section
      className="flex flex-wrap items-center gap-x-4 gap-y-2 border-y border-line bg-grid-card px-6 py-4"
      data-browser-status
    >
      <span
        className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground"
        aria-hidden
      >
        <CheckIcon className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
          {t("automation.flow.statusRow", { n: browsers.length, online })}
          {outdated > 0 ? (
            <Link href={installHref} data-update-available>
              <Badge variant="secondary">{t("automation.flow.needUpdate", { n: outdated })}</Badge>
            </Link>
          ) : null}
        </p>
        <p className="flex items-start gap-1 text-xs text-grid-muted">
          <ShieldCheckIcon className="mt-0.5 size-3 shrink-0" />
          <span className="min-w-0 break-words">
            {t("automation.mayVisit", { hosts: hosts.join(", ") })}
          </span>
        </p>
        {/* A site added here that a browser has not given its OK to yet
            (MH-483): said in the folded row too, or nobody opens "Manage". */}
        {waiting.length > 0 ? (
          <p className="text-xs text-foreground" data-status-pending={waiting.join(",")}>
            {t("automation.access.pending", { hosts: waiting.join(", ") })}
          </p>
        ) : null}
      </div>
      <div className="flex items-center gap-2">
        <Button
          variant="ghost"
          size="sm"
          nativeButton={false}
          render={<Link href={installHref} />}
          data-install-link
        >
          <DownloadIcon />
          {t("automation.ext.link")}
        </Button>
        <Button variant="outline" size="sm" onClick={onManage} data-manage-browsers>
          <SettingsIcon />
          {t("automation.flow.manage")}
        </Button>
      </div>
    </section>
  )
}

export function BrowserRow({
  browser,
  canManage,
  onChange,
}: {
  browser: BrowserAgent
  canManage: boolean
  onChange: () => void
}) {
  const { t, formatDate, locale } = useTranslations()
  const ws = useWorkspace()
  // A browser that may go nowhere opens with its allow-list ready to fill in:
  // that is the setup step it is waiting on.
  const [editing, setEditing] = useState(
    canManage && !browser.revoked_at && browser.allowed_hosts.length === 0,
  )
  const [hosts, setHosts] = useState(browser.allowed_hosts.join(", "))
  const [renaming, setRenaming] = useState(false)
  const [name, setName] = useState(browser.name)
  const [busy, setBusy] = useState(false)
  const revoked = !!browser.revoked_at

  async function saveName(e: FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    setBusy(true)
    try {
      await api(`/api/w/browsers/${encodeURIComponent(browser.id)}`, {
        method: "PATCH",
        json: { name: name.trim() },
        workspaceId: ws.id,
      })
      toast.success(t("automation.renamed"))
      setRenaming(false)
      onChange()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function saveHosts(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api(`/api/w/browsers/${encodeURIComponent(browser.id)}`, {
        method: "PATCH",
        json: {
          allowed_hosts: hosts
            .split(",")
            .map((h) => h.trim())
            .filter(Boolean),
        },
        workspaceId: ws.id,
      })
      toast.success(t("automation.hostsSaved"))
      setEditing(false)
      onChange()
    } catch (err) {
      toast.error(err instanceof ApiError ? err.message : t("common.networkError"))
    } finally {
      setBusy(false)
    }
  }

  async function revoke() {
    await api(`/api/w/browsers/${encodeURIComponent(browser.id)}`, {
      method: "DELETE",
      workspaceId: ws.id,
    })
    toast.success(t("automation.revoked"))
    onChange()
  }

  return (
    <li className="px-6 py-4" data-browser={browser.name} data-browser-status={browser.status}>
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <p className="flex flex-wrap items-center gap-2 text-sm font-medium">
            <Bdi>{browser.name}</Bdi>
            {revoked ? (
              <Badge variant="outline">{t("automation.revokedBadge")}</Badge>
            ) : (
              <Badge variant={browser.status === "online" ? "default" : "outline"}>
                {browser.status === "online" ? t("automation.online") : t("automation.offline")}
              </Badge>
            )}
            {browser.update_available ? (
              // The extension reports its version on every connect, so this
              // clears by itself once the browser is updated (MH-399).
              <Link href={`/${locale}/w/${ws.slug}/automation/extension`} data-update-available>
                <Badge variant="secondary">
                  {t("automation.ext.outdated", { version: browser.extension_version || "?" })}
                </Badge>
              </Link>
            ) : null}
          </p>
          <p className="text-xs text-grid-muted">
            {[browser.platform, browser.browser_version, browser.extension_version]
              .filter(Boolean)
              .join(" · ") || t("automation.unknownPlatform")}
            {" · "}
            {t("automation.lastSeen", { date: formatDate(browser.last_seen_at) })}
          </p>
          <p
            className="mt-1 flex items-start gap-1 text-xs"
            data-hosts={browser.allowed_hosts.join(",")}
          >
            <ShieldCheckIcon className="mt-0.5 size-3 shrink-0 text-grid-muted" />
            {browser.allowed_hosts.length > 0 ? (
              <span className="text-grid-muted">
                {t("automation.mayVisit", { hosts: browser.allowed_hosts.join(", ") })}
              </span>
            ) : (
              // Not an error — it is the safe default, and saying so stops
              // someone "fixing" it by pasting in a wildcard.
              <span className="text-grid-muted">{t("automation.noHostsYet")}</span>
            )}
          </p>
          {/* Whether a site added here works at once, or waits for an OK in the
              browser (MH-483). Said only while the browser is connected: it is
              the browser's own report. */}
          {browser.status === "online" && browser.site_access && !revoked ? (
            <p
              className="mt-1 text-xs text-grid-muted"
              data-site-access={browser.site_access}
              data-pending={browser.pending_hosts.join(",")}
            >
              {browser.pending_hosts.length > 0 ? (
                <span className="text-foreground">
                  {t("automation.access.pending", { hosts: browser.pending_hosts.join(", ") })}
                </span>
              ) : browser.site_access === "all" ? (
                t("automation.access.all")
              ) : (
                t("automation.access.sites")
              )}
            </p>
          ) : null}
        </div>
        {canManage && !revoked ? (
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => setRenaming((v) => !v)}
              data-rename-browser
            >
              <PencilIcon />
              {t("automation.rename")}
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setEditing((v) => !v)}>
              {t("automation.editHosts")}
            </Button>
            <ConfirmButton
              label={t("automation.revoke")}
              title={t("automation.revokeTitle")}
              description={t("automation.revokeBody", { name: isolate(browser.name) })}
              confirmLabel={t("automation.revoke")}
              onConfirm={revoke}
            />
          </div>
        ) : null}
      </div>

      {renaming ? (
        <form onSubmit={saveName} className="mt-3" data-form="rename">
          <FieldGroup className="sm:flex-row sm:items-end">
            <Field className="sm:flex-1">
              <FieldLabel htmlFor={`name-${browser.id}`}>{t("automation.browserName")}</FieldLabel>
              <Input
                id={`name-${browser.id}`}
                value={name}
                maxLength={80}
                onChange={(e) => setName(e.target.value)}
              />
            </Field>
            <Button type="submit" disabled={busy || !name.trim()}>
              <CheckIcon />
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </FieldGroup>
        </form>
      ) : null}

      {editing ? (
        <form onSubmit={saveHosts} className="mt-3" data-form="hosts">
          <FieldGroup className="sm:flex-row sm:items-end">
            <Field className="sm:flex-1">
              <FieldLabel htmlFor={`hosts-${browser.id}`}>
                {t("automation.allowedHosts")}
              </FieldLabel>
              <Input
                id={`hosts-${browser.id}`}
                dir="ltr"
                value={hosts}
                onChange={(e) => setHosts(e.target.value)}
                placeholder="example.com, mail.google.com"
              />
              <FieldDescription>{t("automation.allowedHostsHint")}</FieldDescription>
            </Field>
            <Button type="submit" disabled={busy}>
              <CheckIcon />
              {busy ? t("common.working") : t("common.save")}
            </Button>
          </FieldGroup>
        </form>
      ) : null}
    </li>
  )
}
