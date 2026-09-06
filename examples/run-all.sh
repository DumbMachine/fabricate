#!/bin/sh
# Smoke-run every example app under fab-dev. Intended for contributors.
set -eu

root=$(CDPATH= cd -P -- "$(dirname -- "$0")/.." && pwd)
fab="$root/scripts/fab-dev"

fail=0
assert_contains() {
	haystack=$1
	needle=$2
	label=$3
	case "$haystack" in
		*"$needle"*) printf 'ok  %s\n' "$label" ;;
		*)
			printf 'FAIL %s (missing %s)\n' "$label" "$needle" >&2
			fail=$((fail + 1))
			;;
	esac
}

run() {
	label=$1
	needle=$2
	shift 2
	printf '>> %s\n' "$label" >&2
	out=$("$fab" "$@" 2>/dev/null) || {
		printf 'FAIL %s (command failed)\n' "$label" >&2
		fail=$((fail + 1))
		return
	}
	assert_contains "$out" "$needle" "$label"
}

run "python gmail inbox (direct)" "support@acme.example" \
	run acme-gmail -- python3 "$root/examples/gmail-inbox-python/inbox.py"

run "python gmail inbox (proxy)" "INV-4812" \
	run acme-gmail --proxy -- python3 "$root/examples/gmail-inbox-python/inbox.py" INV-4812

run "python asana planner (direct)" "Refund duplicate charge" \
	run acme-asana -- python3 "$root/examples/asana-planner-python/planner.py"

run "python asana planner (proxy)" "Checkout reliability" \
	run acme-asana --proxy -- python3 "$root/examples/asana-planner-python/planner.py"

run "python support desk (direct)" "Northwind" \
	run acme-support-desk -- python3 "$root/examples/support-desk-python/casefile.py"

run "python support desk (proxy)" "Charged twice" \
	run acme-support-desk --proxy -- python3 "$root/examples/support-desk-python/casefile.py"

run "node hubspot pipeline (direct)" "INV-4812" \
	run acme-hubspot -- node "$root/examples/hubspot-pipeline-node/pipeline.mjs"

run "node hubspot pipeline (proxy)" "Northwind checkout expansion" \
	run acme-hubspot --proxy -- node "$root/examples/hubspot-pipeline-node/pipeline.mjs"

if [ -d "$root/examples/gmail-inbox-node/node_modules/googleapis" ]; then
	run "node gmail inbox (direct)" "support@acme.example" \
		run acme-gmail -- node "$root/examples/gmail-inbox-node/inbox.mjs"
	run "node gmail inbox (proxy)" "support@acme.example" \
		run acme-gmail --proxy -- node "$root/examples/gmail-inbox-node/inbox.mjs"
else
	printf 'skip node gmail inbox (run npm install in examples/gmail-inbox-node)\n' >&2
fi

rust_bin="$root/examples/gmail-inbox-rust/target/debug/gmail-inbox"
if [ -x "$rust_bin" ]; then
	run "rust gmail inbox (direct)" "support@acme.example" \
		run acme-gmail -- "$rust_bin"
	run "rust gmail inbox (proxy)" "support@acme.example" \
		run acme-gmail --proxy -- "$rust_bin"
else
	printf 'skip rust gmail inbox (run cargo build in examples/gmail-inbox-rust)\n' >&2
fi

if [ "$fail" -ne 0 ]; then
	printf 'examples: %s failed\n' "$fail" >&2
	exit 1
fi
printf 'examples: all checks passed\n'
