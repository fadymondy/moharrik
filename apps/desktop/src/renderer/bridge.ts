// window.moharrik (preload.ts), typed. The page has no network of its own: every API call is a
// named method the main process runs with the session's token (main.ts API).
import type { Api } from "@moharrik/client"

import type { ApiMethod, ApiResult, Bridge, EventChannel, InvokeChannel } from "../shared/types"

declare global {
  interface Window {
    moharrik: Bridge
  }
}

export const bridge = window.moharrik

export const invoke = <T = unknown>(channel: InvokeChannel, ...args: unknown[]) => bridge.invoke<T>(channel, ...args)

export function on<A extends unknown[]>(channel: EventChannel, cb: (...args: A) => void): () => void {
  return bridge.on(channel, cb as unknown as (...args: never[]) => void)
}

export class CallError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

/** The API client's methods, run in the main process. Same names and shapes as on the phone. */
export const api = new Proxy({} as Api, {
  get: (_t, method: string) => async (...args: unknown[]) => {
    const r = await invoke<ApiResult>("api", method as ApiMethod, args)
    if (!r.ok) throw new CallError(r.status, r.message)
    return r.data
  },
})
