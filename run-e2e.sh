#!/usr/bin/env bash
set -euo pipefail

# Internal Playwright runner. Contributors should use ./test.

# ─── Colors ───────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

info()  { echo -e "${BLUE}▸${NC} $*"; }
ok()    { echo -e "${GREEN}✔${NC} $*"; }
warn()  { echo -e "${YELLOW}⚠${NC} $*"; }
err()   { echo -e "${RED}✖${NC} $*"; }

# ─── Resolve paths ───────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$SCRIPT_DIR/e2e"
# Honor a caller-provided CORE_DIR so the runner can target a worktree
# instead of the sibling core/. Resolve the path to an absolute one.
CORE_DIR="$(cd "${CORE_DIR:-$SCRIPT_DIR/../core}" && pwd)"

# The frontend and Playwright packages require the exact Node.js version from
# the targeted core checkout. Re-enter this script in a command-scoped nvm or
# mise environment when the caller's shell is using a different version.
if [[ "${WINDSHIFT_NODE_ENV_READY:-0}" != "1" ]]; then
  exec env WINDSHIFT_NODE_ENV_READY=1 \
    "$SCRIPT_DIR/scripts/run-with-core-node.sh" "$CORE_DIR" "$0" "$@"
fi

RUN_ID="$$-$(date +%s)"
BINARY_NAME=".e2e-windshift-${RUN_ID}"
DB_NAME=".e2e-test-${RUN_ID}.db"
LOG_FILE="$CORE_DIR/.e2e-server-${RUN_ID}.log"
ATTACHMENT_DIR="data/e2e-${RUN_ID}/"
AUTH_DIR="$E2E_DIR/.auth-${RUN_ID}"
OUTPUT_DIR="$E2E_DIR/test-results-${RUN_ID}"
REPORT_DIR="$E2E_DIR/playwright-report-${RUN_ID}"
SUMMARY_FILE="$E2E_DIR/e2e-summary-${RUN_ID}.json"
FRESH_OUTPUT_DIR="$OUTPUT_DIR-fresh-setup"
RESTART_OUTPUT_DIR="$OUTPUT_DIR-fresh-setup-restart"
FRESH_REPORT_DIR="$REPORT_DIR-fresh-setup"
RESTART_REPORT_DIR="$REPORT_DIR-fresh-setup-restart"
FRESH_SUMMARY_FILE="$E2E_DIR/e2e-summary-${RUN_ID}-fresh-setup.json"
RESTART_SUMMARY_FILE="$E2E_DIR/e2e-summary-${RUN_ID}-fresh-setup-restart.json"
AUTH_FILE="$AUTH_DIR/user.json"
PORT=""
FRESH_SETUP=false
CRITICAL_BROWSER=false
BROWSER="chromium"
SERVER_PID=""
TEST_EXIT=1
PLAYWRIGHT_ARGS=()
NEEDS_HEADED_BROWSER=false
CONTEXT_PATH="${E2E_CONTEXT_PATH:-}"
USE_POSTGRES=false
# E2E_POSTGRES_DSN can also be exported by the caller (CI uses this to point
# at a service-container Postgres rather than spinning up our own).
POSTGRES_DSN="${E2E_POSTGRES_DSN:-}"
PG_CONTAINER=""
MAILPIT_PID=""
MAILPIT_LOG=""
REQUIRE_MAILPIT="${E2E_REQUIRE_MAILPIT:-0}"
PERSIST_SUMMARY="${E2E_PERSIST_SUMMARY:-0}"
KEEP_ARTIFACTS="${E2E_KEEP_ARTIFACTS:-0}"
SKIP_BROWSER_INSTALL="${E2E_SKIP_BROWSER_INSTALL:-0}"

# ─── Parse arguments ─────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip-build)
      warn "Ignoring --skip-build; E2E servers are rebuilt to avoid stale embedded frontends"
      shift
      ;;
    --fresh-setup)
      FRESH_SETUP=true
      shift
      ;;
    --critical-browser)
      CRITICAL_BROWSER=true
      shift
      ;;
    --browser)
      BROWSER="$2"
      shift 2
      ;;
    --port)
      PORT="$2"
      shift 2
      ;;
    --postgres)
      # No DSN given: spawn a local Postgres container (see below).
      USE_POSTGRES=true
      shift
      ;;
    --context-path)
      CONTEXT_PATH="$2"
      shift 2
      ;;
    --pg-dsn)
      USE_POSTGRES=true
      POSTGRES_DSN="$2"
      shift 2
      ;;
    --headed|--ui|--debug)
      NEEDS_HEADED_BROWSER=true
      PLAYWRIGHT_ARGS+=("$1")
      shift
      ;;
    -*)
      PLAYWRIGHT_ARGS+=("$1")
      shift
      ;;
    *)
      PLAYWRIGHT_ARGS+=("$1")
      shift
      ;;
  esac
