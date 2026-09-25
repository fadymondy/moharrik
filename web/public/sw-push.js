/*
Moharrik's service worker: web push only (MH-378). No caching, no offline
pages — it exists so a member hears about a customer's message when no Moharrik
tab is open.

The payload is the JSON internal/alerts/push.go encrypts to this browser:
  { kind, title, body, url, tag, conversation_id?, item_id? }
Everything in it was decided per member on the server; this file only shows it.

When a Moharrik tab is focused, that tab already raised its own popup from the
event stream, so the push is handed to it (it drops the duplicate) instead of
also putting a notification on screen. A test push is always shown: showing it
is the point of the test.
*/

self.addEventListener("install", () => self.skipWaiting())
self.addEventListener("activate", (event) => event.waitUntil(self.clients.claim()))

self.addEventListener("push", (event) => {
  let data = {}
  try {
    data = event.data ? event.data.json() : {}
  } catch {
    data = { title: event.data ? event.data.text() : "" }
  }
  const title = data.title || "Moharrik"
  event.waitUntil(
    (async () => {
      const tabs = await self.clients.matchAll({ type: "window", includeUncontrolled: true })
      for (const tab of tabs) tab.postMessage({ type: "moharrik-push", payload: data })
      const watching = tabs.some((tab) => tab.focused && tab.visibilityState === "visible")
      if (watching && data.kind !== "test") return
      await self.registration.showNotification(title, {
        body: data.body || "",
        // Same tag as the page's own desktop notification for the same thread:
        // the two replace each other rather than stacking.
        tag: data.tag || undefined,
        renotify: Boolean(data.tag),
        data: { url: data.url || "/" },
      })
    })(),
  )
})

self.addEventListener("notificationclick", (event) => {
  event.notification.close()
  const target = new URL(
    (event.notification.data && event.notification.data.url) || "/",
    self.location.origin,
  )
  // Only ever this origin: the URL came from our own server, and this keeps it so.
  if (target.origin !== self.location.origin) return
  event.waitUntil(
    (async () => {
      const tabs = await self.clients.matchAll({ type: "window", includeUncontrolled: true })
      const tab = tabs.find((c) => new URL(c.url).origin === target.origin)
      if (tab) {
        await tab.focus()
        return tab.navigate(target.href)
      }
      return self.clients.openWindow(target.href)
    })(),
  )
})
