#!/usr/bin/env bash
set -euo pipefail

MAILPIT_VERSION="v1.30.5"
DESTINATION="${1:-.bin/mailpit}"

case "$(uname -s)-$(uname -m)" in
  Linux-x86_64)
    ARCHIVE="mailpit-linux-amd64.tar.gz"
    SHA256="b1499b23e6207d2728a0f154fd27370d0f608dd8ae1a63b734d089dc7f7a8d6f"
    ;;
  Linux-aarch64|Linux-arm64)
    ARCHIVE="mailpit-linux-arm64.tar.gz"
    SHA256="c4c0a587770af1d5fb192e7b580aebfc03115c93da2af308a4b2e227c7d06cd2"
    ;;
  *)
    echo "Unsupported Mailpit CI platform: $(uname -s)/$(uname -m)" >&2
    exit 1
    ;;
esac

TEMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TEMP_DIR"
}
trap cleanup EXIT

URL="https://github.com/axllent/mailpit/releases/download/${MAILPIT_VERSION}/${ARCHIVE}"
curl --fail --location --silent --show-error "$URL" --output "$TEMP_DIR/$ARCHIVE"
printf '%s  %s\n' "$SHA256" "$TEMP_DIR/$ARCHIVE" | sha256sum --check --status
tar -xzf "$TEMP_DIR/$ARCHIVE" -C "$TEMP_DIR" mailpit
mkdir -p "$(dirname "$DESTINATION")"
install -m 0755 "$TEMP_DIR/mailpit" "$DESTINATION"
"$DESTINATION" version
