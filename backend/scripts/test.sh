#!/usr/bin/env bash
# Run API tests with merged coverage across internal packages.
# Needs TimescaleDB: `docker compose up -d timescale` (or set VG_TEST_DATABASE_URL).
set -euo pipefail
cd "$(dirname "$0")/.."
pkgs=$(go list ./internal/... | grep -v testutil | paste -sd, -)
go test -count=1 -coverpkg="$pkgs" -coverprofile=coverage.out "$@" ./...
go tool cover -func=coverage.out | tail -1
