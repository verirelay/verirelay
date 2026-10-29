#!/usr/bin/env bash
#
# Single entry point for building the init binary. Local builds and CI must both
# call this script: if hand-typed go build flags ever diverge from CI, the
# sha256 recorded in the release proves nothing.
#
# Usage: scripts/build-init.sh [output path]
# Default: <repo>/bin/verirelay-linux-amd64
#
# Pinned recipe (any change alters the artifact bytes, so the [expected]
# baseline in the release repository's versions.lock must be updated in the
# same change):
#   GOENV=off GOFLAGS='' GOEXPERIMENT=''
#       Shields the build from implicit go env settings and experiments in the
#       host or CI environment.
#   CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1
#       Static Linux/amd64; PID 1 in the initrd has no dynamic linker.
#   GOTOOLCHAIN=go1.26.10
#       The toolchain is pinned by version, so the host's installed Go does
#       not affect the artifact.
#   -trimpath -buildvcs=false -mod=readonly -ldflags='-buildid='
#       Removes build paths, VCS metadata, and the build id, the three
#       sources of nondeterminism; -mod=readonly makes go.sum drift a hard
#       failure.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd -- "$SCRIPT_DIR/.." && pwd)"

OUT="${1:-$PROJECT_DIR/bin/verirelay-linux-amd64}"
mkdir -p "$(dirname -- "$OUT")"

cd "$PROJECT_DIR"

env \
  GOENV=off \
  GOFLAGS='' \
  GOEXPERIMENT='' \
  CGO_ENABLED=0 \
  GOOS=linux \
  GOARCH=amd64 \
  GOAMD64=v1 \
  GOTOOLCHAIN=go1.26.10 \
  go build \
    -trimpath \
    -buildvcs=false \
    -mod=readonly \
    -ldflags='-buildid=' \
    -o "$OUT" \
    ./cmd/verirelay

sha256sum "$OUT"
