// The Moharrik cube mark — the Claude Digital Twin mark (twin.fadymondy.com/en/brand).
// Mark only; never a wordmark lockup.
//
// Repo-owned: fadymondy.com-v2/web/lib/brand/marks.ts has no Moharrik entry yet. The MarkSpec
// shape below is copied from marks.ts so switching to a synced upstream entry is a one-line change.

export type Cell = readonly [col: number, row: number]

export interface MarkSpec {
  name: string
  cells: readonly Cell[]
  /** Cells rendered in the accent colour instead of the body colour. */
  accentCells: readonly Cell[]
  body: string
  accent: string
  /** Body colour to use when rendered on a dark ground, if different. */
  bodyOnDark?: string
  actionOnDark?: string
}

export const MOHARRIK_MARK: MarkSpec = {
  name: "Moharrik",
  cells: [
    [1, 0],
    [3, 0],
    [0, 1],
    [4, 1],
    [1, 2],
    [3, 2],
    [2, 3],
  ],
  accentCells: [[3, 0]],
  body: "#00A0A8",
  accent: "#C9A227",
  bodyOnDark: "#2EC4CB",
  actionOnDark: "#2EC4CB",
}
