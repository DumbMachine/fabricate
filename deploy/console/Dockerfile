FROM node:24-bookworm-slim AS console
WORKDIR /src
RUN npm install --global pnpm@11.3.0
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY apps/console/package.json apps/console/package.json
RUN pnpm --filter @fabricate/console... install --frozen-lockfile
COPY apps/console apps/console
RUN pnpm --filter @fabricate/console typecheck
RUN pnpm --filter @fabricate/console test
RUN pnpm --filter @fabricate/console build

FROM golang:1.25-bookworm AS binary
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=console /src/console/ui console/ui
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/fab ./cmd/fab

FROM debian:bookworm-slim
RUN apt-get update && apt-get install --no-install-recommends -y ca-certificates curl && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home fabricate && install -d -o fabricate -g fabricate /var/lib/fabricate/logs /var/lib/fabricate/state
COPY --from=binary /out/fab /usr/local/bin/fab
USER 10001:10001
ENV FAB_LOG_DIR=/var/lib/fabricate/logs
EXPOSE 4173
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s CMD curl --fail --silent http://127.0.0.1:4173/healthz || exit 1
ENTRYPOINT ["fab", "serve", "--addr", "0.0.0.0:4173"]
CMD []
