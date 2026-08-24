# Releasing

Version tags trigger GoReleaser through `.github/workflows/release.yml`.
GoReleaser cross-compiles `fab` for macOS and Linux on amd64 and arm64,
publishes versionless archive names for `install.sh`, and emits checksums.

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

The compiled HTTP engine and official provider resources ship inside the `fab`
binary. There are no separate `httpmock` or emulate images to publish. Normal
upstream infrastructure images continue to be pulled by their container
engines on demand.

Before tagging, run `make check`, `make generate-check`, and the relevant
official-client transparent-proxy test for every changed HTTP resource. Run
`make e2e` for changed container lifecycle behavior.
