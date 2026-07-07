#!/usr/bin/env bash
# Bring up the whole Wheelio rideshare suite with fab.
#
#   ./up.sh            # postgres + redis + gmail + linear (Docker)
#   ./up.sh --k8s      # also apply k8s/rideshare.yaml to the CURRENT
#                      # kubectl context (use a throwaway kind cluster)
#
# Requires: fab on PATH, Docker running, `make images` done once
# (gmail/linear mocks). Tear down with ./down.sh.
set -euo pipefail
cd "$(dirname "$0")"

command -v fab >/dev/null || { echo "up.sh: fab not on PATH (make install)"; exit 1; }

echo "==> installing the profile pack (user dir)"
fab profiles add . --all --force >/dev/null

for spec in "postgres rideshare-core" "redis rideshare-dispatch" "gmail rideshare-support" "linear rideshare-ops"; do
  set -- $spec
  slug=$1 name=$2
  if fab ls -o json | grep -q "\"$name\""; then
    echo "==> $name already running, skipping"
  else
    echo "==> fab create $slug -p $name"
    fab create "$slug" -p "$name" >/dev/null
  fi
done

if [ "${1:-}" = "--k8s" ]; then
  command -v kubectl >/dev/null || { echo "up.sh: kubectl not on PATH"; exit 1; }
  echo "==> kubectl apply -f k8s/rideshare.yaml (context: $(kubectl config current-context))"
  kubectl apply -f k8s/rideshare.yaml
  echo "==> payments-webhook is SUPPOSED to be broken:"
  echo "    kubectl -n rideshare get pods"
fi

echo
echo "==> the suite:"
fab ls
echo
echo "creds:  eval \"\$(fab creds rideshare-core --env)\"      # DATABASE_URL"
echo "        eval \"\$(fab creds rideshare-dispatch --env)\"  # REDIS_URL"
echo "        eval \"\$(fab creds rideshare-support --env)\"   # FAB_URL (gmail API)"
echo "        eval \"\$(fab creds rideshare-ops --env)\"       # FAB_URL (linear API)"
echo
echo "start digging: examples/rideshare/README.md — 'The investigation'"
