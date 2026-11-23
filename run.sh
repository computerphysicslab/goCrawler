#!/usr/bin/env bash
# Run goCrawler from any directory by resolving the Go module root first.
# `go run crawler.go` resolves the goCrawler/... imports from the current
# working directory, so invoking it from outside the module fails with
# "cannot find package goCrawler/iolib". This wrapper avoids that.
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

GO="${GO:-go}"
exec "$GO" run crawler.go "$@"
