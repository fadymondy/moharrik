/*
Automation tabs (MH-478, ported from fadymondy.com's driver/tabs.js, FM-255):
the only tabs a run may drive.

Everything a workspace sends — a workflow step, a schedule, an MCP call — runs
in tabs this extension opened itself, in a separate, unfocused "Moharrik"
window, grouped and labelled. The operator's own window, tabs and focus are
never touched. (Before this, each run opened a background tab in whatever
window was focused.)

The set of tabs is kept in chrome.storage.local because the service worker is
killed when idle; a tab the operator closes leaves the set.

The window is desktop-sized — 1440×900 unless the server says otherwise at
hello — because sites lay out for the window they get, and a workflow
recorded on a desktop layout breaks on a tablet one.
*/

import { PERSISTENT_TABS_KEY, RUN_TABS_KEY } from "./run-tabs-core.js"

const OWNED_KEY = "moh.automationTabs"
const WINDOW_KEY = "moh.automationWindow"
const GROUP_TITLE = "Moharrik"

async function readOwned() {
  const stored = (await chrome.storage.local.get(OWNED_KEY))[OWNED_KEY]
  return Array.isArray(stored) ? stored : []
}

async function writeOwned(ids) {
  await chrome.storage.local.set({ [OWNED_KEY]: Array.from(new Set(ids)) })
}

/** Whether a tab was opened by the extension for automation (and still exists). */
export async function isAutomationTab(tabId) {
  if (!(await readOwned()).includes(tabId)) return false
  try {
    await chrome.tabs.get(tabId)
    return true
  } catch {
    await writeOwned((await readOwned()).filter((id) => id !== tabId))
    return false
  }
}

globalThis.chrome?.tabs?.onRemoved?.addListener((tabId) => {
  void readOwned().then((ids) => (ids.includes(tabId) ? writeOwned(ids.filter((id) => id !== tabId)) : undefined))
})

async function windowSize() {
  const agent = (await chrome.storage.local.get("moh.agent"))["moh.agent"] || {}
  const w = Number(agent.automationWindow?.width)
  const h = Number(agent.automationWindow?.height)
  return w >= 800 && h >= 600 ? { width: w, height: h } : { width: 1440, height: 900 }
}

async function automationWindowId() {
  if (!chrome.windows?.create) return null // a harness without windows: tabs go wherever Chrome puts them
  const stored = (await chrome.storage.local.get(WINDOW_KEY))[WINDOW_KEY]
  const size = await windowSize()
  if (typeof stored === "number") {
    try {
      const win = await chrome.windows.get(stored)
      if (win.state === "normal" && (win.width !== size.width || win.height !== size.height)) {
        await chrome.windows.update(stored, { width: size.width, height: size.height }).catch(() => {})
      }
      return stored
    } catch {
      /* closed; make another */
    }
  }
  // Unfocused, so opening it does not pull the operator away from their work.
  const win = await chrome.windows.create({ focused: false, type: "normal", url: "about:blank", ...size })
  await chrome.storage.local.set({ [WINDOW_KEY]: win.id })
  const first = win.tabs?.[0]
  if (first?.id !== undefined) {
    await writeOwned([...(await readOwned()), first.id])
    await group(first.id, win.id)
  }
  return win.id
}

async function group(tabId, windowId) {
  if (!chrome.tabGroups || windowId == null) return
  try {
    const existing = await chrome.tabGroups.query({ windowId, title: GROUP_TITLE })
    const groupId = await chrome.tabs.group(
      existing[0] ? { tabIds: [tabId], groupId: existing[0].id } : { tabIds: [tabId], createProperties: { windowId } },
    )
    await chrome.tabGroups.update(groupId, { title: GROUP_TITLE, color: "cyan", collapsed: false })
  } catch {
    /* grouping is a courtesy; the owned set is what enforces */
  }
}

/** Tabs a run holds, and kept site tabs: nothing else may reuse them. */
export async function reservedTabIds() {
  const stored = await chrome.storage.local.get([RUN_TABS_KEY, PERSISTENT_TABS_KEY])
  const ids = new Set()
  for (const map of [stored[RUN_TABS_KEY], stored[PERSISTENT_TABS_KEY]]) {
    for (const entry of Object.values(map || {})) if (typeof entry?.tab === "number") ids.add(entry.tab)
  }
  return ids
}

/** Automation tabs that still exist. */
export async function automationTabIds() {
  const out = []
  for (const id of await readOwned()) {
    try {
      await chrome.tabs.get(id)
      out.push(id)
    } catch {
      /* gone */
    }
  }
  return out
}

/**
 * A NEW automation tab for one run, opened in the background of the Moharrik
 * window: several runs share that window, and one run's tab becoming active
 * must not be what another run depends on.
 */
export async function openBackgroundAutomationTab() {
  const windowId = await automationWindowId()
  const created = await chrome.tabs.create({ ...(windowId != null ? { windowId } : {}), url: "about:blank", active: false })
  await writeOwned([...(await readOwned()), created.id])
  await group(created.id, windowId)
  return created.id
}
