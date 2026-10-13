# secrets/

Nothing in this folder is ever committed (`apps/.gitignore`). The app builds and runs without these files; push is just turned off.

| File | What it is | Used by |
|------|------------|---------|
| `google-services.json` | Firebase Android app config for `com.fadymondy.moharrik` | `app.config.js` → `plugins/with-firebase.js` (Android) |
| `GoogleService-Info.plist` | Firebase iOS app config for `com.fadymondy.moharrik` | `app.config.js` → `plugins/with-firebase.js` (iOS) |

Download both from the Firebase console (Project settings → Your apps). On Codemagic, the `moharrik_app_env` group writes them here from `GOOGLE_SERVICES_JSON` and `GOOGLE_SERVICE_INFO_PLIST`, both base64.

The server sends to these apps using the same Firebase project's service-account key. Put it in the server's `.env` as `FCM_SERVICE_ACCOUNT_JSON`.

After adding or removing a file, regenerate the native projects with `npx expo prebuild --clean`.
