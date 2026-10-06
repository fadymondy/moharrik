"use client"

// MH-1157: the contact's record frame. The layout loads the contact once and shares it with every
// tab; the header (cover, avatar, name, actions, key facts, tabs) stays put while the tabs change.
import Link from "next/link"
import { useParams, usePathname } from "next/navigation"
import { createContext, useContext, useState, type ReactNode } from "react"
import { useSWRConfig } from "swr"
import {
  ActivityIcon,
  BrainIcon,
  Building2Icon,
  CalendarIcon,
  HandshakeIcon,
  LayoutGridIcon,
  Link2Icon,
  ListPlusIcon,
  MessageSquareIcon,
  PencilLineIcon,
  SparklesIcon,
  StickyNoteIcon,
  UserRoundIcon,
} from "lucide-react"
import { Badge, Button } from "@fadymondy/nasaq/web"

import { Bdi, isolate } from "@/components/bidi"
import { ConfirmButton } from "@/components/confirm-button"
import { Ltr } from "@/components/copy-field"
import { LifecycleBadge } from "@/components/crm/icp"
import { MergeContactAction } from "@/components/crm/contact-merge"
import { NotFoundState } from "@/components/not-found-state"
import {
  RecordAvatar,
  RecordChip,
  RecordHeader,
  RecordSkeleton,
  type RecordTab,
} from "@/components/record/record"
import { ErrorState } from "@/components/states"
import { useWorkspace } from "@/components/workspace/workspace-context"
import { api, ApiError, contactIdentity, type ContactDetail } from "@/lib/api"
import { useContactTimeline, type ContactConversationRow } from "@/lib/contact-timeline"
import { useTranslations } from "@/lib/i18n"
import { inboxHref } from "@/lib/inbox-link"
import { useContact } from "@/lib/queries"
import { useDocumentTitle } from "@/lib/title"
import { channelName } from "./c360-text"

type ContactData = NonNullable<ReturnType<typeof useContact>["data"]>

export type ContactRecordValue = {
  data: ContactData
  contact: ContactDetail
  canWrite: boolean
  /** The workspace root, `/<locale>/w/<slug>`. */
  base: string
  /** This contact's root: the overview tab. */
  rec: string
  /** Re-fetch the contact and its unified timeline. */
  refresh: () => void
  /** Re-fetch the contact only. */
  reload: () => void
  reportsRev: number
  reportsChanged: () => void
}

const Ctx = createContext<ContactRecordValue | null>(null)

export function useContactRecord(): ContactRecordValue {
  const v = useContext(Ctx)
  if (!v) throw new Error("useContactRecord outside ContactRecord")
  return v
}

/** The newest conversation the contact is in: what "Message" opens. */
function latest(rows: ContactConversationRow[] | null | undefined): ContactConversationRow | null {
  let best: ContactConversationRow | null = null
  for (const r of rows ?? []) if (!best || r.last_message_at > best.last_message_at) best = r
  return best
}

