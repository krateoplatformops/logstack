#!/bin/bash

set -euo pipefail

TRACE_ID="${1:-}"
LEVEL_FILTER="${2:-}"

if [[ -z "$TRACE_ID" ]]; then
  echo "Usage: $0 <traceId> [level]"
  exit 1
fi

docker run --rm --net=host gcr.io/etcd-development/etcd:v3.6.1 etcdctl \
  --endpoints=http://localhost:30083 \
  get "logs/${TRACE_ID}/" --prefix --sort-by=key | \
  awk 'NR % 2 == 0' | \
  jq --arg level "$LEVEL_FILTER" '
    if $level == "" or .level == $level then . else empty end
  '
