"use client"

import { useState } from "react"
import {
  ActivityIcon,
  GaugeIcon,
  HandshakeIcon,
  LogOutIcon,
  MailIcon,
  MessageSquareIcon,
} from "lucide-react"
import { Badge, Button } from "@fadymondy/nasaq/web"

import { Bdi } from "@/components/bidi"
import {
  AiSummaryPanel,
  AutomationsPanel,
  ConversationsPanel,
  JourneyPanel,
  NextActionsPanel,
  useGenerateReport,
} from "@/components/contacts/c360-sections"
import { useContactRecord } from "@/components/contacts/contact-record"
import { UnsubscribeDialog, type UnsubscribeTarget } from "@/components/mail/subscriber-admin"
import { RecordBody, RecordCard, RecordStat } from "@/components/record/record"
import { ErrorState, LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { contactIdentity, type SubscriptionStatus } from "@/lib/api"
import { useContactTimeline } from "@/lib/contact-timeline"
import { useTranslations } from "@/lib/i18n"
import { cn } from "@/lib/utils"

const statusVariant: Record<SubscriptionStatus, "brand" | "neutral" | "outline" | "danger"> = {
  subscribed: "brand",
  pending: "neutral",
  unsubscribed: "outline",
  bounced: "danger",
}

/** MH-1157: the contact at a glance: the numbers, what to do next, where they are in the journey. */
export default function ContactOverviewPage() {
  const { t, formatNumber } = useTranslations()
  const ws = useWorkspace()
  const { data, contact, canWrite, rec, base, refresh, reload, reportsChanged } = useContactRecord()
  const timeline = useContactTimeline(ws.id, contact.id)
  const { reports, generate, generating } = useGenerateReport(contact.id, reportsChanged)
  const [leaving, setLeaving] = useState<UnsubscribeTarget | null>(null)
  const canRead = ws.can("contacts:read")
  const conversations = timeline.data?.overview?.conversations ?? []
  const subs = contact.subscriptions ?? []

  return (
    <RecordBody className="[&_[data-card]]:rounded-xl">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4" data-record-stats>
        <RecordStat
          icon={<GaugeIcon />}
          label={t("icp.fit")}
          value={contact.score === null ? "—" : formatNumber(contact.score)}
          hint={contact.score === null ? t("icp.unscored") : undefined}
          href={`${rec}/profile`}
        />
        <RecordStat
          icon={<HandshakeIcon />}
          label={t("record.tab.deals")}
          value={formatNumber(data.deals_total)}
          href={`${rec}/deals`}
        />
        <RecordStat
          icon={<ActivityIcon />}
          label={t("record.tab.activity")}
          value={formatNumber(data.activities_total)}
          href={`${rec}/activity`}
        />
        <RecordStat
          icon={<MessageSquareIcon />}
          label={t("record.conversations")}
          value={timeline.data ? formatNumber(conversations.length) : "—"}
          href={`${rec}/activity`}
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        {canRead ? (
          <div className="min-w-0 lg:col-span-2">
            <AiSummaryPanel generate={generate} generating={generating} reports={reports} />
          </div>
        ) : null}
        <div className={cn("min-w-0", canRead ? "" : "lg:col-span-3")}>
          <NextActionsPanel
            contact={contact}
            canWrite={canWrite}
            rec={rec}
            generate={generate}
            generating={generating}
            onChanged={() => {
              refresh()
              void timeline.mutate()
            }}
          />
        </div>
      </div>

      <JourneyPanel contact={contact} />

      <div className="grid gap-4 lg:grid-cols-3">
        {timeline.error ? (
          <div className="lg:col-span-2">
            <ErrorState error={timeline.error} />
          </div>
        ) : !timeline.data ? (
          <div className="lg:col-span-2">
            <LoadingRows rows={3} />
          </div>
        ) : (
          <div className="flex min-w-0 flex-col gap-4 lg:col-span-2">
            {timeline.data.overview?.conversations ? (
              <ConversationsPanel rows={timeline.data.overview.conversations} base={base} />
            ) : null}
            {timeline.data.overview?.automations ? (
              <AutomationsPanel rows={timeline.data.overview.automations} base={base} />
            ) : null}
          </div>
        )}

        <RecordCard
          title={
            <span className="inline-flex items-center gap-2">
              <MailIcon className="size-4 text-muted-foreground" />
              {t("record.subscriptions")}
            </span>
          }
          bodyClassName="px-4 pb-4"
          data-contact-subscriptions
        >
          {subs.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("record.noSubscriptions")}</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {subs.map((s) => (
                <li key={s.id} className="flex flex-wrap items-center gap-2">
                  <Badge variant={statusVariant[s.status]} data-sub={`${s.list_name}:${s.status}`}>
                    <Bdi>{s.list_name}</Bdi> · {t(`sub.${s.status}`)}
                  </Badge>
                  {canWrite && s.status !== "unsubscribed" ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="ms-auto"
                      data-unsubscribe-from={s.list_name}
                      onClick={() =>
                        setLeaving({
                          contactId: contact.id,
                          who: contact.name || contactIdentity(contact),
                          listId: s.list_id,
                          listName: s.list_name,
                        })
                      }
                    >
                      <LogOutIcon />
                      {t("contacts.unsubscribe")}
                    </Button>
                  ) : null}
                </li>
              ))}
            </ul>
          )}
        </RecordCard>
      </div>
      <UnsubscribeDialog target={leaving} onClose={() => setLeaving(null)} onDone={reload} />
    </RecordBody>
  )
}
