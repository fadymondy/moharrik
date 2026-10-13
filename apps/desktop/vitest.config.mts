import { defineConfig } from "vitest/config"

// Unit tests cover the main process (pure helpers); the renderer is exercised by `npm run shots`.
export default defineConfig({ test: { root: ".", include: ["src/**/*.test.ts"] } })
