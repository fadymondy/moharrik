// The apps' strings, Arabic-first. One dictionary for phone and desktop so both say the same
// thing; the web's wording (web/lang/*.json) is the reference where a string exists there too.

export type Locale = "ar" | "en"

const en = {
  "app.name": "Moharrik",
  "app.tagline": "Your inbox, CRM and alerts, on the go.",

  "common.retry": "Try again",
  "common.cancel": "Cancel",
  "common.save": "Save",
  "common.saved": "Saved",
  "common.back": "Back",
  "common.loading": "Loading…",
  "common.networkError": "Can't reach Moharrik. Check your connection and try again.",
  "common.search": "Search",
  "common.done": "Done",
  "common.more": "Load more",
  "common.none": "None",

  "signin.title": "Sign in to Moharrik",
  "signin.body": "You'll sign in in your browser, the same way as on app.moharrik.com, then come straight back here.",
  "signin.button": "Sign in with the browser",
  "signin.waiting": "Finish signing in in your browser…",
  "signin.failed": "Sign-in didn't finish. Try again.",
  "signin.lost": "You were signed out. Sign in again to continue.",
  "signin.server": "Server",

  "workspaces.title": "Choose a workspace",
  "workspaces.body": "You can switch at any time from Settings.",
  "workspaces.none": "You aren't a member of any workspace yet. Create one on app.moharrik.com.",
  "workspaces.members": "{n} members",

  "tabs.inbox": "Inbox",
  "tabs.notifications": "Notifications",
  "tabs.contacts": "Contacts",
  "tabs.deals": "Deals",
  "tabs.settings": "Settings",

  "inbox.all": "All",
  "inbox.unread": "Unread",
  "inbox.open": "Open",
  "inbox.snoozed": "Snoozed",
  "inbox.closed": "Closed",
  "inbox.empty": "No conversations here.",
  "inbox.searchPlaceholder": "Search conversations",
  "inbox.you": "You",
  "inbox.replyPlaceholder": "Write a reply…",
  "inbox.send": "Send",
  "inbox.sending": "Sending…",
  "inbox.sendHint": "Replies are sent to the customer on {channel}.",
  "inbox.close": "Close",
  "inbox.reopen": "Reopen",
  "inbox.snooze": "Snooze",
  "inbox.snooze1h": "For an hour",
  "inbox.snoozeTomorrow": "Until tomorrow",
  "inbox.snoozeWeek": "Until next week",
  "inbox.snoozedUntil": "Snoozed until {at}",
  "inbox.assign": "Assign",
  "inbox.assignMe": "Assign to me",
  "inbox.unassigned": "Unassigned",
  "inbox.assignedTo": "Assigned to {name}",
  "inbox.labels": "Labels",
  "inbox.noLabels": "No labels yet. Create them on the web.",
  "inbox.markUnread": "Mark unread",
  "inbox.noMessages": "No messages yet.",
  "inbox.attachment": "Attachment",
  "inbox.group": "Group",

  "channel.email": "Email",
  "channel.whatsapp": "WhatsApp",
  "channel.chat": "Website chat",
  "channel.telegram": "Telegram",
  "channel.messenger": "Messenger",
  "channel.instagram": "Instagram",
  "channel.sms": "SMS",

  "notifications.title": "Notifications",
  "notifications.empty": "You're all caught up.",
  "notifications.markAllRead": "Mark all read",
  "notifications.unreadOnly": "Unread only",

  "contacts.contacts": "Contacts",
  "contacts.companies": "Companies",
  "contacts.searchContacts": "Search name, email or phone",
  "contacts.searchCompanies": "Search companies",
  "contacts.empty": "No matches.",
  "contacts.timeline": "Timeline",
  "contacts.timelineEmpty": "Nothing has happened with this contact yet.",
  "contacts.email": "Email",
  "contacts.phone": "Phone",
  "contacts.company": "Company",
  "contacts.lifecycle": "Stage",
  "contacts.score": "Score",
  "contacts.conversations": "Conversations",
  "lifecycle.subscriber": "Subscriber",
  "lifecycle.lead": "Lead",
  "lifecycle.qualified": "Qualified",
  "lifecycle.customer": "Customer",
  "lifecycle.none": "—",

  "event.contact.created": "Contact created",
  "event.activity": "Activity: {kind}",
  "event.deal.opened": "Deal opened in {stage}",
  "event.deal.won": "Deal won",
  "event.deal.lost": "Deal lost",
  "event.deal.stage_changed": "Deal moved to {stage}",
  "event.lifecycle.changed": "Lifecycle: {from} → {to}",
  "event.conversation": "Conversation on {channel}",
  "event.lead.submitted": "Form submitted: {form}",
  "event.rule.ran": "Automation ran: {rule}",

  "deals.title": "Deals",
  "deals.empty": "No deals in this stage.",
  "deals.noStages": "Set up the pipeline on the web first.",
  "deals.move": "Move to stage",
  "deals.moved": "Moved to {stage}",
  "deals.count": "{n} deals",

  "settings.title": "Settings",
  "settings.workspace": "Workspace",
  "settings.switch": "Switch workspace",
  "settings.push": "Push notifications",
  "settings.pushBody": "What reaches this phone and your other devices in this workspace.",
  "settings.pushMessages": "New customer messages",
  "settings.pushAssignedOnly": "Only conversations assigned to me",
  "settings.pushAssignments": "When a conversation is assigned to me",
  "settings.pushNotifications": "Notification-center items",
  "settings.pushOff": "Notifications are off for Moharrik on this device. Turn them on in the system settings.",
  "settings.language": "Language",
  "settings.appearance": "Appearance",
  "settings.system": "System",
  "settings.light": "Light",
  "settings.dark": "Dark",
  "settings.account": "Account",
  "settings.signedInAs": "Signed in as {email}",
  "settings.signOut": "Sign out",
  "settings.signOutBody": "This device stops receiving notifications and its token is revoked.",
  "settings.version": "Version {v}",
  "settings.relaunch": "The layout direction changes after the app restarts.",

  "alert.newMessage": "New message from {sender}",
  "alert.assigned": "Assigned to you",
  "alert.assignedBody": "The conversation with {sender} is yours now.",
  "alert.notification": "Notification",

  "tray.open": "Open Moharrik",
  "tray.unread": "{n} unread",
  "tray.quit": "Quit",
  "desktop.notifications": "Desktop notifications",
  "desktop.notify": "Show notifications",
  "desktop.notifyBody": "New customer messages, assignments and notification-center items, as the server decides for you.",
  "desktop.sound": "Play a sound",
  "desktop.badge": "Unread count on the app icon",
  "desktop.signinBody": "Sign in in your browser as you do on app.moharrik.com, then come back here.",
  "desktop.pick": "Pick a conversation",

  "time.now": "now",
  "time.m": "{n}m",
  "time.h": "{n}h",
  "time.d": "{n}d",
  "time.yesterday": "Yesterday",
}

