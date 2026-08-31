#!/usr/bin/env bash

# Internal frontend overlay runner. Contributors should use ./test.

set -euo pipefail

if [ "$#" -lt 2 ]; then
  echo "Usage: ./run-overlay-script.sh /path/to/core script/path [args...]" >&2
  exit 1
fi

CORE_DIR="$(cd "$1" && pwd)"
SCRIPT_PATH="$2"
shift 2

if [ ! -f "$CORE_DIR/go.mod" ]; then
  echo "Error: $CORE_DIR does not appear to be the core repo (no go.mod found)" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/core-test-script-XXXXX")"

cleanup() {
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

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

rsync -a \
  --exclude='.git' \
  --exclude='.worktrees' \
  --exclude='README.md' \
  --exclude='node_modules/' \
  --exclude='playwright-report*/' \
  --exclude='test-results*/' \
  --exclude='*.db' \
  --exclude='*.db-shm' \
  --exclude='*.db-wal' \
  "$SCRIPT_DIR/" "$WORK_DIR/"

if [ ! -f "$WORK_DIR/$SCRIPT_PATH" ]; then
  echo "Error: overlaid script not found: $SCRIPT_PATH" >&2
  exit 1
fi

cd "$WORK_DIR"
"$WORK_DIR/scripts/run-with-core-node.sh" "$WORK_DIR" bash "$SCRIPT_PATH" "$@"
