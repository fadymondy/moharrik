/*
One brand's mark (MH-527): the real simple-icons glyph in its own hex when the
package has this brand, else a neutral monogram — never a guessed corporate
colour. `size` sets the tile; the glyph itself is drawn at roughly 55% of it.
*/

import type { Brand } from "@/lib/brands"
import { cn } from "@/lib/utils"

export function BrandIcon({
  brand,
  size = 40,
  className,
}: {
  brand: Brand
  size?: number
  className?: string
}) {
  const tint = brand.icon ? `#${brand.icon.hex}` : undefined
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center justify-center rounded-lg border border-nq-line",
        className,
      )}
      style={{
        width: size,
        height: size,
        // A brand's own colour, softly tinted behind its mark — same idea as a
        // macOS app icon's backdrop, at low opacity so it reads in both themes.
        backgroundColor: tint
          ? `color-mix(in oklab, ${tint} 16%, var(--nq-surface))`
          : "var(--nq-surface-soft)",
      }}
      aria-hidden
    >
      {brand.icon ? (
        <svg
          viewBox="0 0 24 24"
          width={size * 0.55}
          height={size * 0.55}
          fill={`#${brand.icon.hex}`}
        >
          <path d={brand.icon.path} />
        </svg>
      ) : (
        <brand.fallbackIcon size={size * 0.5} className="text-muted-foreground" />
      )}
    </span>
  )
}
