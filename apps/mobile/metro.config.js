// Metro for the Moharrik app.
//
// - @moharrik/client is the shared TypeScript in ../shared/src (not a published package): Metro
//   watches that folder and resolves the name to its index, compiling it like the app's own code.
// - android/ and ios/ build trees are kept out of the file watcher; a Gradle build fills them with
//   tens of thousands of files and Metro would crawl them all (Mahaam's metro.config.js).
const path = require("path");
const { getDefaultConfig } = require("expo/metro-config");

const SHARED = path.resolve(__dirname, "../shared/src");
const config = getDefaultConfig(__dirname);

config.watchFolders = [...(config.watchFolders ?? []), SHARED];
// The shared sources import nothing from npm, but if they ever do, resolve from the app's
// node_modules (the shared package's own node_modules hold only its dev tools).
config.resolver.nodeModulesPaths = [path.resolve(__dirname, "node_modules")];

const blocked = [
  /[\\/]android[\\/](app[\\/]build|build|\.gradle|\.cxx|app[\\/]\.cxx)[\\/].*/,
  /[\\/]ios[\\/](build|Pods|DerivedData)[\\/].*/,
  /[\\/]shared[\\/](node_modules|src[\\/]test)[\\/].*/,
  /.*\.log$/,
];
const existing = config.resolver.blockList;
config.resolver.blockList = [...(Array.isArray(existing) ? existing : existing ? [existing] : []), ...blocked];

const resolveRequest = config.resolver.resolveRequest;
config.resolver.resolveRequest = (context, name, platform) => {
  if (name === "@moharrik/client") return { type: "sourceFile", filePath: path.join(SHARED, "index.ts") };
  return resolveRequest ? resolveRequest(context, name, platform) : context.resolveRequest(context, name, platform);
};

module.exports = config;
