# Moharrik — one production image: the Go API and the Next.js web, deployed by Hosbah.
#
# The web process is the public one (PORT, default 3000); it proxies /api and /graphql to the API,
# which listens only inside the container (127.0.0.1:8120), so the session and CSRF cookies stay
# first-party exactly as in development. Hosbah's health check (/api/health) goes through the web
# to the API, so a release takes traffic only when both answer.
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
FROM node:24-alpine AS web
WORKDIR /web
ENV NEXT_TELEMETRY_DISABLED=1
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# Public, build-time values (inlined into the bundle); passed by the deploy as build args.
ARG NEXT_PUBLIC_MAHAAM_EMBED_ORIGIN=https://console.mahaam.app
ENV NEXT_PUBLIC_MAHAAM_EMBED_ORIGIN=$NEXT_PUBLIC_MAHAAM_EMBED_ORIGIN
RUN npm run build

# ── Runtime ────────────────────────────────────────────────────────────────────────────────────
FROM node:24-alpine
WORKDIR /app
RUN apk add --no-cache ca-certificates tini tzdata
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    APP_ENV=production \
    ADDR=127.0.0.1:8120 \
    API_ORIGIN=http://127.0.0.1:8120 \
    HOSTNAME=0.0.0.0 \
    PORT=3000 \
    LOCALE_DIR=/app/lang \
    STORAGE_DIR=/app/storage
COPY --from=api /out/ /app/bin/
# The migrate command reads the schema and post-migration SQL from the working directory.
COPY internal/db/schema /app/internal/db/schema
COPY internal/db/post /app/internal/db/post
COPY lang /app/lang
COPY --from=web /web/.next/standalone /app/web
COPY --from=web /web/.next/static /app/web/.next/static
COPY --from=web /web/public /app/web/public
COPY deploy/start.sh /app/start.sh
RUN chmod +x /app/start.sh && mkdir -p /app/storage && chown -R node:node /app/storage
USER node
EXPOSE 3000
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/app/start.sh"]
