#!/usr/bin/env node
// Ported from Zekra (togo/cabrain mobile/scripts/set-build-number.js).
/*
Stamp the store build number into app.json before `expo prebuild` generates the
native projects from it (CI: codemagic.yaml at the repository root).

Both stores reject an upload whose build number is not higher than the last
one. Codemagic's $BUILD_NUMBER rises by one per build of a workflow, which is
exactly the property the stores need. The committed app.json keeps "1" / 1, so
local development builds never need this.

This script is the ONLY thing that moves ios.buildNumber / android.versionCode
(eas.json uses appVersionSource "local" for the same reason: one source).
The marketing version is expo.version and is bumped by hand for a release.

Usage: node scripts/set-build-number.js <positive integer>
*/
const fs = require("node:fs");
const path = require("node:path");

const n = Number(process.argv[2]);
// versionCode is a 32-bit int on Play and must stay below 2100000000.
if (!Number.isInteger(n) || n < 1 || n >= 2100000000) {
  console.error("usage: node scripts/set-build-number.js <positive integer>");
  process.exit(1);
}

const file = path.join(__dirname, "..", "app.json");
const json = JSON.parse(fs.readFileSync(file, "utf8"));
json.expo.android = { ...json.expo.android, versionCode: n };
json.expo.ios = { ...json.expo.ios, buildNumber: String(n) };
fs.writeFileSync(file, `${JSON.stringify(json, null, 2)}\n`);

console.log(`app.json: android.versionCode = ${n}, ios.buildNumber = "${n}" (version ${json.expo.version})`);
