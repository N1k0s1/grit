#!/bin/sh
# Measures grit daemon on a synthetic 200 MB transcript: peak and idle
# memory (RSS) and CPU while idle. Uses a throwaway home folder, so nothing
# of yours is read. Usage: scripts/bench-daemon.sh [MB] [IDLE_SECONDS]
set -eu
MB=${1:-200}
IDLE=${2:-120}
HOME_DIR=$(mktemp -d)
trap 'kill $PID 2>/dev/null || true; rm -rf "$HOME_DIR"' EXIT
CGO_ENABLED=0 go build -o "$HOME_DIR/grit" ./cmd/grit
go run ./scripts/gen-transcript -home "$HOME_DIR" -mb "$MB"
GRIT_HOME="$HOME_DIR" "$HOME_DIR/grit" daemon -every 30s >/dev/null 2>&1 &
PID=$!
peak=0
# First pass: parse the whole file. Sample RSS until it has settled.
for _ in $(seq 1 60); do
  rss=$(ps -o rss= -p $PID | tr -d ' ')
  [ "$rss" -gt "$peak" ] && peak=$rss
  sleep 1
done
cpu0=$(ps -o time= -p $PID | tr -d ' ')
sleep "$IDLE"
cpu1=$(ps -o time= -p $PID | tr -d ' ')
rss=$(ps -o rss= -p $PID | tr -d ' ')
[ "$rss" -gt "$peak" ] && peak=$rss
secs() { echo "$1" | awk -F'[:.]' '{ if (NF==4) print $1*3600+$2*60+$3+$4/100; else print $1*60+$2+$3/100 }'; }
idle_cpu=$(awk -v a="$(secs "$cpu0")" -v b="$(secs "$cpu1")" -v t="$IDLE" 'BEGIN { printf "%.2f", (b-a)/t*100 }')
echo "transcript: ${MB} MB"
echo "peak RSS:   $((peak / 1024)) MB"
echo "idle RSS:   $((rss / 1024)) MB"
echo "idle CPU:   ${idle_cpu}% over ${IDLE}s"
# On macOS, ps counts pages already returned to the system with MADV_FREE.
# The physical footprint (what Activity Monitor shows) does not.
if command -v footprint >/dev/null 2>&1; then
  footprint -p $PID 2>/dev/null | awk '/phys_footprint:/ { print "footprint:  " $2 " " $3 }'
fi
