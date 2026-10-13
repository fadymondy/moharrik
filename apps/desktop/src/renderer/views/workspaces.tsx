import { useQuery } from "@tanstack/react-query"
import { Check } from "lucide-react"

import type { CurrentWorkspace } from "../../shared/types"
import { api, invoke } from "../bridge"
import { usePrefs } from "../prefs"
import { Avatar, Empty, ErrorView, IconButton, Loading } from "../ui"

export function Workspaces({ current, onCancel }: { current?: string; onCancel?: () => void }) {
  const { t } = usePrefs()
  const q = useQuery({ queryKey: ["workspaces"], queryFn: () => api.workspaces() })
  const list = q.data?.workspaces ?? []
  const pick = (w: (typeof list)[number]) => {
    if (w.id === current) return onCancel?.()
    const cw: CurrentWorkspace = { id: w.id, slug: w.slug, name: w.name, role: w.role, avatar_url: w.avatar_url }
    void invoke("session:selectWorkspace", cw)
  }
  return (
    <div className="center">
      <div className="list-card">
        <header style={{ display: "flex", alignItems: "center", gap: 12 }}>
          <div className="flex1">
            <h1>{t("workspaces.title")}</h1>
            <p>{t("workspaces.body")}</p>
          </div>
          {onCancel ? (
            <IconButton label={t("common.cancel")} onClick={onCancel}>
              ✕
            </IconButton>
          ) : null}
        </header>
        <div className="scroll">
          {q.isLoading ? (
            <Loading />
          ) : q.isError ? (
            <ErrorView onRetry={() => void q.refetch()} />
          ) : list.length === 0 ? (
            <Empty
              title={t("workspaces.none")}
              action={
                <button className="btn secondary" onClick={() => void invoke("session:signOut")}>
                  {t("settings.signOut")}
                </button>
              }
            />
          ) : (
            list.map((w) => (
              <button key={w.id} className="row" data-workspace={w.slug} onClick={() => pick(w)} style={{ alignItems: "center" }}>
                <Avatar name={w.name} url={w.avatar_url} />
                <span className="body">
                  <span className="m">{w.name}</span>
                  <span className="small muted">
                    {t("workspaces.members", { n: w.member_count })} · {w.role}
                  </span>
                </span>
                {w.id === current ? <Check size={20} className="action" /> : null}
              </button>
            ))
          )}
        </div>
      </div>
    </div>
  )
}
