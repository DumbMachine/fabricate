# Private infrastructure profile catalogs

Organizations can ship private infrastructure profiles from a wrapper binary
without forking Fabricate. Embed the usual `<engine>/<name>/profile.yaml`
layout, create an `fs.FS` rooted at the catalog, and call
`profile.RegisterCatalog("name", catalogFS)` before `cli.Execute()`.

Registered catalogs participate in the normal precedence stack; user profiles
still shadow matching catalog entries. The profile schema and supported engine
set remain public contracts.

Compiled HTTP resources are intentionally not loaded from profile catalogs.
An organization adding a provider API builds it as a root-module
`httpresource.Resource`, registers it in its binary's resource registry, and
uses environment manifests and scenarios. Dynamic resource/plugin loading is
not part of HTTP/API v1.
