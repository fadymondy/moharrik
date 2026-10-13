// Bundles the main process and the preload to CommonJS (the sandboxed preload must be one file).
import { readFileSync } from "node:fs";
import { build, context } from "esbuild";

const pkg = JSON.parse(readFileSync(new URL("../package.json", import.meta.url), "utf8"));

const watch = process.argv.includes("--watch");
const opts = {
  entryPoints: { main: "src/main/main.ts", preload: "src/main/preload.ts" },
  outdir: "dist",
  outExtension: { ".js": ".cjs" },
  bundle: true,
  platform: "node",
  format: "cjs",
  target: "node22",
  external: ["electron"],
  // The server the app signs in to by default; MOHARRIK_URL overrides it at build or run time.
  define: { __MOHARRIK_URL__: JSON.stringify(process.env.MOHARRIK_URL || pkg.moharrik.serverUrl) },
  sourcemap: watch ? "inline" : false,
  minify: !watch,
  logLevel: "info",
};
if (watch) await (await context(opts)).watch();
else await build(opts);
