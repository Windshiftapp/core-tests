#!/bin/bash
# Run the Go test suite via overlay onto core, then render an HTML report.
#
# Usage:
#   ./tests/run-tests.sh                     # Full Go test surface (./...)
#   ./tests/run-tests.sh -run Pattern        # Run specific tests
#   ./tests/run-tests.sh -v                  # Verbose
#   ./tests/run-tests.sh ./tests             # Narrow scope to a single package
#
# Engine selection (matches the GitHub Actions workflow):
#   TEST_DB_TYPE=postgres TEST_POSTGRES_DSN=<dsn> ./tests/run-tests.sh
#
# core-tests is not a Go module on its own; this script overlays it onto the
# adjacent core/ checkout (mirrors overlay.sh) into a temp dir and runs
# `go test` there. The HTML report and JSON output are written back to the
# real core-tests/tests/reports/ so they're easy to find.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TESTS_REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)" # core-tests/
CORE_DIR="$(cd "$TESTS_REPO_ROOT/../core" && pwd 2>/dev/null || true)"

if [ -z "$CORE_DIR" ] || [ ! -f "$CORE_DIR/go.mod" ]; then
  echo "Error: could not locate the core repo at $TESTS_REPO_ROOT/../core (no go.mod)." >&2
  echo "Set up a sibling core/ checkout, or run via core-tests/overlay.sh /path/to/core." >&2
  exit 1
fi

REPORT_DIR="$SCRIPT_DIR/reports"
REPORT_FILE="$REPORT_DIR/test-report.html"
JSON_OUTPUT="$REPORT_DIR/test-output.json"
mkdir -p "$REPORT_DIR"

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

# Resolve scope. Default to the full Go test surface (./...), matching the
# GitHub Actions test-sqlite / test-postgres jobs. Pass `./tests` (or any other
# narrower path) to scope down — useful when iterating on a single package.
TEST_ARGS=("$@")
HAS_PACKAGE=false
for a in "$@"; do
  case "$a" in
    ./*|.|...|*/...) HAS_PACKAGE=true ;;
  esac
done
if [ "$HAS_PACKAGE" = false ]; then
  # Default scope: ./internal/... + ./tests/... . Excludes `main`, which
  # embeds frontend/dist/ (//go:embed all:frontend/dist) — frontend/ is
  # excluded from the overlay rsync since no Go test touches it.
  TEST_ARGS+=("./internal/..." "./tests/...")
fi

# Overlay core + core-tests into a temp dir (same recipe as overlay.sh).
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/core-tests-XXXXX")
cleanup() {
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

echo "==> Overlaying core ($CORE_DIR) + tests into $WORK_DIR"
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
  --exclude='frontend/' \
  "$CORE_DIR/" "$WORK_DIR/"
rsync -a \
  --exclude='.git' \
  --exclude='overlay.sh' \
  --exclude='README.md' \
  --exclude='.github' \
  --exclude='node_modules/' \
  "$TESTS_REPO_ROOT/" "$WORK_DIR/"

# Clean up stale per-test SQLite files from prior interrupted runs (older than
# 5 minutes — preserves any concurrent run's databases).
find "$WORK_DIR" -maxdepth 1 -name "test_*.db*" -type f -mmin +5 -delete 2>/dev/null || true
find "${TMPDIR:-/tmp}" -maxdepth 2 -name "windshift-tests" -type d -mmin +30 -exec rm -rf {} \; 2>/dev/null || true

cd "$WORK_DIR"

# Build the report-renderer. Source is overlaid from core-tests so it lives
# at <work>/tests/cmd/testreport/main.go.
TESTREPORT_BIN="$WORK_DIR/.testreport"
go build -o "$TESTREPORT_BIN" ./tests/cmd/testreport

echo "==> Running tests: ${TEST_ARGS[*]}"
echo ""
set +e
go test -tags=test -json "${TEST_ARGS[@]}" 2>&1 | tee "$JSON_OUTPUT" | "$TESTREPORT_BIN" > "$REPORT_FILE"
TEST_EXIT_CODE=${PIPESTATUS[0]}
set -e

# Per-test pass/fail counts (the JSON also has package-level events; filter on
# Test to count only individual cases).
PASSED=$(grep '"Action":"pass"' "$JSON_OUTPUT" 2>/dev/null | grep -c '"Test":' || true)
FAILED=$(grep '"Action":"fail"' "$JSON_OUTPUT" 2>/dev/null | grep -c '"Test":' || true)
PASSED=$((PASSED + 0))
FAILED=$((FAILED + 0))

echo ""
echo "========================================"
echo "Test Results Summary"
echo "========================================"
if [ "$FAILED" -gt 0 ]; then
  echo -e "${RED}FAILED${NC}"
  echo -e "  Passed: ${GREEN}$PASSED${NC}"
  echo -e "  Failed: ${RED}$FAILED${NC}"
else
  echo -e "${GREEN}PASSED${NC}"
  echo -e "  Passed: ${GREEN}$PASSED${NC}"
fi
echo ""
echo "HTML Report: $REPORT_FILE"
echo "JSON Output: $JSON_OUTPUT"
echo ""

if [[ "$OSTYPE" == "darwin"* ]]; then
  echo "Opening report in browser..."
  open "$REPORT_FILE" 2>/dev/null || true
fi

exit "$TEST_EXIT_CODE"
