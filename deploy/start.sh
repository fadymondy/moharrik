#!/bin/sh
# Runs Moharrik's two processes in one container: the Go API (inside the container only, on
# ADDR) and the Next.js web (the public PORT). If either stops, the other is stopped and the
# container exits, so Docker/Hosbah restarts it whole rather than serving half an app.
set -u

/app/bin/moharrik-api &
api=$!
cd /app/web && node server.js &
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
