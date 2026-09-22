/*
The recorder's page half (MH-235): watch what the operator does and send each
action to the service worker as a step.

What becomes a step:
  click   — on the nearest interactive element (a, button, role=button,
            checkbox, radio, submit, summary), not the <span> inside it
  type    — ONE step per field, holding the final value, emitted when the
            field is left or Enter is pressed. Never one step per key.
  press   — Enter and Escape
  select  — a dropdown choice, by its visible label
  scroll  — only a real move (300px or more), so a page that loads more on
            scroll replays; wheel jitter is not a step

A password field's characters never leave the page: the step is sent with
secret: true and no text, and the server turns it into a placeholder.

The page cannot keep state across navigations, so this holds nothing but the
field currently being typed into. The list of steps lives in the worker.

Injected by chrome.scripting after installSelectors, so it is one
self-contained function with no imports.
*/

/** Injected into the page. Idempotent per document. */
export function recordInPage(initialCount) {
  if (globalThis.__mohRecorder) {
    globalThis.__mohRecorder.setCount(initialCount)
    return true
  }
  const S = globalThis.__mohSelectors
  if (!S) return false

  // The bar lives in a CLOSED shadow root, so neither the page's CSS nor its
  // scripts can restyle it, click it, or read what is being recorded.
  const host = document.createElement("div")
  host.id = "__moh_record_bar__"
  host.style.cssText = "position:fixed;right:16px;bottom:16px;z-index:2147483647"
  const root = host.attachShadow({ mode: "closed" })
  root.innerHTML = `
    <style>
      .bar{display:flex;align-items:center;gap:10px;font:13px/1.4 system-ui,sans-serif;background:#140b29;color:#f3f0ff;padding:8px 10px 8px 14px;border-radius:6px;box-shadow:0 4px 18px rgba(0,0,0,.35)}
      .dot{width:9px;height:9px;border-radius:50%;background:#e5484d;animation:p 1.2s infinite}
      @keyframes p{50%{opacity:.35}}
      button{font:inherit;background:#4B00B6;color:#fff;border:0;border-radius:4px;padding:4px 10px;cursor:pointer}
    </style>
    <div class="bar"><span class="dot"></span><span class="label">Recording · 0 steps</span><button type="button">Stop</button></div>`

  const label = root.querySelector(".label")
  root.querySelector("button").addEventListener("click", (event) => {
    event.stopPropagation()
    flush()
    chrome.runtime.sendMessage({ type: "record.stop" }).catch(() => {})
  })
  document.documentElement.appendChild(host)

  const setCount = (n) => {
    label.textContent = `Recording · ${n} step${n === 1 ? "" : "s"}`
  }
  setCount(initialCount || 0)

  const send = (step) => {
    chrome.runtime
      .sendMessage({ type: "record.step", step })
      .then((reply) => {
        if (typeof reply?.count === "number") setCount(reply.count)
      })
      .catch(() => {})
  }

  // Anything inside the bar is ours, not the operator acting on the page.
  const inBar = (event) => event.composedPath().includes(host)
  const selectorOf = (el) => S.selectorFor(el)

  const isTextField = (el) => {
    if (el instanceof HTMLTextAreaElement) return true
    if (el?.isContentEditable) return true
    if (!(el instanceof HTMLInputElement)) return false
    return !["checkbox", "radio", "submit", "button", "reset", "image", "file", "range", "color"].includes(
      (el.type || "text").toLowerCase(),
    )
  }

  // The field being typed into, and its value when typing began.
  let pending = null

  function flush() {
    if (!pending) return
    const { el, initial } = pending
    pending = null
    if (!el.isConnected) return
    const value = el.isContentEditable ? el.innerText : el.value
    if (value === initial) return // focused and left without changing anything
    const secret = el instanceof HTMLInputElement && (el.type || "").toLowerCase() === "password"
    const selector = selectorOf(el)
    if (!selector) return
    send({
      command: "type",
      args: { selector, text: secret ? "" : value, clear: true },
      secret,
    })
  }

  const onFocusIn = (event) => {
    if (inBar(event)) return
    const el = event.target
    if (!isTextField(el)) return
    if (pending?.el === el) return
    flush()
    pending = { el, initial: el.isContentEditable ? el.innerText : el.value }
  }

  const onFocusOut = (event) => {
    if (pending?.el === event.target) flush()
  }

  const INTERACTIVE =
    'a[href],button,[role="button"],[role="link"],[role="tab"],[role="menuitem"],[role="option"],[role="checkbox"],[role="switch"],summary,label,input[type="checkbox"],input[type="radio"],input[type="submit"],input[type="button"]'

  // Enter in a form makes the browser click the submit button itself. That
  // click is the Enter's consequence — replaying the Enter repeats it — so it
  // is not a step of its own.
  let lastEnterAt = 0

  const onClick = (event) => {
    if (inBar(event) || !event.isTrusted) return
    if (event.detail === 0 && Date.now() - lastEnterAt < 250) return
    const target = event.target instanceof Element ? event.target : null
    if (!target) return
    // Clicking into a text field is focus; the type step already covers it.
    if (isTextField(target) || target instanceof HTMLSelectElement || target instanceof HTMLOptionElement) return
    flush()
    const el = target.closest(INTERACTIVE) || target
    if (el === document.documentElement || el === document.body) return
    const selector = selectorOf(el)
    if (selector) send({ command: "click", args: { selector } })
  }

  const onKeyDown = (event) => {
    if (inBar(event) || !event.isTrusted) return
    if (event.key !== "Enter" && event.key !== "Escape") return
    // Typing then Enter: the value first, then the key, in that order.
    flush()
    if (event.key === "Enter") lastEnterAt = Date.now()
    send({ command: "press", args: { key: event.key } })
  }

  const onChange = (event) => {
    const el = event.target
    if (!(el instanceof HTMLSelectElement)) return
    if (pending?.el !== el) flush()
    const option = el.selectedOptions[0]
    const selector = selectorOf(el)
    if (selector) {
      send({ command: "select", args: { selector, value: option ? option.label || option.text : el.value } })
    }
  }

  let scrollFrom = window.scrollY
  let scrollTimer = null
  const onScroll = () => {
    clearTimeout(scrollTimer)
    scrollTimer = setTimeout(() => {
      const dy = Math.round(window.scrollY - scrollFrom)
      if (Math.abs(dy) >= 300) {
        send({ command: "scroll", args: { dy } })
        scrollFrom = window.scrollY
      }
    }, 700)
  }

  // Leaving the page mid-field must not lose what was typed.
  const onPageHide = () => flush()

  document.addEventListener("focusin", onFocusIn, true)
  document.addEventListener("focusout", onFocusOut, true)
  document.addEventListener("click", onClick, true)
  document.addEventListener("keydown", onKeyDown, true)
  document.addEventListener("change", onChange, true)
  window.addEventListener("scroll", onScroll, { passive: true })
  window.addEventListener("pagehide", onPageHide)

  globalThis.__mohRecorder = {
    setCount,
    stop() {
      flush()
      document.removeEventListener("focusin", onFocusIn, true)
      document.removeEventListener("focusout", onFocusOut, true)
      document.removeEventListener("click", onClick, true)
      document.removeEventListener("keydown", onKeyDown, true)
      document.removeEventListener("change", onChange, true)
      window.removeEventListener("scroll", onScroll)
      window.removeEventListener("pagehide", onPageHide)
      host.remove()
      delete globalThis.__mohRecorder
    },
  }

  // A field that already had focus when recording started.
  if (document.activeElement && isTextField(document.activeElement)) {
    onFocusIn({ target: document.activeElement, composedPath: () => [] })
  }
  return true
}

/** Injected to end recording in a page: flushes the field in progress, removes the bar. */
export function stopRecordingInPage() {
  globalThis.__mohRecorder?.stop()
  return true
}
