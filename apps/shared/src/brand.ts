// The Moharrik brand on Nasaq's grounds (web/: <NasaqProvider brand="moharrik">, tokens from
// @fadymondy/nasaq/dist/tokens.css). Copied as values, not imported: the phone app has no CSS.
// Keep in step with the web when Nasaq's moharrik block changes.

export interface Palette {
  bg: string
  surface: string
  raised: string
  overlay: string
  line: string
  fg: string
  body: string
  muted: string
  /** The brand teal: primary actions, the unread dot, links. */
  action: string
  onAction: string
  /** The mark's gold cell: one accent per screen at most. */
  accent: string
  success: string
  warning: string
  danger: string
}

export const light: Palette = {
  bg: "#F0EBE1",
  surface: "#F7F4EC",
  raised: "#FAF8F3",
  overlay: "#FDFCF9",
  line: "#DED5C4",
  fg: "#0E1A3C",
  body: "#4A4438",
  muted: "#6E6551",
  action: "#00A0A8",
  onAction: "#0B1429",
  accent: "#C9A227",
  success: "#4E9A3E",
  warning: "#E0931A",
  danger: "#D9455F",
}

export const dark: Palette = {
  bg: "#0B1429",
  surface: "#0E1A3C",
  raised: "#132147",
  overlay: "#1B2A52",
  line: "#25355C",
  fg: "#F0EBE1",
  body: "#C8D0E4",
  muted: "#8A97B8",
  action: "#2EC4CB",
  onAction: "#0B1429",
  accent: "#C9A227",
  success: "#4E9A3E",
  warning: "#E0931A",
  danger: "#D9455F",
}

export const palette = (scheme: "light" | "dark"): Palette => (scheme === "dark" ? dark : light)

/** The mark (web/app/icon.svg): a 5×4 lattice; [col,row] cells, the second one gold. */
export const MARK_CELLS: ReadonlyArray<readonly [number, number]> = [
  [1, 0],
  [3, 0],
  [0, 1],
  [4, 1],
  [1, 2],
  [3, 2],
  [2, 3],
]
export const MARK_ACCENT_INDEX = 1

/** Arabic is set in IBM Plex Sans Arabic, English in Inter (web/app/styles/fonts.css, app/layout.tsx). */
export const FONTS = { latin: "Inter", arabic: "IBM Plex Sans Arabic" } as const

export const APP = {
  name: "Moharrik",
  nameAr: "محرك",
  /** iOS bundle id and Android application id; the desktop app adds ".desktop". */
  id: "com.fadymondy.moharrik",
  desktopId: "com.fadymondy.moharrik.desktop",
  /** The custom URL scheme: deep links and the phone's sign-in redirect. */
  scheme: "moharrik",
} as const