export type Key = keyof typeof en

const ar: Record<Key, string> = {
  "app.name": "محرك",
  "app.tagline": "صندوق الوارد والعملاء والتنبيهات، معك أينما كنت.",

  "common.retry": "حاول مرة أخرى",
  "common.cancel": "إلغاء",
  "common.save": "حفظ",
  "common.saved": "تم الحفظ",
  "common.back": "رجوع",
  "common.loading": "جارٍ التحميل…",
  "common.networkError": "تعذّر الوصول إلى محرك. تحقّق من الاتصال وحاول مرة أخرى.",
  "common.search": "بحث",
  "common.done": "تم",
  "common.more": "تحميل المزيد",
  "common.none": "لا شيء",

  "signin.title": "تسجيل الدخول إلى محرك",
  "signin.body": "ستسجّل الدخول من المتصفح بالطريقة نفسها التي تستخدمها على app.moharrik.com، ثم تعود إلى هنا مباشرة.",
  "signin.button": "تسجيل الدخول من المتصفح",
  "signin.waiting": "أكمل تسجيل الدخول في المتصفح…",
  "signin.failed": "لم يكتمل تسجيل الدخول. حاول مرة أخرى.",
  "signin.lost": "تم تسجيل خروجك. سجّل الدخول مجددًا للمتابعة.",
  "signin.server": "الخادم",

  "workspaces.title": "اختر مساحة العمل",
  "workspaces.body": "يمكنك التبديل في أي وقت من الإعدادات.",
  "workspaces.none": "لست عضوًا في أي مساحة عمل بعد. أنشئ واحدة على app.moharrik.com.",
  "workspaces.members": "{n} أعضاء",

  "tabs.inbox": "الوارد",
  "tabs.notifications": "الإشعارات",
  "tabs.contacts": "جهات الاتصال",
  "tabs.deals": "الصفقات",
  "tabs.settings": "الإعدادات",

  "inbox.all": "الكل",
  "inbox.unread": "غير المقروءة",
  "inbox.open": "مفتوحة",
  "inbox.snoozed": "مؤجّلة",
  "inbox.closed": "مغلقة",
  "inbox.empty": "لا توجد محادثات هنا.",
  "inbox.searchPlaceholder": "ابحث في المحادثات",
  "inbox.you": "أنت",
  "inbox.replyPlaceholder": "اكتب ردًا…",
  "inbox.send": "إرسال",
  "inbox.sending": "جارٍ الإرسال…",
  "inbox.sendHint": "يُرسل الرد إلى العميل عبر {channel}.",
  "inbox.close": "إغلاق",
  "inbox.reopen": "إعادة فتح",
  "inbox.snooze": "تأجيل",
  "inbox.snooze1h": "لمدة ساعة",
  "inbox.snoozeTomorrow": "حتى الغد",
  "inbox.snoozeWeek": "حتى الأسبوع القادم",
  "inbox.snoozedUntil": "مؤجّلة حتى {at}",
  "inbox.assign": "إسناد",
  "inbox.assignMe": "أسندها إليّ",
  "inbox.unassigned": "غير مُسندة",
  "inbox.assignedTo": "مُسندة إلى {name}",
  "inbox.labels": "الوسوم",
  "inbox.noLabels": "لا توجد وسوم بعد. أنشئها من الويب.",
  "inbox.markUnread": "تعليم كغير مقروءة",
  "inbox.noMessages": "لا توجد رسائل بعد.",
  "inbox.attachment": "مرفق",
  "inbox.group": "مجموعة",

  "channel.email": "البريد الإلكتروني",
  "channel.whatsapp": "واتساب",
  "channel.chat": "دردشة الموقع",
  "channel.telegram": "تيليجرام",
  "channel.messenger": "ماسنجر",
  "channel.instagram": "إنستغرام",
  "channel.sms": "رسائل SMS",

  "notifications.title": "الإشعارات",
  "notifications.empty": "لا جديد. أنت مطّلع على كل شيء.",
  "notifications.markAllRead": "تعليم الكل كمقروء",
  "notifications.unreadOnly": "غير المقروءة فقط",

  "contacts.contacts": "جهات الاتصال",
  "contacts.companies": "الشركات",
  "contacts.searchContacts": "ابحث بالاسم أو البريد أو الهاتف",
  "contacts.searchCompanies": "ابحث في الشركات",
  "contacts.empty": "لا توجد نتائج.",
  "contacts.timeline": "السجل",
  "contacts.timelineEmpty": "لم يحدث شيء مع جهة الاتصال هذه بعد.",
  "contacts.email": "البريد الإلكتروني",
  "contacts.phone": "الهاتف",
  "contacts.company": "الشركة",
  "contacts.lifecycle": "المرحلة",
  "contacts.score": "التقييم",
  "contacts.conversations": "المحادثات",
  "lifecycle.subscriber": "مشترك",
  "lifecycle.lead": "عميل محتمل",
  "lifecycle.qualified": "مؤهَّل",
  "lifecycle.customer": "عميل",
  "lifecycle.none": "—",

  "event.contact.created": "أُنشئت جهة الاتصال",
  "event.activity": "نشاط: {kind}",
  "event.deal.opened": "فُتحت صفقة في {stage}",
  "event.deal.won": "رُبحت الصفقة",
  "event.deal.lost": "خُسرت الصفقة",
  "event.deal.stage_changed": "نُقلت الصفقة إلى {stage}",
  "event.lifecycle.changed": "دورة الحياة: {from} ← {to}",
  "event.conversation": "محادثة عبر {channel}",
  "event.lead.submitted": "أُرسل نموذج: {form}",
  "event.rule.ran": "عملت أتمتة: {rule}",

  "deals.title": "الصفقات",
  "deals.empty": "لا توجد صفقات في هذه المرحلة.",
  "deals.noStages": "أعدّ مراحل خط المبيعات من الويب أولًا.",
  "deals.move": "نقل إلى مرحلة",
  "deals.moved": "نُقلت إلى {stage}",
  "deals.count": "{n} صفقات",

  "settings.title": "الإعدادات",
  "settings.workspace": "مساحة العمل",
  "settings.switch": "تبديل مساحة العمل",
  "settings.push": "الإشعارات الفورية",
  "settings.pushBody": "ما يصل إلى هذا الهاتف وأجهزتك الأخرى في مساحة العمل هذه.",
  "settings.pushMessages": "رسائل العملاء الجديدة",
  "settings.pushAssignedOnly": "المحادثات المُسندة إليّ فقط",
  "settings.pushAssignments": "عند إسناد محادثة إليّ",
  "settings.pushNotifications": "عناصر مركز الإشعارات",
  "settings.pushOff": "الإشعارات متوقفة لمحرك على هذا الجهاز. فعّلها من إعدادات النظام.",
  "settings.language": "اللغة",
  "settings.appearance": "المظهر",
  "settings.system": "حسب النظام",
  "settings.light": "فاتح",
  "settings.dark": "داكن",
  "settings.account": "الحساب",
  "settings.signedInAs": "مسجّل الدخول باسم {email}",
  "settings.signOut": "تسجيل الخروج",
  "settings.signOutBody": "يتوقف هذا الجهاز عن تلقي الإشعارات ويُلغى رمزه.",
  "settings.version": "الإصدار {v}",
  "settings.relaunch": "يتغيّر اتجاه الواجهة بعد إعادة تشغيل التطبيق.",

  "alert.newMessage": "رسالة جديدة من {sender}",
  "alert.assigned": "أُسندت إليك",
  "alert.assignedBody": "المحادثة مع {sender} أصبحت لك الآن.",
  "alert.notification": "إشعار",

  "tray.open": "فتح محرك",
  "tray.unread": "{n} غير مقروءة",
  "tray.quit": "إنهاء",
  "desktop.notifications": "إشعارات سطح المكتب",
  "desktop.notify": "إظهار الإشعارات",
  "desktop.notifyBody": "رسائل العملاء الجديدة والإسنادات وعناصر مركز الإشعارات، كما يحددها الخادم لك.",
  "desktop.sound": "تشغيل صوت",
  "desktop.badge": "عدد غير المقروء على أيقونة التطبيق",
  "desktop.signinBody": "سجّل الدخول في المتصفح كما تفعل في app.moharrik.com، ثم عُد إلى هنا.",
  "desktop.pick": "اختر محادثة",

  "time.now": "الآن",
  "time.m": "{n} د",
  "time.h": "{n} س",
  "time.d": "{n} ي",
  "time.yesterday": "أمس",
}

