#!/usr/bin/env bash
# Start/stop whereis.cam against a fake Home Assistant, for local UI driving.
#
#   serve.sh start [cam_zone]   build, start fake HA + server (fresh DB, 3 seeded subscribers)
#   serve.sh stop
#
# Env: APP_PORT (8099), HA_PORT (8123), RUN_DIR (${TMPDIR:-/tmp}/whereis-run)
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
UNIT=$(cd "$HERE/../../.." && pwd)
RUN_DIR=${RUN_DIR:-${TMPDIR:-/tmp}/whereis-run}
APP_PORT=${APP_PORT:-8099}
HA_PORT=${HA_PORT:-8123}

stop() {
  for port in "$APP_PORT" "$HA_PORT"; do
    pids=$(lsof -ti:"$port" -sTCP:LISTEN 2>/dev/null || true)
    [ -n "$pids" ] && kill $pids 2>/dev/null || true
  done
}

case "${1:-start}" in
  stop)
    stop
    echo "stopped"
    ;;
  start)
    stop
    mkdir -p "$RUN_DIR"
    rm -rf "$RUN_DIR/data"
    (cd "$UNIT" && go build -o "$RUN_DIR/whereis" ./cmd/api)

    python3 -I "$HERE/mockha.py" "$HA_PORT" "${2:-}" >"$RUN_DIR/mockha.log" 2>&1 &

    # Run from RUN_DIR, not the repo: the server loads ./.env and legacy ./data/*.json
    # from its working directory, which would pull in real credentials/subscribers.
    (cd "$RUN_DIR" && HASS_URL="http://127.0.0.1:$HA_PORT" HASS_TOKEN=dummy PORT="$APP_PORT" \
      ADMIN_PASSWORD=testpass DB_PATH=./data/test.db \
      nohup ./whereis >"$RUN_DIR/server.log" 2>&1 &)

    for _ in $(seq 1 60); do
      curl -sf "http://localhost:$APP_PORT/health" >/dev/null && break
      sleep 0.5
    done
    curl -sf "http://localhost:$APP_PORT/health" >/dev/null || { echo "server failed to start; see $RUN_DIR/server.log"; tail -20 "$RUN_DIR/server.log"; exit 1; }

    for e in a b c; do
      curl -s -o /dev/null -X POST "http://localhost:$APP_PORT/api/alerts/subscribe" \
        -H 'Content-Type: application/json' -d "{\"email\":\"seed-$e@example.com\"}"
    done
    echo "whereis.cam on http://localhost:$APP_PORT (dashboard password: testpass)"
    echo "logs: $RUN_DIR/server.log"
    ;;
  *)
    echo "usage: $0 start [cam_zone] | stop" >&2
    exit 2
    ;;
esac
