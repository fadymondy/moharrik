// Plain-http servers for a LOCAL stack only (e.g. http://10.0.2.2:8130 from the emulator).
//
// Android release builds refuse cleartext traffic, so a release APK pointed at a local http
// server cannot sign in. Setting MOHARRIK_LOCAL_HTTP=1 before `expo prebuild` turns cleartext on
// for that build. Codemagic never sets it, so store builds stay https-only.
const { withAndroidManifest } = require('expo/config-plugins');

module.exports = function withLocalHttp(config) {
  if (process.env.MOHARRIK_LOCAL_HTTP !== '1') return config;
  return withAndroidManifest(config, (cfg) => {
    const app = cfg.modResults.manifest.application?.[0];
    if (app) app.$['android:usesCleartextTraffic'] = 'true';
    return cfg;
  });
};