done

case "$BROWSER" in
  chromium|firefox|webkit) ;;
  *)
    err "--browser must be chromium, firefox, or webkit"
    exit 1
    ;;
esac

if [[ "$BROWSER" != "chromium" && "$CRITICAL_BROWSER" != true && "$FRESH_SETUP" != true ]]; then
  err "Firefox and WebKit are intentionally limited to --critical-browser or --fresh-setup runs"
  exit 1
fi

# A non-empty DSN (from --pg-dsn or $E2E_POSTGRES_DSN) implies postgres mode.
if [[ -n "$POSTGRES_DSN" ]]; then
  USE_POSTGRES=true
fi

if [[ -n "$CONTEXT_PATH" ]]; then
  if [[ "$CONTEXT_PATH" != /* || "$CONTEXT_PATH" == "/" || "$CONTEXT_PATH" == */ ]]; then
    err "--context-path must be a non-root absolute path without a trailing slash (example: /windshift)"
    exit 1
  fi
fi

for flag_name in REQUIRE_MAILPIT PERSIST_SUMMARY KEEP_ARTIFACTS SKIP_BROWSER_INSTALL; do
  flag_value="${!flag_name}"
  if [[ "$flag_value" != "0" && "$flag_value" != "1" ]]; then
    err "$flag_name must be 0 or 1"
    exit 1
  fi
done

# ─── Pick a free port when none was provided ─────────────────────────
free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("",0)); print(s.getsockname()[1]); s.close()'
}
if [[ -z "$PORT" ]]; then
  PORT=$(free_port)
fi

# ─── Cleanup on exit ─────────────────────────────────────────────────
cleanup() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    info "Stopping server (PID $SERVER_PID)..."
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    ok "Server stopped"
  fi
  if [[ -n "$MAILPIT_PID" ]] && kill -0 "$MAILPIT_PID" 2>/dev/null; then
    kill "$MAILPIT_PID" 2>/dev/null || true
    wait "$MAILPIT_PID" 2>/dev/null || true
  fi
  if [[ -n "$PG_CONTAINER" ]]; then
    info "Removing Postgres container '$PG_CONTAINER'..."
    docker rm -f "$PG_CONTAINER" >/dev/null 2>&1 || true
  fi
  if [[ "$USE_POSTGRES" == false ]]; then
    rm -f "$CORE_DIR/$DB_NAME" "$CORE_DIR/$DB_NAME-shm" "$CORE_DIR/$DB_NAME-wal"
  fi
  rm -rf "$CORE_DIR/$ATTACHMENT_DIR"
  rm -rf "$AUTH_DIR"
  rm -f "$CORE_DIR/$BINARY_NAME"
  rm -rf "$CORE_DIR/frontend/node_modules/.vite"

  if [[ "$KEEP_ARTIFACTS" == "1" ]]; then
    info "Keeping E2E artifacts because E2E_KEEP_ARTIFACTS=1"
  else
    rm -f "$LOG_FILE" "$MAILPIT_LOG"
    rm -rf \
      "$REPORT_DIR" "$OUTPUT_DIR" \
      "$FRESH_REPORT_DIR" "$FRESH_OUTPUT_DIR" \
      "$RESTART_REPORT_DIR" "$RESTART_OUTPUT_DIR"
  fi
  if [[ "$PERSIST_SUMMARY" != "1" ]]; then
    rm -f "$SUMMARY_FILE" "$FRESH_SUMMARY_FILE" "$RESTART_SUMMARY_FILE"
  fi
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# ─── Optional: Postgres engine ────────────────────────────────────────
# Default is SQLite (file DB in CORE_DIR). Pass --postgres to spin up a
# disposable Postgres-16 container, --pg-dsn <url> (or set $E2E_POSTGRES_DSN)
# to point at one that's already running (this is what CI does via a service
# container).
if [[ "$USE_POSTGRES" == true && -z "$POSTGRES_DSN" ]]; then
  if ! command -v docker >/dev/null 2>&1; then
    err "--postgres requires docker on PATH (or pass --pg-dsn / set E2E_POSTGRES_DSN)"
    exit 1
  fi
  PG_PORT=$(free_port)
  # Docker container names must start with [a-zA-Z0-9], so no leading dot.
  PG_CONTAINER="e2e-pg-${RUN_ID}"
  PG_DB="e2e_${RUN_ID//-/_}"
  info "Starting Postgres container '$PG_CONTAINER' on port $PG_PORT..."
  docker run -d \
    --name "$PG_CONTAINER" \
    -e POSTGRES_USER=e2e \
    -e POSTGRES_PASSWORD=e2e \
    -e POSTGRES_DB="$PG_DB" \
    -p "${PG_PORT}:5432" \
    postgres:16 > /dev/null
  info "Waiting for Postgres to accept connections..."
  for i in $(seq 1 30); do
    if docker exec "$PG_CONTAINER" pg_isready -U e2e -d "$PG_DB" >/dev/null 2>&1; then
      ok "Postgres ready"
      break
    fi
    if [[ $i -eq 30 ]]; then
      err "Postgres did not become ready within 30s"
      docker logs "$PG_CONTAINER" 2>&1 | tail -20 || true
      exit 1
    fi
    sleep 1
  done
  POSTGRES_DSN="postgresql://e2e:e2e@localhost:${PG_PORT}/${PG_DB}?sslmode=disable"
