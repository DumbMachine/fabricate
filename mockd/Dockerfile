# Local-only image for fab's stateful httpmock engine. Builds the Go mock
# server (engine/httpmock/server — multi-package, SQLite via mattn/go-sqlite3)
# with CGO and ships it on alpine (musl libc, matching the build base). The
# engine bind-mounts a profile's fixture at /seed.json and sets MOCK_SERVICE.
#
# Build with:
#   make images
# or directly:
#   docker build -t fabricate/httpmock:local mockd
#
# Build context is engine/httpmock so the server/ sources are in scope.

FROM golang:1.23-alpine AS build
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY . ./
RUN CGO_ENABLED=1 go build -o /out/fab-httpmock-server .

FROM alpine:3.20
COPY --from=build /out/fab-httpmock-server /usr/local/bin/fab-httpmock-server
ENV SEED_FILE=/seed.json \
    PORT=8080 \
    DB_PATH=/tmp/mock.db
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/fab-httpmock-server"]
