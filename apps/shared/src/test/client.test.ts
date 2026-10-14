import { describe, expect, it, vi } from "vitest"

import { api, ApiError, MoharrikClient, qs, type TokenStore } from "../index"

function memStore(initial: string | null = null): TokenStore & { value: string | null } {
  const s = {
    value: initial,
    async get() {
      return s.value
    },
    async set(v: string | null) {
      s.value = v
    },
  }
  return s
}

function fakeFetch(handler: (url: string, init: RequestInit) => Response) {
  const calls: { url: string; init: RequestInit }[] = []
  const f = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
    calls.push({ url: String(url), init: init ?? {} })
    return handler(String(url), init ?? {})
  })
  return { f: f as unknown as typeof fetch, calls }
}

const json = (v: unknown, status = 200) =>
  new Response(JSON.stringify(v), { status, headers: { "Content-Type": "application/json" } })

describe("MoharrikClient", () => {
  it("sends the device token and the workspace, and fills path and query", async () => {
    const { f, calls } = fakeFetch(() => json({ conversations: [], unread_count: 0 }))
    const c = new MoharrikClient({ baseUrl: "https://app.moharrik.com/", store: memStore("togo_pat_x"), fetch: f, clientName: "test/1" })
    c.workspaceId = "ws1"
    await api(c).conversations({ unread: true, q: "" })
    const { url, init } = calls[0]!
    expect(url).toBe("https://app.moharrik.com/api/w/conversations?order=recent&limit=40&unread=true")
    const h = init.headers as Record<string, string>
    expect(h.Authorization).toBe("Bearer togo_pat_x")
    expect(h["X-Workspace-ID"]).toBe("ws1")
    expect(h["X-Moharrik-Client"]).toBe("test/1")

    await api(c).conversation("a/b")
    expect(calls[1]!.url).toBe("https://app.moharrik.com/api/w/conversations/a%2Fb")
  })

  it("leaves the workspace out of account calls", async () => {
    const { f, calls } = fakeFetch(() => json({ workspaces: [] }))
    const c = new MoharrikClient({ baseUrl: "http://x", store: memStore("t"), fetch: f })
    c.workspaceId = "ws1"
    await api(c).workspaces()
    expect((calls[0]!.init.headers as Record<string, string>)["X-Workspace-ID"]).toBeUndefined()
  })

  it("sends JSON bodies", async () => {
    const { f, calls } = fakeFetch(() => json({ conversation: {} }))
    const c = new MoharrikClient({ baseUrl: "http://x", store: memStore("t"), fetch: f })
    await api(c).updateConversation("c1", { status: "closed" })
    expect(calls[0]!.init.method).toBe("PATCH")
    expect(JSON.parse(String(calls[0]!.init.body))).toEqual({ status: "closed" })
    expect((calls[0]!.init.headers as Record<string, string>)["Content-Type"]).toBe("application/json")
  })

  it("surfaces the API's problem detail", async () => {
    const { f } = fakeFetch(() => json({ title: "Unprocessable Entity", detail: "body is required", status: 422 }, 422))
    const c = new MoharrikClient({ baseUrl: "http://x", store: memStore("t"), fetch: f })
    const err = await api(c)
      .reply("c1", "")
      .catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(422)
    expect((err as ApiError).message).toBe("body is required")
  })

  it("forgets a refused token once and reports the sign-out", async () => {
    const { f } = fakeFetch(() => json({ title: "Unauthorized", status: 401 }, 401))
    const store = memStore("togo_pat_old")
    const onSignedOut = vi.fn()
    const c = new MoharrikClient({ baseUrl: "http://x", store, fetch: f, onSignedOut })
    await expect(api(c).bell()).rejects.toBeInstanceOf(ApiError)
    expect(store.value).toBeNull()
    expect(onSignedOut).toHaveBeenCalledTimes(1)
    // Signed out now: a further 401 has no token to forget.
    await expect(api(c).bell()).rejects.toBeInstanceOf(ApiError)
    expect(onSignedOut).toHaveBeenCalledTimes(1)
  })

  it("renews an expired token once and retries", async () => {
    const store = memStore("togo_pat_old")
    const refreshStore = memStore("mrt_old")
    const { f, calls } = fakeFetch((url, init) => {
      if (url.endsWith("/api/auth/app/refresh")) {
        expect(JSON.parse(String(init.body))).toEqual({ refresh_token: "mrt_old" })
        return json({ token: "togo_pat_new", refresh_token: "mrt_new", email: "a@b.c", expires_at: "x" })
      }
      const auth = (init.headers as Record<string, string>).Authorization
      return auth === "Bearer togo_pat_new" ? json({ unread: 0 }) : json({ title: "Unauthorized" }, 401)
    })
    const onSignedOut = vi.fn()
    const c = new MoharrikClient({ baseUrl: "http://x", store, refreshStore, fetch: f, onSignedOut })
    // Two calls fail together: one refresh serves both.
    await Promise.all([api(c).bell(), api(c).bell()])
    expect(calls.filter((x) => x.url.endsWith("/refresh"))).toHaveLength(1)
    expect(store.value).toBe("togo_pat_new")
    expect(refreshStore.value).toBe("mrt_new")
    expect(onSignedOut).not.toHaveBeenCalled()
  })

  it("signs out when the refresh token is refused too", async () => {
    const store = memStore("togo_pat_old")
    const refreshStore = memStore("mrt_old")
    const { f } = fakeFetch(() => json({ title: "Unauthorized" }, 401))
    const onSignedOut = vi.fn()
    const c = new MoharrikClient({ baseUrl: "http://x", store, refreshStore, fetch: f, onSignedOut })
    await expect(api(c).bell()).rejects.toBeInstanceOf(ApiError)
    expect(store.value).toBeNull()
    expect(refreshStore.value).toBeNull()
    expect(onSignedOut).toHaveBeenCalledTimes(1)
  })

  it("returns undefined for 204", async () => {
    const { f } = fakeFetch(() => new Response(null, { status: 204 }))
    const c = new MoharrikClient({ baseUrl: "http://x", store: memStore("t"), fetch: f })
    await expect(api(c).signOut()).resolves.toBeUndefined()
  })

  it("qs drops empty values", () => {
    expect(qs({ a: "", b: undefined, c: 0, d: false, e: "x y" })).toBe("?c=0&d=false&e=x+y")
    expect(qs({})).toBe("")
  })
})
