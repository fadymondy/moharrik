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
  "signin.body": "Use your email and password, or the account you use with Apple or Google.",
  "signin.button": "Sign in with the browser",
  "signin.waiting": "Finish signing in in your browser…",
  "signin.failed": "Sign-in didn't finish. Try again.",
  "signin.lost": "You were signed out. Sign in again to continue.",
  "signin.server": "Server",
  "signin.apple": "Sign in with Apple",
  "signin.google": "Sign in with Google",
  "signin.email": "Sign in with email",
  "signin.or": "or",
  "signin.emailField": "Email",
  "signin.password": "Password",
  "signin.submit": "Sign in",
  "signin.create": "Create account",
  "signin.toSignUp": "New to Moharrik? Create an account",
  "signin.toSignIn": "Already have an account? Sign in",
  "signin.forgot": "Forgot password?",
  "signin.forgotSent": "If that address has an account, we've emailed a link to reset the password.",
  "signin.forgotNeedsEmail": "Enter your email first, then tap Forgot password.",
  "signin.other": "Other ways to sign in (browser)",
  "signin.legal": "By creating an account, you agree to the Terms of service and the Privacy policy.",
  "signin.error.invalid_credentials": "That email and password don't match.",
  "signin.error.email_taken": "An account with this email already exists. Sign in instead.",
  "signin.error.invalid_email": "Enter a valid email address.",
  "signin.error.password_too_short": "Use a password of at least 8 characters.",
  "signin.error.password_too_long": "That password is too long.",
  "signin.error.rate_limited": "Too many attempts. Wait a few minutes and try again.",
  "signin.error.account_conflict": "An account with this email already has a password. Sign in with email, then connect Apple or Google from your account on the web.",
  "signin.error.identity_taken": "That account is connected to a different Moharrik account.",
  "signin.error.email_unverified": "Verify your email address with the provider, then try again.",
  "signin.error.no_email": "The provider didn't share an email address, so we can't sign you in with it.",
  "signin.error.suspended": "This account is suspended. Contact support to restore access.",
  "signin.error.not_configured": "This sign-in option isn't available on this server.",

  "workspaces.title": "Choose a workspace",
  "workspaces.body": "You can switch at any time from Settings.",
  "workspaces.none": "You aren't in a workspace yet",
  "workspaces.noneBody": "Create one for your team now, or ask a teammate to invite you.",
  "workspaces.create": "Create workspace",
  "workspaces.nameField": "Workspace name",
  "workspaces.nameHint": "Your company or team name. You'll be the owner and can invite teammates.",
  "workspaces.createFailed": "Couldn't create the workspace. Try again.",
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
  "inbox.block": "Block customer",
  "inbox.unblock": "Unblock customer",
  "inbox.blockBody": "Their new messages are dropped and nobody in the workspace can reply until they're unblocked.",
  "inbox.blocked": "This customer is blocked. Their messages are dropped and replies are off.",
  "inbox.blockFailed": "Couldn't change the block. Only managers, admins and owners can block customers.",
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
  "deals.chip": "{stage} · {n}",
  "stage.lead": "Lead",
  "stage.qualified": "Qualified",
  "stage.proposal": "Proposal",
  "stage.won": "Won",
  "stage.lost": "Lost",

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
  "settings.deleteAccount": "Delete account",
  "settings.deleteAccountBody": "Deletes your account and everything only you have. There is no undo.",
  "settings.about": "About",
  "settings.privacy": "Privacy policy",
  "settings.terms": "Terms of service",
  "settings.support": "Help and support",
  "delete.title": "Delete your account",
  "delete.body": "Your account, sign-in methods, app sessions and push registrations are deleted, and Sign in with Apple is revoked. You leave every shared workspace; what you made there stays with the team.",
  "delete.workspaces": "These workspaces have no other members and will be deleted with everything in them:",
  "delete.blocking": "You're the only owner of these workspaces, which have other members. Make someone else an owner or delete the workspace on the web first:",
  "delete.fresh": "You signed in recently, so no password is needed.",
  "delete.password": "Your password",
  "delete.reauth": "To confirm it's you, sign in again, then delete within 10 minutes.",
  "delete.reauthButton": "Sign in again",
  "delete.confirm": "Type {email} to confirm",
  "delete.button": "Delete account permanently",
  "delete.done": "Your account was deleted.",
  "delete.error.wrong_password": "That password isn't right.",
  "delete.error.reauth_required": "Confirm it's you: enter your password, or sign in again and delete within 10 minutes.",
  "delete.error.sole_owner": "You're the only owner of a workspace with other members. Transfer ownership or delete it first.",
  "delete.error.confirm_mismatch": "Type your account's email address exactly.",

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
  "signin.body": "استخدم بريدك الإلكتروني وكلمة المرور، أو حسابك في Apple أو Google.",
  "signin.button": "تسجيل الدخول من المتصفح",
  "signin.waiting": "أكمل تسجيل الدخول في المتصفح…",
  "signin.failed": "لم يكتمل تسجيل الدخول. حاول مرة أخرى.",
  "signin.lost": "تم تسجيل خروجك. سجّل الدخول مجددًا للمتابعة.",
  "signin.server": "الخادم",
  "signin.apple": "تسجيل الدخول باستخدام Apple",
  "signin.google": "تسجيل الدخول باستخدام Google",
  "signin.email": "تسجيل الدخول بالبريد الإلكتروني",
  "signin.or": "أو",
  "signin.emailField": "البريد الإلكتروني",
  "signin.password": "كلمة المرور",
  "signin.submit": "تسجيل الدخول",
  "signin.create": "إنشاء حساب",
  "signin.toSignUp": "جديد في محرّك؟ أنشئ حسابًا",
  "signin.toSignIn": "لديك حساب؟ سجّل الدخول",
  "signin.forgot": "نسيت كلمة المرور؟",
  "signin.forgotSent": "إن كان لهذا البريد حساب، فقد أرسلنا إليه رابطًا لإعادة تعيين كلمة المرور.",
  "signin.forgotNeedsEmail": "أدخل بريدك الإلكتروني أولًا، ثم اضغط «نسيت كلمة المرور».",
  "signin.other": "طرق أخرى لتسجيل الدخول (المتصفح)",
  "signin.legal": "بإنشاء حساب، فإنك توافق على شروط الاستخدام وسياسة الخصوصية.",
  "signin.error.invalid_credentials": "البريد الإلكتروني وكلمة المرور غير متطابقين.",
  "signin.error.email_taken": "يوجد حساب بهذا البريد بالفعل. سجّل الدخول بدلًا من ذلك.",
  "signin.error.invalid_email": "أدخل بريدًا إلكترونيًا صحيحًا.",
  "signin.error.password_too_short": "استخدم كلمة مرور من 8 أحرف على الأقل.",
  "signin.error.password_too_long": "كلمة المرور طويلة جدًا.",
  "signin.error.rate_limited": "محاولات كثيرة. انتظر بضع دقائق ثم حاول مرة أخرى.",
  "signin.error.account_conflict": "يوجد حساب بهذا البريد وله كلمة مرور. سجّل الدخول بالبريد، ثم اربط Apple أو Google من حسابك على الويب.",
  "signin.error.identity_taken": "هذا الحساب مرتبط بحساب آخر في محرّك.",
  "signin.error.email_unverified": "أكّد بريدك الإلكتروني لدى مقدّم الخدمة ثم حاول مرة أخرى.",
  "signin.error.no_email": "لم يشارك مقدّم الخدمة عنوان بريد إلكتروني، لذا لا يمكننا تسجيل دخولك به.",
  "signin.error.suspended": "هذا الحساب موقوف. تواصل مع الدعم لاستعادته.",
  "signin.error.not_configured": "خيار تسجيل الدخول هذا غير متاح على هذا الخادم.",

  "workspaces.title": "اختر مساحة العمل",
  "workspaces.body": "يمكنك التبديل في أي وقت من الإعدادات.",
  "workspaces.none": "لست في أي مساحة عمل بعد",
  "workspaces.noneBody": "أنشئ مساحة لفريقك الآن، أو اطلب من زميل أن يدعوك.",
  "workspaces.create": "إنشاء مساحة عمل",
  "workspaces.nameField": "اسم مساحة العمل",
  "workspaces.nameHint": "اسم شركتك أو فريقك. ستكون مالكها ويمكنك دعوة زملائك.",
  "workspaces.createFailed": "تعذّر إنشاء مساحة العمل. حاول مرة أخرى.",
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
  "inbox.block": "حظر العميل",
  "inbox.unblock": "إلغاء حظر العميل",
  "inbox.blockBody": "تُتجاهل رسائله الجديدة ولا يمكن لأحد في مساحة العمل الرد عليه حتى يُلغى الحظر.",
  "inbox.blocked": "هذا العميل محظور. تُتجاهل رسائله والرد متوقف.",
  "inbox.blockFailed": "تعذّر تغيير الحظر. الحظر متاح للمديرين والمسؤولين والمالكين فقط.",
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
  "deals.chip": "{stage} · {n}",
  "stage.lead": "عميل محتمل",
  "stage.qualified": "مؤهَّل",
  "stage.proposal": "عرض سعر",
  "stage.won": "تم الفوز",
  "stage.lost": "خسارة",

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
  "settings.deleteAccount": "حذف الحساب",
  "settings.deleteAccountBody": "يحذف حسابك وكل ما يخصك وحدك. لا يمكن التراجع.",
  "settings.about": "حول",
  "settings.privacy": "سياسة الخصوصية",
  "settings.terms": "شروط الاستخدام",
  "settings.support": "المساعدة والدعم",
  "delete.title": "حذف حسابك",
  "delete.body": "يُحذف حسابك وطرق تسجيل دخولك وجلسات التطبيقات وتسجيلات الإشعارات، ويُلغى تفويض Apple. وتُزال من كل مساحات العمل المشتركة، ويبقى ما أنشأته فيها مع الفريق.",
  "delete.workspaces": "مساحات العمل هذه ليس فيها أعضاء غيرك وستُحذف بكل ما فيها:",
  "delete.blocking": "أنت المالك الوحيد لمساحات العمل هذه وفيها أعضاء آخرون. اجعل شخصًا آخر مالكًا أو احذف المساحة من الويب أولًا:",
  "delete.fresh": "سجّلت الدخول منذ قليل، لذا لا حاجة لكلمة المرور.",
  "delete.password": "كلمة المرور",
  "delete.reauth": "لتأكيد أنك صاحب الحساب، سجّل الدخول مجددًا ثم احذف خلال ١٠ دقائق.",
  "delete.reauthButton": "تسجيل الدخول مجددًا",
  "delete.confirm": "اكتب {email} للتأكيد",
  "delete.button": "حذف الحساب نهائيًا",
  "delete.done": "تم حذف حسابك.",
  "delete.error.wrong_password": "كلمة المرور غير صحيحة.",
  "delete.error.reauth_required": "أكّد أنك صاحب الحساب: أدخل كلمة المرور، أو سجّل الدخول مجددًا واحذف خلال ١٠ دقائق.",
  "delete.error.sole_owner": "أنت المالك الوحيد لمساحة عمل فيها أعضاء آخرون. انقل الملكية أو احذفها أولًا.",
  "delete.error.confirm_mismatch": "اكتب البريد الإلكتروني لحسابك كما هو تمامًا.",

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

