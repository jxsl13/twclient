#!/usr/bin/env bash
# run.sh — reproducibly (re)generate physics/testdata/ddnet_vectors.json from
# DDNet's real CCharacterCore physics (ddnet@c7d760d5a) via the Docker builder.
#
#   ./run.sh            build the image + extract the JSON to ../ddnet_vectors.json
#
# Requires Docker. The output is GENERATED DATA (input->quantized-output), not
# DDNet source, so committing it carries no GPL obligation (V152).
set -euo pipefail
cd "$(dirname "$0")"

OUT="../ddnet_vectors.json"
IMG="twclient-ddnetvec:c7d760d5a"

echo ">> docker build $IMG"
# Build the builder stage (which runs the driver and stores the JSON).
docker build --target builder -t "$IMG" .

echo ">> extracting ddnet_vectors.json"
CID="$(docker create "$IMG")"
trap 'docker rm -f "$CID" >/dev/null 2>&1 || true' EXIT
docker cp "$CID:/ddnet_vectors.json" "$OUT"

bytes="$(wc -c < "$OUT")"
echo ">> wrote $OUT ($bytes bytes)"
test "$bytes" -gt 0
