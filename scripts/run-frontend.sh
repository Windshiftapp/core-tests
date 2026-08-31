#!/usr/bin/env bash

set -euo pipefail

mode="${1:-}"
if [[ -z "$mode" ]]; then
  echo "Usage: run-frontend.sh quick|all [vitest arguments]" >&2
  exit 2
fi
shift

cd frontend
npm ci

case "$mode" in
  quick)
    if [[ $# -ne 0 ]]; then
      echo "Error: the quick frontend suite does not accept arguments" >&2
      exit 2
    fi
    bun --bun run test:coverage
    ;;
  all)
    bun --bun vitest run --config vitest.all.config.js --coverage "$@"
    ;;
  *)
    echo "Error: unknown frontend mode '$mode'" >&2
    exit 2
    ;;
esac
