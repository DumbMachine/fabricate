# ADR 0001: keep HTTP resources in the root module and use modernc SQLite

Status: accepted for the HTTP/API v1 implementation

Fabricate's compiled HTTP resources, shared engine, supervisor, and CLI live in
the root Go module. The separate legacy API module has been removed; provider
APIs return by implementing the root-module `httpresource.Resource` contract.

New HTTP state uses `modernc.org/sqlite`. It keeps the reference path free of
CGO and makes local and CI execution use the same Go binary. Every service
instance owns separate baseline and live database files. Foreign keys and a
bounded busy timeout are configured explicitly; database handles are closed
before baseline copies or replacements. WAL is not enabled until reset/copy
tests demonstrate a need and a safe checkpoint sequence.

The trade-off is a larger Go dependency and binary. This is preferable to a
second module, platform-specific C toolchains, or a second implementation in a
container.
