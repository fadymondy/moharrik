"use client"

/*
The Pick button beside a selector field (MH-400).

It asks the chosen paired browser to open the step's page and let the person
click the element; the browser answers with a selector, which goes into the
field. The server call is two halves — start, then poll — because a pick waits
for a human and a request held open that long dies in proxies
(internal/automation/pick.go).

The polling lives here, not in the popover: opening the page in the browser
takes focus away from this tab, and a popover that closes on the way must not
take the answer with it.
*/

import { useEffect, useRef, useState } from "react"
import { CrosshairIcon, LoaderCircleIcon } from "lucide-react"
import { toast } from "@fadymondy/nasaq/web"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverTitle,
  PopoverTrigger,
} from "@/components/ui/popover"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, type ElementPick } from "@/lib/api"
import { useTranslations } from "@/lib/i18n"

const usable = (url: string) => /^https?:\/\/[^\s]+$/i.test(url.trim()) && !url.includes("{{")

export function PickControl({
  agentId,
  unavailable,
  pageUrl,
  onPicked,
  id,
}: {
  agentId: string
  /** Why picking is not possible right now, said on the button's popover instead of a dead button. */
  unavailable?: string
  pageUrl: string
  onPicked: (selector: string, pick: NonNullable<ElementPick["result"]>) => void
  id: string
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const [open, setOpen] = useState(false)
  const [url, setUrl] = useState(pageUrl)
  const [waiting, setWaiting] = useState(false)
  const [err, setErr] = useState<string | null>(null)
  const stop = useRef(false)

  useEffect(() => {
    if (!open) setUrl(pageUrl)
  }, [pageUrl, open])
  useEffect(() => () => void (stop.current = true), [])

  async function start() {
    setErr(null)
    setWaiting(true)
    stop.current = false
    try {
      const { pick } = await api<{ pick: ElementPick }>(
        `/api/w/browsers/${encodeURIComponent(agentId)}/pick`,
        {
          json: { url: url.trim() },
          workspaceId: ws.id,
        },
      )
      setOpen(false)
      const deadline = Date.now() + 120_000
      while (!stop.current && Date.now() < deadline) {
        await new Promise((r) => setTimeout(r, 1000))
        const { pick: now } = await api<{ pick: ElementPick }>(
          `/api/w/browsers/${encodeURIComponent(agentId)}/pick/${encodeURIComponent(pick.id)}`,
          { workspaceId: ws.id },
        )
        if (now.status === "picked" && now.result) {
          onPicked(now.result.selector, now.result)
          toast.success(
            now.result.fragile
              ? t("automation.pick.pickedFragile", { selector: now.result.selector })
              : t("automation.pick.picked", { selector: now.result.selector }),
          )
          return
        }
        if (now.status === "failed") {
          setErr(now.error || t("automation.pick.failed"))
          setOpen(true)
          return
        }
      }
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : t("common.networkError"))
      setOpen(true)
    } finally {
      setWaiting(false)
    }
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger
        render={
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="shrink-0"
            data-pick={id}
            disabled={waiting}
            aria-label={t("automation.pick.button")}
          >
            {waiting ? <LoaderCircleIcon className="animate-spin" /> : <CrosshairIcon />}
            <span className="hidden sm:inline">
              {waiting ? t("automation.pick.waiting") : t("automation.pick.button")}
            </span>
          </Button>
        }
      />
      <PopoverContent align="end" className="w-80" data-pick-popover={id}>
        <PopoverTitle>{t("automation.pick.title")}</PopoverTitle>
        {unavailable ? (
          <PopoverDescription>{unavailable}</PopoverDescription>
        ) : (
          <>
            <PopoverDescription>{t("automation.pick.body")}</PopoverDescription>
            <div className="space-y-1.5">
              <Label htmlFor={`${id}-url`} className="text-xs">
                {t("automation.pick.page")}
              </Label>
              <Input
                id={`${id}-url`}
                dir="ltr"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder="https://example.com/login"
              />
              {!usable(url) ? (
                <p className="text-xs text-muted-foreground">{t("automation.pick.needUrl")}</p>
              ) : null}
            </div>
            <Button
              type="button"
              size="sm"
              disabled={!usable(url) || waiting}
              onClick={start}
              data-pick-start
            >
              <CrosshairIcon />
              {t("automation.pick.start")}
            </Button>
          </>
        )}
        {err ? (
          <p className="text-xs text-nq-danger-text" role="alert" data-pick-error>
            {err}
          </p>
        ) : null}
      </PopoverContent>
    </Popover>
  )
}
