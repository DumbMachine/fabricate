# fabricate — build, test, verify.
#
# The root module contains the CLI, infrastructure engines, and compiled HTTP resources.

FAB_INSTALL_DIR ?= $(HOME)/bin

MAKE_TARGETS := build install install-dev test vet fmt check generate generate-check e2e conformance docs-examples docs-operations verify clean start landing docs

.PHONY: $(MAKE_TARGETS)

# Extra goals are resource names: `make conformance gmail asana`,
# `make docs-examples gmail`. Known targets are excluded so
# `make docs-examples check` still runs `check`.
RESOURCE_SELECTORS := $(filter-out $(MAKE_TARGETS),$(MAKECMDGOALS))

ifneq ($(filter conformance docs-examples docs-operations,$(MAKECMDGOALS)),)
ifneq ($(RESOURCE_SELECTORS),)
.PHONY: $(RESOURCE_SELECTORS)
$(RESOURCE_SELECTORS):
	@:
endif
endif

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
# generate-check also dumps operation and scenario catalogs so docs stay
# locked to the compiled OpenAPI surface and catalog scenario state.
generate:
	go generate ./resources/...

generate-check: generate
	@$(MAKE) docs-operations
	git diff --exit-code -- 'resources/*/generated/**' 'packages/docs-content/resources/_generated/*.operations.json' 'packages/docs-content/resources/_generated/*.scenarios.json'

# End-to-end smoke for `fab run` through the real CLI.
e2e:
	go test -tags e2e -count=1 -timeout 15m -v ./e2e/...

# Black-box official-client / curl conformance. Writes compatibility reports
# into packages/docs-content/resources/_generated/. Default is every resource
# with resources/<id>/conformance/{curl.sh and/or sdk.json}.
# `make conformance gmail asana` limits to those resources.
conformance:
	@tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/fabricate-conformance-bin.XXXXXX"); trap 'rm -rf "$$tmp"' EXIT; \
	go build -o "$$tmp/fab" ./cmd/fab; \
	go build -o "$$tmp/docsexamples" ./cmd/docsexamples; \
	ids="$(RESOURCE_SELECTORS)"; \
	if [ -z "$$ids" ]; then ids=$$(./scripts/test.sh --list); fi; \
	if [ -z "$$ids" ]; then echo "conformance: no resources found" >&2; exit 1; fi; \
	DOCSEXAMPLES_BIN="$$tmp/docsexamples" ./scripts/test.sh "$$tmp/fab" $$ids

# Recapture dirty command snapshots against local environment manifests.
# Default considers the full catalog and skips unchanged provenance.
# `make docs-examples gmail asana` limits to those resources; unknown
# names fail. Force every selected example: make docs-examples DOCS_EXAMPLES_FLAGS=--all
docs-examples:
	@tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/fabricate-docs-examples-bin.XXXXXX"); trap 'rm -rf "$$tmp"' EXIT; \
	go build -o "$$tmp/fab" ./cmd/fab; \
	go build -o "$$tmp/docsexamples" ./cmd/docsexamples; \
	"$$tmp/docsexamples" capture --repo "$(CURDIR)" --fab "$$tmp/fab" $(DOCS_EXAMPLES_FLAGS) $(foreach r,$(RESOURCE_SELECTORS),--resource $(r))

# Dump compiled HTTP operations and catalog scenario counts into
# packages/docs-content/resources/_generated/. Default is every official
# resource. `make docs-operations gmail asana` limits to those resources;
# unknown names fail. CI fails if the catalogs drifted.
docs-operations:
	@tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/fabricate-docs-operations-bin.XXXXXX"); trap 'rm -rf "$$tmp"' EXIT; \
	go build -o "$$tmp/docsexamples" ./cmd/docsexamples; \
	"$$tmp/docsexamples" operations --repo "$(CURDIR)" $(foreach r,$(RESOURCE_SELECTORS),--resource $(r))

# Unit tests plus container e2e. Resource clients are `make conformance`.
verify: check e2e
	@echo "verify: OK"

clean:
	rm -rf bin