// The server seeds every new workspace with the same five stages (internal/crm/crm.go
// defaultStages), named in the workspace's language at the time. Those names map back to a stable
// key so the app shows them in its own language; a stage the workspace renamed or added keeps the
// name the server sends.
const DEFAULT_STAGES: Record<string, "lead" | "qualified" | "proposal" | "won" | "lost"> = {
  lead: "lead",
  qualified: "qualified",
  proposal: "proposal",
  won: "won",
  lost: "lost",
  "عميل محتمل": "lead",
  "مؤهَّل": "qualified",
  "مؤهل": "qualified",
  "عرض سعر": "proposal",
  "تم الفوز": "won",
  "خسارة": "lost",
}

/** A pipeline stage's display name: the default stages in the app's language, any other as named. */
export function stageName(t: T, name: string | undefined | null): string {
  const n = (name ?? "").trim()
  const key = DEFAULT_STAGES[n.toLowerCase()] ?? DEFAULT_STAGES[n]
  return key ? t(`stage.${key}`) : n
}

const intlLocale = (l: Locale) => (l === "ar" ? "ar-EG" : "en-US")

/** A count in the locale's digits and grouping. */
export function num(locale: Locale, n: number | undefined | null): string {
  try {
    return new Intl.NumberFormat(intlLocale(locale)).format(n ?? 0)
  } catch {
    return String(n ?? 0)
  }
}

