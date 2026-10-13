import { inflateSync } from "node:zlib"
import { describe, expect, it } from "vitest"

import { encodePng, rasterMark } from "./png"

describe("encodePng", () => {
  it("writes a valid PNG that round-trips the pixels", () => {
    const px = rasterMark(20, { color: "#0B1429", accent: "#E8A33D" })
    const png = encodePng(px, 20, 20)
    expect([...png.subarray(0, 8)]).toEqual([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
    expect(png.readUInt32BE(16)).toBe(20)
    expect(png.readUInt32BE(20)).toBe(20)
    const idat = png.indexOf("IDAT")
    const raw = inflateSync(png.subarray(idat + 4, idat + 4 + png.readUInt32BE(idat - 4)))
    expect(raw.length).toBe((20 * 4 + 1) * 20)
    expect(raw.subarray(1, 81)).toEqual(Buffer.from(px.subarray(0, 80)))
    expect(png.subarray(-8, -4).toString("ascii")).toBe("IEND")
  })
})

describe("rasterMark", () => {
  it("paints the lattice and the unread dot", () => {
    const plain = rasterMark(32, { color: "#000000", accent: "#ff0000" })
    const dotted = rasterMark(32, { color: "#000000", accent: "#ff0000", dot: "#00ff00" })
    expect(plain.some((v, i) => i % 4 === 3 && v === 255)).toBe(true)
    const corner = (25 * 32 + 25) * 4 // the dot's centre
    expect([...dotted.subarray(corner, corner + 4)]).toEqual([0, 255, 0, 255])
    expect(plain[corner + 1]).not.toBe(255)
  })
})
