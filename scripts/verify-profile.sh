#!/usr/bin/env bash
# Verify one Harness Profile: lint, command audit, optional classification.
# Usage: scripts/verify-profile.sh [--strict] [--parent <parent.yaml>] [--sample <paths.txt|dir>] [--json] <profile.yaml>
set -euo pipefail

usage() {
  echo "用法：$0 [--strict] [--parent <parent.yaml>] [--sample <paths.txt|dir>] [--json] <profile.yaml>" >&2
  exit 2
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
flags=(--commands)
parent=""
profile=""
while [ $# -gt 0 ]; do
  case "$1" in
    --strict) flags+=(--strict) ;;
    --json) flags+=(--json) ;;
    --parent) [ $# -ge 2 ] || usage; parent="$2"; shift ;;
    --sample) [ $# -ge 2 ] || usage; flags+=(--classify "$2"); shift ;;
    -h|--help) usage ;;
    -*) echo "未知参数：$1" >&2; usage ;;
    *) [ -z "$profile" ] || usage; profile="$1" ;;
  esac
  shift
done
[ -n "$profile" ] || usage

abs() { case "$1" in /*) printf '%s' "$1" ;; *) printf '%s/%s' "$PWD" "$1" ;; esac; }
files=()
[ -z "$parent" ] || files+=("$(abs "$parent")")
files+=("$(abs "$profile")")
# resolve --classify path to absolute too
args=()
prev=""
for a in "${flags[@]}"; do
  if [ "$prev" = "--classify" ]; then args+=("$(abs "$a")"); else args+=("$a"); fi
  prev="$a"
done

if [ -n "${VALISE_BIN:-}" ]; then
  exec "$VALISE_BIN" profiles lint "${args[@]}" "${files[@]}"
fi
export PATH="/usr/local/go/bin:$PATH" GOTOOLCHAIN=go1.27.3
cd "$REPO_ROOT/core"
exec go run ./cmd/profilelint "${args[@]}" "${files[@]}"
