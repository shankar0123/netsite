#!/usr/bin/env bash
# wait-healthy.sh — block until every NetSite-dev compose service is
# healthy, or fail loudly after a deadline.
#
# What: polls `docker compose ps --format json` for each service named
# in $REQUIRED, waiting for `Health` == "healthy" or — for services
# with no healthcheck — `State` == "running".
#
# How: a fixed-deadline retry loop. No exponential backoff because
# Docker reports state on subsecond granularity and we want fast feedback.
#
# Why a script (not `docker compose up --wait`): `--wait` only honours
# the first failure and exits, not what we want in a developer
# workflow that wants a status report. This script reports each service
# explicitly.

set -euo pipefail

cd "$(dirname "$0")/../deploy/compose"

REQUIRED=(postgres clickhouse nats prometheus grafana otel-collector)
DEADLINE_S=${WAIT_HEALTHY_DEADLINE:-90}

started=$(date +%s)
while :; do
    all_ok=1
    summary=""
    for svc in "${REQUIRED[@]}"; do
        status_json=$(docker compose ps --format json --status running "$svc" 2>/dev/null || true)
        if [[ -z "$status_json" ]]; then
            all_ok=0
            summary+="$svc=down "
            continue
        fi

        # `docker compose ps --format json` returns one JSON object per
        # line in newer versions, an array in older. Handle both.
        first_line=$(printf '%s\n' "$status_json" | head -n1)
        health=$(printf '%s' "$first_line" | sed -n 's/.*"Health":"\([^"]*\)".*/\1/p')
        state=$(printf '%s' "$first_line" | sed -n 's/.*"State":"\([^"]*\)".*/\1/p')

        if [[ -n "$health" ]]; then
            # Service has a healthcheck — wait for it to be healthy.
            if [[ "$health" == "healthy" ]]; then
                summary+="$svc=ok "
            else
                all_ok=0
                summary+="$svc=$health "
            fi
        else
            # No healthcheck — running is enough.
            if [[ "$state" == "running" ]]; then
                summary+="$svc=run "
            else
                all_ok=0
                summary+="$svc=$state "
            fi
        fi
    done

    if (( all_ok == 1 )); then
        echo "All services healthy ($summary)"
        exit 0
    fi

    elapsed=$(( $(date +%s) - started ))
    if (( elapsed >= DEADLINE_S )); then
        echo "Timed out after ${elapsed}s. Status: $summary" >&2
        exit 1
    fi

    printf '\r[%3ds] %s' "$elapsed" "$summary"
    sleep 2
done
