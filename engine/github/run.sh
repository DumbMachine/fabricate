#!/bin/sh
# PID 1 for the fab-emulate image. Starts emulate on the internal
# backend port and the URL-rewriting reverse proxy on the externally-
# exposed port. The proxy forwards each request and rewrites
# `localhost:4000` in JSON responses to whatever Host header the
# consumer used — so SDK callers that follow `*_url` fields land back
# on the same address they dialled in on.
#
# Both processes run as background children; this shell stays PID 1 so
# it can receive SIGTERM from `docker stop` and propagate it, AND so
# that the container exits if either child dies (a one-side crash
# would otherwise leave the surviving process serving 502s
# indefinitely). `exec emulate-proxy` would have looked simpler but
# replaces the shell — the trap below would be discarded with it.
set -eu

emulate --service "$EMULATE_SERVICE" --port "$EMULATE_BACKEND_PORT" --seed "$EMULATE_SEED" &
EMULATE_PID=$!

emulate-proxy &
PROXY_PID=$!

shutdown() {
	# Best-effort: signal both children; ignore errors from a child
	# that's already exited.
	kill -TERM "$EMULATE_PID" 2>/dev/null || true
	kill -TERM "$PROXY_PID" 2>/dev/null || true
}
trap shutdown INT TERM

# `wait -n` returns when ANY child exits. Whichever survives gets
# SIGTERM'd via shutdown, so the container exits whole. Without -n,
# `wait` blocks for all children — a crashed emulate would leave the
# proxy serving 502s until docker timeout.
wait -n
EXIT=$?
shutdown
wait
exit "$EXIT"
