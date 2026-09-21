/*
What a step actually does in this browser.

Every run gets its OWN tab, opened on its first navigation and closed when the
run ends. Driving whatever tab happened to be focused would mean a run typing
into somebody's half-written email.

The step vocabulary is deliberately small. Each one is a thing a person could
watch happen, and anything that cannot be seen from outside — reading storage,
executing arbitrary script — is not here. A browser extension holding a
workspace's trust should do the obvious things well rather than everything.
*/

const tabs = new Map() // run id -> tab id

const DEFAULT_TIMEOUT = 20000

export async function runStep(runID, step) {
  const command = String(step.command || "").toLowerCase()
  const args = step.args || {}

  switch (command) {
    case "goto":
    case "navigate":
    case "open":
      return goto(runID, args)
    case "click":
      return inTab(runID, click, args)
    case "type":
    case "fill":
      return inTab(runID, type, args)
    case "extract":
    case "read":
      return inTab(runID, extract, args)
    case "wait":
      return wait(runID, args)
    case "close":
      return closeRun(runID)
    default:
      throw new Error(`this browser does not know the step "${step.command}"`)
  }
}

/** closeRun drops the run's tab. Called when a run finishes, however it ended. */
export async function closeRun(runID) {
  const tabID = tabs.get(runID)
  tabs.delete(runID)
  if (tabID != null) {
    try {
      await chrome.tabs.remove(tabID)
    } catch {
      // Already gone (the person closed it). Not an error.
    }
  }
  return { summary: "closed" }
}

async function goto(runID, args) {
  const url = String(args.url || "")
  if (!url) throw new Error("this step has no url")

  let tabID = tabs.get(runID)
  if (tabID == null) {
    const tab = await chrome.tabs.create({ url, active: false })
    tabs.set(runID, tab.id)
    tabID = tab.id
  } else {
    await chrome.tabs.update(tabID, { url })
  }
  await waitForLoad(tabID)
  const tab = await chrome.tabs.get(tabID)
  return { url: tab.url, title: tab.title, summary: tab.title || tab.url }
}

function waitForLoad(tabID, timeout = DEFAULT_TIMEOUT) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      chrome.tabs.onUpdated.removeListener(listener)
      // Resolved, not rejected: a page that never reaches "complete" (a long
      // poll, a stuck tracker) is still usable, and failing the step here
      // would make half the web unautomatable.
      resolve()
    }, timeout)
    function listener(id, info) {
      if (id === tabID && info.status === "complete") {
        clearTimeout(timer)
        chrome.tabs.onUpdated.removeListener(listener)
        resolve()
      }
    }
    chrome.tabs.onUpdated.addListener(listener)
  })
}

async function inTab(runID, fn, args) {
  const tabID = tabs.get(runID)
  if (tabID == null) throw new Error("this run has not opened a page yet — start with a goto step")
  return fn(tabID, args)
}

async function click(tabID, args) {
  const selector = String(args.selector || "")
  if (!selector) throw new Error("this step has no selector")
  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId: tabID },
    args: [selector],
    func: (sel) => {
      const el = document.querySelector(sel)
      if (!el) return { ok: false }
      el.scrollIntoView({ block: "center" })
      el.click()
      return { ok: true, text: (el.textContent || "").trim().slice(0, 120) }
    },
  })
  if (!result?.ok) throw new Error(`nothing on the page matches ${selector}`)
  return { summary: `clicked ${selector}`, text: result.text }
}

async function type(tabID, args) {
  const selector = String(args.selector || "")
  const text = String(args.text ?? "")
  if (!selector) throw new Error("this step has no selector")
  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId: tabID },
    args: [selector, text],
    func: (sel, value) => {
      const el = document.querySelector(sel)
      if (!el) return { ok: false }
      el.focus()
      // Set through the native setter and fire input/change, or a React-style
      // field ignores the value entirely.
      const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype
      const setter = Object.getOwnPropertyDescriptor(proto, "value")?.set
      if (setter) setter.call(el, value)
      else el.value = value
      el.dispatchEvent(new Event("input", { bubbles: true }))
      el.dispatchEvent(new Event("change", { bubbles: true }))
      return { ok: true }
    },
  })
  if (!result?.ok) throw new Error(`nothing on the page matches ${selector}`)
  // The value itself is NOT echoed back: a "type" step is how a password
  // reaches a page, and the result travels to the server and into a run record.
  return { summary: `typed into ${selector}` }
}

async function extract(tabID, args) {
  const selector = String(args.selector || "body")
  const all = !!args.all
  const attribute = args.attribute ? String(args.attribute) : ""
  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId: tabID },
    args: [selector, all, attribute],
    func: (sel, many, attr) => {
      const read = (el) => (attr ? el.getAttribute(attr) : (el.innerText || el.textContent || "").trim())
      if (many) {
        return { values: [...document.querySelectorAll(sel)].slice(0, 200).map(read) }
      }
      const el = document.querySelector(sel)
      return el ? { value: read(el) } : null
    },
  })
  if (result == null) throw new Error(`nothing on the page matches ${selector}`)
  if (result.values) {
    return { values: result.values, count: result.values.length, summary: `${result.values.length} matches for ${selector}` }
  }
  const value = String(result.value ?? "")
  return { value, summary: value.slice(0, 120) }
}

async function wait(runID, args) {
  const ms = Math.min(Number(args.ms) || 1000, 30000)
  const selector = args.selector ? String(args.selector) : ""
  if (!selector) {
    await new Promise((r) => setTimeout(r, ms))
    return { summary: `waited ${ms}ms` }
  }
  const tabID = tabs.get(runID)
  if (tabID == null) throw new Error("this run has not opened a page yet — start with a goto step")
  const deadline = Date.now() + ms
  for (;;) {
    const [{ result }] = await chrome.scripting.executeScript({
      target: { tabId: tabID },
      args: [selector],
      func: (sel) => !!document.querySelector(sel),
    })
    if (result) return { summary: `${selector} appeared` }
    if (Date.now() > deadline) throw new Error(`${selector} did not appear`)
    await new Promise((r) => setTimeout(r, 250))
  }
}