// Unicode bidi controls: the marks ICU puts in Arabic amounts, and the isolates we wrap them in.
const BIDI_MARKS = /[\u200e\u200f\u061c]/g
const RLI = "\u2067"
const FSI = "\u2068"
const PDI = "\u2069"
const NUMBER_RUN = /[-\u2212]?[0-9\u0660-\u0669](?:[0-9\u0660-\u0669\u066b\u066c.,\s\u00a0\u202f]*[0-9\u0660-\u0669])?/

/**
 * Money from minor units (the API's value_minor) in the workspace currency.
 *
 * Arabic amounts come out of Intl as "‏٢٤٬٠٠٠ US$": a right-to-left mark, the number, then a
 * Latin symbol. Laid out right to left the "$" (a weak character) jumps to the other side of "US"
 * and the amount reads "$US ٢٤٬٠٠٠". So for Arabic the symbol goes in its own first-strong isolate
 * (Latin symbols stay "US$", Arabic ones like "ج.م.‏" stay Arabic) and the whole amount in a
 * right-to-left isolate, so it reads the same inside any sentence. English stays as Intl writes it.
 */
export function money(locale: Locale, minor: number | undefined | null, currency: string | undefined): string {
  const v = (minor ?? 0) / 100
  const code = currency || "USD"
  let s: string
  try {
    s = new Intl.NumberFormat(intlLocale(locale), {
      style: "currency",
      currency: code,
      maximumFractionDigits: v % 1 === 0 ? 0 : 2,
    }).format(v)
  } catch {
    s = `${num(locale, v)} ${code}`
  }
  if (locale !== "ar") return s
  s = s.replace(BIDI_MARKS, "").trim()
  const m = NUMBER_RUN.exec(s)
  if (!m) return `${RLI}${s}${PDI}`
  const before = s.slice(0, m.index).trim()
  const after = s.slice(m.index + m[0].length).trim()
  const iso = (x: string) => (x ? `${FSI}${x}${PDI}` : "")
  return `${RLI}${[iso(before), m[0], iso(after)].filter(Boolean).join(" ")}${PDI}`
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
