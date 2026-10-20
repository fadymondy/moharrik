"use client"

/*
The inbox customer-intelligence panel (MH-1063). It answers "who is this and where do we stand" beside
the thread, using only what the backend already holds, in one contact fetch (GET /api/w/contacts/{id}):
the linked contact and company, the ideal-customer FIT score with its reasons (there is no intent score,
so none is shown), the lifecycle stage as a journey, the open deal on the pipeline, and the latest CRM
activities. A section the role cannot read (contacts:read) is left out, never zeroed. Every action is
one that already exists in the thread — link or create a contact, open a deal, assign, snooze — and
goes through the same dialogs and endpoints.
*/

import Link from "next/link"
import { useState, type ReactNode } from "react"
import {
  BriefcaseIcon,
  Building2Icon,
  ExternalLinkIcon,
  GaugeIcon,
  LinkIcon,
  MailIcon,
  PhoneIcon,
  RouteIcon,
  UserIcon,
  UserPlusIcon,
} from "lucide-react"
import {
  Badge,
  Button,
  Stepper,
  StepperItem,
  Timeline as NqTimeline,
  TimelineItem,
  toast,
} from "@fadymondy/nasaq/web"

import { PersonAvatar } from "@/components/avatar-with-fallback"
import { Bdi } from "@/components/bidi"
import { Ltr } from "@/components/copy-field"
import { ActivityIcon, formatMoney, PickSelect, useMemberNames } from "@/components/crm/shared"
import { ScoreMeter, ScoreReasons } from "@/components/crm/icp"
import {
  CreateContactDialog,
  LinkContactDialog,
  OpenDealDialog,
} from "@/components/inbox/thread-crm"
import { SnoozeControl } from "@/components/inbox/snooze-control"
import { LoadingRows } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import {
  api,
  ApiError,
  LIFECYCLE_STAGES,
  type Conversation,
  type Deal,
  type Stage,
} from "@/lib/api"
import { channelIcon, channelLabel } from "@/lib/inbox-channels"
import { useTranslations } from "@/lib/i18n"
import { useContact, useConversation, usePipelineStages } from "@/lib/queries"
import { cn } from "@/lib/utils"

const RECENT_ACTIVITIES = 5
const TOP_REASONS = 3

function Section({
  id,
  title,
  icon,
  children,
}: {
  id: string
  title: string
  icon: ReactNode
  children: ReactNode
}) {
  return (
    <section
      className="border-b border-nq-line px-4 py-4 last:border-b-0"
      data-intel-section={id}
      aria-label={title}
    >
      <h3 className="mb-3 flex items-center gap-2 text-xs font-medium tracking-wide text-muted-foreground uppercase [&>svg]:size-3.5">
        {icon}
        {title}
      </h3>
      {children}
    </section>
  )
}

/** An empty section says so and points at the action that fills it. */
function SectionEmpty({ body, children }: { body: string; children?: ReactNode }) {
  return (
    <div className="flex flex-col items-start gap-2" data-intel-empty>
      <p className="text-sm text-muted-foreground">{body}</p>
      {children ? <div className="flex flex-wrap gap-2">{children}</div> : null}
    </div>
  )
}

