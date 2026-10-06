/*
The inbox is a dock on every workspace page (MH-1147), not a page. A link opens it over
whatever page is showing: `?inbox=<conversation id>` opens that thread, `?inbox=unread`
the unread list, `?inbox=all` the list; `&inbox_channel=<type>` starts it on that
channel's tab. InboxDock reads the parameter, opens, and takes it
back out of the address bar.

Query-only on purpose: the link keeps the reader on the page they were on. Links that
leave the app (a web push, a desktop notification) still use /inbox?c=<id>, which
redirects to the workspace home with the dock open.
*/

export const INBOX_PARAM = "inbox"
export const INBOX_CHANNEL_PARAM = "inbox_channel"

export function inboxHref(target: string = "all", channel?: string): string {
  const q = `?${INBOX_PARAM}=${encodeURIComponent(target)}`
  return channel ? `${q}&${INBOX_CHANNEL_PARAM}=${encodeURIComponent(channel)}` : q
}

// The conversation open in the dock right now, if the dock is open: live alerts skip a
// popup for the thread the reader is already looking at. Module state, not context, as
// the alerts provider sits above the workspace shell that mounts the dock.
let openId: string | null = null

export function setOpenConversation(id: string | null) {
  openId = id
}

export function openConversation(): string | null {
  return openId
}
