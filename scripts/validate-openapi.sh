#!/bin/sh
set -eu

if ! command -v npx >/dev/null 2>&1; then
  echo "npx is required to validate OpenAPI." >&2
  exit 1
fi

NPX_CACHE_DIR="${NPX_CACHE_DIR:-.cache/npx}"

mkdir -p "$NPX_CACHE_DIR"

env \
  npm_config_cache="$NPX_CACHE_DIR" \
  npx --yes @redocly/cli@1.9.0 lint api/openapi.yaml
