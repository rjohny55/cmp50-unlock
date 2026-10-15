#!/usr/bin/env bash
# Target Ubuntu/Debian glibc, including when cross-building on Alpine/musl.
set -Eeuo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath \
    -ldflags='-I /lib64/ld-linux-x86-64.so.2 -s -w -X main.version=1.0.1' \
    -o bin/cmp-idle-governor-linux-amd64 ./cmd/cmp-idle-governor
printf 'Built Ubuntu/Debian linux/amd64 binary; review sha256sum before updating checksums.sha256.\n'
