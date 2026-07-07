# Releasing (binaries and images)

Two things ship: the `fab` binary (a plain Go module — users
`go install` or `make install` from a clone) and the two engine
images the `httpmock` and `github` engines run:

| Image | Built from | Local dev tag | Published name |
|---|---|---|---|
| httpmock server | `mockd/` | `fabricate/httpmock:local` | `ghcr.io/dumbmachine/fabricate-httpmock` |
| emulate (GitHub) | `engine/github/` | `fabricate/emulate:local` | `ghcr.io/dumbmachine/fabricate-emulate` |

## How image resolution works

Engines pick an image in this order:

1. `--image` flag on `fab create`
2. `image:` in the profile (built-in profiles deliberately don't pin,
   so the default can evolve without touching every profile)
3. `$FAB_HTTPMOCK_IMAGE` / `$FAB_EMULATE_IMAGE`
4. the engine's compiled-in default (currently the `:local` dev tags)

So today — before any published release — `make images` is required
once per machine. After images are published, anyone can skip the
build with:

```bash
export FAB_HTTPMOCK_IMAGE=ghcr.io/dumbmachine/fabricate-httpmock:latest
export FAB_EMULATE_IMAGE=ghcr.io/dumbmachine/fabricate-emulate:latest
```

## Cutting a release

```bash
git tag v0.1.0
git push origin v0.1.0
```

The `release` workflow builds both images for linux/amd64 + linux/arm64
and pushes them to GHCR tagged `v0.1.0` and `latest`.

**One-time, after the first release:** GHCR packages default to
private. Open each package on GitHub (Profile → Packages) → Package
settings → Change visibility → Public, so `fab` users can pull
anonymously. Also link the packages to the repo for discoverability.

## Flipping the defaults (the plan)

Once the first release is published and verified, change the
compiled-in defaults so a fresh `go install` works with zero local
image builds:

1. In `engine/httpmock/httpmock.go` and `engine/github/github.go`, set
   `defaultImage` to the **pinned version tag** (e.g.
   `ghcr.io/dumbmachine/fabricate-httpmock:v0.1.0`) — pinned, not
   `latest`, so an old binary never silently picks up a newer,
   incompatible server.
2. Keep `make images` + the `:local` tags as the dev loop; developers
   iterating on mockd override with
   `FAB_HTTPMOCK_IMAGE=fabricate/httpmock:local`.
3. Bump the pinned tag as part of each release that changes mockd or
   the emulate image (grep for `defaultImage`).
4. Update the "requires the fabricate httpmock image" notes in the
   httpmock/github profile descriptions to drop the `make images`
   caveat.

The version-coupling rule of thumb: the fixture format and
`MOCK_SERVICE` contract are the interface between the fab binary and
the httpmock image. Pinning the default image per binary release keeps
that interface stable without inventing a protocol version.

## Binary releases (optional, later)

`go install github.com/dumbmachine/fabricate/cmd/fab@latest` already
works once the repo is public. If prebuilt binaries become worth it,
add GoReleaser on the same `v*` tag trigger; nothing in the layout
blocks it.
