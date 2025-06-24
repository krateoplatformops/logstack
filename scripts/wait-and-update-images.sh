#!/bin/bash

set -euo pipefail

# Configurazione
NODE="kind-control-plane"
DEPLOY_FILE="manifests/deploy.yaml"
TIMEOUT=60  # secondi
INTERVAL=3  # secondi
END=$((SECONDS + TIMEOUT))

echo "🔄 Waiting for images to appear in node $NODE..."

FROSTBEAT_IMG=""
AUGER_IMG=""

# Attendi fino al timeout
while [ $SECONDS -lt $END ]; do
    IMAGES=$(kubectl get node "$NODE" -o json | jq -r '.status.images[] | .names[]')

    FROSTBEAT_IMG=$(echo "$IMAGES" | grep '^kind.local/frostbeat' | head -n1 || true)
    AUGER_IMG=$(echo "$IMAGES" | grep '^kind.local/auger' | head -n1 || true)

    if [[ -n "$FROSTBEAT_IMG" && -n "$AUGER_IMG" ]]; then
        echo "✅ Found images:"
        echo " - frostbeat: $FROSTBEAT_IMG"
        echo " - auger:     $AUGER_IMG"
        break
    fi

    echo "⏳ Images not ready yet. Retrying in $INTERVAL seconds..."
    sleep $INTERVAL
done

if [[ -z "$FROSTBEAT_IMG" || -z "$AUGER_IMG" ]]; then
    echo "❌ Timeout reached. Images not found."
    exit 1
fi

# Aggiorna il file deploy.yaml
echo "✏️  Updating $DEPLOY_FILE with new image references..."

# usa sed per sostituire le immagini
sed -i.bak \
    -e "s|^\(\s*image:\s*\)kind.local/frostbeat.*$|\1$FROSTBEAT_IMG|" \
    -e "s|^\(\s*image:\s*\)kind.local/auger.*$|\1$AUGER_IMG|" \
    "$DEPLOY_FILE"

echo "✅ Updated $DEPLOY_FILE"
