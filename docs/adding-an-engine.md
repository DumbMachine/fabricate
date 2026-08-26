# Adding an infrastructure engine

An engine provisions real infrastructure software such as a database, cache,
broker, or host. A provider-shaped HTTP API belongs under `resources/<id>`; see
[Adding an HTTP API resource](adding-an-http-api-resource.md).

1. Add `engine/<id>/<id>.go` implementing `engine.Engine` (`Info`, `Create`,
   `Destroy`). `engine/redis` is the smallest reference.
2. Register it in `cli/engines.go`.
3. Add at least one built-in profile under `profiles/<id>/` and include that
   directory in `profiles/embed.go`.
4. Reject unsupported seed types and clean partial resources on every failure.
5. Keep `Destroy` idempotent so stale local state cannot strand resources.
6. Extend `target/kubetarget` only when the engine genuinely supports that
   target.
7. Add unit tests, then run `make check`; add or update `make e2e` lifecycle
   coverage when the behavior needs a real container.

Images should be ordinary upstream product images whenever possible. A new
Fabricate-owned provider API image is not an engine extension point.
