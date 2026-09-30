/** @type {import('next').NextConfig} */
const API_ORIGIN = process.env.API_ORIGIN ?? "http://localhost:8120"

const nextConfig = {
  // One self-contained server for the production image (see the root Dockerfile).
  output: "standalone",

  // MH-658 worktree note: this worktree's node_modules is a junction to
  // moharrik-integrate's (git-ignored dirs aren't in a fresh worktree), which
  // sits outside this project's own directory tree. Turbopack's default root
  // inference refuses to resolve through that ("points out of the filesystem
  // root") — widening its root to the common ancestor of both worktrees is
  // the fix Next's own Turbopack monorepo docs recommend for this shape.
  // Worktree-local only: never meant to land outside this branch.
  turbopack: {
    root: process.env.TURBOPACK_ROOT || undefined,
  },

  // dev.moharrik.com tunnels to `next dev` on the workstation; Next blocks dev assets for other origins.
  allowedDevOrigins: ["dev.moharrik.com"],

  // The Go API and this app feel like one origin: /api and /graphql are proxied to the API, so
  // the togo_session cookie and the CSRF cookie are first-party. Realtime (/events) is not in P0.
  async rewrites() {
    return [
      { source: "/api/:path*", destination: `${API_ORIGIN}/api/:path*` },
      { source: "/graphql", destination: `${API_ORIGIN}/graphql` },
      { source: "/auth/circlexo/:path*", destination: `${API_ORIGIN}/auth/circlexo/:path*` },
      { source: "/sso/circlexo", destination: `${API_ORIGIN}/sso/circlexo` },
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
