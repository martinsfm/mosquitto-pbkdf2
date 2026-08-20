#!/usr/bin/env bash
# Cross-compiles a single, self-contained gateway.exe for Windows.
# The result has zero runtime dependencies: no .NET, no Python, no Java,
# no DLLs beyond what every Windows install already ships - copy the .exe
# (and a gateway.yaml next to it) to the target machine and run it.
set -euo pipefail
cd "$(dirname "$0")/.."

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags="-s -w" -o build/gateway.exe ./cmd/gateway

echo "Built build/gateway.exe"
