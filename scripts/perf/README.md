# Performance Validation Harness

This folder provides a runnable validation harness for million-scale readiness:

- API latency/error SLO checks via `k6`
- queue depth / DB growth snapshots
- dropped-statistics budget checks from backend logs
- one-command release gate runner

## Backend toolchain and dependency checks

Backend source builds use Go `1.27.1`. Before releasing a toolchain or
dependency upgrade, run these checks from the sibling `magpie-backend`
repository:

```bash
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build ./cmd/magpie
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

Set `MAGPIE_TEST_POSTGRES_DSN` to an isolated PostgreSQL test database to include
the storage, migration, ingestion, and operation-budget checks. Backend CI
provides that database automatically.

The backend also has benchmarks for current plaintext queue decoding, real
Redis dequeue/requeue cycles with one worker and concurrent workers, and HTTP
checks with normal and oversized judge responses. The concurrent queue case
uses 32 workers when `GOMAXPROCS=4`. Use a separate Redis instance and an empty
database for queue benchmarks. From the `magpie` repository root, for example:

```bash
docker run --rm -d --name magpie-bench-redis \
  -p 127.0.0.1:16379:6379 redis:7-alpine \
  redis-server --save '' --appendonly no

cd ../magpie-backend
REDIS_URL=redis://127.0.0.1:16379 \
MAGPIE_BENCH_REDIS_URL=redis://127.0.0.1:16379/15 \
GOMAXPROCS=4 go test -p 1 ./internal/jobs/queue/proxy ./internal/jobs/checker \
  -run '^$' -bench '^Benchmark' -benchmem -benchtime=1s -count=5

docker stop magpie-bench-redis
```

Compare the old and proposed versions on the same host with the same workload.
Record allocations and throughput, plus any cryptographic operations, JSON
encodes, Redis commands, or database queries added per check. The current
plaintext payload must reuse its stored route hash, and normal requeues must
leave the payload untouched. These local benchmarks supplement the load and
soak gate below.

## Tag checker rules and shared results

Tag settings resolve from runtime snapshots with no additional per-check
cryptographic operations, JSON encodes, Redis commands, or database queries.
Asynchronous statistics persistence adds one evidence-column JSON encode per
physical event and latest rows for participating workspaces. Shared results
retain independent workspace verdicts and current configuration keys.

The backend's `docs/performance/tag-checker-settings.md` records snapshot
lookup, million-assignment retained memory, stream serialization, SQL budgets,
and PostgreSQL throughput at one and eight workspaces. The eight-workspace
sample has a substantial persistence cost, so run the load and soak gate with
the sharing level expected in production. From `magpie-backend`:

```sh
go test ./internal/checkerconfig -run '^$' -bench '^Benchmark' -benchmem -count=3
# Set MAGPIE_TEST_POSTGRES_DSN to an isolated test database first.
go test ./internal/database -run '^TestCheckerEvidencePersistenceOperationBudgetPostgres$' -count=1 -v
MAGPIE_TEST_CHECKER_SCALE_ROUTES=1000000 \
  go test ./internal/database -run '^TestCheckerRefreshAndRecentChecksScalePostgres$' -count=1 -v -timeout=20m
```

The scale regression loads a million routes that all have checker overrides. It
measures unchanged reconciliation, one-route tag membership and rule edits, and
the actual recent-checks dashboard query. Allocation limits and SQL execution
plans catch workspace-wide reads even when projection writes remain scoped.
The regular suite uses 20,000 routes; the explicit million-route run is a release
validation gate. Column-only saves have a separate regression asserting that no
checker refresh occurs.

Keep the queue hash-reuse and scheduling-only requeue regressions in the full
test suite. Configurations and relevant assignments are refreshed outside
workers; queue payloads do not carry tag rules. The feature requires the new
frontend and every backend instance, with `--migrate-only` completed before
workers restart. Legacy current health starts unknown until matching checks
arrive, which also affects TCP rotator pools immediately after the upgrade.

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
