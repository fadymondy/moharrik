#!/bin/sh
# Runs Moharrik's two processes in one container. The Go API is the public process on PORT
# (default 3000): it answers /api, /graphql and /docs itself and proxies everything else —
# pages, assets — to Next, which listens only inside the container (WEB_PORT, default 3001).
# One port, one container; websocket upgrades pass through the API's proxy, so the browser
# socket (/api/agents/ws) needs no route of its own. If either process stops, the other is
# stopped and the container exits, so Docker/Hosbah restarts it whole rather than half an app.
set -u

PORT="${PORT:-3000}"
WEB_PORT="${WEB_PORT:-3001}"
export ADDR="${ADDR:-0.0.0.0:$PORT}"
export WEB_ORIGIN="${WEB_ORIGIN:-http://127.0.0.1:$WEB_PORT}"
# Next's server-side calls to the API (app/feedback/route.ts reads API_ORIGIN at request time) go
# to the API on the loopback, on the port the API actually listens on: ADDR's, which may have been
# set on its own. Next's /api rewrite is fixed at build time instead (see the Dockerfile) — and the
# API never hands it an /api path anyway (MH-424, internal/server/webproxy.go).
API_PORT="${ADDR##*:}"
export API_ORIGIN="${API_ORIGIN:-http://127.0.0.1:$API_PORT}"

/app/bin/moharrik-api &
api=$!
( cd /app/web && HOSTNAME=127.0.0.1 PORT="$WEB_PORT" exec node server.js ) &
web=$!

stop() {
  kill "$api" "$web" 2>/dev/null
  wait "$api" "$web" 2>/dev/null
}
trap 'stop; exit 0' TERM INT

while kill -0 "$api" 2>/dev/null && kill -0 "$web" 2>/dev/null; do
  sleep 2
done
echo "moharrik: a process exited (api alive: $(kill -0 "$api" 2>/dev/null && echo yes || echo no), web alive: $(kill -0 "$web" 2>/dev/null && echo yes || echo no)); stopping" >&2
stop
exit 1
