#!/bin/bash
set -e

# --- Platform detection ---
case "$(uname -s)" in
    Darwin) DDNET_SERVER="${DDNET_SERVER:-/Applications/DDNet-Server.app/Contents/MacOS/DDNet-Server}" ;;
    Linux)  DDNET_SERVER="${DDNET_SERVER:-DDNet-Server}" ;;
    *)      echo "Unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

mkdir -p /tmp/ddnet-logs

for port in $(seq 8303 8342); do
  cfg=/tmp/ddnet-srv-$port.cfg
  cat > "$cfg" <<CFGEOF
sv_port $port
sv_name "ML Training Server $port"
sv_max_clients 32
sv_max_clients_per_ip 32
sv_map Tutorial
sv_sixup 1
sv_register 0
password ""
CFGEOF
  "$DDNET_SERVER" -f "$cfg" > "/tmp/ddnet-logs/srv-$port.log" 2>&1 &
  echo "Started DDNet-Server on port $port (PID: $!)"
done

sleep 2
echo "Servers running: $(pgrep -f DDNet-Server | wc -l)"
