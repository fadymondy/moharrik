import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { CheckCheck } from "lucide-react"
import { useMemo, useState } from "react"

import { ago, type NotificationItem } from "@moharrik/client"

import { useSession } from "../app"
import { api, invoke } from "../bridge"
import { usePrefs } from "../prefs"
import { Chip, Empty, ErrorView, Header, IconButton, Loading, Tag } from "../ui"

const SEVERITY: Record<string, string> = {
  critical: "var(--danger)",
  error: "var(--danger)",
  high: "var(--danger)",
  warning: "var(--warning)",
  medium: "var(--warning)",
  success: "var(--success)",
}

export function NotificationsScreen({ focus }: { focus?: string }) {
  const { t, locale } = usePrefs()
  const { ws, session } = useSession()
  const qc = useQueryClient()
  const [unreadOnly, setUnreadOnly] = useState(false)
  const list = useInfiniteQuery({
    queryKey: ["notifications", ws, unreadOnly],
    queryFn: ({ pageParam }) => api.notifications({ unread: unreadOnly || undefined, cursor: pageParam || undefined }),
    initialPageParam: "",
    getNextPageParam: (p) => p.next_cursor || undefined,
  })
  const items = useMemo(() => list.data?.pages.flatMap((p) => p.items ?? []) ?? [], [list.data])
  const refresh = () => void qc.invalidateQueries({ queryKey: ["notifications", ws] })
  const markAll = useMutation({ mutationFn: () => api.markAllNotificationsRead(), onSuccess: refresh })
  const mark = useMutation({ mutationFn: (id: string) => api.markNotification(id, true), onSuccess: refresh })

  function open(n: NotificationItem) {
    if (!n.read_at) mark.mutate(n.id)
    if (/^https?:\/\//.test(n.url)) void invoke("app:openExternal", n.url)
  }

  return (
    <>
      <Header
        title={t("notifications.title")}
        sub={session.workspace?.name}
        end={
          <>
            <Chip label={t("notifications.unreadOnly")} active={unreadOnly} onClick={() => setUnreadOnly((v) => !v)} />
            <IconButton label={t("notifications.markAllRead")} onClick={() => markAll.mutate()}>
              <CheckCheck size={20} className="action" />
            </IconButton>
          </>
        }
      />
      {list.isLoading ? (
        <Loading />
      ) : list.isError ? (
        <ErrorView onRetry={() => void list.refetch()} />
      ) : items.length === 0 ? (
        <Empty title={t("notifications.empty")} />
      ) : (
        <div
          className="scroll"
          onScroll={(e) => {
            const el = e.currentTarget
            if (el.scrollTop + el.clientHeight > el.scrollHeight - 200 && list.hasNextPage && !list.isFetchingNextPage) void list.fetchNextPage()
          }}
        >
          {items.map((n) => (
            <button key={n.id} className={`row${n.id === focus ? " on" : ""}`} onClick={() => open(n)} data-notification={n.id}>
              <span style={{ paddingTop: 7 }}>
                <span className="dot" style={{ background: n.read_at ? "var(--line)" : (SEVERITY[n.severity] ?? "var(--action)") }} />
              </span>
              <span className="body">
                <span className="line">
                  <span className={`flex1 ${n.read_at ? "m" : "b"}`}>{n.title}</span>
                  <span className="xs muted">{ago(t, locale, n.occurred_at)}</span>
                </span>
                {n.body ? <span className="small bodytone selectable">{n.body}</span> : null}
                <span className="tags" style={{ marginTop: 2 }}>
                  {n.source_name ? <Tag text={n.source_name} /> : null}
                  {n.kind ? <Tag text={n.kind} /> : null}
                </span>
              </span>
            </button>
          ))}
        </div>
      )}
    </>
  )
}
