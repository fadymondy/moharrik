// The small pieces every screen uses: the same names as the phone's components/ui.tsx, as DOM.
import { useEffect, useRef, useState, type ReactNode } from "react"

import { MARK_ACCENT_INDEX, MARK_CELLS, type Conversation } from "@moharrik/client"

import { usePrefs } from "./prefs"

const LABEL_COLORS: Record<string, string> = {
  gray: "#8A8F98",
  red: "#D9455F",
  orange: "#E8742A",
  amber: "#E0931A",
  green: "#4E9A3E",
  teal: "#00A0A8",
  blue: "#3B7DD8",
  violet: "#7E5BD6",
  pink: "#D65BA6",
}
export const labelColor = (name: string | undefined) => LABEL_COLORS[name ?? ""] ?? LABEL_COLORS.gray!

export function customerName(c: Pick<Conversation, "customer_name" | "customer_email" | "customer_phone" | "customer_handle" | "contact">): string {
  return c.contact?.name || c.customer_name || c.customer_email || c.customer_phone || c.customer_handle || "—"
}

/** The mark (web/app/icon.svg): the lattice, the second cell gold. */
export function Mark({ size = 40 }: { size?: number }) {
  return (
    <svg width={size} height={(size * 4) / 5} viewBox="0 0 5 4" aria-hidden>
      {MARK_CELLS.map(([x, y], i) => (
        <rect key={i} x={x + 0.08} y={y + 0.08} width={0.84} height={0.84} rx={0.18} fill={i === MARK_ACCENT_INDEX ? "var(--accent)" : "var(--action)"} />
      ))}
    </svg>
  )
}

export function Avatar({ name, url, size = 40 }: { name: string; url?: string | null; size?: number }) {
  const [broken, setBroken] = useState(false)
  const initials = (name || "?")
    .trim()
    .split(/\s+/)
    .slice(0, 2)
    .map((w) => w[0] ?? "")
    .join("")
    .toUpperCase()
  return (
    <span className="avatar" style={{ width: size, height: size, fontSize: size * 0.38 }}>
      {url && !broken ? <img src={url} alt="" onError={() => setBroken(true)} /> : initials}
    </span>
  )
}

export function Tag({ text, color }: { text: string; color?: string }) {
  return (
    <span className="tag">
      {color ? <i style={{ background: color }} /> : null}
      {text}
    </span>
  )
}

export function Count({ n, tone }: { n: number; tone?: "danger" }) {
  if (!n) return null
  return <span className={`count${tone ? ` ${tone}` : ""}`}>{n > 99 ? "99+" : n}</span>
}

export function Header({ title, sub, start, end }: { title: ReactNode; sub?: ReactNode; start?: ReactNode; end?: ReactNode }) {
  return (
    <div className="header">
      {start}
      <div style={{ minWidth: 0 }}>
        <h1 className="ellipsis">{title}</h1>
        {sub ? <div className="sub ellipsis">{sub}</div> : null}
      </div>
      {end ? <div className="end">{end}</div> : null}
    </div>
  )
}

export function Loading() {
  return (
    <div className="loading">
      <div className="spinner" />
    </div>
  )
}

export function Empty({ title, action }: { title: string; action?: ReactNode }) {
  return (
    <div className="empty">
      <Mark size={44} />
      <div>{title}</div>
      {action}
    </div>
  )
}

export function ErrorView({ onRetry }: { onRetry: () => void }) {
  const { t } = usePrefs()
  return (
    <div className="empty">
      <div className="danger">{t("common.networkError")}</div>
      <button className="btn secondary" onClick={onRetry}>
        {t("common.retry")}
      </button>
    </div>
  )
}

export function IconButton({ label, onClick, children, disabled }: { label: string; onClick: () => void; children: ReactNode; disabled?: boolean }) {
  return (
    <button className="icon" title={label} aria-label={label} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  )
}

export function Segmented<V extends string>({ value, onChange, options }: { value: V; onChange: (v: V) => void; options: { value: V; label: string }[] }) {
  return (
    <div className="seg" role="tablist">
      {options.map((o) => (
        <button key={o.value} role="tab" aria-selected={o.value === value} className={o.value === value ? "on" : ""} onClick={() => onChange(o.value)}>
          {o.label}
        </button>
      ))}
    </div>
  )
}

export function Chip({ label, active, onClick }: { label: string; active?: boolean; onClick: () => void }) {
  return (
    <button className={`chip${active ? " on" : ""}`} aria-pressed={!!active} onClick={onClick}>
      {label}
    </button>
  )
}

export function Switch({ value, onChange, disabled, label }: { value: boolean; onChange: (v: boolean) => void; disabled?: boolean; label: string }) {
  return <button role="switch" aria-checked={value} aria-label={label} className={`switch${value ? " on" : ""}`} disabled={disabled} onClick={() => onChange(!value)} />
}

export function Section({ title, children, list = true }: { title: string; children: ReactNode; list?: boolean }) {
  return (
    <section className="section">
      <h3>{title}</h3>
      <div className={`card${list ? " list" : ""}`}>{children}</div>
    </section>
  )
}

/** A dropdown anchored to its button; closes on an outside click or Escape. */
export function Popover({ button, children, open, onOpenChange }: { button: ReactNode; children: ReactNode; open: boolean; onOpenChange: (v: boolean) => void }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const click = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && onOpenChange(false)
    const key = (e: KeyboardEvent) => e.key === "Escape" && onOpenChange(false)
    document.addEventListener("mousedown", click)
    document.addEventListener("keydown", key)
    return () => {
      document.removeEventListener("mousedown", click)
      document.removeEventListener("keydown", key)
    }
  }, [open, onOpenChange])
  return (
    <div className="pop" ref={ref}>
      {button}
      {open ? <div className="menu">{children}</div> : null}
    </div>
  )
}

export function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return v
}

let toastListener: ((s: string) => void) | null = null
export const toast = (s: string) => toastListener?.(s)

export function Toaster() {
  const [text, setText] = useState("")
  useEffect(() => {
    let id: ReturnType<typeof setTimeout> | undefined
    toastListener = (s) => {
      setText(s)
      clearTimeout(id)
      id = setTimeout(() => setText(""), 2600)
    }
    return () => {
      toastListener = null
      clearTimeout(id)
    }
  }, [])
  return text ? (
    <div className="toast" role="status">
      {text}
    </div>
  ) : null
}
