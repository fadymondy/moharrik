/*
The extension's icons: the Moharrik mark, drawn cell by cell (MH-478).

  node extension/make-icons.mjs

The mark only — never the wordmark (CLAUDE.md). Its cells and colours are the
ones web/lib/brand/mark.ts renders in the app header, copied here because this
directory is loaded by Chrome as it is and cannot import from the web app.

Drawn rather than scaled: every cell lands on whole pixels at every size, so the
16-pixel toolbar icon is as sharp as the 128-pixel store icon instead of a
blurred downscale. Two sets: the brand body colour for light toolbars, and the
lifted body colour the app uses on dark grounds (bodyOnDark) — the popup
switches the toolbar icon to that set when the browser is in dark mode.

No dependencies: a PNG is a few zlib-compressed rows and three chunks.
*/
import fs from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"
import zlib from "node:zlib"

const here = path.dirname(fileURLToPath(import.meta.url))

// web/lib/brand/mark.ts — MOHARRIK_MARK.
const CELLS = [
  [1, 0],
  [3, 0],
  [0, 1],
  [4, 1],
  [1, 2],
  [3, 2],
  [2, 3],
]
const ACCENT = new Set(["3,0"])
const COLOURS = {
  light: { body: "#00A0A8", accent: "#C9A227" },
  dark: { body: "#2EC4CB", accent: "#C9A227" },
}
// Chrome's store guidance: 128-pixel art sits in a 96-pixel box. The toolbar
// sizes use the whole square — at 16 pixels a margin costs a whole cell.
const SIZES = { 16: 3, 32: 6, 48: 9, 128: 19 }

const COLS = 5
const ROWS = 4

function hex(c) {
  return [1, 3, 5].map((i) => parseInt(c.slice(i, i + 2), 16))
}

function draw(size, cell, colours) {
  const px = new Uint8Array(size * size * 4) // transparent
  const w = COLS * cell
  const h = ROWS * cell
  const ox = Math.floor((size - w) / 2)
  const oy = Math.floor((size - h) / 2)
  for (const [c, r] of CELLS) {
    const [R, G, B] = hex(ACCENT.has(`${c},${r}`) ? colours.accent : colours.body)
    for (let y = oy + r * cell; y < oy + (r + 1) * cell; y++) {
      for (let x = ox + c * cell; x < ox + (c + 1) * cell; x++) {
        const i = (y * size + x) * 4
        px[i] = R
        px[i + 1] = G
        px[i + 2] = B
        px[i + 3] = 255
      }
    }
  }
  return png(size, size, px)
}

const CRC = new Uint32Array(256).map((_, n) => {
  let c = n
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
  return c >>> 0
})
function crc32(buf) {
  let c = 0xffffffff
  for (const b of buf) c = CRC[(c ^ b) & 0xff] ^ (c >>> 8)
  return (c ^ 0xffffffff) >>> 0
}
function chunk(type, data) {
  const len = Buffer.alloc(4)
  len.writeUInt32BE(data.length)
  const td = Buffer.concat([Buffer.from(type), data])
  const crc = Buffer.alloc(4)
  crc.writeUInt32BE(crc32(td))
  return Buffer.concat([len, td, crc])
}
function png(w, h, rgba) {
  const raw = Buffer.alloc((w * 4 + 1) * h)
  for (let y = 0; y < h; y++) {
    raw[y * (w * 4 + 1)] = 0
    Buffer.from(rgba.buffer, y * w * 4, w * 4).copy(raw, y * (w * 4 + 1) + 1)
  }
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(w, 0)
  ihdr.writeUInt32BE(h, 4)
  ihdr[8] = 8
  ihdr[9] = 6 // RGBA
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr),
    chunk("IDAT", zlib.deflateSync(raw, { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ])
}

const out = path.join(here, "icons")
fs.mkdirSync(out, { recursive: true })
for (const [theme, colours] of Object.entries(COLOURS)) {
  for (const [size, cell] of Object.entries(SIZES)) {
    const name = theme === "light" ? `icon-${size}.png` : `icon-dark-${size}.png`
    fs.writeFileSync(path.join(out, name), draw(Number(size), cell, colours))
  }
}
// The mark as SVG for the popup and options headers, same cells.
const svg = (colours) =>
  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${COLS} ${ROWS}" shape-rendering="crispEdges">` +
  CELLS.map(([c, r]) => `<rect x="${c}" y="${r}" width="1" height="1" fill="${ACCENT.has(`${c},${r}`) ? colours.accent : colours.body}"/>`).join("") +
  `</svg>\n`
fs.writeFileSync(path.join(out, "mark.svg"), svg(COLOURS.light))
fs.writeFileSync(path.join(out, "mark-dark.svg"), svg(COLOURS.dark))
console.log("icons written to", out)
