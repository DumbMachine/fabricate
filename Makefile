# fabricate — build, test, verify.
#
# The three-module layout:
#   .        the fab CLI + engines (module github.com/dumbmachine/fabricate)
#   mockd/   the httpmock server that runs inside the container (own module, CGO/SQLite)
#   engine/github/proxy/  tiny proxy baked into the emulate image (own module)

FAB_INSTALL_DIR ?= $(HOME)/bin

.PHONY: build install test vet fmt check images push-images e2e sdk-e2e verify clean

build:
	go build -o bin/fab ./cmd/fab
	@echo "bin/fab"

install:
	@mkdir -p "$(FAB_INSTALL_DIR)"
	go build -o "$(FAB_INSTALL_DIR)/fab" ./cmd/fab
	@echo "fab → $(FAB_INSTALL_DIR)/fab"

# Fast unit tests across all modules. No Docker required.
test:
	go test ./...
	cd mockd && go test ./...
	cd engine/github/proxy && go build ./...

vet:
	go vet ./...
	cd mockd && go vet ./...
	cd engine/github/proxy && go vet ./...

fmt:
	gofmt -l -w .

# check is the fast inner loop: format, vet, unit tests. Run this after
# every change; it needs no Docker and finishes in seconds.
check: vet test
	@out=$$(gofmt -l . 2>/dev/null); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@echo "check: OK"

# Local Docker images for the httpmock and github (emulate) engines.
# Build once before `fab create gmail|linear|google-play|github ...`.
images:
	docker build -t fabricate/httpmock:local mockd
	docker build -t fabricate/emulate:local engine/github

# Publish multi-arch images to a registry (default GHCR). Normally CI
# does this on a v* tag (.github/workflows/release.yml); run manually
# for a one-off:
#   make push-images IMAGE_TAG=v0.1.0
# Requires `docker login ghcr.io` and a buildx builder (docker buildx
# create --use, once). See docs/releasing.md.
REGISTRY  ?= ghcr.io/dumbmachine
IMAGE_TAG ?= latest
PLATFORMS ?= linux/amd64,linux/arm64

push-images:
	docker buildx build --platform $(PLATFORMS) \
		-t $(REGISTRY)/fabricate-httpmock:$(IMAGE_TAG) --push mockd
	docker buildx build --platform $(PLATFORMS) \
		-t $(REGISTRY)/fabricate-emulate:$(IMAGE_TAG) --push engine/github

# End-to-end smoke: real Docker containers via the real CLI. Needs a
# running Docker daemon; httpmock cases also need `make images`.
e2e:
	go test -tags e2e -count=1 -timeout 15m -v ./e2e/...

# SDK conformance: drive the REST httpmock services with each vendor's
# OFFICIAL client (googleapis, ...), proving the mocks match the real wire
# contract — not just hand-written curl. Needs Docker + `make images` +
# Node; skips per-service (with a message) when a prerequisite is missing.
sdk-e2e:
	go build -o bin/fab ./cmd/fab
	cd e2e/sdk && npm install --no-audit --no-fund --silent && FAB_BIN="$(CURDIR)/bin/fab" npm test

# verify is the full loop an agent (or CI) runs before calling work done:
# fast checks first, then the Docker-backed end-to-end smoke, then the
# official-client conformance suite.
verify: check e2e sdk-e2e
	@echo "verify: OK"

clean:
	rm -rf bin
