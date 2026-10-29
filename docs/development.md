English | [简体中文](development.zh-CN.md)

# Development

## Prerequisites

- Go `1.26.10` (the `go` directive in `go.mod`).
- Linux. The binary builds only for Linux.
- Redis and Valkey for the storage integration tests.

## Build

```bash
go build ./...
```

## Tests

```bash
go vet ./...
go test ./...
go test -race ./internal/storage ./internal/proxy
```

Redis/Valkey integration tests run behind the `integration` build tag:

```bash
VERIRELAY_TEST_REDIS_ADDR=127.0.0.1:6379 go test -tags=integration ./internal/storage
VERIRELAY_TEST_VALKEY_ADDR=127.0.0.1:6380 go test -tags=integration ./internal/storage
```

## Deterministic init build

`scripts/build-init.sh` is the single entry point for building the init binary, for local builds and CI alike:

```bash
./scripts/build-init.sh [output path]
```

Default output: `bin/verirelay-linux-amd64`.

The script pins the toolchain, environment, and build flags, so the artifact is byte-for-byte reproducible. Any change to that recipe changes the artifact and must update the `[expected]` baseline in the [release](https://github.com/verirelay/release) repository's `versions.lock`.

## CI

`.github/workflows/ci.yml` runs on pushes to `main` and on pull requests:

1. gofumpt format check.
2. `go vet ./...`.
3. `golangci-lint`.
4. `go test ./...`.
5. The `build-init.sh` build, and an assertion that the artifact is a statically linked x86-64 ELF.
6. `actionlint` on the workflow files.

`make check` runs format, vet, test, build, and lint locally at the versions CI pins.

## Release

The release workflow (`release.yml`) runs on `v*` tags or by manual dispatch on an existing tag:

1. Rebuild the init binary with `build-init.sh`.
2. Write `SHA256SUMS` and a component build report (`verirelay-component-build.json`, `verirelay-component-build/1`).
3. Sign the artifacts with cosign keyless.
4. Add GitHub provenance attestation.
5. Publish a GitHub release.
