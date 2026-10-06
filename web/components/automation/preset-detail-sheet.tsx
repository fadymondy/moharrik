"use client"

/*
A preset's detail sheet (MH-527): the plain description first, the technical
notes collapsed below it, then Add / Add & run. Replaces the old page's
always-expanded row — the gallery's cards stay short, and this is where the
detail goes.

Add opens the existing add dialog unchanged (browser + host-allow + WhatsApp
number + a write's confirmation). Add & run is new: one click that adds the
preset and starts it immediately, offered only when that is actually safe to
do blind — a browser is available, every required input already has a
default, and the preset does not publish/send/submit (a write always asks
first, from the workflow row's own confirmation, same as anywhere else).
*/

import Link from "next/link"
import { useState } from "react"
import {
  CheckIcon,
  ChevronDownIcon,
  ExternalLinkIcon,
  PlayIcon,
  PlusIcon,
  ShieldCheckIcon,
} from "lucide-react"
import {
  Badge,
  Button,
  Collapsible,
  CollapsiblePanel,
  CollapsibleTrigger,
  Sheet,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  toast,
} from "@fadymondy/nasaq/web"

import { AddPresetDialog } from "@/components/automation/preset-add-dialog"
import { BrandIcon } from "@/components/automation/brand-icon"
import { plainOf } from "@/components/automation/preset-card"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type AutomationPreset, type BrowserAgent } from "@/lib/api"
import type { Brand } from "@/lib/brands"
import { useTranslations } from "@/lib/i18n"

export function PresetDetailSheet({
  preset: p,
  brand,
  browsers,
  canRun,
  canManage,
  profile,
  onClose,
  onChanged,
}: {
  preset: AutomationPreset
  brand: Brand
  browsers: BrowserAgent[]
  canRun: boolean
  canManage: boolean
  profile: Record<string, string>
  onClose: () => void
  onChanged: () => void
}) {
  const { t, locale } = useTranslations()
  const ws = useWorkspace()
  const plain = plainOf(p, locale)
  const [adding, setAdding] = useState(false)
  const [runningAfterAdd, setRunningAfterAdd] = useState(false)

  const active = browsers.filter((b) => !b.revoked_at)
  const missingRequired = p.inputs.some((i) => i.required && !i.default)
  // A write always asks first, from the workflow row's own confirmation — never blind, one-click here.
  const canAddAndRun =
    canRun && !p.workflow_id && !p.writes && active.length > 0 && !missingRequired

  async function addAndRun() {
    setRunningAfterAdd(true)
    try {
      const added = await api<{ workflow: { id: string; name: string } }>(
        `/api/w/automation/presets/${encodeURIComponent(p.slug)}/add`,
        {
          json: { locale: locale === "ar" ? "ar" : "en" },
          workspaceId: ws.id,
        },
      )
      const values = Object.fromEntries(
        p.inputs.filter((i) => i.default).map((i) => [i.name, i.default]),
      )
      await api(`/api/w/workflows/${encodeURIComponent(added.workflow.id)}/run`, {
        json: { agent_id: active[0].id, values },
        workspaceId: ws.id,
      })
      toast.success(t("presets.addedAndRunning", { name: added.workflow.name }))
      onChanged()
      onClose()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    } finally {
      setRunningAfterAdd(false)
    }
  }

  return (
    <>
      <Sheet open onOpenChange={(v) => (v ? null : onClose())}>
        <SheetContent side="end" className="w-full sm:max-w-md" data-preset-sheet={p.slug}>
          <SheetHeader className="flex-row items-start gap-3 space-y-0">
            <BrandIcon brand={brand} size={44} />
            <div className="min-w-0 flex-1">
              <SheetTitle>{plain.name}</SheetTitle>
              <p className="text-xs text-muted-foreground">{brand.name}</p>
            </div>
          </SheetHeader>
          <div className="flex-1 space-y-4 overflow-y-auto px-4">
            {plain.summary ? <p className="text-sm text-nq-fg-body">{plain.summary}</p> : null}
            <div className="flex flex-wrap items-center gap-1.5">
              {p.writes ? <Badge variant="neutral">{t("presets.publishes")}</Badge> : null}
              {p.dry_run_of ? <Badge variant="outline">{t("presets.rehearsal")}</Badge> : null}
              {p.workflow_id ? (
                <Badge variant="outline" data-preset-added>
                  <CheckIcon />
                  {t("presets.added")}
                </Badge>
              ) : null}
            </div>
            <p
              className="flex items-start gap-1 text-xs text-muted-foreground"
              data-preset-hosts={p.hosts.join(",")}
            >
              <ShieldCheckIcon className="mt-0.5 size-3 shrink-0" />
              <span className="min-w-0 break-words">
                {p.hosts.length
                  ? t("presets.goesTo", { hosts: p.hosts.join(", ") })
                  : t("presets.anySite")}
              </span>
            </p>
            <Collapsible data-preset-technical>
              <CollapsibleTrigger
                className="group flex items-center gap-1 text-xs text-muted-foreground underline-offset-2 hover:underline"
                data-technical-toggle
              >
                <ChevronDownIcon className="size-3 transition-transform group-aria-expanded:rotate-180" />
                {t("presets.technical")}
              </CollapsibleTrigger>
              <CollapsiblePanel className="mt-2 space-y-1 border-s-2 border-nq-line ps-3" dir="ltr">
                <p className="text-xs font-medium text-foreground">
                  {p.name}{" "}
                  <span className="font-mono font-normal text-muted-foreground">({p.slug})</span>
                </p>
                <p className="text-xs text-muted-foreground">{p.description}</p>
                {p.inputs.length > 0 ? (
                  <p className="text-xs text-muted-foreground" data-preset-inputs>
                    {t("presets.asksFor", {
                      names: p.inputs.map((i) => (i.required ? `${i.name}*` : i.name)).join(", "),
                    })}
                  </p>
                ) : null}
              </CollapsiblePanel>
            </Collapsible>
          </div>
          <SheetFooter className="flex-row justify-end gap-2 border-t border-nq-line">
            {p.workflow_id ? (
              <Button
                nativeButton={false}
                render={
                  <Link href={`/${locale}/w/${ws.slug}/automation/workflows/${p.workflow_id}`} />
                }
                data-open-added
              >
                <ExternalLinkIcon />
                {t("presets.open")}
              </Button>
            ) : (
              <>
                {canAddAndRun ? (
                  <Button
                    variant="secondary"
                    onClick={addAndRun}
                    disabled={runningAfterAdd}
                    data-add-and-run={p.slug}
                  >
                    <PlayIcon />
                    {runningAfterAdd ? t("common.working") : t("presets.addAndRun")}
                  </Button>
                ) : null}
                {canRun ? (
                  <Button onClick={() => setAdding(true)} data-add-preset={p.slug}>
                    <PlusIcon />
                    {t("presets.add")}
                  </Button>
                ) : null}
              </>
            )}
          </SheetFooter>
        </SheetContent>
      </Sheet>
      {adding ? (
        <AddPresetDialog
          preset={p}
          browsers={active}
          canManage={canManage}
          profile={profile}
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false)
            onChanged()
            onClose()
          }}
        />
      ) : null}
    </>
  )
}