fi

# ─── Optional/required: Mailpit (SMTP catcher) ───────────────────────
# If `mailpit` is on PATH (or MAILPIT_BIN points to it), spawn it on a free
# SMTP+HTTP port pair and wait for the HTTP API. CI sets
# E2E_REQUIRE_MAILPIT=1, which turns a missing or unhealthy process into an
# immediate suite failure. Ad-hoc local runs retain optional skipping. Expose
# the selected ports via env vars. Tests that need email capture pick them
# up from $MAILPIT_HTTP_PORT; the global setup also points the default
# outbound channel at $MAILPIT_SMTP_PORT.
MAILPIT_COMMAND=""
if [[ -n "${MAILPIT_BIN:-}" ]]; then
  if [[ ! -x "$MAILPIT_BIN" ]]; then
    err "MAILPIT_BIN is not executable: $MAILPIT_BIN"
    exit 1
  fi
  MAILPIT_COMMAND="$MAILPIT_BIN"
elif command -v mailpit >/dev/null 2>&1; then
  MAILPIT_COMMAND="$(command -v mailpit)"
fi

if [[ -n "$MAILPIT_COMMAND" ]]; then
  MAILPIT_SMTP_PORT=$(free_port)
  MAILPIT_HTTP_PORT=$(free_port)
  MAILPIT_LOG="$CORE_DIR/.e2e-mailpit-${RUN_ID}.log"
  info "Starting Mailpit (SMTP $MAILPIT_SMTP_PORT, HTTP $MAILPIT_HTTP_PORT)..."
  "$MAILPIT_COMMAND" \
    --smtp 127.0.0.1:$MAILPIT_SMTP_PORT \
    --listen 127.0.0.1:$MAILPIT_HTTP_PORT \
    --quiet \
    > "$MAILPIT_LOG" 2>&1 &
  MAILPIT_PID=$!
  export MAILPIT_SMTP_PORT MAILPIT_HTTP_PORT
  for i in $(seq 1 50); do
    if curl -sf "http://127.0.0.1:$MAILPIT_HTTP_PORT/api/v1/messages" >/dev/null 2>&1; then
      ok "Mailpit ready (PID $MAILPIT_PID)"
      break
    fi
    if ! kill -0 "$MAILPIT_PID" 2>/dev/null; then
      err "Mailpit exited before becoming ready. Check log: $MAILPIT_LOG"
      tail -20 "$MAILPIT_LOG" 2>/dev/null || true
      exit 1
    fi
    if [[ $i -eq 50 ]]; then
      err "Mailpit did not become ready within 5s. Check log: $MAILPIT_LOG"
      tail -20 "$MAILPIT_LOG" 2>/dev/null || true
      exit 1
    fi
    sleep 0.1
  done
else
  if [[ "$REQUIRE_MAILPIT" == "1" ]]; then
    err "Mailpit is required but no executable was found (set MAILPIT_BIN or install mailpit on PATH)"
    exit 1
  else
    warn "mailpit not found on PATH — email-capture tests will skip"
  fi
fi

# ─── Build ────────────────────────────────────────────────────────────
# Frontend must build first — main.go uses //go:embed all:frontend/dist,
# so the Go binary bundles whatever is in dist/ at compile time.
# A fresh core checkout has no node_modules, so the build script's `vite`
# would not resolve. Mirrors the Playwright dependency guard below.
if [[ ! -x "$CORE_DIR/frontend/node_modules/.bin/vite" ]]; then
  info "Installing frontend dependencies..."
  (cd "$CORE_DIR/frontend" && npm ci)
  ok "Frontend dependencies installed"
