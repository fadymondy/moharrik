/*
  Moharrik chat widget.

  Paste on any site:
    <script src="https://app.moharrik.com/chat/v1.js" data-key="cwk_…" defer></script>

  Options on the same tag: data-locale="ar", data-theme="light|dark",
  data-position="start" (default is the inline end), data-launcher="false"
  (then call window.MoharrikChat.open()).

  No dependencies, no build step, and everything lives in a shadow root so the
  customer's CSS cannot bleed in and this cannot bleed out.

  The API origin is taken from this script's own src, not hardcoded: the widget
  is served by the same app that answers /api/chat/*, and the Origin header the
  browser sends is the customer's site, which is what the key's allow-list is
  checked against.
*/
;(function () {
  "use strict"

  var self = document.currentScript
  if (!self) return
  var key = self.getAttribute("data-key")
  if (!key) {
    console.warn("[moharrik-chat] no data-key on the script tag")
    return
  }

  var api = new URL(self.src, location.href).origin
  var locale = (self.getAttribute("data-locale") || document.documentElement.lang || "en").slice(0, 2).toLowerCase()
  if (locale !== "ar") locale = "en"
  var rtl = locale === "ar"
  var theme = self.getAttribute("data-theme") || ""
  var atStart = self.getAttribute("data-position") === "start"
  var showLauncher = self.getAttribute("data-launcher") !== "false"

  var T = {
    en: {
      title: "Chat with us",
      placeholder: "Write a message…",
      send: "Send",
      launcher: "Chat with us",
      close: "Close chat",
      sending: "Sending…",
      failed: "That did not send. Try again.",
      blocked: "This chat is not available on this site.",
      unknown: "This chat is not set up.",
      you: "You",
      them: "Support",
      empty: "Send a message and we will reply here.",
    },
    ar: {
      title: "تحدّث معنا",
      placeholder: "اكتب رسالة…",
      send: "إرسال",
      launcher: "تحدّث معنا",
      close: "إغلاق المحادثة",
      sending: "جارٍ الإرسال…",
      failed: "لم تُرسل الرسالة. حاول مرة أخرى.",
      blocked: "هذه المحادثة غير متاحة على هذا الموقع.",
      unknown: "هذه المحادثة غير مُعدّة.",
      you: "أنت",
      them: "الدعم",
      empty: "أرسل رسالة وسنرد عليك هنا.",
    },
  }[locale]

  var STORE = "moharrik.chat." + key
  var visitorToken = null
  try {
    visitorToken = localStorage.getItem(STORE)
  } catch (e) {
    /* private mode: the conversation simply does not resume */
  }

  var host = document.createElement("div")
  host.setAttribute("data-moharrik-chat", "")
  var root = host.attachShadow({ mode: "open" })
  document.body.appendChild(host)

  var dark =
    theme === "dark" ||
    (theme !== "light" && window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches)

  var css = [
    ":host{all:initial}",
    "*{box-sizing:border-box;font-family:ui-sans-serif,system-ui,-apple-system,'Segoe UI',Roboto,'Helvetica Neue',Arial,'Noto Sans Arabic',sans-serif}",
    ".wrap{position:fixed;bottom:20px;" + (atStart ? (rtl ? "right" : "left") : rtl ? "left" : "right") + ":20px;z-index:2147483000;direction:" + (rtl ? "rtl" : "ltr") + "}",
    ".btn{display:inline-flex;align-items:center;gap:8px;border:0;border-radius:999px;padding:12px 18px;cursor:pointer;font-size:14px;font-weight:600;background:#00A0A8;color:#0B1429;box-shadow:0 6px 24px rgba(0,0,0,.18)}",
    ".panel{display:none;flex-direction:column;width:340px;max-width:calc(100vw - 40px);height:460px;max-height:calc(100vh - 120px);border-radius:14px;overflow:hidden;box-shadow:0 12px 40px rgba(0,0,0,.28);background:" + (dark ? "#0F1B33" : "#ffffff") + ";color:" + (dark ? "#E7ECF5" : "#0B1429") + ";border:1px solid " + (dark ? "#24324f" : "#e4e7ee") + "}",
    ".panel.open{display:flex}",
    ".head{display:flex;align-items:center;justify-content:space-between;gap:8px;padding:12px 14px;background:#00A0A8;color:#0B1429;font-weight:600;font-size:14px}",
    ".x{border:0;background:transparent;cursor:pointer;font-size:18px;line-height:1;color:#0B1429;padding:2px 6px}",
    ".log{flex:1;overflow-y:auto;padding:12px;display:flex;flex-direction:column;gap:8px}",
    ".msg{max-width:85%;padding:8px 11px;border-radius:12px;font-size:13.5px;line-height:1.45;white-space:pre-wrap;word-break:break-word}",
    ".in{align-self:flex-start;background:" + (dark ? "#1b2a47" : "#eef1f6") + "}",
    ".out{align-self:flex-end;background:#00A0A8;color:#0B1429}",
    ".who{font-size:11px;opacity:.65;margin:0 2px}",
    ".note{font-size:12.5px;opacity:.7;text-align:center;padding:10px}",
    ".err{font-size:12.5px;color:#d9455f;padding:0 12px 6px}",
    ".form{display:flex;gap:8px;padding:10px;border-top:1px solid " + (dark ? "#24324f" : "#e4e7ee") + "}",
    ".ta{flex:1;resize:none;height:38px;max-height:110px;padding:9px 10px;border-radius:9px;font-size:13.5px;border:1px solid " + (dark ? "#2b3a5c" : "#d7dce6") + ";background:" + (dark ? "#0B1429" : "#fff") + ";color:inherit}",
    ".ta:focus{outline:2px solid #00A0A8;outline-offset:1px}",
    ".send{border:0;border-radius:9px;padding:0 14px;cursor:pointer;font-size:13.5px;font-weight:600;background:#00A0A8;color:#0B1429}",
    ".send:disabled{opacity:.55;cursor:default}",
  ].join("")

  root.innerHTML =
    "<style>" +
    css +
    "</style>" +
    '<div class="wrap">' +
    '<div class="panel" part="panel" role="dialog" aria-label="' +
    T.title +
    '">' +
    '<div class="head"><span>' +
    T.title +
    '</span><button class="x" type="button" aria-label="' +
    T.close +
    '">×</button></div>' +
    '<div class="log" aria-live="polite"></div>' +
    '<div class="err" hidden></div>' +
    '<form class="form"><textarea class="ta" rows="1" placeholder="' +
    T.placeholder +
    '"></textarea><button class="send" type="submit">' +
    T.send +
    "</button></form>" +
    "</div>" +
    (showLauncher ? '<button class="btn" type="button">' + T.launcher + "</button>" : "") +
    "</div>"

  var panel = root.querySelector(".panel")
  var log = root.querySelector(".log")
  var errBox = root.querySelector(".err")
  var form = root.querySelector(".form")
  var ta = root.querySelector(".ta")
  var sendBtn = root.querySelector(".send")
  var launcher = root.querySelector(".btn")
  var closeBtn = root.querySelector(".x")

  var seen = Object.create(null)
  var polling = null
  var greeted = false

  function showError(text) {
    errBox.textContent = text
    errBox.hidden = !text
  }

  function note(text) {
    if (log.querySelector(".note")) return
    var n = document.createElement("div")
    n.className = "note"
    n.textContent = text
    log.appendChild(n)
  }

  function addMessage(m) {
    if (seen[m.id]) return
    seen[m.id] = true
    var n = log.querySelector(".note")
    if (n) n.remove()
    var wrap = document.createElement("div")
    wrap.style.display = "flex"
    wrap.style.flexDirection = "column"
    wrap.style.alignItems = m.direction === "in" ? "flex-end" : "flex-start"
    var who = document.createElement("div")
    who.className = "who"
    who.textContent = m.direction === "in" ? T.you : T.them
    var bubble = document.createElement("div")
    // 'in' is inbound from the workspace's side — i.e. what THIS visitor sent.
    bubble.className = "msg " + (m.direction === "in" ? "out" : "in")
    bubble.textContent = m.body
    wrap.appendChild(who)
    wrap.appendChild(bubble)
    log.appendChild(wrap)
    log.scrollTop = log.scrollHeight
  }

  function refusalText(status) {
    if (status === 403) return T.blocked
    if (status === 404) return T.unknown
    return T.failed
  }

  function poll() {
    if (!visitorToken) return Promise.resolve()
    return fetch(api + "/api/chat/" + encodeURIComponent(key) + "/messages?visitor_token=" + encodeURIComponent(visitorToken), {
      headers: { Accept: "application/json" },
    })
      .then(function (res) {
        if (!res.ok) {
          // A token the server no longer knows (widget deleted, thread purged):
          // forget it so the next message starts a fresh conversation.
          if (res.status === 404) {
            visitorToken = null
            try {
              localStorage.removeItem(STORE)
            } catch (e) {}
          }
          return null
        }
        return res.json()
      })
      .then(function (data) {
        if (!data || !data.messages) return
        data.messages.forEach(addMessage)
      })
      .catch(function () {
        /* offline: the next poll tries again */
      })
  }

  function startPolling() {
    if (polling) return
    poll()
    polling = setInterval(poll, 5000)
  }

  function stopPolling() {
    if (!polling) return
    clearInterval(polling)
    polling = null
  }

  function open() {
    panel.classList.add("open")
    if (!greeted) {
      greeted = true
      note(T.empty)
    }
    startPolling()
    ta.focus()
  }

  function close() {
    panel.classList.remove("open")
    stopPolling()
  }

  if (launcher) launcher.addEventListener("click", function () {
    panel.classList.contains("open") ? close() : open()
  })
  closeBtn.addEventListener("click", close)

  ta.addEventListener("input", function () {
    ta.style.height = "38px"
    ta.style.height = Math.min(ta.scrollHeight, 110) + "px"
  })
  ta.addEventListener("keydown", function (e) {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault()
      form.dispatchEvent(new Event("submit", { cancelable: true, bubbles: true }))
    }
  })

  form.addEventListener("submit", function (e) {
    e.preventDefault()
    var text = ta.value.trim()
    if (!text) return
    showError("")
    sendBtn.disabled = true
    sendBtn.textContent = T.sending
    var payload = { body: text }
    if (visitorToken) payload.visitor_token = visitorToken
    fetch(api + "/api/chat/" + encodeURIComponent(key) + "/messages", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify(payload),
    })
      .then(function (res) {
        if (!res.ok) {
          showError(refusalText(res.status))
          return null
        }
        return res.json()
      })
      .then(function (data) {
        if (!data) return
        if (data.visitor_token) {
          visitorToken = data.visitor_token
          try {
            localStorage.setItem(STORE, visitorToken)
          } catch (e) {}
        }
        ta.value = ""
        ta.style.height = "38px"
        startPolling()
        return poll()
      })
      .catch(function () {
        showError(T.failed)
      })
      .finally(function () {
        sendBtn.disabled = false
        sendBtn.textContent = T.send
      })
  })

  // Resume a conversation this browser already has, so a returning visitor sees
  // the thread (and any reply) without writing again.
  if (visitorToken) poll()

  window.MoharrikChat = { open: open, close: close }
})()
