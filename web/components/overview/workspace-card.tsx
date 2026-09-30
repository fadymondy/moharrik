"use client"

import Link from "next/link"
import { Building2Icon, KeyRoundIcon, UsersIcon } from "lucide-react"

import { Button } from "@fadymondy/nasaq/web"
import { Ltr } from "@/components/copy-field"
import { Panel } from "@/components/overview/overview-cards"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { useTranslations } from "@/lib/i18n"

/** The workspace's own settings, which used to be the whole page. There is no general Settings page to move them to. */
export function WorkspaceCard({
  base,
  timezone,
  currency,
  limited,
}: {
  base: string
  timezone: string
  currency: string
  limited: boolean
}) {
  const { t } = useTranslations()
  const ws = useWorkspace()
  const details = ws.current?.workspace
  return (
    <Panel
      id="workspace"
      title={t("overview.workspace")}
      icon={<Building2Icon />}
      className={limited ? "lg:col-span-2" : undefined}
    >
      {limited ? (
        <p className="border-b border-nq-line px-5 py-3 text-sm text-muted-foreground">
          {t("overview.limitedRole")}
        </p>
      ) : null}
      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 px-5 pt-4 text-sm">
        {[
          [
            t("overview.slug"),
            <Ltr key="s" mono>
              /{ws.slug}
            </Ltr>,
          ],
          [
            t("overview.locale"),
            details?.locale ? (
              <Ltr key="l" mono>
                {details.locale}
              </Ltr>
            ) : (
              "—"
            ),
          ],
          [
            t("overview.timezone"),
            <Ltr key="tz" mono>
              {timezone}
            </Ltr>,
          ],
          [
            t("overview.currency"),
            <Ltr key="c" mono>
              {currency}
            </Ltr>,
          ],
        ].map(([label, value], i) => (
          <div key={i} className="min-w-0">
            <dt className="eyebrow mb-1">{label}</dt>
            <dd className="text-foreground [overflow-wrap:anywhere]">{value}</dd>
          </div>
        ))}
      </dl>
      <div className="flex flex-wrap gap-2 px-5 py-4">
        {ws.can("members:read") ? (
          <Button
            variant="secondary"
            size="sm"
            nativeButton={false}
            render={<Link href={`${base}/members`} />}
          >
            <UsersIcon />
            {t("nav.members")}
          </Button>
        ) : null}
        <Button
          variant="secondary"
          size="sm"
          nativeButton={false}
          render={<Link href={`${base}/settings/mcp`} />}
        >
          <KeyRoundIcon />
          {t("nav.mcpTokens")}
        </Button>
      </div>
    </Panel>
  )
}