fi

info "Building frontend..."
(cd "$CORE_DIR/frontend" && npm run build)
ok "Frontend built"

info "Building Go server..."
(cd "$CORE_DIR" && go build -o "$BINARY_NAME" .)
ok "Go server built"

# ─── Install Playwright deps ─────────────────────────────────────────
if [[ ! -x "$E2E_DIR/node_modules/.bin/playwright" ]]; then
  info "Installing Playwright dependencies..."
  (cd "$E2E_DIR" && npm ci)
  ok "Playwright dependencies installed"
else
  ok "Playwright dependencies already installed"
fi

info "Checking E2E suite ownership boundaries..."
(cd "$E2E_DIR" && bun --bun run test:suite-boundaries)
ok "E2E suite ownership boundaries verified"

# Browser binaries live outside node_modules in Playwright's cache, so they can
# be missing even when npm deps are present (for example after a package-lock
# update, cache cleanup, or a new checkout reusing an old node_modules/). Ensure
# the runtime binaries on every run; Playwright skips work when they already
# match the installed package version.
if [[ "$SKIP_BROWSER_INSTALL" == "1" ]]; then
  warn "Skipping Playwright browser installation; caller guarantees $BROWSER is provisioned"
elif [[ "$BROWSER" == "chromium" && -n "${PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH:-}" ]]; then
  if [[ ! -x "$PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH" ]]; then
    err "PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH is not executable: $PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH"
    exit 1
  fi
  info "Using Chromium executable from PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=$PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH"
  warn "Skipping Playwright browser download; videos are disabled for system-Chromium runs"
else
  if [[ "$NEEDS_HEADED_BROWSER" == true || "$BROWSER" != "chromium" ]]; then
    info "Ensuring Playwright $BROWSER browser is installed..."
    (cd "$E2E_DIR" && bun --bun playwright install "$BROWSER")
  else
    info "Ensuring Playwright Chromium headless shell is installed..."
    (cd "$E2E_DIR" && bun --bun playwright install --only-shell chromium)
  fi
fi
ok "Playwright browser dependencies ready"

# ─── Start/restart server ─────────────────────────────────────────────
if [[ "$USE_POSTGRES" == true ]]; then
  DB_FLAGS=(-pg-conn "$POSTGRES_DSN")
else
  DB_FLAGS=(-db "$DB_NAME")
fi
CONTEXT_FLAGS=()
if [[ -n "$CONTEXT_PATH" ]]; then
  CONTEXT_FLAGS=(-context-path "$CONTEXT_PATH")
  info "Context path enabled: $CONTEXT_PATH"
fi
start_server() {
  if [[ "$USE_POSTGRES" == true ]]; then
    info "Starting server on port $PORT (engine: postgres, log: $LOG_FILE)..."
  else
    info "Starting server on port $PORT (engine: sqlite, db: $DB_NAME, log: $LOG_FILE)..."
  fi
  # Test-mode timing overrides are intentionally identical across restarts.
  (cd "$CORE_DIR" && exec env \
    SESSION_SECRET=test-secret \
    WINDSHIFT_NOTIFICATION_BATCH_INTERVAL=5s \
    NOTIFICATION_FLUSH_INTERVAL=500ms \
    WINDSHIFT_E2E_DISABLE_RATE_LIMITS=1 \
    WINDSHIFT_E2E_TEST_HOOKS=1 \
    "./$BINARY_NAME" "${DB_FLAGS[@]}" "${CONTEXT_FLAGS[@]+"${CONTEXT_FLAGS[@]}"}" -p "$PORT" -no-csrf -attachment-path "$ATTACHMENT_DIR" \
    >> "$LOG_FILE" 2>&1) &
  SERVER_PID=$!
  ok "Server started (PID $SERVER_PID, log: $LOG_FILE)"
}

stop_server() {
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    info "Stopping server (PID $SERVER_PID)..."
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    ok "Server stopped"
  fi
  SERVER_PID=""
}

wait_for_server() {
  info "Waiting for server to be ready..."
  local timeout=30
  local elapsed=0
  until curl -sf "http://localhost:$PORT${CONTEXT_PATH}/api/setup/status" > /dev/null 2>&1; do
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      err "Server process died. Check log: $LOG_FILE"
      tail -20 "$LOG_FILE" 2>/dev/null || true
      return 1
    fi
    if [[ $elapsed -ge $timeout ]]; then
      err "Server did not become ready within ${timeout}s. Check log: $LOG_FILE"
      tail -20 "$LOG_FILE" 2>/dev/null || true
      return 1
    fi
    sleep 1
    elapsed=$((elapsed + 1))
  done
  ok "Server ready (took ${elapsed}s)"
}

