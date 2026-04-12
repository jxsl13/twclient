#!/bin/bash
# Start N DDNet servers and connect bots to all of them for parallel training.
# Usage: ./start-training.sh [num_servers] [bots_per_server]
#   Default: 20 servers, 63 bots each = 1260 total bots

set -e

# --- Platform detection ---
case "$(uname -s)" in
    Darwin) _DEFAULT_SERVER="/Applications/DDNet-Server.app/Contents/MacOS/DDNet-Server" ;;
    Linux)  _DEFAULT_SERVER="DDNet-Server" ;;
    *)      echo "Unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

NUM_SERVERS=${1:-20}
BOTS_PER_SERVER=${2:-63}
BASE_PORT=8303
DDNET_SERVER="${DDNET_SERVER:-$_DEFAULT_SERVER}"
SERVER_CFG="server.cfg"
LOG_DIR="/tmp/tw-training"

mkdir -p "$LOG_DIR"

# Kill any existing instances
pkill -f "DDNet-Server" 2>/dev/null || true
pkill -f "go-build.*racebot" 2>/dev/null || true
sleep 2

echo "Starting $NUM_SERVERS DDNet servers (ports $BASE_PORT-$((BASE_PORT + NUM_SERVERS - 1)))..."

PIDS=()
for i in $(seq 0 $((NUM_SERVERS - 1))); do
    PORT=$((BASE_PORT + i))
    "$DDNET_SERVER" -f "$SERVER_CFG" "sv_port $PORT" \
        > "$LOG_DIR/server-$PORT.log" 2>&1 &
    PIDS+=($!)
    echo "  Server $((i+1))/$NUM_SERVERS on port $PORT (pid $!)"
done

# Wait for servers to initialize
echo "Waiting for servers to start..."
sleep 3

# Build comma-separated server list
SERVERS=""
for i in $(seq 0 $((NUM_SERVERS - 1))); do
    PORT=$((BASE_PORT + i))
    if [ -n "$SERVERS" ]; then
        SERVERS="$SERVERS,"
    fi
    SERVERS="${SERVERS}localhost:$PORT"
done

TOTAL_BOTS=$((NUM_SERVERS * BOTS_PER_SERVER))
echo "Starting $TOTAL_BOTS bots ($BOTS_PER_SERVER per server) across $NUM_SERVERS servers..."
echo "Servers: $SERVERS"

# Trap Ctrl+C to kill everything
cleanup() {
    echo ""
    echo "Stopping training..."
    pkill -f "go-build.*racebot" 2>/dev/null || true
    sleep 2
    pkill -f "DDNet-Server" 2>/dev/null || true
    echo "All processes stopped."
    exit 0
}
trap cleanup SIGINT SIGTERM

ASSUME_NO_MOVING_GC_UNSAFE_RISK_IT_WITH=go1.26 \
    go run ./cmd/racebot \
    -server "$SERVERS" \
    -bots "$BOTS_PER_SERVER" \
    -log info \
    -headless \
    2>&1 | tee "$LOG_DIR/bots.log"
