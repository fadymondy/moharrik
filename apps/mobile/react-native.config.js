// Firebase's native modules are linked per platform only when that platform's config file is in
// secrets/ (plugins/with-firebase.js, secrets/README.md). Unlinked, the JS side is unaffected:
// every Firebase call goes through src/lib/firebase.ts, which reports false when there is no
// native module, so builds without Firebase credentials still build and run with push off.
// Ported from Zekra (togo/cabrain mobile/react-native.config.js).
const fs = require("fs");
const path = require("path");

const has = (file) => fs.existsSync(path.join(__dirname, "secrets", file));
const ios = has("GoogleService-Info.plist");
const android = has("google-services.json");

const dependencies = {};
for (const name of ["@react-native-firebase/app", "@react-native-firebase/messaging"]) {
  const platforms = {};
  if (!ios) platforms.ios = null;
  if (!android) platforms.android = null;
  if (Object.keys(platforms).length) dependencies[name] = { platforms };
}

module.exports = { dependencies };
