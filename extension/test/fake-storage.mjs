// chrome.storage.local as the driver uses it: get(key | keys | nothing), set,
// remove. The run-tab bookkeeping (driver/run-tabs-core.js) keeps its maps in
// storage because a service worker is stopped when idle, so the fakes need a
// storage that actually stores.
export function fakeStorage(initial = {}) {
  const map = new Map(Object.entries(initial))
  const pick = (keys) => {
    if (keys == null) return Object.fromEntries(map)
    const list = Array.isArray(keys) ? keys : typeof keys === "string" ? [keys] : Object.keys(keys)
    const out = {}
    for (const k of list) if (map.has(k)) out[k] = structuredClone(map.get(k))
    return out
  }
  return {
    local: {
      get: async (keys) => pick(keys),
      set: async (items) => {
        for (const [k, v] of Object.entries(items)) map.set(k, structuredClone(v))
      },
      remove: async (keys) => {
        for (const k of Array.isArray(keys) ? keys : [keys]) map.delete(k)
      },
    },
    onChanged: { addListener: () => {} },
  }
}
