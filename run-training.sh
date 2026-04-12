#!/bin/bash
set -e
cd "$(dirname "$0")"

# --- Platform detection ---
case "$(uname -s)" in
    Darwin) NCPU=$(sysctl -n hw.ncpu) ;;
    Linux)  NCPU=$(nproc) ;;
    *)      echo "Unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

SERVERS=$(printf 'localhost:%s,' $(seq 8303 8342) | sed 's/,$//')

export ASSUME_NO_MOVING_GC_UNSAFE_RISK_IT_WITH=go1.26

echo "Starting training: 40 servers x 20 bots = 800 bots"
echo "GOMAXPROCS will auto-scale to $NCPU - 2 cores"
exec ./racebot \
    -server "$SERVERS" \
    -bots 20 \
    -headless \
    -log info \
    -checkpoint racebot.ckpt \
    -steps 5000
