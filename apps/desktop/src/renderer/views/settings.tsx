import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { ChevronRight } from "lucide-react"
import { useEffect, useState } from "react"

import type { PushPrefs } from "@moharrik/client"

import { useSession } from "../app"
import { api, invoke } from "../bridge"
import { usePrefs } from "../prefs"
import { Avatar, Header, Section, Segmented, Switch } from "../ui"

export function SettingsScreen({ onSwitch }: { onSwitch?: () => void }) {
  const { t, prefs, set } = usePrefs()
  const { ws, session, server } = useSession()
  const qc = useQueryClient()
  const [version, setVersion] = useState("")
  useEffect(() => void invoke<{ version: string }>("app:info").then((i) => setVersion(i.version)), [])

  // The workspace's push preferences: the same ones the phone sets, for your phone notifications.
  const push = useQuery({ queryKey: ["pushPrefs", ws], queryFn: () => api.pushPrefs() })
  const savePush = useMutation({
    mutationFn: (p: PushPrefs) => api.setPushPrefs(p),
    onMutate: (p) => qc.setQueryData(["pushPrefs", ws], p),
    onSettled: () => void qc.invalidateQueries({ queryKey: ["pushPrefs", ws] }),
  })
  const p = push.data
  const setP = (k: keyof Omit<PushPrefs, "$schema">, v: boolean) => p && savePush.mutate({ ...p, [k]: v })
  const ws0 = session.workspace!

  return (
    <>
      <Header title={t("settings.title")} />
      <div className="scroll" data-settings>
        <div className="settings">
          <Section title={t("settings.workspace")}>
            <button className="row" style={{ alignItems: "center" }} onClick={onSwitch ?? (() => document.querySelector<HTMLButtonElement>(".ws")?.click())}>
              <Avatar name={ws0.name} url={ws0.avatar_url} />
              <span className="body">
                <span className="m">{ws0.name}</span>
                <span className="small muted">{t("settings.switch")}</span>
              </span>
              <ChevronRight size={18} className="muted mirror" />
            </button>
          </Section>

          <Section title={t("desktop.notifications")}>
            <Toggle label={t("desktop.notify")} sub={t("desktop.notifyBody")} value={prefs.notify} onChange={(v) => void set({ notify: v })} />
            <Toggle label={t("desktop.sound")} value={prefs.sound} disabled={!prefs.notify} onChange={(v) => void set({ sound: v })} />
            <Toggle label={t("desktop.badge")} value={prefs.badge} onChange={(v) => void set({ badge: v })} />
          </Section>

          {p ? (
            <Section title={t("settings.push")}>
              <div className="pad small muted">{t("settings.pushBody")}</div>
              <Toggle label={t("settings.pushMessages")} value={p.messages} onChange={(v) => setP("messages", v)} />
              <Toggle label={t("settings.pushAssignedOnly")} value={p.assigned_only} disabled={!p.messages} onChange={(v) => setP("assigned_only", v)} />
              <Toggle label={t("settings.pushAssignments")} value={p.assignments} onChange={(v) => setP("assignments", v)} />
              <Toggle label={t("settings.pushNotifications")} value={p.notifications} onChange={(v) => setP("notifications", v)} />
            </Section>
          ) : null}

          <Section title={t("settings.language")} list={false}>
            <Segmented
              value={prefs.locale}
              onChange={(v) => void set({ locale: v })}
              options={[
                { value: "ar", label: "العربية" },
                { value: "en", label: "English" },
              ]}
            />
          </Section>

          <Section title={t("settings.appearance")} list={false}>
            <Segmented
              value={prefs.theme}
              onChange={(v) => void set({ theme: v })}
              options={[
                { value: "system", label: t("settings.system") },
                { value: "light", label: t("settings.light") },
                { value: "dark", label: t("settings.dark") },
              ]}
            />
          </Section>

          <Section title={t("settings.account")}>
            <div className="toggle">
              <span className="flex1">
                <div>{t("settings.signedInAs", { email: session.email })}</div>
                <div className="small muted" dir="ltr" style={{ textAlign: "start" }}>
                  {server.replace(/^https?:\/\//, "")}
                </div>
              </span>
              <button
                className="btn danger"
                onClick={() => {
                  if (window.confirm(`${t("settings.signOut")}\n\n${t("settings.signOutBody")}`)) void invoke("session:signOut")
                }}
              >
                {t("settings.signOut")}
              </button>
            </div>
          </Section>
          <div className="xs muted" style={{ textAlign: "center", marginTop: 16 }}>
            {t("settings.version", { v: version || "…" })}
          </div>
        </div>
      </div>
    </>
  )
}

function Toggle({ label, sub, value, onChange, disabled }: { label: string; sub?: string; value: boolean; onChange: (v: boolean) => void; disabled?: boolean }) {
  return (
    <div className={`toggle${disabled ? " off" : ""}`}>
      <span className="flex1">
        <div>{label}</div>
        {sub ? <div className="small muted">{sub}</div> : null}
      </span>
      <Switch label={label} value={value} onChange={onChange} disabled={disabled} />
    </div>
  )
}
