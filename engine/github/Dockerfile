# Local-only image used by fab's emulate-backed engines (github today,
# stripe / slack / ... when we extend the lineup). Lives next to the
# github engine because that's the first consumer; the image itself
# is generic — the engine picks the service via EMULATE_SERVICE env.
#
# Build with:
#   make images
# or directly:
#   docker build -t fabricate/emulate:local engine/github
#
# Wraps the npm `emulate` package (vercel-labs/emulate). The fab engine
# bind-mounts the profile's seed YAML at /seed.yaml before container
# start, then sets EMULATE_SERVICE=<service> to pick which emulator
# this container actually runs.
#
# A tiny Go reverse proxy sits in front of emulate to rewrite the
# `localhost:4000` URLs emulate stamps into response bodies — without
# this, anything an SDK extracts from an `*_url` field is unreachable on
# the consumer's port mapping. Sources live next to this Dockerfile
# under proxy/.

FROM golang:1.23-alpine AS proxy-build
WORKDIR /src
COPY proxy/go.mod ./
COPY proxy/main.go ./
RUN CGO_ENABLED=0 go build -o /out/emulate-proxy ./...

FROM node:20-alpine

# Pinned. Bump deliberately when an upstream wire-format change is
# needed; rebuild the image and refresh integration goldens.
ARG EMULATE_VERSION=0.6.0
RUN npm install -g emulate@${EMULATE_VERSION}

COPY --from=proxy-build /out/emulate-proxy /usr/local/bin/emulate-proxy
COPY run.sh /run.sh
RUN chmod +x /run.sh

ENV EMULATE_SEED=/seed.yaml \
    EMULATE_SERVICE=github \
    EMULATE_BACKEND_PORT=4001 \
    EMULATE_PROXY_PORT=4000

EXPOSE 4000

ENTRYPOINT ["/run.sh"]
