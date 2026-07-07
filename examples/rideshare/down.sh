#!/usr/bin/env bash
# Tear the Wheelio suite down.
#   ./down.sh          # fab instances
#   ./down.sh --k8s    # also delete the rideshare namespace
set -euo pipefail

for name in rideshare-core rideshare-dispatch rideshare-support rideshare-ops; do
  fab destroy "$name" 2>/dev/null || true
done

if [ "${1:-}" = "--k8s" ]; then
  kubectl delete namespace rideshare --ignore-not-found
fi
echo "down."
