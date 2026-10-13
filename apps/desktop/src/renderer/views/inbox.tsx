// The unified inbox: filters and the list on one side, the open conversation on the other.
import { useInfiniteQuery, useQuery } from "@tanstack/react-query"
import { Users } from "lucide-react"
import { useMemo, useState } from "react"

import { ago, channelName, type Conversation, type ConversationFilter } from "@moharrik/client"

import { useNav, useSession } from "../app"
import { api } from "../bridge"
import { usePrefs } from "../prefs"
import { Avatar, Chip, customerName, Empty, ErrorView, Header, labelColor, Loading, Segmented, Tag, useDebounced } from "../ui"
import { ConversationPane } from "./conversation"

type Status = "" | "open" | "snoozed" | "closed"

export function InboxScreen({ openId }: { openId?: string }) {
  const { t } = usePrefs()
  const { ws, session } = useSession()
  const { go } = useNav()
  const [status, setStatus] = useState<Status>("open")
  const [unread, setUnread] = useState(false)
  const [channel, setChannel] = useState("")
  const [search, setSearch] = useState("")
  const q = useDebounced(search.trim(), 300)

  const filter: ConversationFilter = { status, unread: unread || undefined, channel: channel || undefined, q: q || undefined }
  const list = useInfiniteQuery({
    queryKey: ["conversations", ws, filter],
    queryFn: ({ pageParam }) => api.conversations({ ...filter, cursor: pageParam || undefined }),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
  })
  const members = useQuery({ queryKey: ["members", ws], queryFn: () => api.members(), staleTime: 5 * 60_000 })
  const memberName = useMemo(() => {
    const m = new Map<string, string>()
    for (const x of members.data?.members ?? []) m.set(x.user_id, x.email.split("@")[0] ?? x.email)
    return m
  }, [members.data])

  const first = list.data?.pages[0]
  const rows = useMemo(() => {
    const pinned = first?.pinned ?? []
    const seen = new Set(pinned.map((p) => p.id))
    return [...pinned, ...(list.data?.pages.flatMap((p) => p.conversations ?? []) ?? []).filter((x) => !seen.has(x.id))]
  }, [list.data, first])
  const channels = (first?.channel_counts ?? []).filter((x) => x.total > 0)

  return (
    <div className="split">
      <div className="pane">
        <Header title={t("tabs.inbox")} sub={session.workspace?.name} />
        <div className="toolbar">
          <input className="search" type="search" value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t("inbox.searchPlaceholder")} />
          <Segmented<Status>
            value={status}
            onChange={setStatus}
            options={[
              { value: "open", label: t("inbox.open") },
              { value: "snoozed", label: t("inbox.snoozed") },
              { value: "closed", label: t("inbox.closed") },
              { value: "", label: t("inbox.all") },
            ]}
          />
          <div className="chips">
            <Chip label={t("inbox.unread")} active={unread} onClick={() => setUnread((v) => !v)} />
            {channels.map((ch) => (
              <Chip
                key={ch.channel}
                label={`${channelName(t, ch.channel)}${ch.unread ? ` · ${ch.unread}` : ""}`}
                active={channel === ch.channel}
                onClick={() => setChannel((v) => (v === ch.channel ? "" : ch.channel))}
              />
            ))}
          </div>
        </div>
        {list.isLoading ? (
          <Loading />
        ) : list.isError ? (
          <ErrorView onRetry={() => void list.refetch()} />
        ) : rows.length === 0 ? (
          <Empty title={t("inbox.empty")} />
        ) : (
          <div
            className="scroll"
            onScroll={(e) => {
              const el = e.currentTarget
              if (el.scrollTop + el.clientHeight > el.scrollHeight - 200 && list.hasNextPage && !list.isFetchingNextPage) void list.fetchNextPage()
            }}
          >
            {rows.map((c) => (
              <ConversationRow
                key={c.id}
                c={c}
                on={c.id === openId}
                assignee={c.assignee_user_id ? memberName.get(c.assignee_user_id) : undefined}
                onClick={() => go({ screen: "conversation", id: c.id })}
              />
            ))}
          </div>
        )}
      </div>
      <div className="pane">{openId ? <ConversationPane key={openId} id={openId} /> : <Empty title={t("desktop.pick")} />}</div>
    </div>
  )
}

function ConversationRow({ c, onClick, assignee, on }: { c: Conversation; onClick: () => void; assignee?: string; on: boolean }) {
  const { t, locale } = usePrefs()
  const name = customerName(c)
  return (
    <button className={`row${on ? " on" : ""}`} onClick={onClick} data-conversation-row={c.id}>
      <Avatar name={name} url={c.avatar_url} />
      <span className="body">
        <span className="line">
          {c.is_group ? <Users size={14} className="muted" /> : null}
          <span className={`flex1 ellipsis ${c.unread ? "b" : "m"}`}>{name}</span>
          <span className={`xs ${c.unread ? "action" : "muted"}`}>{ago(t, locale, c.last_message_at)}</span>
        </span>
        {c.subject && c.subject !== c.preview ? <span className="small m ellipsis">{c.subject}</span> : null}
        <span className="line">
          <span className={`small flex1 ellipsis ${c.unread ? "bodytone" : "muted"}`}>{c.preview || t("inbox.noMessages")}</span>
          {c.unread ? <span className="dot" /> : null}
        </span>
        <span className="tags" style={{ marginTop: 2 }}>
          <Tag text={channelName(t, c.channel)} />
          {c.status !== "open" ? <Tag text={t(c.status === "snoozed" ? "inbox.snoozed" : "inbox.closed")} /> : null}
          {assignee ? <Tag text={assignee} /> : null}
          {(c.labels ?? []).slice(0, 3).map((l) => (
            <Tag key={l.id} text={l.name} color={labelColor(l.color)} />
          ))}
        </span>
      </span>
    </button>
  )
}
