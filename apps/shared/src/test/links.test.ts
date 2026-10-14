import { describe, expect, it } from "vitest"

import { linkOf, siteUrl, targetOfPush, targetOfUrl, type Target } from "../index"

describe("deep links", () => {
  it("routes a message push to its conversation in its workspace", () => {
    expect(
      targetOfPush({
        kind: "message",
        workspace_id: "w1",
        slug: "acme",
        conversation_id: "c9",
        url: "https://app.moharrik.com/ar/w/acme/inbox?c=c9",
      }),
    ).toEqual({ screen: "conversation", workspaceId: "w1", slug: "acme", id: "c9" })
  })

  it("routes an assignment and a notification", () => {
    expect(targetOfPush({ kind: "assigned", workspace_id: "w1", conversation_id: "c1" })?.screen).toBe("conversation")
    expect(targetOfPush({ kind: "notification", workspace_id: "w1", item_id: "n1" })).toEqual({
      screen: "notifications",
      workspaceId: "w1",
      slug: undefined,
      itemId: "n1",
    })
    expect(targetOfPush({})).toBeNull()
    expect(targetOfPush(null)).toBeNull()
  })

  it("reads app links and web links of the same page", () => {
    expect(targetOfUrl("moharrik://w/acme/inbox/c9")).toEqual({ screen: "conversation", slug: "acme", id: "c9" })
    expect(targetOfUrl("https://app.moharrik.com/en/w/acme/inbox?c=c9")).toEqual({ screen: "conversation", slug: "acme", id: "c9" })
    expect(targetOfUrl("moharrik://w/acme/notifications")).toEqual({ screen: "notifications", slug: "acme", itemId: undefined })
    expect(targetOfUrl("moharrik://w/acme")).toEqual({ screen: "inbox", slug: "acme" })
    expect(targetOfUrl("not a url")).toBeNull()
  })

  it("linkOf round-trips", () => {
    const targets: Target[] = [
      { screen: "conversation", slug: "a b", id: "c/1" },
      { screen: "notifications", slug: "acme", itemId: "n1" },
      { screen: "inbox", slug: "acme" },
    ]
    for (const t of targets) expect(targetOfUrl(linkOf(t))).toEqual(t)
  })
})

describe("site pages", () => {
  it("links the legal and support pages in the app's language", () => {
    expect(siteUrl("en", "privacy")).toBe("https://moharrik.com/en/privacy")
    expect(siteUrl("ar", "terms")).toBe("https://moharrik.com/ar/terms")
    expect(siteUrl("ar", "contact")).toBe("https://moharrik.com/ar/contact")
  })
})
