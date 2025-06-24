#!/bin/bash

set -euo pipefail

NODE_NAME="kind-control-plane"
TIMEOUT_SECONDS=60
SLEEP_INTERVAL=3

echo "🔍 Verifying access to node: $NODE_NAME"
if ! kubectl get node "$NODE_NAME" &>/dev/null; then
  echo "❌ Node $NODE_NAME not found"
  exit 1
fi

echo "📦 Finding kind.local images in container runtime..."
IMAGES=$(docker exec "$NODE_NAME" crictl images -q | xargs -n1 docker exec "$NODE_NAME" crictl inspecti | jq -r '.repoTags[]' | grep '^kind.local/' || true)

if [[ -z "$IMAGES" ]]; then
  echo "✅ No kind.local images found in container runtime"
else
  echo "🗑️ Deleting kind.local images:"
  echo "$IMAGES" | while read -r image; do
    echo "  - Removing $image"
    docker exec "$NODE_NAME" crictl rmi "$image" || true
  done
fi

echo "♻️ Restarting node $NODE_NAME to refresh status..."
docker restart "$NODE_NAME" >/dev/null

echo "⏳ Waiting for image state to reflect removal (timeout: $TIMEOUT_SECONDS seconds)..."
end=$((SECONDS + TIMEOUT_SECONDS))
while (( SECONDS < end )); do
  COUNT=$(kubectl get node "$NODE_NAME" -o json | jq '[.status.images[].names[] | select(startswith("kind.local"))] | length')
  if [[ "$COUNT" -eq 0 ]]; then
    echo "✅ No kind.local images reported in node status"
    exit 0
  fi
  echo "⏱️  Still found $COUNT kind.local image(s)... retrying in $SLEEP_INTERVAL seconds"
  sleep "$SLEEP_INTERVAL"
done

echo "⚠️ Timeout reached. Some kind.local images still appear in node status"
exit 1
