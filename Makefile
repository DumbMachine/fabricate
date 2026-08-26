# fabricate — build, test, verify.
#
# The root module contains the CLI, infrastructure engines, and compiled HTTP resources.

FAB_INSTALL_DIR ?= $(HOME)/bin

.PHONY: build install install-dev test vet fmt check generate generate-check e2e verify clean start landing docs

# `make start` runs both public-site dev servers. Add `landing` or `docs` to
# start just that app: `make start landing` or `make start docs`.
START_APPS := $(filter landing docs,$(MAKECMDGOALS))

start:
ifeq ($(START_APPS),landing)
	pnpm landing:dev
else ifeq ($(START_APPS),docs)
	pnpm docs:dev
else
	pnpm site:dev
endif

# These are selector goals consumed by `start`; they deliberately do nothing
# on their own so `make start landing` does not launch a second process.
landing docs:
	@:

build:
	go build -o bin/fab ./cmd/fab
	@echo "bin/fab"

install:
	@mkdir -p "$(FAB_INSTALL_DIR)"
	go build -o "$(FAB_INSTALL_DIR)/fab" ./cmd/fab
	@echo "fab → $(FAB_INSTALL_DIR)/fab"

# Contributor command: install an absolute symlink back to this checkout.
# fab-dev recompiles through Go's build cache on every invocation, so it always
# executes the current source while preserving the caller's working directory.
install-dev:
	@FAB_INSTALL_DIR="$(FAB_INSTALL_DIR)" ./scripts/install-dev.sh

# Fast unit tests. No Docker required.
test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

# check is the fast inner loop: format, vet, unit tests. Run this after
# every change; it needs no Docker and finishes in seconds.
check: vet test
	@out=$$(gofmt -l . 2>/dev/null); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@echo "check: OK"

# Compiled HTTP resources own committed strict server bindings. The generator
# is pinned through go.mod's tool directive; no global binary is required.
generate:
	go generate ./resources/...

generate-check: generate
	git diff --exit-code -- 'resources/*/generated/**'

# End-to-end smoke for container engines through the real CLI.
e2e:
	go test -tags e2e -count=1 -timeout 15m -v ./e2e/...

# HTTP-resource conformance belongs in root-module Go tests and each
# resource's official-client proxy suite as it is brought back.
verify: check e2e
	@echo "verify: OK"

clean:
	rm -rf bin
