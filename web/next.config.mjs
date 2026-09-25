/** @type {import('next').NextConfig} */
const API_ORIGIN = process.env.API_ORIGIN ?? "http://localhost:8120"

const nextConfig = {
  // One self-contained server for the production image (see the root Dockerfile).
  output: "standalone",

  // dev.moharrik.com tunnels to `next dev` on the workstation; Next blocks dev assets for other origins.
  allowedDevOrigins: ["dev.moharrik.com"],

  // The Go API and this app feel like one origin: /api and /graphql are proxied to the API, so
  // the togo_session cookie and the CSRF cookie are first-party. Realtime (/events) is not in P0.
  async rewrites() {
    return [
      { source: "/api/:path*", destination: `${API_ORIGIN}/api/:path*` },
      { source: "/graphql", destination: `${API_ORIGIN}/graphql` },
    ]
  },

  // Dev only: never let a browser keep a stale page or chunk (dev chunk names are stable).
  async headers() {
    if (process.env.NODE_ENV !== "development") return []
    return [
      {
        source: "/:path((?!api|graphql).*)",
        headers: [{ key: "Cache-Control", value: "no-store, must-revalidate" }],
      },
    ]
  },
}

export default nextConfig