export function CustomerPanel({
  id,
  onChanged,
}: {
  /** The open conversation. */
  id: string
  onChanged: () => void
}) {
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const detail = useConversation(ws.id, id)
  const members = useMemberNames(ws.id)
  const canRead = ws.can("contacts:read")
  const canWrite = ws.can("inbox:write") && ws.can("contacts:write")
  const canTriage = ws.can("inbox:write")
  const c = detail.data?.conversation
  const contactId = c?.contact_id || undefined
  const contactQ = useContact(ws.id, canRead ? contactId : undefined)
  const dealId = c?.deal?.id
  const stagesQ = usePipelineStages(ws.id, canRead && (!!dealId || !!contactQ.data))
  const [dialog, setDialog] = useState<"link" | "create" | "deal" | null>(null)
  const base = `/${locale}/w/${ws.slug}`

  if (detail.error) {
    return (
      <p className="px-4 py-6 text-sm text-muted-foreground" data-intel-error>
        {detail.error instanceof ApiError ? detail.error.message : t("common.networkError")}
      </p>
    )
  }
  if (!c || !detail.data) return <LoadingRows rows={4} layout="detail" />
  const conv: Conversation = c
  const visitor = detail.data.visitor

  function refresh() {
    void detail.mutate()
    void contactQ.mutate()
    onChanged()
  }

  async function assign(userId: string) {
    try {
      await api(`/api/w/conversations/${encodeURIComponent(conv.id)}`, {
        method: "PATCH",
        json: { assignee_user_id: userId },
        workspaceId: ws.id,
      })
      toast.success(t("inbox.assigned"))
      refresh()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : t("common.networkError"))
    }
  }

  const contact = contactQ.data?.contact
  const who =
    conv.contact?.name ||
    conv.contact?.email ||
    conv.customer_name ||
    conv.customer_email ||
    conv.customer_phone ||
    t("inbox.anonymous")
  const ChannelIcon = channelIcon(conv.channel)

  // The deal this conversation became; else the contact's most recent open deal.
  const deals: Deal[] = contactQ.data?.deals ?? []
  const deal: Deal | undefined =
    deals.find((d) => d.id === dealId) ?? deals.find((d) => d.stage_kind === "open")
  const otherOpen = deals.filter((d) => d.stage_kind === "open" && d.id !== deal?.id).length
  const activities = (contactQ.data?.activities ?? []).slice(0, RECENT_ACTIVITIES)

  return (
    <div className="flex min-w-0 flex-col" data-customer-panel={conv.id}>
      {/* who */}
      <div className="flex items-start gap-3 border-b border-nq-line px-4 py-4">
        <PersonAvatar
          id={conv.contact_id || conv.id}
          name={who}
          photoUrl={contact?.avatar_url || conv.avatar_url}
          size="lg"
          className="shrink-0"
        />
        <div className="min-w-0 flex-1 space-y-1">
          <p className="truncate text-sm font-medium text-foreground" data-intel-name>
            <Bdi>{who}</Bdi>
          </p>
          <Badge variant="info" className="gap-1 [&>svg]:size-3">
            <ChannelIcon />
            {channelLabel(conv.channel, t)}
          </Badge>
          {conv.whatsapp_number ? (
            <Badge
              variant="outline"
              className="ms-1"
              title={t("inbox.whatsappNumber.on")}
              data-thread-whatsapp-number={conv.whatsapp_number.id}
            >
              {conv.whatsapp_number.label ? <Bdi>{conv.whatsapp_number.label}</Bdi> : null}
              {conv.whatsapp_number.phone ? <Ltr>+{conv.whatsapp_number.phone}</Ltr> : null}
            </Badge>
          ) : null}
          {conv.customer_email ? (
            <p className="flex items-center gap-1.5 truncate text-xs text-muted-foreground [&>svg]:size-3.5 [&>svg]:shrink-0">
              <MailIcon />
              <Ltr>{conv.customer_email}</Ltr>
            </p>
          ) : null}
          {conv.customer_phone ? (
            <p className="flex items-center gap-1.5 truncate text-xs text-muted-foreground [&>svg]:size-3.5 [&>svg]:shrink-0">
              <PhoneIcon />
              <Ltr>{conv.customer_phone}</Ltr>
            </p>
          ) : null}
        </div>
      </div>

      {/* contact + company */}
      <Section id="contact" title={t("inbox.intel.contact")} icon={<UserIcon />}>
        {conv.contact ? (
          <div className="space-y-2 text-sm" data-intel-contact={conv.contact.id}>
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <Link
                href={`${base}/contacts/${conv.contact.id}`}
                className="truncate font-medium text-foreground underline-offset-4 hover:underline"
              >
                <Bdi>{conv.contact.name || conv.contact.email}</Bdi>
              </Link>
              {conv.contact_link === "auto" ? (
                <Badge variant="neutral">{t("inbox.crm.auto")}</Badge>
              ) : null}
            </div>
            {contact?.title ? (
              <p className="text-xs text-muted-foreground">
                <Bdi>{contact.title}</Bdi>
              </p>
            ) : null}
            {conv.contact.company ? (
              <p className="flex items-center gap-1.5 text-muted-foreground [&>svg]:size-3.5">
                <Building2Icon />
                <Link
                  href={`${base}/companies/${conv.contact.company.id}`}
                  className="truncate text-foreground underline-offset-4 hover:underline"
                  data-intel-company
                >
                  <Bdi>{conv.contact.company.name}</Bdi>
                </Link>
              </p>
            ) : (
              <p className="text-xs text-muted-foreground">{t("inbox.intel.noCompany")}</p>
            )}
            {contact?.tags?.length ? (
              <div className="flex flex-wrap gap-1">
                {contact.tags.map((tag) => (
                  <Badge key={tag} variant="outline">
                    <Bdi>{tag}</Bdi>
                  </Badge>
                ))}
              </div>
            ) : null}
            <div className="flex flex-wrap gap-2 pt-1">
              <Button
                variant="secondary"
                size="sm"
                nativeButton={false}
                render={<Link href={`${base}/contacts/${conv.contact.id}`} />}
                data-intel-action="profile"
              >
                <ExternalLinkIcon />
                {t("inbox.intel.viewProfile")}
              </Button>
              {canWrite ? (
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => setDialog("link")}
                  data-intel-action="change"
                >
                  <LinkIcon />
                  {t("inbox.crm.change")}
                </Button>
              ) : null}
            </div>
          </div>
        ) : (
          <SectionEmpty body={t("inbox.intel.noContact")}>
            {canWrite ? (
              <>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => setDialog("link")}
                  data-intel-action="link"
                >
                  <LinkIcon />
                  {t("inbox.crm.link")}
                </Button>
                <Button
                  variant="secondary"
                  size="sm"
                  onClick={() => setDialog("create")}
                  data-intel-action="create"
                >
                  <UserPlusIcon />
                  {t("inbox.crm.create")}
                </Button>
              </>
            ) : null}
          </SectionEmpty>
        )}
      </Section>

      {/* everything below is the contact's, and needs contacts:read */}
      {canRead && contactId ? (
        contactQ.error ? (
          <p className="px-4 py-4 text-sm text-muted-foreground" data-intel-error>
            {contactQ.error instanceof ApiError ? contactQ.error.message : t("common.networkError")}
          </p>
        ) : !contact ? (
          <LoadingRows rows={3} />
        ) : (
          <>
            <Section id="fit" title={t("inbox.intel.fit")} icon={<GaugeIcon />}>
              {contact.score === null ? (
                <SectionEmpty body={t("inbox.intel.unscored")}>
                  <Button
                    variant="secondary"
                    size="sm"
                    nativeButton={false}
                    render={<Link href={`${base}/settings/icp`} />}
                    data-intel-action="icp"
                  >
                    {t("icp.editProfile")}
                  </Button>
                </SectionEmpty>
              ) : (
                <div className="space-y-2" data-intel-fit={contact.score}>
                  <div className="flex items-center justify-between gap-2">
                    <ScoreMeter score={contact.score} />
                    <span className="text-xs text-muted-foreground">{t("inbox.intel.fitOf")}</span>
                  </div>
                  <ScoreReasons
                    reasons={[...(contact.score_reasons ?? [])]
                      .sort((a, b) => Math.abs(b.contribution) - Math.abs(a.contribution))
                      .slice(0, TOP_REASONS)}
                  />
                  {(contact.score_reasons ?? []).length > TOP_REASONS ? (
                    <Link
                      href={`${base}/contacts/${contact.id}`}
                      className="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
                    >
                      {t("inbox.intel.allReasons", {
                        n: (contact.score_reasons ?? []).length,
                      })}
                    </Link>
                  ) : null}
                </div>
              )}
            </Section>

            <Section id="lifecycle" title={t("inbox.intel.lifecycle")} icon={<RouteIcon />}>
              {contact.lifecycle ? (
                <div className="space-y-2" data-intel-lifecycle={contact.lifecycle}>
                  <div className="overflow-x-auto">
                    <Stepper
                      current={LIFECYCLE_STAGES.indexOf(contact.lifecycle)}
                      orientation="vertical"
                      className="pointer-events-none"
                    >
                      {LIFECYCLE_STAGES.map((st) => (
                        <StepperItem key={st} title={t(`lifecycle.${st}`)} />
                      ))}
                    </Stepper>
                  </div>
                  {contact.lifecycle_at ? (
                    <p className="text-xs text-muted-foreground">
                      {t("inbox.intel.since", { date: formatDate(contact.lifecycle_at) })}
                      {contact.lifecycle_history?.[0]
                        ? ` · ${
                            contact.lifecycle_history[0].source === "manual"
                              ? t("lifecycle.source.manualBy", {
                                  who: members.name(contact.lifecycle_history[0].actor_user_id),
                                })
                              : t(`lifecycle.source.${contact.lifecycle_history[0].source}`)
                          }`
                        : ""}
                    </p>
                  ) : null}
                </div>
              ) : (
                <SectionEmpty body={t("inbox.intel.noLifecycle")}>
                  <Button
                    variant="secondary"
                    size="sm"
                    nativeButton={false}
                    render={<Link href={`${base}/contacts/${contact.id}`} />}
                    data-intel-action="set-lifecycle"
                  >
                    {t("inbox.intel.setLifecycle")}
                  </Button>
                </SectionEmpty>
              )}
            </Section>
          </>
        )
      ) : null}

      {/* the deal: the contact's page carries it; the conversation alone names it */}
      <Section id="deal" title={t("inbox.intel.deal")} icon={<BriefcaseIcon />}>
        {deal ? (
          <div className="space-y-3" data-intel-deal={deal.id}>
            <div className="flex flex-wrap items-start justify-between gap-x-3 gap-y-1">
              <Link
                href={`${base}/deals/${deal.id}`}
                className="min-w-0 truncate text-sm font-medium text-foreground underline-offset-4 hover:underline"
              >
                <Bdi>{deal.title}</Bdi>
              </Link>
              {deal.value_minor > 0 ? (
                <span className="text-sm font-medium text-foreground tabular-nums">
                  {formatMoney(deal.value_minor, deal.currency, locale)}
                </span>
              ) : null}
            </div>
            <DealStages deal={deal} stages={stagesQ.data?.stages ?? []} />
            {otherOpen > 0 ? (
              <p className="text-xs text-muted-foreground">
                {t("inbox.intel.otherDeals", { n: otherOpen })}
              </p>
            ) : null}
          </div>
        ) : conv.deal ? (
          <div className="text-sm" data-intel-deal={conv.deal.id}>
            <Link
              href={`${base}/deals/${conv.deal.id}`}
              className="font-medium text-foreground underline-offset-4 hover:underline"
            >
              <Bdi>{conv.deal.title}</Bdi>
            </Link>
          </div>
        ) : (
          <SectionEmpty body={t("inbox.intel.noDeal")}>
            {canWrite ? (
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setDialog("deal")}
                disabled={conv.message_count === 0}
                data-intel-action="deal"
              >
                <BriefcaseIcon />
                {t("inbox.crm.openDeal")}
              </Button>
            ) : null}
          </SectionEmpty>
        )}
      </Section>

      {/* recent activity */}
      {canRead && contactId && contact ? (
        <Section id="activity" title={t("inbox.intel.activity")} icon={<RouteIcon />}>
          {activities.length === 0 ? (
            <SectionEmpty body={t("inbox.intel.noActivity")}>
              <Button
                variant="secondary"
                size="sm"
                nativeButton={false}
                render={<Link href={`${base}/contacts/${contact.id}`} />}
                data-intel-action="log-activity"
              >
                {t("inbox.intel.logActivity")}
              </Button>
            </SectionEmpty>
          ) : (
            <div data-intel-activities>
              <NqTimeline>
                {activities.map((a) => (
                  <TimelineItem
                    key={a.id}
                    icon={<ActivityIcon kind={a.kind} />}
                    title={t(`activity.${a.kind}`)}
                    description={a.body ? <Bdi className="line-clamp-2">{a.body}</Bdi> : undefined}
                    time={a.created_at}
                    data-intel-activity={a.id}
                  />
                ))}
              </NqTimeline>
              <Link
                href={`${base}/contacts/${contact.id}`}
                className="mt-2 inline-block text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
              >
                {t("inbox.intel.allActivity", { n: contactQ.data?.activities_total ?? 0 })}
              </Link>
            </div>
          )}
        </Section>
      ) : null}

      {/* triage: assign + snooze, the same controls the thread header has */}
      {canTriage ? (
        <Section id="actions" title={t("inbox.intel.actions")} icon={<UserIcon />}>
          <div className="flex flex-col gap-2" data-intel-actions>
            <PickSelect
              id={`intel-assignee-${conv.id}`}
              value={conv.assignee_user_id ?? ""}
              onChange={(v) => void assign(v)}
              options={members.options}
              noneLabel={t("inbox.unassigned")}
            />
            <div>
              <SnoozeControl c={conv} onChanged={refresh} />
            </div>
          </div>
        </Section>
      ) : null}

      <LinkContactDialog
        open={dialog === "link"}
        onOpenChange={(v) => setDialog(v ? "link" : null)}
        c={conv}
        onDone={refresh}
      />
      <CreateContactDialog
        open={dialog === "create"}
        onOpenChange={(v) => setDialog(v ? "create" : null)}
        c={conv}
        visitor={visitor}
        onDone={refresh}
      />
      <OpenDealDialog
        open={dialog === "deal"}
        onOpenChange={(v) => setDialog(v ? "deal" : null)}
        c={conv}
        onDone={refresh}
      />
    </div>
  )
}

/** The deal's place on the pipeline: the open stages in order, and its own if it was won or lost. */
function DealStages({ deal, stages }: { deal: Deal; stages: Stage[] }) {
  const { t } = useTranslations()
  const ordered = [...stages].sort((a, b) => a.position - b.position)
  const open = ordered.filter((s) => s.kind === "open")
  if (deal.stage_kind !== "open") {
    return (
      <Badge
        variant={deal.stage_kind === "won" ? "success" : "danger"}
        className={cn("w-fit")}
        data-intel-stage={deal.stage_id}
      >
        {deal.stage_name}
      </Badge>
    )
  }
  const idx = open.findIndex((s) => s.id === deal.stage_id)
  if (open.length === 0 || idx < 0) {
    return (
      <Badge variant="neutral" className="w-fit" data-intel-stage={deal.stage_id}>
        {t("inbox.crm.stage")}: {deal.stage_name}
      </Badge>
    )
  }
  return (
    <div className="overflow-x-auto" data-intel-stage={deal.stage_id}>
      <Stepper current={idx} className="pointer-events-none min-w-[18rem]">
        {open.map((s) => (
          <StepperItem key={s.id} title={s.name} />
        ))}
      </Stepper>
    </div>
  )
}
