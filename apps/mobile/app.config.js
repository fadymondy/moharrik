// app.json stays the source of truth; this only adds what depends on the build environment and on
// files that are not in git (the same split as Zekra's mobile/app.config.js).
//
// Server: EXPO_PUBLIC_MOHARRIK_URL overrides app.json extra.serverUrl, e.g. a local stack
// (http://10.0.2.2:8080 from the Android emulator). The sign-in screen can change it too. A release
// build only reaches a plain-http server when prebuilt with MOHARRIK_LOCAL_HTTP=1 (plugins/with-local-http.js).
//
// Firebase (push): the config files live in secrets/ (gitignored — secrets/README.md; Codemagic
// decodes them from secure variables before prebuild). Each platform is wired only when ITS file
// exists (plugins/with-firebase.js), so a checkout without them still prebuilds and runs, with
// push off rather than a crash at launch.
//
// Apple team: automatic signing under JW9HJH86GC, overridable with APPLE_TEAM_ID.
const fs = require("fs");
const path = require("path");

const IOS_FIREBASE = "./secrets/GoogleService-Info.plist";
const ANDROID_FIREBASE = "./secrets/google-services.json";
const APPLE_TEAM_ID = process.env.APPLE_TEAM_ID || "JW9HJH86GC";

const has = (rel) => fs.existsSync(path.join(__dirname, rel));

module.exports = ({ config }) => {
  const ios = has(IOS_FIREBASE);
  const android = has(ANDROID_FIREBASE);
  return {
    ...config,
    ios: {
      ...config.ios,
      appleTeamId: APPLE_TEAM_ID,
      ...(ios ? { googleServicesFile: IOS_FIREBASE } : {}),
    },
    android: {
      ...config.android,
      ...(android ? { googleServicesFile: ANDROID_FIREBASE } : {}),
    },
    plugins: [
      ...(config.plugins ?? []),
      "./plugins/with-local-http",
      ["./plugins/with-firebase", { ios, android, notificationIcon: "./assets/brand/notification-icon.png", notificationColor: "#00A0A8" }],
    ],
    extra: {
      ...config.extra,
      serverUrl: process.env.EXPO_PUBLIC_MOHARRIK_URL || config.extra?.serverUrl,
      // Diagnostics only; the app decides at runtime from the native Firebase app (src/lib/push.ts).
      firebase: { ios, android },
    },
  };
};
