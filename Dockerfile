# Moharrik — one production image: the Go API and the Next.js web, deployed by Hosbah.
#
# The Go API is the public process (PORT, default 3000): it serves /api, /graphql and /docs and
# proxies every other path to Next, which listens only inside the container (WEB_PORT, 3001).
# One port, one container; websocket upgrades pass through the proxy, so the browser socket
# (/api/agents/ws) needs no route of its own. Hosbah's health check hits /api/health directly.
#
# Generated code (internal/db/gen, internal/graph/gen) is not committed; it is produced here with
# the same sqlc and gqlgen versions `togo generate` uses.

# ── API: generate, then build the binaries ─────────────────────────────────────────────────────
FROM golang:1.26 AS api
WORKDIR /src
# sqlc's Postgres parser needs cgo, hence the full (Debian) Go image for this stage.
RUN go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN sqlc generate && go run github.com/99designs/gqlgen generate
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/moharrik-api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/moharrik-migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/moharrik-admin ./cmd/moharrik-admin

# ── Web: a standalone Next.js server ───────────────────────────────────────────────────────────
FROM node:25-alpine AS web
WORKDIR /web
ENV NEXT_TELEMETRY_DISABLED=1
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# Public, build-time values (inlined into the bundle); passed by the deploy as build args.
ARG NEXT_PUBLIC_MAHAAM_EMBED_ORIGIN=https://console.mahaam.app
ENV NEXT_PUBLIC_MAHAAM_EMBED_ORIGIN=$NEXT_PUBLIC_MAHAAM_EMBED_ORIGIN
# Next bakes its rewrites into the build, so the /api and /graphql rewrite destination is decided
# here, not by start.sh: the API on the loopback at the default PORT. Left unset it was
# localhost:8120, where nothing listens in this container — an /api path that reached Next was a
# 500 (MH-424). The API now answers every /api path itself; this keeps the fallback honest too.
ARG API_ORIGIN=http://127.0.0.1:3000
ENV API_ORIGIN=$API_ORIGIN
RUN npm run build

# ── Extension: the downloadable zip (MH-399) ───────────────────────────────────────────────────
# Chrome loads the extension directory as it is, so "building" it is packaging: a versioned zip
# of extension/ without its test harness, and build.json with the zip's sha256. The API serves
# both (GET /api/automation/extension and …/download) and refuses a zip that no longer matches
# its recorded checksum. EXTENSION_SITE bakes another deployment's origin in; the default is
# production. The zip is built here, never committed.
FROM node:25-alpine AS extension
WORKDIR /src
COPY extension/ ./extension/
ARG EXTENSION_SITE=https://app.moharrik.com
ARG GIT_COMMIT=
RUN GIT_COMMIT=$GIT_COMMIT node extension/build.mjs --site "$EXTENSION_SITE" --out /out/extension

# ── Runtime ────────────────────────────────────────────────────────────────────────────────────
FROM node:25-alpine
WORKDIR /app
RUN apk add --no-cache ca-certificates tini tzdata
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    APP_ENV=production \
    HOSTNAME=0.0.0.0 \
    PORT=3000 \
    LOCALE_DIR=/app/lang \
    STORAGE_DIR=/app/storage
COPY --from=api /out/ /app/bin/
# The migrate command reads the schema and post-migration SQL from the working directory.
COPY internal/db/schema /app/internal/db/schema
COPY internal/db/post /app/internal/db/post
COPY lang /app/lang
COPY --from=extension /out/extension /app/extension/dist
COPY --from=web /web/.next/standalone /app/web
COPY --from=web /web/.next/static /app/web/.next/static
COPY --from=web /web/public /app/web/public
COPY deploy/start.sh /app/start.sh
RUN chmod +x /app/start.sh && mkdir -p /app/storage && chown -R node:node /app/storage
USER node
EXPOSE 3000
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/app/start.sh"]
