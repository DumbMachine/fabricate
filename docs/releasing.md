# Releasing (binaries and images)

Two things ship on every `v*` tag, from `.github/workflows/release.yml`:

| Artifact | Built by | Where it lands |
|---|---|---|
| `fab` CLI binaries | GoReleaser (`.goreleaser.yaml`) | archives attached to the GitHub release |
| httpmock server image | `docker buildx` from `mockd/` | `ghcr.io/dumbmachine/fabricate-httpmock:{vX,latest}` |
| emulate (GitHub) image | `docker buildx` from `engine/github/` | `ghcr.io/dumbmachine/fabricate-emulate:{vX,latest}` |

End users install the binary with no build step:

```bash
curl -fsSL https://raw.githubusercontent.com/dumbmachine/fabricate/main/install.sh | sh
```

`install.sh` downloads the archive for the host OS/arch from
`releases/latest/download/` (the archive names are versionless —
`fabricate_<os>_<arch>.tar.gz` — so that URL resolves without an API
call; keep `.goreleaser.yaml`'s `name_template` and `install.sh` in sync).

## How image resolution works

Engines pick an image in this order:

1. `--image` flag on `fab create`
2. `image:` in the profile (built-in profiles deliberately don't pin)
3. `$FAB_HTTPMOCK_IMAGE` / `$FAB_EMULATE_IMAGE`
4. the binary's compiled-in `defaultImage`

The compiled default is set **per build**:

- **Released binary** — GoReleaser injects the GHCR image pinned to the
  release tag via `-ldflags -X …/engine/httpmock.defaultImage=…:vX` (and
  the emulate equivalent). So a released `fab` auto-pulls the matching
  image with no `make images` and no env vars. Pinned, not `latest`, so
  an old binary never silently picks up a newer, incompatible server.
- **Source / `make install` build** — no ldflags, so `defaultImage`
  stays the `:local` dev tag that `make images` builds. Developers
  iterating on mockd just `make images`; to test a published image,
  set `FAB_HTTPMOCK_IMAGE`.

This is automatic — there is **no per-release code edit** to bump the
default (it used to be a manual `const`; it's now an ldflags-injected
`var`). Just tag.

## Cutting a release

```bash
git tag v0.2.0
git push origin v0.2.0
```

The `release` workflow runs two jobs in parallel: `binaries` (GoReleaser
creates the GitHub release and uploads the CLI archives + checksums) and
`images` (buildx pushes both images to GHCR, `v0.2.0` + `latest`).

**One-time, after the first release that pushes them:** GHCR packages
default to private, so an anonymous `docker pull` fails with
`unauthorized` and auto-pull won't work. Open each package on GitHub
(Profile → Packages → package → settings) → Change visibility → Public,
and link it to the repo. Until this is done, the curl-installed binary
can't pull its image; verify with `docker pull ghcr.io/dumbmachine/fabricate-httpmock:latest`
from a logged-out Docker.

## Version coupling

The fixture format and `MOCK_SERVICE` contract are the interface between
the fab binary and the httpmock image. Pinning the default image to the
binary's own release tag keeps that interface stable without inventing a
protocol version — a `v0.2.0` binary always talks to the `v0.2.0` image.