export function ContactRecord({ children }: { children: ReactNode }) {
  const { id } = useParams<{ id: string }>()
  const pathname = usePathname()
  const { t, locale, formatDate } = useTranslations()
  const ws = useWorkspace()
  const canWrite = ws.can("contacts:write")
  const detail = useContact(ws.id, id)
  const timeline = useContactTimeline(ws.id, id)
  const { mutate: mutateAny } = useSWRConfig()
  const [reportsRev, setReportsRev] = useState(0)
  const base = `/${locale}/w/${ws.slug}`
  const rec = `${base}/contacts/${id}`
  const contact = detail.data?.contact

  useDocumentTitle(
    contact
      ? `${contact.name || contactIdentity(contact)} · ${ws.summary.name}`
      : t("contacts.detailTitle"),
  )

  if (detail.error) {
    const notFound = detail.error instanceof ApiError && detail.error.status === 404
    if (notFound)
      return (
        <NotFoundState
          title={t("contacts.notFound")}
          body={t("contacts.notFoundBody")}
          backHref={`${base}/contacts`}
          backLabel={t("contacts.backToList")}
        />
      )
    return <ErrorState error={detail.error} />
  }
  if (!detail.data || !contact) return <RecordSkeleton />

  const data: ContactData = detail.data
  const who = contact.name || contactIdentity(contact)
  const refresh = () => {
    void detail.mutate()
    void mutateAny(
      (k) => Array.isArray(k) && typeof k[0] === "string" && k[0].endsWith("/timeline"),
    )
  }

  const rest = pathname.split(`/contacts/${id}`)[1]?.split("/")[1] ?? ""
  const active = rest || "overview"
  const conversations = timeline.data?.overview?.conversations ?? []
  const last = latest(conversations)

  const channels = new Set<string>()
  if (contact.email) channels.add("email")
  if (contact.phone) channels.add("phone")
  for (const i of contact.identities ?? []) channels.add(i.channel)
  for (const c of conversations) channels.add(c.channel)

  const tabs: RecordTab[] = [
    { value: "overview", label: t("record.tab.overview"), icon: LayoutGridIcon, href: rec },
    {
      value: "activity",
      label: t("record.tab.activity"),
      icon: ActivityIcon,
      href: `${rec}/activity`,
      count: data.activities_total || undefined,
    },
    {
      value: "deals",
      label: t("record.tab.deals"),
      icon: HandshakeIcon,
      href: `${rec}/deals`,
      count: data.deals_total || undefined,
    },
    {
      value: "profile",
      label: t("record.tab.profile"),
      icon: UserRoundIcon,
      href: `${rec}/profile`,
    },
    ...(canWrite
      ? [
          {
            value: "details",
            label: t("record.tab.details"),
            icon: PencilLineIcon,
            href: `${rec}/details`,
          },
        ]
      : []),
    {
      value: "accounts",
      label: t("contacts.identities"),
      icon: Link2Icon,
      href: `${rec}/accounts`,
      count: contact.identities?.length || undefined,
    },
    {
      value: "fields",
      label: t("contacts.meta"),
      icon: ListPlusIcon,
      href: `${rec}/fields`,
      count: Object.keys(contact.meta ?? {}).length || undefined,
    },
    {
      value: "intelligence",
      label: t("record.tab.intelligence"),
      icon: SparklesIcon,
      href: `${rec}/intelligence`,
    },
    {
      value: "context",
      label: t("record.tab.context"),
      icon: BrainIcon,
      href: `${rec}/context`,
    },
  ]

  const value: ContactRecordValue = {
    data,
    contact,
    canWrite,
    base,
    rec,
    refresh,
    reload: () => void detail.mutate(),
    reportsRev,
    reportsChanged: () => {
      setReportsRev((n) => n + 1)
      void mutateAny(
        (k) => Array.isArray(k) && typeof k[0] === "string" && k[0].endsWith("/context/reports"),
      )
    },
  }

  return (
    <Ctx.Provider value={value}>
      <div data-contact-record={contact.id}>
        <RecordHeader
          back={{ href: `${base}/contacts`, label: t("nav.contacts") }}
          avatar={<RecordAvatar name={who} src={contact.avatar_url} />}
          coverSrc={contact.avatar_url}
          status={contact.lifecycle ? <LifecycleBadge stage={contact.lifecycle} /> : null}
          title={contact.name ? <Bdi>{contact.name}</Bdi> : <Ltr>{who}</Ltr>}
          subtitle={
            <span className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
              {contact.name ? <Ltr>{contactIdentity(contact)}</Ltr> : null}
              {contact.title ? (
                <span>
                  <Bdi>{contact.title}</Bdi>
                </span>
              ) : null}
              {contact.company ? (
                <Link
                  href={`${base}/companies/${contact.company.id}`}
                  className="text-primary hover:underline"
                  data-record-company
                >
                  <Bdi>{contact.company.name}</Bdi>
                </Link>
              ) : null}
            </span>
          }
          actions={
            <>
              {last ? (
                <Button
                  size="sm"
                  nativeButton={false}
                  render={<Link href={inboxHref(last.id)} />}
                  data-action="record-message"
                >
                  <MessageSquareIcon />
                  {t("record.message")}
                </Button>
              ) : null}
              {canWrite ? (
                <Button
                  variant="secondary"
                  size="sm"
                  nativeButton={false}
                  render={<Link href={`${rec}/activity?focus=activity-body`} />}
                  data-action="record-log-activity"
                >
                  <StickyNoteIcon />
                  {t("c360.actions.logActivity")}
                </Button>
              ) : null}
              {canWrite ? (
                <>
                  {/* This page's contact is always the merge target (dst), so the survivor is
                      always here: "go to the survivor" means refetch, not navigate. */}
                  <MergeContactAction contact={contact} onMerged={refresh} />
                  <ConfirmButton
                    label={t("common.delete")}
                    title={t("contacts.deleteTitle")}
                    description={t("contacts.deleteBody", {
                      email: isolate(contactIdentity(contact)),
                    })}
                    confirmLabel={t("common.delete")}
                    onConfirm={async () => {
                      await api(`/api/w/contacts/${encodeURIComponent(contact.id)}`, {
                        method: "DELETE",
                        workspaceId: ws.id,
                      })
                      window.location.href = `${base}/contacts`
                    }}
                  />
                </>
              ) : null}
            </>
          }
          chips={
            <>
              <RecordChip icon={<SparklesIcon />} title={t("icp.fit")}>
                {t("icp.fit")} · {contact.score === null ? t("icp.unscored") : contact.score}
              </RecordChip>
              {[...channels].map((c) => (
                <Badge key={c} variant="info" data-channel={c} className="h-7 rounded-full px-2.5">
                  {channelName(t, c)}
                </Badge>
              ))}
              {contact.company ? (
                <RecordChip icon={<Building2Icon />}>
                  <Bdi>{contact.company.name}</Bdi>
                </RecordChip>
              ) : null}
              {contact.source ? (
                <RecordChip>{t("c360.source", { source: isolate(contact.source) })}</RecordChip>
              ) : null}
            </>
          }
          meta={
            <span className="inline-flex items-center gap-1.5">
              <CalendarIcon className="size-3.5" />
              {t("c360.since", { when: formatDate(contact.created_at) })}
            </span>
          }
          tabs={tabs}
          active={active}
        />
        {children}
      </div>
    </Ctx.Provider>
  )
}
