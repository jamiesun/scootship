---

## Release assets

Each release publishes single-binary archives for Linux, macOS, and Windows:

- `scootship-vX.Y.Z-linux-amd64.tar.gz`
- `scootship-vX.Y.Z-linux-arm64.tar.gz`
- `scootship-vX.Y.Z-linux-armv7.tar.gz`
- `scootship-vX.Y.Z-macos-amd64.tar.gz`
- `scootship-vX.Y.Z-macos-arm64.tar.gz`
- `scootship-vX.Y.Z-windows-amd64.tar.gz`

Every archive has a matching `.sha256` file. Verify before installing:

```sh
sha256sum -c scootship-vX.Y.Z-linux-amd64.tar.gz.sha256
```

Release binaries are built with `CGO_ENABLED=0`, `-trimpath`, and a tag-derived version injected into `internal/version.Version`. The dashboard assets remain embedded in the binary; no Node runtime, external web server, or CDN dependency is required.
