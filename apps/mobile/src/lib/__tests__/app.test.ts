import { translator } from "@moharrik/client"

import { eventText } from "../events"
import { routeOf } from "../navigate"

jest.mock("../session", () => ({ useSession: jest.fn() }))

describe("routeOf", () => {
  it("opens a conversation inside its workspace", () => {
    expect(routeOf({ screen: "conversation", id: "c 1", workspaceId: "w1" })).toBe("/conversation/c%201?ws=w1")
  })
  it("falls back to the slug, and to no workspace at all", () => {
    expect(routeOf({ screen: "notifications", slug: "acme" })).toBe("/notifications?ws=acme")
    expect(routeOf({ screen: "inbox" })).toBe("/inbox")
  })
})

describe("eventText", () => {
  const en = translator("en")
  const ar = translator("ar")
  it("names a stage move and keeps the deal title as the body", () => {
    expect(eventText(en, { kind: "deal.stage_changed", data: { stage: "Proposal", title: "Website" } })).toEqual({ title: "Deal moved to Proposal", body: "Website" })
  })
  it("translates lifecycle stages", () => {
    expect(eventText(ar, { kind: "lifecycle.changed", data: { from: "lead", to: "customer" } }).title).toContain(ar("lifecycle.customer"))
  })
  it("groups activity kinds and shows unknown kinds as-is", () => {
    expect(eventText(en, { kind: "activity.call", data: {} }).title).toBe("Activity: call")
    expect(eventText(en, { kind: "something.new", data: {} }).title).toBe("something.new")
  })
})
