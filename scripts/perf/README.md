# Performance Validation Harness

This folder provides a runnable validation harness for million-scale readiness:

- API latency/error SLO checks via `k6`
- queue depth / DB growth snapshots
- dropped-statistics budget checks from backend logs
- one-command release gate runner

## Prerequisites

- running Magpie stack (`docker compose up -d`)
- `k6`
- `docker` with compose support

## Required auth inputs

One of these:

- `MAGPIE_TOKEN` (recommended for CI)
- `MAGPIE_USER_EMAIL` + `MAGPIE_USER_PASSWORD`
  - if missing and `MAGPIE_REGISTER_IF_MISSING=true` (default), scripts auto-register a user

Optional:

- `MAGPIE_BASE_URL` (default `http://localhost:5656`)
- `MAGPIE_COMPOSE_FILE` (default `<repo>/docker-compose.yml`)
- `MAGPIE_ENV_FILE` (default `<repo>/.env`)

## Workload scripts

- `k6/read-path.js`
  - read-heavy REST/GraphQL checks
  - default duration: `30m`
- `k6/write-path.js`
  - sustained `/api/addProxies` ingestion
  - default duration: `30m`
- `k6/mixed-soak.js`
  - mixed read/write soak profile
  - default duration: `2h`

## Quick run

```bash
cd scripts/perf
./run-gate.sh
```

By default this runs read + write suites and skips long soak.

To include soak:

```bash
PERF_SOAK_DURATION=24h ./run-gate.sh
```

If your running stack is in another directory (for example installer-created `~/magpie`), point the scripts to that Compose/env pair:

```bash
MAGPIE_COMPOSE_FILE=~/magpie/docker-compose.yml \
MAGPIE_ENV_FILE=~/magpie/.env \
./run-gate.sh
```

## Snapshot and budget tools

- `capture-snapshot.sh`
  - collects:
    - `proxy_queue` depth
    - `scrapesite_queue` depth
    - `proxies` row count
    - `proxy_statistics` row count
    - `proxy_statistics.response_body` non-empty row count
    - table/database byte sizes
- `assert-snapshot-delta.sh <start> <end>`
  - checks growth deltas against configured budgets
- `check-drop-budget.sh`
  - parses backend logs for `dropped_total` from proxy-statistics queue drops
  - log source controls:
    - `PERF_DROP_BUDGET_SOURCE=auto` (default): compose backend logs if available, else `PERF_BACKEND_LOG_FILE` if set, else skip with warning
    - `PERF_DROP_BUDGET_SOURCE=compose`: require compose backend logs
    - `PERF_DROP_BUDGET_SOURCE=file`: require `PERF_BACKEND_LOG_FILE`
    - `PERF_DROP_BUDGET_SOURCE=skip`: skip drop-budget check

## Budget env overrides

You can tune gate budgets for your infra:

- `PERF_MAX_PROXY_QUEUE_DEPTH_DELTA` (default `50000`)
- `PERF_MAX_SCRAPESITE_QUEUE_DEPTH_DELTA` (default `5000`)
- `PERF_MAX_PROXY_STATISTICS_ROW_DELTA` (default `25000000`)
- `PERF_MAX_PROXY_STATISTICS_RESPONSE_ROWS_DELTA` (default `250000`)
- `PERF_MAX_PROXY_STATISTICS_BYTES_DELTA` (default `16106127360`)
- `PERF_MAX_DATABASE_BYTES_DELTA` (default `21474836480`)
- `PERF_PROXY_STAT_DROPS_BUDGET` (default `0`)
- `PERF_DROP_LOG_SINCE` (default `24h`)
- `PERF_DROP_BUDGET_SOURCE` (default `auto`)
- `PERF_BACKEND_LOG_FILE` (used when drop-budget source is `file` or `auto` fallback)

## Suite-level load controls

The `k6` scripts expose per-suite rate/vu env vars (for example `PERF_RATE_PROXY_PAGE`, `PERF_RATE_ADD_PROXIES`, `PERF_SOAK_RATE_PROXY_PAGE`). See each script header for defaults.

## Local backend (no backend container)

If you run backend with `go run` and only keep Postgres/Redis in compose, capture backend logs and pass them into the gate:

```bash
# terminal 1
cd ../magpie-backend
go run ./cmd/magpie 2>&1 | tee /tmp/magpie-backend.log
```

```bash
# terminal 2
cd scripts/perf
PERF_BACKEND_LOG_FILE=/tmp/magpie-backend.log ./run-gate.sh
```
