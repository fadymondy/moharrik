// Android notification channel "messages", the one the server's FCM payload names
// (internal/alerts/fcm.go fcmPayload: android.notification.channel_id = "messages").
//
// On Android 8+ a notification can only be shown on a channel the app created; for an unknown id
// FCM falls back to its generic "Miscellaneous" channel at default importance, so a customer's
// message would arrive silently, without a heads-up banner. Creating it needs native code, and
// expo-notifications would register its own FirebaseMessagingService and compete with React Native
// Firebase's, so this plugin creates it in MainApplication.onCreate. The channel name is localised
// through string resources (values / values-ar).
const fs = require("fs");
const path = require("path");
const { withDangerousMod, withMainApplication } = require("expo/config-plugins");

const MARKER = "moharrik:notification-channel";
const NAMES = { values: "Messages", "values-ar": "الرسائل" };

function strings(dir, name) {
  const file = path.join(dir, "notification_channel.xml");
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(
    file,
    `<?xml version="1.0" encoding="utf-8"?>\n<!-- plugins/with-notification-channel.js -->\n<resources>\n  <string name="channel_messages">${name}</string>\n</resources>\n`,
  );
}

module.exports = function withNotificationChannel(config) {
  config = withDangerousMod(config, [
    "android",
    async (cfg) => {
      const res = path.join(cfg.modRequest.platformProjectRoot, "app", "src", "main", "res");
      for (const [dir, name] of Object.entries(NAMES)) strings(path.join(res, dir), name);
      return cfg;
    },
  ]);
  return withMainApplication(config, (cfg) => {
    let src = cfg.modResults.contents;
    if (src.includes(MARKER)) return cfg;
    if (cfg.modResults.language !== "kt") throw new Error("with-notification-channel: expected a Kotlin MainApplication");
    const at = src.indexOf("super.onCreate()");
    if (at < 0) throw new Error("with-notification-channel: MainApplication.onCreate changed shape; update the plugin");
    const end = at + "super.onCreate()".length;
    src =
      src.slice(0, end) +
      `
    // ${MARKER}: the channel the server's pushes use (plugins/with-notification-channel.js).
    if (android.os.Build.VERSION.SDK_INT >= 26) {
      val channel = android.app.NotificationChannel("messages", getString(R.string.channel_messages), android.app.NotificationManager.IMPORTANCE_HIGH)
      getSystemService(android.app.NotificationManager::class.java)?.createNotificationChannel(channel)
    }` +
      src.slice(end);
    cfg.modResults.contents = src;
    return cfg;
  });
};
