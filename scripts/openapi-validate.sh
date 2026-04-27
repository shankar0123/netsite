#!/usr/bin/env bash
# openapi-validate.sh — lint api/openapi.yaml.
#
# What: invokes `redocly lint` against the OpenAPI spec.
# How:  uses npx so callers do not need a global redocly install.
# Why:  the spec is hand-written (OQ-02 in PROJECT_STATE.md §5);
#       lint is the only safety net catching malformed YAML or
#       broken $ref pointers before clients hit them.

set -euo pipefail

cd "$(dirname "$0")/.."

if ! command -v npx >/dev/null 2>&1; then
    echo "npx is required. Install Node.js >=20." >&2
    exit 1
fi

echo "Linting api/openapi.yaml with @redocly/cli..."
npx --yes @redocly/cli@latest lint api/openapi.yaml
