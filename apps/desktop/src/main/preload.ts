// The window's only door out: a fixed set of invoke channels and event channels, exposed as
// window.moharrik. No Node, no ipcRenderer, no arbitrary channel names reach the page.
import { contextBridge, ipcRenderer, type IpcRendererEvent } from "electron"

import { EVENTS, INVOKE } from "../shared/types"

const invoke = new Set<string>(INVOKE)
const events = new Set<string>(EVENTS)

contextBridge.exposeInMainWorld("moharrik", {
  platform: process.platform,
  invoke(channel: string, ...args: unknown[]): Promise<unknown> {
    if (!invoke.has(channel)) return Promise.reject(new Error(`blocked channel ${channel}`))
    return ipcRenderer.invoke(channel, ...args)
  },
  on(channel: string, cb: (...args: unknown[]) => void): () => void {
    if (!events.has(channel)) throw new Error(`blocked channel ${channel}`)
    const listener = (_e: IpcRendererEvent, ...args: unknown[]) => cb(...args)
    ipcRenderer.on(channel, listener)
    return () => ipcRenderer.removeListener(channel, listener)
  },
})
