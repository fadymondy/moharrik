// Sign in in the browser (the same login as app.moharrik.com), back through a loopback redirect
// the main process listens on (session.ts). The window only starts it and shows the waiting state.
import { useState } from "react"

import type { SessionOut } from "../../shared/types"
import { invoke } from "../bridge"
import { usePrefs } from "../prefs"
import { Mark } from "../ui"

export function SignIn({ server, onServer }: { server: string; onServer: (s: string) => void }) {
  const { t, locale, set } = usePrefs()
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)
  const [editServer, setEditServer] = useState(false)
  const [url, setUrl] = useState(server)

  async function go() {
    setBusy(true)
    setFailed(false)
    try {
      if (editServer && url.trim() && url.trim() !== server) {
        const s = await invoke<SessionOut | null>("session:setServer", url.trim())
        onServer(s?.server ?? url.trim())
      }
      const r = await invoke<{ ok: boolean; message?: string }>("session:signIn")
      if (!r.ok && r.message) setFailed(true)
    } catch {
      setFailed(true)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="center">
      <div className="signin">
        <div className="mark">
          <Mark size={96} />
        </div>
        <h1>{t("signin.title")}</h1>
        <p>{t("desktop.signinBody")}</p>
        {failed ? <div className="danger">{t("signin.failed")}</div> : null}
        <button className="btn" data-signin onClick={go} disabled={busy}>
          {busy ? <span className="spinner" /> : null}
          {busy ? t("signin.waiting") : t("signin.button")}
        </button>
        {busy ? (
          <button className="btn secondary" onClick={() => void invoke("session:cancelSignIn")}>
            {t("common.cancel")}
          </button>
        ) : null}
        {editServer ? (
          <label style={{ display: "flex", flexDirection: "column", gap: 6, textAlign: "start" }}>
            <span className="small muted">{t("signin.server")}</span>
            <input className="field" dir="ltr" value={url} onChange={(e) => setUrl(e.target.value)} spellCheck={false} placeholder="https://app.moharrik.com" />
          </label>
        ) : null}
        <div className="links">
          <button className="action m" onClick={() => void set({ locale: locale === "ar" ? "en" : "ar" })}>
            {locale === "ar" ? "English" : "العربية"}
          </button>
          <button className="muted small" onClick={() => setEditServer((v) => !v)}>
            {t("signin.server")}: <span dir="ltr">{server.replace(/^https?:\/\//, "")}</span>
          </button>
        </div>
      </div>
    </div>
  )
}