run_playwright_project() {
  local project="$1"
  local output_dir="$2"
  local report_dir="$3"
  local summary_file="$4"
  local fresh_setup_phase=""
  if [[ "$project" == "fresh-setup" ]]; then
    fresh_setup_phase="initial"
  elif [[ "$project" == "fresh-setup-restart" ]]; then
    fresh_setup_phase="restart"
  fi

  info "Running Playwright project: $project"
  echo ""
  (cd "$E2E_DIR" \
    && BASE_URL="http://localhost:$PORT${CONTEXT_PATH}" \
       BASE_ORIGIN="http://localhost:$PORT" \
       E2E_CONTEXT_PATH="$CONTEXT_PATH" \
       E2E_AUTH_FILE="$AUTH_FILE" \
       E2E_OUTPUT_DIR="$output_dir" \
       E2E_REPORT_DIR="$report_dir" \
       E2E_SUMMARY_PATH="$summary_file" \
       E2E_FRESH_SETUP_PHASE="$fresh_setup_phase" \
       E2E_BROWSER="$BROWSER" \
       E2E_CRITICAL_BROWSER_MATRIX="$([[ "$CRITICAL_BROWSER" == true ]] && echo 1 || echo 0)" \
       E2E_REQUIRE_MAILPIT="$REQUIRE_MAILPIT" \
       E2E_FAIL_ON_UNEXPECTED_SKIP="${E2E_FAIL_ON_UNEXPECTED_SKIP:-0}" \
       E2E_FAIL_ON_RETRY="${E2E_FAIL_ON_RETRY:-0}" \
       MAILPIT_SMTP_PORT="${MAILPIT_SMTP_PORT:-}" \
       MAILPIT_HTTP_PORT="${MAILPIT_HTTP_PORT:-}" \
       bun --bun playwright test --project="$project" "${PLAYWRIGHT_ARGS[@]+"${PLAYWRIGHT_ARGS[@]}"}")
}

: > "$LOG_FILE"
start_server
wait_for_server

# ─── Run Playwright tests ────────────────────────────────────────────
if [[ "$FRESH_SETUP" == true ]]; then
  TEST_EXIT=0
  run_playwright_project \
    fresh-setup "$FRESH_OUTPUT_DIR" "$FRESH_REPORT_DIR" "$FRESH_SUMMARY_FILE" \
    || TEST_EXIT=$?
  if [[ $TEST_EXIT -ne 0 ]]; then
    err "Fresh setup phase failed (exit code $TEST_EXIT)"
    if [[ "$KEEP_ARTIFACTS" == "1" ]]; then
      info "HTML report retained at: $FRESH_REPORT_DIR"
    fi
    exit $TEST_EXIT
  fi

  info "Restarting the server against the configured database..."
  stop_server
  start_server
  wait_for_server

  run_playwright_project \
    fresh-setup-restart "$RESTART_OUTPUT_DIR" "$RESTART_REPORT_DIR" "$RESTART_SUMMARY_FILE" \
    || TEST_EXIT=$?
  if [[ $TEST_EXIT -ne 0 ]]; then
    err "Fresh setup restart phase failed (exit code $TEST_EXIT)"
    if [[ "$KEEP_ARTIFACTS" == "1" ]]; then
      info "HTML report retained at: $RESTART_REPORT_DIR"
    fi
    exit $TEST_EXIT
  fi
else
  TEST_EXIT=0
  if [[ "$CRITICAL_BROWSER" == true ]]; then
    PLAYWRIGHT_PROJECT="${BROWSER}-critical"
  else
    PLAYWRIGHT_PROJECT="chromium"
  fi
  run_playwright_project "$PLAYWRIGHT_PROJECT" "$OUTPUT_DIR" "$REPORT_DIR" "$SUMMARY_FILE" \
    || TEST_EXIT=$?
  if [[ $TEST_EXIT -ne 0 ]]; then
    err "Tests failed (exit code $TEST_EXIT)"
    if [[ "$KEEP_ARTIFACTS" == "1" ]]; then
      info "HTML report retained at: $REPORT_DIR"
    fi
    exit $TEST_EXIT
  fi
fi

echo ""
ok "All tests passed!"
