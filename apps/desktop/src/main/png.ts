// The brand mark as pixels, for the tray (drawn in code so it is crisp at every scale and can follow
// the taskbar's light/dark), and a minimal PNG encoder for nativeImage.
import { deflateSync } from "node:zlib"

import { MARK_ACCENT_INDEX, MARK_CELLS } from "@moharrik/client"

const hex = (c: string): [number, number, number] => [parseInt(c.slice(1, 3), 16), parseInt(c.slice(3, 5), 16), parseInt(c.slice(5, 7), 16)]

/** RGBA pixels of the 5×4 lattice centred in a size×size square (web/app/icon.svg). */
export function rasterMark(size: number, o: { color: string; accent: string; dot?: string | null }): Uint8Array {
  const px = new Uint8Array(size * size * 4)
  const cell = size / 5
  const top = (size - cell * 4) / 2
  const fill = (x0: number, y0: number, w: number, h: number, rgb: [number, number, number]) => {
    for (let y = Math.max(0, Math.round(y0)); y < Math.min(size, Math.round(y0 + h)); y++)
      for (let x = Math.max(0, Math.round(x0)); x < Math.min(size, Math.round(x0 + w)); x++) {
        const i = (y * size + x) * 4
        px[i] = rgb[0]
        px[i + 1] = rgb[1]
        px[i + 2] = rgb[2]
        px[i + 3] = 255
      }
  }
  MARK_CELLS.forEach(([c, r], i) => fill(c * cell, top + r * cell, cell, cell, hex(i === MARK_ACCENT_INDEX ? o.accent : o.color)))
  if (o.dot) {
    // The unread dot: a filled circle in the bottom-end corner.
    const rad = size * 0.22
    const cx = size - rad
    const cy = size - rad
    const rgb = hex(o.dot)
    for (let y = 0; y < size; y++)
      for (let x = 0; x < size; x++)
        if ((x + 0.5 - cx) ** 2 + (y + 0.5 - cy) ** 2 <= rad * rad) {
          const i = (y * size + x) * 4
          px.set([rgb[0], rgb[1], rgb[2], 255], i)
        }
  }
  return px
}

const table = (() => {
  const t = new Uint32Array(256)
  for (let n = 0; n < 256; n++) {
    let c = n
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
    t[n] = c >>> 0
  }
  return t
})()

function crc32(buf: Uint8Array): number {
  let c = 0xffffffff
  for (const b of buf) c = table[(c ^ b) & 0xff]! ^ (c >>> 8)
  return (c ^ 0xffffffff) >>> 0
}

function chunk(type: string, data: Uint8Array): Buffer {
  const out = Buffer.alloc(12 + data.length)
  out.writeUInt32BE(data.length, 0)
  out.write(type, 4, "ascii")
  Buffer.from(data).copy(out, 8)
  out.writeUInt32BE(crc32(out.subarray(4, 8 + data.length)), 8 + data.length)
  return out
}

/** RGBA → PNG (8-bit, colour type 6, no filtering). */
export function encodePng(rgba: Uint8Array, width: number, height: number): Buffer {
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(width, 0)
  ihdr.writeUInt32BE(height, 4)
  ihdr[8] = 8
  ihdr[9] = 6
  const raw = Buffer.alloc((width * 4 + 1) * height)
  for (let y = 0; y < height; y++) Buffer.from(rgba.buffer, rgba.byteOffset + y * width * 4, width * 4).copy(raw, y * (width * 4 + 1) + 1)
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]), chunk("IHDR", ihdr), chunk("IDAT", deflateSync(raw)), chunk("IEND", new Uint8Array())])
}
