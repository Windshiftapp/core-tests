#!/bin/bash
# Internal Go overlay runner. Contributors should use ./test.
#
# Usage:
#   ./overlay.sh /path/to/core [-- go test args...]
#
# This script creates a temporary directory, copies the main core repo
# and overlays test files on top, then runs `go test` there. The main
# repo is never modified.
#
# Examples:
#   ./overlay.sh ../core
#   ./overlay.sh ../core -- -run TestSpecific -v
#   ./overlay.sh ../core -- -tags=test -count=1 ./internal/handlers/...

set -euo pipefail

if [ -z "${1:-}" ] || [ "$1" = "--" ]; then
  echo "Usage: ./overlay.sh /path/to/core [-- go test args...]"
  echo ""
  echo "Runs tests in an isolated temp directory. The main repo is never modified."
  echo ""
  echo "Examples:"
  echo "  ./overlay.sh ../core"
  echo "  ./overlay.sh ../core -- -run TestSpecific -v"
  echo "  ./overlay.sh ../core -- -tags=test -count=1 ./internal/handlers/..."
  exit 1
fi

CORE_DIR="$(cd "$1" && pwd)"
shift

if [ ! -f "$CORE_DIR/go.mod" ]; then
  echo "Error: $CORE_DIR does not appear to be the core repo (no go.mod found)"
  exit 1
fi

# Consume the optional "--" separator
if [ "${1:-}" = "--" ]; then
  shift
fi

# Default test args if none provided. Scope is the test-bearing packages:
# ./internal/... + ./tests/... . Explicit CI invocations use ./..., so the
# overlay retains the built frontend/dist required by main.go's go:embed.
if [ $# -eq 0 ]; then
  # Run internal and full-server packages separately. Integration packages
  # boot a full server per test and need a larger timeout.
  # 25m: ./tests boots a full server per test; fracindex stress alone ~10m.
  INTERNAL_ARGS=(-tags=test -timeout=25m ./internal/...)
  INTEGRATION_ARGS=(-tags=test -timeout=25m ./tests/...)
  RUN_DEFAULT_SUITE=1
else
  TEST_ARGS=("$@")
  RUN_DEFAULT_SUITE=0
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/core-test-XXXXX")

cleanup() {
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

echo "==> Creating test workspace in $WORK_DIR"

# Copy main repo (exclude .git and build artifacts)
rsync -a \
  --exclude='.git' \
  --exclude='bin/' \
  --exclude='/dist/' \
  --exclude='*.db' \
  --exclude='*.db-shm' \
  --exclude='*.db-wal' \
  --exclude='coverage.out' \
  --exclude='windshift' \
  --exclude='ethereal' \
  --exclude='.DS_Store' \
  --exclude='node_modules/' \
  --exclude='/data/' \
  --exclude='releases/' \
  "$CORE_DIR/" "$WORK_DIR/"

# Overlay test files on top
rsync -a \
  --exclude='.git' \
  --exclude='overlay.sh' \
  --exclude='README.md' \
  --exclude='.github' \
  "$SCRIPT_DIR/" "$WORK_DIR/"

echo "==> Running tests"
cd "$WORK_DIR"
set +e
EXIT_CODE=0

if [ "$RUN_DEFAULT_SUITE" -eq 1 ]; then
  go test "${INTERNAL_ARGS[@]}"
  if [ $? -ne 0 ]; then
    EXIT_CODE=1
  fi

  go test "${INTEGRATION_ARGS[@]}"
  if [ $? -ne 0 ]; then
    EXIT_CODE=1
  fi
else
  go test "${TEST_ARGS[@]}"
  EXIT_CODE=$?
fi
set -e

if [ $EXIT_CODE -eq 0 ]; then
  echo "==> Tests passed"
else
  echo "==> Tests failed (exit code $EXIT_CODE)"
fi

echo "==> Cleaning up $WORK_DIR"
exit $EXIT_CODE
