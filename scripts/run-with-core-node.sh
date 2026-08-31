#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -lt 2 ]]; then
  echo "Usage: run-with-core-node.sh /path/to/core command [args...]" >&2
  exit 1
fi

CORE_DIR="$(cd "$1" && pwd)"
shift

VERSION_FILE="$CORE_DIR/.nvmrc"
if [[ ! -f "$VERSION_FILE" ]]; then
  echo "Error: Node version file not found: $VERSION_FILE" >&2
  exit 1
fi

REQUIRED_VERSION="$(tr -d '[:space:]' < "$VERSION_FILE")"
if [[ -z "$REQUIRED_VERSION" ]]; then
  echo "Error: Node version file is empty: $VERSION_FILE" >&2
  exit 1
fi

NORMALIZED_REQUIRED_VERSION="${REQUIRED_VERSION#v}"
CURRENT_VERSION="$(node --version 2>/dev/null || true)"
NORMALIZED_CURRENT_VERSION="${CURRENT_VERSION#v}"

if [[ "$NORMALIZED_CURRENT_VERSION" == "$NORMALIZED_REQUIRED_VERSION" ]]; then
  exec "$@"
fi

if [[ -n "$CURRENT_VERSION" ]]; then
  echo "==> Temporarily switching Node.js from $CURRENT_VERSION to v$NORMALIZED_REQUIRED_VERSION" >&2
else
  echo "==> Temporarily activating Node.js v$NORMALIZED_REQUIRED_VERSION" >&2
fi

# nvm is normally a shell function and is not exported to scripts. Load it
# explicitly when available, then use it only if the requested version is
# already installed. Sourcing and activation affect this runner process only.
if ! declare -F nvm >/dev/null 2>&1; then
  NVM_SCRIPT="${NVM_DIR:-${HOME:-}/.nvm}/nvm.sh"
  if [[ -f "$NVM_SCRIPT" ]]; then
    # shellcheck disable=SC1090
    source "$NVM_SCRIPT" >/dev/null 2>&1
  fi
fi

if declare -F nvm >/dev/null 2>&1; then
  NVM_VERSION="$(nvm version "$REQUIRED_VERSION" 2>/dev/null || true)"
  if [[ -n "$NVM_VERSION" && "$NVM_VERSION" != "N/A" ]]; then
    nvm use --silent "$REQUIRED_VERSION" >/dev/null
    exec "$@"
  fi
fi

# mise exec creates a command-scoped environment and does not change the
# caller's shell or repository configuration. It also works when the user's
# interactive mise activation is not present in this non-interactive shell.
if command -v mise >/dev/null 2>&1; then
  exec mise exec "node@$NORMALIZED_REQUIRED_VERSION" -- "$@"
fi

echo "Error: Node.js v$NORMALIZED_REQUIRED_VERSION is required by $VERSION_FILE." >&2
echo "Install it with 'nvm install $NORMALIZED_REQUIRED_VERSION' or 'mise install node@$NORMALIZED_REQUIRED_VERSION'." >&2
exit 1