export const strings: Record<Locale, Record<Key, string>> = { en, ar }

export type T = (key: Key, vars?: Record<string, string | number>) => string

export function translator(locale: Locale): T {
  const d = strings[locale]
  return (key, vars) => {
    let s = d[key] ?? en[key] ?? key
    if (vars) for (const [k, v] of Object.entries(vars)) s = s.split(`{${k}}`).join(String(v))
    return s
  }
}

export const isRTL = (l: Locale) => l === "ar"

/** A channel's display name; unknown channels show as their id. */
export function channelName(t: T, channel: string | undefined): string {
  const k = `channel.${channel ?? ""}` as Key
  return k in en ? t(k) : (channel ?? "")
}

/** A short relative time: "now", "5m", "3h", "Yesterday", or a date. */
export function ago(t: T, locale: Locale, iso: string | undefined | null, now = Date.now()): string {
  if (!iso) return ""
  const at = Date.parse(iso)
  if (Number.isNaN(at)) return ""
  const s = Math.max(0, Math.round((now - at) / 1000))
  if (s < 60) return t("time.now")
  if (s < 3600) return t("time.m", { n: Math.floor(s / 60) })
  if (s < 86400) return t("time.h", { n: Math.floor(s / 3600) })
  if (s < 2 * 86400) return t("time.yesterday")
  if (s < 7 * 86400) return t("time.d", { n: Math.floor(s / 86400) })
  return new Date(at).toLocaleDateString(locale === "ar" ? "ar-EG" : "en-GB", { day: "numeric", month: "short" })
}

/** Money from minor units (the API's value_minor) in the workspace currency. */
export function money(locale: Locale, minor: number | undefined | null, currency: string | undefined): string {
  const v = (minor ?? 0) / 100
  try {
    return new Intl.NumberFormat(locale === "ar" ? "ar-EG" : "en-US", {
      style: "currency",
      currency: currency || "USD",
      maximumFractionDigits: v % 1 === 0 ? 0 : 2,
    }).format(v)
  } catch {
    return `${v} ${currency ?? ""}`.trim()
  }
}

/** The text of a desktop notification for a live event the server marked alert:true. */
export function alertText(t: T, ev: { kind: string; sender?: string; snippet?: string; title?: string }): { title: string; body: string } {
  if (ev.kind === "assigned") {
    const body = t("alert.assignedBody", { sender: ev.sender || "—" })
    return { title: t("alert.assigned"), body: ev.snippet ? `${body}\n${ev.snippet}` : body }
  }
  if (ev.kind === "notification") return { title: ev.title || t("alert.notification"), body: ev.snippet ?? "" }
  return { title: ev.sender ? t("alert.newMessage", { sender: ev.sender }) : t("app.name"), body: ev.snippet ?? "" }
}
