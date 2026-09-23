/*
Tabs for runs, bound to Chrome (MH-478; the rules are in run-tabs-core.js,
ported from fadymondy.com FM-267).

Every run gets its own tab in the Moharrik window and only ever acts in it;
when it ends, the tab is emptied and kept for the next run, or closed. A run
may instead be bound to a KEPT site tab by key (the server says which, with
the step): one tab per key that stays open, so a run reads the live page
rather than reloading it.

How many tabs, and which kept tabs exist, come from the server at hello.ok;
the defaults below are used against an older server.
*/

import { detach } from "./cdp.js"
import { createRunTabs, DEFAULT_MAX_AGE_MS, signedOutAt } from "./run-tabs-core.js"
import { automationTabIds, openBackgroundAutomationTab } from "./tabs.js"

const DEFAULT_TABS_PER_BROWSER = 3

// What hello.ok said (background.js keeps it in moh.agent), cached for the
// synchronous limits() and persistentConfig().
let agentInfo = {}
async function refreshAgentInfo() {
  try {
    agentInfo = (await chrome.storage.local.get("moh.agent"))["moh.agent"] || {}
  } catch {
    agentInfo = {}
  }
}
void refreshAgentInfo()
globalThis.chrome?.storage?.onChanged?.addListener((changes, area) => {
  if (area === "local" && changes["moh.agent"]) agentInfo = changes["moh.agent"].newValue || {}
})

function tabsPerBrowser() {
  const n = Number(agentInfo.tabsPerBrowser)
  return Number.isInteger(n) && n > 0 ? n : DEFAULT_TABS_PER_BROWSER
}

/** How many runs this browser can hold tabs for at once; reported at hello. */
export function tabCapacity() {
  return tabsPerBrowser()
}

function persistentList() {
  return Array.isArray(agentInfo.persistentTabs) ? agentInfo.persistentTabs : []
}

export const runTabs = createRunTabs({
  storage: {
    get: async (key) => (await chrome.storage.local.get(key))[key],
    set: (key, value) => chrome.storage.local.set({ [key]: value }),
  },
  tabs: {
    exists: async (tabId) => {
      try {
        await chrome.tabs.get(tabId)
        return true
      } catch {
        return false
      }
    },
    info: async (tabId) => {
      try {
        const tab = await chrome.tabs.get(tabId)
        return { url: tab.url || tab.pendingUrl || "", discarded: Boolean(tab.discarded), frozen: Boolean(tab.frozen) }
      } catch {
        return null
      }
    },
    owned: () => automationTabIds(),
    isIdle: async (tabId) => {
      try {
        const tab = await chrome.tabs.get(tabId)
        const url = tab.url || tab.pendingUrl || ""
        return url === "" || url === "about:blank" || url === "chrome://newtab/"
      } catch {
        return false
      }
    },
    open: async ({ persistent } = {}) => {
      const tabId = await openBackgroundAutomationTab()
      if (persistent) {
        // Memory Saver must not throw the kept session away; pinned so it reads
        // as a kept tab — in the Moharrik window only.
        try {
          await chrome.tabs.update(tabId, { autoDiscardable: false, pinned: true })
        } catch {
          /* best effort */
        }
      }
      return tabId
    },
    blank: async (tabId) => {
      await chrome.tabs.update(tabId, { url: "about:blank" })
    },
    close: async (tabId) => {
      try {
        await chrome.tabs.remove(tabId)
      } catch {
        /* already gone */
      }
    },
  },
  debugger: { detach },
  limits: () => ({ maxTabs: tabsPerBrowser(), maxIdle: tabsPerBrowser() }),
  persistentConfig: (key) => {
    const entry = persistentList().find((p) => p.key === key)
    if (!entry) return { match: "", maxAgeMs: DEFAULT_MAX_AGE_MS }
    const minutes = Number(entry.max_age_minutes)
    return { match: entry.match || "", maxAgeMs: minutes > 0 ? minutes * 60_000 : DEFAULT_MAX_AGE_MS }
  },
})

/** Is a kept tab signed out? A sentence, or "". The URL tells for most sites. */
export async function signedOut(key, tabId) {
  try {
    const tab = await chrome.tabs.get(tabId)
    return signedOutAt(key, tab.url || "")
  } catch {
    return ""
  }
}
