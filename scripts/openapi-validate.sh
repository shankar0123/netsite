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

# Pin the redocly CLI version. Floating @latest caused a silent
# strictness bump between the v0.0.6 push (which lint-passed) and the
# v0.0.7 push (which suddenly failed on rules that hadn't existed in
# the same form before). Same lesson as golangci-lint: pin both the
# CLI and any rule version that gates CI.
REDOCLY_VERSION="${REDOCLY_VERSION:-1.34.5}"
echo "Linting api/openapi.yaml with @redocly/cli@${REDOCLY_VERSION}..."
npx --yes "@redocly/cli@${REDOCLY_VERSION}" lint api/openapi.yaml
