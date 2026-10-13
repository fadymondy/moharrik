"""Renders the app's brand assets from the web's sources (run from apps/mobile).

    python scripts/make-assets.py

- Icons, splash and the Android notification icon come from the Moharrik mark (web/app/icon.svg):
  seven squares of a 5x4 lattice, the second one gold. They are drawn here cell by cell instead of
  rasterising the SVG, so the output is pixel-exact at every size and needs no SVG renderer.
- Lusail (the Arabic face) comes from web/public/fonts/lusail. The web ships only woff2, which React
  Native cannot load, so it is unpacked to TTF.

The outputs are committed; rerun this only when the mark or the fonts change.
"""

import os
import sys

from PIL import Image, ImageDraw

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.normpath(os.path.join(HERE, ".."))
WEB = os.path.normpath(os.path.join(ROOT, "..", "..", "web"))
OUT = os.path.join(ROOT, "assets")

# apps/shared/src/brand.ts — the same values, kept in step by hand (they change rarely).
ACTION = "#00A0A8"
ACTION_DARK = "#2EC4CB"
ACCENT = "#C9A227"
PAPER = "#F0EBE1"
INK = "#0B1429"
CELLS = [(1, 0), (3, 0), (0, 1), (4, 1), (1, 2), (3, 2), (2, 3)]
ACCENT_INDEX = 1


def mark(size, ground, scale=0.62, colour=ACTION, accent=ACCENT, mono=None):
    """The mark centred on a square of `size`, filling `scale` of its width."""
    img = Image.new("RGBA", (size, size), ground or (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    cell = size * scale / 5
    ox = (size - cell * 5) / 2
    oy = (size - cell * 4) / 2
    for i, (cx, cy) in enumerate(CELLS):
        fill = mono or (accent if i == ACCENT_INDEX else colour)
        x, y = ox + cx * cell, oy + cy * cell
        d.rectangle([round(x), round(y), round(x + cell) - 1, round(y + cell) - 1], fill=fill)
    return img


def save(img, name):
    path = os.path.join(OUT, name)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    img.save(path)
    print("wrote", os.path.relpath(path, ROOT))


def icons():
    save(mark(1024, PAPER), "brand/icon.png")
    # Android adaptive icon: 108dp canvas, the safe zone is the middle 66dp, so the mark stays small.
    save(mark(1024, None, scale=0.42), "brand/adaptive-fg.png")
    save(Image.new("RGBA", (1024, 1024), PAPER), "brand/adaptive-bg.png")
    save(mark(1024, None, scale=0.42, mono="#FFFFFF"), "brand/adaptive-mono.png")
    save(mark(512, None, scale=0.9), "brand/splash.png")
    save(mark(512, None, scale=0.9, colour=ACTION_DARK), "brand/splash-dark.png")
    # Status-bar icon: Android draws it white from the alpha channel alone.
    save(mark(96, None, scale=0.84, mono="#FFFFFF"), "brand/notification-icon.png")
    save(mark(48, PAPER, scale=0.8), "brand/favicon.png")


def fonts():
    try:
        from fontTools.ttLib import TTFont
    except ImportError:
        sys.exit("fonts need fontTools and brotli: pip install fonttools brotli")
    src = os.path.join(WEB, "public", "fonts", "lusail")
    for weight in ("Regular", "Medium", "Bold"):
        f = TTFont(os.path.join(src, f"Lusail-{weight}.woff2"))
        f.flavor = None
        path = os.path.join(OUT, "fonts", f"Lusail-{weight}.ttf")
        os.makedirs(os.path.dirname(path), exist_ok=True)
        f.save(path)
        print("wrote", os.path.relpath(path, ROOT))


if __name__ == "__main__":
    icons()
    fonts()
