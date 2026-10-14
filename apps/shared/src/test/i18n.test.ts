import { describe, expect, it } from "vitest"

import { ago, alertText, channelName, money, num, parseLive, stageName, strings, translator } from "../index"

describe("strings", () => {
  it("Arabic and English have the same keys, none empty, same placeholders", () => {
    const { en, ar } = strings
    expect(Object.keys(ar).sort()).toEqual(Object.keys(en).sort())
    const vars = (s: string) => (s.match(/\{\w+\}/g) ?? []).sort()
    for (const k of Object.keys(en) as (keyof typeof en)[]) {
      expect(ar[k].trim(), k).not.toBe("")
      expect(vars(ar[k]), k).toEqual(vars(en[k]))
    }
  })

  it("interpolates", () => {
    expect(translator("en")("settings.signedInAs", { email: "a@b.c" })).toBe("Signed in as a@b.c")
    expect(translator("ar")("alert.newMessage", { sender: "رنا" })).toBe("رسالة جديدة من رنا")
  })

  it("names channels and keeps unknown ids", () => {
    const t = translator("en")
    expect(channelName(t, "whatsapp")).toBe("WhatsApp")
    expect(channelName(t, "carrier-pigeon")).toBe("carrier-pigeon")
  })

  it("formats relative times", () => {
    const t = translator("en")
    const now = Date.parse("2026-10-13T12:00:00Z")
    expect(ago(t, "en", "2026-10-13T11:59:30Z", now)).toBe("now")
    expect(ago(t, "en", "2026-10-13T11:55:00Z", now)).toBe("5m")
    expect(ago(t, "en", "2026-10-13T09:00:00Z", now)).toBe("3h")
    expect(ago(t, "en", "2026-10-12T09:00:00Z", now)).toBe("Yesterday")
    expect(ago(t, "en", null, now)).toBe("")
  })

  it("writes desktop alerts like the server writes pushes", () => {
    const t = translator("en")
    expect(alertText(t, { kind: "assigned", sender: "Rana", snippet: "hi" })).toEqual({
      title: "Assigned to you",
      body: "The conversation with Rana is yours now.\nhi",
    })
    expect(alertText(t, { kind: "message", sender: "Rana", snippet: "hello" })).toEqual({ title: "New message from Rana", body: "hello" })
    expect(alertText(t, { kind: "notification", title: "Deal won" }).title).toBe("Deal won")
  })
})

describe("live events", () => {
  it("parses the SSE payload and ignores junk", () => {
    const ev = parseLive({ event: "message", data: JSON.stringify({ kind: "assigned", conversation_id: "c1", alert: true }) } as never)
    expect(ev?.kind).toBe("assigned")
    expect(ev?.conversation_id).toBe("c1")
    expect(parseLive({ event: "message", data: "not json" } as never)).toBeNull()
  })
})

describe("stageName", () => {
  it("translates the default pipeline's stages by name, whichever language seeded them", () => {
    const en = translator("en")
    const ar = translator("ar")
    expect(["Lead", "Qualified", "Proposal", "Won", "Lost"].map((s) => stageName(ar, s))).toEqual(["عميل محتمل", "مؤهَّل", "عرض سعر", "تم الفوز", "خسارة"])
    expect(stageName(en, "عرض سعر")).toBe("Proposal")
    expect(stageName(en, " lead ")).toBe("Lead")
  })

  it("keeps a custom stage's own name", () => {
    expect(stageName(translator("ar"), "Negotiation")).toBe("Negotiation")
    expect(stageName(translator("en"), undefined)).toBe("")
  })

  it("labels a chip with locale digits", () => {
    const ar = translator("ar")
    expect(ar("deals.chip", { stage: stageName(ar, "Lead"), n: num("ar", 2) })).toBe("عميل محتمل · ٢")
    expect(translator("en")("deals.chip", { stage: "Won", n: num("en", 1200) })).toBe("Won · 1,200")
  })
})

describe("money", () => {
  const LRM_RLM_ALM = /[‎‏؜]/

  it("leaves English amounts as Intl writes them", () => {
    expect(money("en", 2400000, "USD")).toBe("$24,000")
    expect(money("en", 1250, "USD")).toBe("$12.50")
  })

  it("isolates Arabic amounts so the symbol can't flip around the number", () => {
    const s = money("ar", 2400000, "USD")
    expect(s).toBe("⁧٢٤٬٠٠٠ ⁨US$⁩⁩")
    expect(s).not.toMatch(LRM_RLM_ALM)
    expect(money("ar", 3000000, "USD")).toContain("٣٠٬٠٠٠")
  })

  it("keeps Arabic currency symbols whole and handles a missing currency", () => {
    const egp = money("ar", 150000, "EGP")
    expect(egp.startsWith("⁧١٬٥٠٠ ⁨")).toBe(true)
    expect(egp).toContain("ج.م.")
    expect(money("ar", null, undefined)).toBe("⁧٠ ⁨US$⁩⁩")
  })

  it("falls back to the code when Intl rejects the currency", () => {
    expect(money("en", 500, "NOT-A-CODE")).toBe("5 NOT-A-CODE")
    expect(money("ar", 500, "NOT-A-CODE")).toBe("⁧٥ ⁨NOT-A-CODE⁩⁩")
  })
})
