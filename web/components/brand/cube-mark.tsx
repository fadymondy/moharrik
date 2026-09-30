import type { MarkSpec } from "@/lib/brand/mark"

/**
 * Renders a cube-lattice mark from its cell list, normalised to the mark's own
 * bounding box (a mark may start at column 1+; measuring from 0 pushes it
 * off-centre). Ported from fadymondy.com-v2/web/components/marks/cube-mark.tsx.
 * Mark only — never a wordmark lockup.
 */
export function CubeMark({
  mark,
  size = 24,
  className,
}: {
  mark: MarkSpec
  size?: number
  className?: string
}) {
  const cols = mark.cells.map(([c]) => c)
  const rows = mark.cells.map(([, r]) => r)
  const minCol = Math.min(...cols)
  const minRow = Math.min(...rows)
  const width = Math.max(...cols) - minCol + 1
  const height = Math.max(...rows) - minRow + 1
  const unit = 100 / Math.max(width, height)
  const accentSet = new Set(mark.accentCells.map(([c, r]) => `${c},${r}`))

  return (
    <svg
      viewBox={`0 0 ${width * unit} ${height * unit}`}
      width={size}
      height={(size * height) / width}
      preserveAspectRatio="xMidYMid meet"
      className={className}
      role="img"
      aria-label={mark.name}
    >
      {mark.cells.map(([c, r]) => (
        <rect
          key={`${c},${r}`}
          x={(c - minCol) * unit}
          y={(r - minRow) * unit}
          width={unit}
          height={unit}
          // Body follows the brand token, so the lifted dark-theme colour applies on dark grounds.
          fill={accentSet.has(`${c},${r}`) ? mark.accent : `var(--nq-brand, ${mark.body})`}
        />
      ))}
    </svg>
  )
}
